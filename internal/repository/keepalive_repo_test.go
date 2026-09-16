package repository

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func newKeepaliveTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.KeepaliveRun{}, &model.SMS{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestKeepaliveSentCountThisMonthAndLastRun(t *testing.T) {
	repo := NewKeepaliveRepository(newKeepaliveTestDB(t))
	now := time.Date(2026, 9, 16, 7, 0, 0, 0, time.Local)
	runs := []model.KeepaliveRun{
		{ICCID: "A", TargetICCID: "B", Status: model.KeepaliveSent, RanAt: now.AddDate(0, 0, -20)}, // tháng trước
		{ICCID: "A", TargetICCID: "B", Status: model.KeepaliveSent, RanAt: now.AddDate(0, 0, -10)},
		{ICCID: "A", TargetICCID: "C", Status: model.KeepaliveFailed, RanAt: now.AddDate(0, 0, -5)},
		{ICCID: "A", TargetICCID: "C", Status: model.KeepaliveSent, RanAt: now.AddDate(0, 0, -1)},
		{ICCID: "A", Status: model.KeepaliveSkipped, Reason: "trần", RanAt: now},
		{ICCID: "X", TargetICCID: "B", Status: model.KeepaliveSent, RanAt: now},
	}
	for i := range runs {
		if err := repo.Add(&runs[i]); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := repo.SentCountThisMonth("A", now); err != nil || n != 2 {
		t.Fatalf("SentCountThisMonth = %d %v, want 2", n, err)
	}
	last, err := repo.LastRun("A")
	if err != nil || last == nil || last.Status != model.KeepaliveSkipped {
		t.Fatalf("LastRun = %+v %v", last, err)
	}
	if last, _ := repo.LastRun("NONE"); last != nil {
		t.Fatalf("LastRun none = %+v", last)
	}
	sentAt, err := repo.LastSentAt("A")
	if err != nil || sentAt == nil || !sentAt.Equal(now.AddDate(0, 0, -1)) {
		t.Fatalf("LastSentAt = %v %v", sentAt, err)
	}
	if at, _ := repo.LastSentAt("NONE"); at != nil {
		t.Fatalf("LastSentAt none = %v", at)
	}
	counts, err := repo.TargetCounts(now.AddDate(0, 0, -15))
	if err != nil || counts["B"] != 2 || counts["C"] != 1 {
		t.Fatalf("TargetCounts = %v %v", counts, err)
	}
	list, total, err := repo.List("A", 1, 2)
	if err != nil || total != 5 || len(list) != 2 || list[0].Status != model.KeepaliveSkipped {
		t.Fatalf("List = %+v %d %v", list, total, err)
	}
	all, total, _ := repo.List("", 1, 50)
	if total != 6 || len(all) != 6 {
		t.Fatalf("List all = %d/%d", len(all), total)
	}
}

func TestKeepaliveLastActivityAll(t *testing.T) {
	db := newKeepaliveTestDB(t)
	repo := NewKeepaliveRepository(db)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	rows := []model.SMS{
		{ICCID: "A", Phone: "1", Type: "received", Timestamp: base, CreatedAt: base},
		{ICCID: "A", Phone: "1", Type: "sent", Timestamp: base, CreatedAt: base.AddDate(0, 0, 3)},
		{ICCID: "B", Phone: "1", Type: "received", Timestamp: base, CreatedAt: base.AddDate(0, 0, 1)},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.LastActivityAll()
	if err != nil {
		t.Fatal(err)
	}
	if !got["A"].Equal(base.AddDate(0, 0, 3)) || !got["B"].Equal(base.AddDate(0, 0, 1)) || len(got) != 2 {
		t.Fatalf("LastActivityAll = %v", got)
	}
}
