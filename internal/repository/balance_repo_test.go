package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func newBalanceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.BalanceSnapshot{}, &model.BalanceAlert{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestRecentSnapshotsAscendingAndLimited(t *testing.T) {
	repo := NewBalanceRepository(newBalanceTestDB(t))
	base := time.Date(2026, 9, 1, 6, 0, 0, 0, time.Local)
	for i, v := range []int64{50000, 40000, 30000, 20000} {
		if err := repo.AddSnapshot("ICCID-1", v, base.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	repo.AddSnapshot("ICCID-2", 99000, base)
	got, err := repo.RecentSnapshots("ICCID-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].BalanceVND != 40000 || got[1].BalanceVND != 30000 || got[2].BalanceVND != 20000 {
		t.Fatalf("got %+v", got)
	}
}

func TestAlertedWithin(t *testing.T) {
	repo := NewBalanceRepository(newBalanceTestDB(t))
	if err := repo.AddAlert(&model.BalanceAlert{ICCID: "ICCID-1", Kind: model.BalanceAlertLow, BalanceVND: 10000, SentAt: time.Now().Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := repo.AlertedWithin("ICCID-1", 24*time.Hour); !ok {
		t.Fatal("expected alerted within 24h")
	}
	if ok, _ := repo.AlertedWithin("ICCID-1", time.Hour); ok {
		t.Fatal("expected not alerted within 1h")
	}
	if ok, _ := repo.AlertedWithin("ICCID-2", 24*time.Hour); ok {
		t.Fatal("other iccid must be false")
	}
	alerts, total, err := repo.ListAlerts(1, 10)
	if err != nil || total != 1 || len(alerts) != 1 {
		t.Fatalf("ListAlerts = %v %d %v", alerts, total, err)
	}
}
