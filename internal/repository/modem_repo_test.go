package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func TestUpsertIgnoresRuntimeOnlyPortName(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}); err != nil {
		t.Fatal(err)
	}

	repo := NewModemRepository(db)
	if err := repo.Upsert(&model.Modem{ICCID: "89840000000000000000", IMEI: "123456789012345", PortName: "COM19"}); err != nil {
		t.Fatalf("upsert modem: %v", err)
	}
}

func TestSetFirstSeenIfNullDoesNotOverwrite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}); err != nil {
		t.Fatal(err)
	}
	repo := NewModemRepository(db)
	const iccid = "89840000000000000001"
	if err := repo.Upsert(&model.Modem{ICCID: iccid}); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := repo.SetFirstSeenIfNull(iccid, first); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetFirstSeenIfNull(iccid, first.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	m, err := repo.FindByICCID(iccid)
	if err != nil {
		t.Fatal(err)
	}
	if m.FirstSeenAt == nil || !m.FirstSeenAt.Equal(first) {
		t.Fatalf("first_seen_at = %v, want %v", m.FirstSeenAt, first)
	}
	reg := first.Add(time.Hour)
	if err := repo.TouchRegistered(iccid, reg); err != nil {
		t.Fatal(err)
	}
	m, _ = repo.FindByICCID(iccid)
	if m.LastRegisteredAt == nil || !m.LastRegisteredAt.Equal(reg) {
		t.Fatalf("last_registered_at = %v, want %v", m.LastRegisteredAt, reg)
	}
}

func TestLastReceivedSMSAtIgnoresSent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SMS{}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	rows := []model.SMS{
		// timestamp (SCTS mạng) cố tình lệch thứ tự: mốc phải theo created_at của dòng id lớn nhất.
		{ICCID: "A", Phone: "1", Type: "received", Timestamp: base.Add(9 * time.Hour), CreatedAt: base},
		{ICCID: "A", Phone: "1", Type: "received", Timestamp: base, CreatedAt: base.Add(time.Hour)},
		{ICCID: "A", Phone: "1", Type: "sent", Timestamp: base.Add(5 * time.Hour), CreatedAt: base.Add(5 * time.Hour)},
		{ICCID: "B", Phone: "1", Type: "sent", Timestamp: base, CreatedAt: base},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := NewModemRepository(db).LastReceivedSMSAt()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d iccids, want 1: %v", len(got), got)
	}
	if !got["A"].Equal(base.Add(time.Hour)) {
		t.Fatalf("A = %v, want %v", got["A"], base.Add(time.Hour))
	}
}

func TestSetPhoneNumberWritesHistoryOnlyWhenChanged(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.PhoneNumberHistory{}); err != nil {
		t.Fatal(err)
	}
	repo := NewModemRepository(db)
	const iccid = "89840000000000000002"
	if err := repo.Upsert(&model.Modem{ICCID: iccid}); err != nil {
		t.Fatal(err)
	}
	if changed, err := repo.SetPhoneNumber(iccid, "0912345678", "ussd"); err != nil || !changed {
		t.Fatalf("first set: changed=%v err=%v", changed, err)
	}
	if changed, err := repo.SetPhoneNumber(iccid, "0912345678", "ussd"); err != nil || changed {
		t.Fatalf("same number: changed=%v err=%v", changed, err)
	}
	if changed, err := repo.SetPhoneNumber(iccid, "0987654321", "manual"); err != nil || !changed {
		t.Fatalf("second set: changed=%v err=%v", changed, err)
	}
	if _, err := repo.SetPhoneNumber("missing", "0987654321", "manual"); err == nil {
		t.Fatal("unknown iccid should error")
	}
	m, _ := repo.FindByICCID(iccid)
	if m.PhoneNumber != "0987654321" {
		t.Fatalf("phone = %q", m.PhoneNumber)
	}
	hist, err := repo.PhoneHistory(iccid)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history = %v err=%v", hist, err)
	}
	if hist[0].OldPhone != "0912345678" || hist[0].NewPhone != "0987654321" || hist[0].Source != "manual" || hist[1].OldPhone != "" || hist[1].Source != "ussd" {
		t.Fatalf("history rows = %+v", hist)
	}
}
