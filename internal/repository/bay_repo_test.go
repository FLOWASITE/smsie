package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func newBayTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.ModemBay{}, &model.SimSlotEvent{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func intp(n int) *int { return &n }

func TestMigrateFromModemsCopiesSlotToBayOnce(t *testing.T) {
	db := newBayTestDB(t)
	db.Create(&model.Modem{ICCID: "ICCID-1", IMEI: "IMEI-A", SlotNumber: intp(15)})
	db.Create(&model.Modem{ICCID: "ICCID-2", IMEI: "IMEI-A", SlotNumber: intp(16)}) // trùng IMEI → bỏ qua
	db.Create(&model.Modem{ICCID: "ICCID-3", IMEI: "", SlotNumber: intp(17)})       // không IMEI → bỏ qua
	repo := NewBayRepository(db)
	if err := repo.MigrateFromModems(); err != nil {
		t.Fatal(err)
	}
	if err := repo.MigrateFromModems(); err != nil { // idempotent
		t.Fatal(err)
	}
	var bays []model.ModemBay
	db.Find(&bays)
	if len(bays) != 1 || bays[0].IMEI != "IMEI-A" || *bays[0].SlotNumber != 15 || bays[0].CurrentICCID != "ICCID-1" {
		t.Fatalf("bays = %+v", bays)
	}
	var n int64
	db.Model(&model.SimSlotEvent{}).Count(&n)
	if n != 0 {
		t.Fatalf("migration must not write events, got %d", n)
	}
}

func TestObserveWritesEventsAndSyncsSlotCache(t *testing.T) {
	db := newBayTestDB(t)
	db.Create(&model.Modem{ICCID: "ICCID-1", IMEI: "IMEI-A", PhoneNumber: "0987654321"})
	db.Create(&model.Modem{ICCID: "ICCID-2", IMEI: "IMEI-B"})
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intp(15), CurrentICCID: "ICCID-1"})
	db.Create(&model.ModemBay{IMEI: "IMEI-B", SlotNumber: intp(16), CurrentICCID: "ICCID-2"})
	repo := NewBayRepository(db)

	events, err := repo.Observe(Observation{IMEI: "IMEI-B", ICCID: "ICCID-1", Operator: "Viettel", PortName: "COM22", At: time.Date(2026, 9, 15, 14, 22, 8, 0, time.Local)})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Event != model.SlotEventMoved || events[1].PhoneNumber != "0987654321" || events[1].Operator != "Viettel" || events[1].PortName != "COM22" {
		t.Fatalf("events = %+v", events)
	}
	var m model.Modem
	db.First(&m, "iccid = ?", "ICCID-1")
	if m.SlotNumber == nil || *m.SlotNumber != 16 {
		t.Fatalf("modems.slot_number cache = %v", m.SlotNumber)
	}
	var a, b model.ModemBay
	db.First(&a, "imei = ?", "IMEI-A")
	db.First(&b, "imei = ?", "IMEI-B")
	if a.CurrentICCID != "" || b.CurrentICCID != "ICCID-1" {
		t.Fatalf("bays a=%+v b=%+v", a, b)
	}
	var stored int64
	db.Model(&model.SimSlotEvent{}).Count(&stored)
	if stored != 2 {
		t.Fatalf("stored events = %d", stored)
	}
}

func TestFillBalanceOnlyRecentEventMissingBalance(t *testing.T) {
	db := newBayTestDB(t)
	old := time.Now().Add(-10 * time.Minute)
	recent := time.Now().Add(-1 * time.Minute)
	db.Create(&model.SimSlotEvent{ICCID: "ICCID-1", Event: model.SlotEventMoved, DetectedAt: old})
	db.Create(&model.SimSlotEvent{ICCID: "ICCID-1", Event: model.SlotEventInserted, DetectedAt: recent})
	repo := NewBayRepository(db)
	if err := repo.FillBalance("ICCID-1", 48500, time.Now()); err != nil {
		t.Fatal(err)
	}
	var evs []model.SimSlotEvent
	db.Order("detected_at").Find(&evs)
	if evs[0].BalanceVND != nil {
		t.Fatalf("old event must stay empty: %+v", evs[0])
	}
	if evs[1].BalanceVND == nil || *evs[1].BalanceVND != 48500 {
		t.Fatalf("recent event must be filled: %+v", evs[1])
	}
}

func TestMarkEmptyWritesRemoved(t *testing.T) {
	db := newBayTestDB(t)
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intp(15), CurrentICCID: "ICCID-1"})
	repo := NewBayRepository(db)
	if err := repo.MarkEmpty("IMEI-A", "COM17", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkEmpty("IMEI-A", "COM17", time.Now()); err != nil { // lần 2 không ghi thêm
		t.Fatal(err)
	}
	var evs []model.SimSlotEvent
	db.Find(&evs)
	if len(evs) != 1 || evs[0].Event != model.SlotEventRemoved || *evs[0].FromSlot != 15 || evs[0].ICCID != "ICCID-1" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestAssignSlotRejectsTakenSlot(t *testing.T) {
	db := newBayTestDB(t)
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intp(15)})
	db.Create(&model.ModemBay{IMEI: "IMEI-B"})
	repo := NewBayRepository(db)
	if err := repo.AssignSlot("IMEI-B", intp(15)); err != ErrSlotTaken {
		t.Fatalf("expected ErrSlotTaken, got %v", err)
	}
	if err := repo.AssignSlot("IMEI-B", intp(16)); err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignSlot("IMEI-A", nil); err != nil {
		t.Fatal(err)
	}
	var a model.ModemBay
	db.First(&a, "imei = ?", "IMEI-A")
	if a.SlotNumber != nil {
		t.Fatalf("expected unassigned, got %v", *a.SlotNumber)
	}
}
