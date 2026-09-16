package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func seed(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.ModemBay{}, &model.SMS{}, &model.CallRecording{}, &model.BalanceSnapshot{}, &model.SimSlotEvent{}, &model.KeepaliveRun{}, &model.BalanceAlert{}, &model.SimAlert{}); err != nil {
		t.Fatal(err)
	}
	slot3 := 3
	db.Create(&model.Modem{ICCID: "SIM-A", PhoneNumber: "0911", IMEI: "IMEI-A"})
	db.Create(&model.Modem{ICCID: "SIM-B", PhoneNumber: "0922", SlotNumber: &slot3}) // không có bay → fallback slot_number
	slot1 := 1
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: &slot1, CurrentICCID: "SIM-A"})

	sep := time.Date(2026, 9, 10, 12, 0, 0, 0, time.Local)
	before := time.Date(2026, 8, 31, 23, 59, 59, 0, time.Local) // ngoài tháng
	after := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)     // biên phải: ngoài tháng
	for _, s := range []model.SMS{
		{ICCID: "SIM-A", Phone: "1", Type: "received", Timestamp: sep},
		{ICCID: "SIM-A", Phone: "1", Type: "received", Timestamp: sep},
		{ICCID: "SIM-A", Phone: "1", Type: "sent", Timestamp: sep},
		{ICCID: "SIM-A", Phone: "1", Type: "sent", Timestamp: before},
		{ICCID: "SIM-A", Phone: "1", Type: "sent", Timestamp: after},
	} {
		db.Create(&s)
	}
	db.Create(&model.CallRecording{ICCID: "SIM-A", FileName: "a", ContentType: "audio/wav", DurationSeconds: 30, CreatedAt: sep})
	db.Create(&model.CallRecording{ICCID: "SIM-A", FileName: "b", ContentType: "audio/wav", DurationSeconds: 45, CreatedAt: sep})
	db.Create(&model.CallRecording{ICCID: "SIM-A", FileName: "c", ContentType: "audio/wav", DurationSeconds: 99, CreatedAt: before})
	db.Create(&model.BalanceSnapshot{ICCID: "SIM-A", BalanceVND: 90000, ReadAt: before})
	db.Create(&model.BalanceSnapshot{ICCID: "SIM-A", BalanceVND: 50000, ReadAt: sep.AddDate(0, 0, -5)})
	db.Create(&model.BalanceSnapshot{ICCID: "SIM-A", BalanceVND: 40000, ReadAt: sep})
	db.Create(&model.BalanceSnapshot{ICCID: "SIM-A", BalanceVND: 35000, ReadAt: sep.AddDate(0, 0, 5)})
	db.Create(&model.SimSlotEvent{ICCID: "SIM-A", Event: "inserted", DetectedAt: sep})
	db.Create(&model.KeepaliveRun{ICCID: "SIM-A", Status: "sent", RanAt: sep})
	db.Create(&model.KeepaliveRun{ICCID: "SIM-A", Status: "failed", RanAt: sep})
	db.Create(&model.BalanceAlert{ICCID: "SIM-A", Kind: "low", SentAt: sep})
	db.Create(&model.SimAlert{ICCID: "SIM-A", Kind: "no_sms", SentAt: sep})
	db.Create(&model.SimAlert{ICCID: "SIM-A", Kind: "no_sms", SentAt: after})
	return db
}

func i64(v int64) *int64 { return &v }

func TestMonthlyCountsAndBoundaries(t *testing.T) {
	db := seed(t)
	r, err := Monthly(db, time.Date(2026, 9, 17, 8, 0, 0, 0, time.Local), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Month != "2026-09" || len(r.Rows) != 2 {
		t.Fatalf("report = %+v", r)
	}
	a, b := r.Rows[0], r.Rows[1]
	if a.ICCID != "SIM-A" || a.SlotNumber == nil || *a.SlotNumber != 1 || b.ICCID != "SIM-B" || b.SlotNumber == nil || *b.SlotNumber != 3 {
		t.Fatalf("thứ tự/khe sai: %+v %+v", a, b)
	}
	if a.SMSReceived != 2 || a.SMSSent != 1 || a.SMSFailed != 0 || a.Calls != 2 || a.CallSeconds != 75 || a.SlotEvents != 1 || a.KeepaliveSent != 1 || a.Alerts != 2 {
		t.Fatalf("SIM-A = %+v", a)
	}
	if a.BalanceStart == nil || *a.BalanceStart != 50000 || a.BalanceEnd == nil || *a.BalanceEnd != 35000 || a.BalanceDelta == nil || *a.BalanceDelta != -15000 {
		t.Fatalf("balance SIM-A = %v %v %v", a.BalanceStart, a.BalanceEnd, a.BalanceDelta)
	}
	if b.SMSReceived != 0 || b.Calls != 0 || b.BalanceStart != nil || b.BalanceEnd != nil || b.BalanceDelta != nil || b.PhoneNumber != "0922" {
		t.Fatalf("SIM-B phải toàn 0/null: %+v", b)
	}
	if r.Totals.SMSReceived != 2 || r.Totals.CallSeconds != 75 || r.Totals.Alerts != 2 || r.Totals.BalanceDelta == nil || *r.Totals.BalanceDelta != -15000 {
		t.Fatalf("totals = %+v", r.Totals)
	}
}

func TestMonthlyAllowedFilterAndEmptyMonth(t *testing.T) {
	db := seed(t)
	r, err := Monthly(db, time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local), []string{"SIM-B"})
	if err != nil || len(r.Rows) != 1 || r.Rows[0].ICCID != "SIM-B" {
		t.Fatalf("allowed: %v %+v", err, r.Rows)
	}
	if r, _ = Monthly(db, time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local), []string{}); len(r.Rows) != 0 {
		t.Fatalf("allowed rỗng phải trả 0 dòng: %+v", r.Rows)
	}
	r, _ = Monthly(db, time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local), nil)
	if r.Month != "2026-07" || r.Rows[0].SMSSent != 0 || r.Rows[0].BalanceStart != nil || r.Totals.SMSReceived != 0 {
		t.Fatalf("tháng trống: %+v", r)
	}
}

func TestWriteCSV(t *testing.T) {
	db := seed(t)
	r, _ := Monthly(db, time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local), nil)
	var buf bytes.Buffer
	if err := WriteCSV(&buf, r); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.HasPrefix(s, "\ufeffICCID,") || !strings.Contains(s, "Số dư đầu tháng") {
		t.Fatalf("thiếu BOM/tiêu đề Việt: %q", s[:80])
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "SIM-A,0911,1,2,1,0,2,75,50000,35000,-15000,1,1,2") || !strings.HasPrefix(lines[2], "SIM-B,0922,3,0,0,0,0,0,,,,") || !strings.HasPrefix(lines[3], "Tổng,") {
		t.Fatalf("csv:\n%s", s)
	}
}
