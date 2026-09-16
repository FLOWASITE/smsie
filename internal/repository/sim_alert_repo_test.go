package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func TestSimAlertRepo(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SimAlert{}); err != nil {
		t.Fatal(err)
	}
	repo := NewSimAlertRepository(db)
	now := time.Now()
	if err := repo.Add(&model.SimAlert{ICCID: "A", Kind: model.SimAlertNoSMS, SentAt: now.Add(-8 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(&model.SimAlert{ICCID: "A", Kind: model.SimAlertAbsent, SentAt: now}); err != nil {
		t.Fatal(err)
	}

	if ok, _ := repo.AlertedWithin("A", model.SimAlertNoSMS, 7*24*time.Hour); ok {
		t.Fatal("no_sms 8 days ago must not count as within 7 days")
	}
	if ok, _ := repo.AlertedWithin("A", model.SimAlertAbsent, 7*24*time.Hour); !ok {
		t.Fatal("absent now must count")
	}
	if ok, _ := repo.AlertedWithin("A", model.SimAlertUnregistered, 7*24*time.Hour); ok {
		t.Fatal("kind must be matched")
	}

	list, total, err := repo.List(1, 10)
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("list: %v total=%d len=%d", err, total, len(list))
	}
	if list[0].Kind != model.SimAlertAbsent {
		t.Fatalf("expected sent_at DESC, got %s first", list[0].Kind)
	}
}
