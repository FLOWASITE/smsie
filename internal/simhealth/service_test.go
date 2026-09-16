package simhealth

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/logic"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"gorm.io/gorm"
)

func TestServiceRemindsEverySevenDays(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.ModemBay{}, &model.SMS{}, &model.SimSlotEvent{}, &model.Webhook{}, &model.SimAlert{}); err != nil {
		t.Fatal(err)
	}
	seen := time.Now().Add(-60 * 24 * time.Hour)
	db.Create(&model.Modem{ICCID: "A", PhoneNumber: "0900000001", FirstSeenAt: &seen})
	db.Create(&model.SMS{ICCID: "A", Type: "received", Timestamp: time.Now().Add(-31 * 24 * time.Hour)})
	db.Create(&model.SMS{ICCID: "A", Type: "sent", Timestamp: time.Now()}) // sent không tính
	slot3 := 3
	db.Create(&model.ModemBay{IMEI: "I-A", SlotNumber: &slot3, CurrentICCID: "A"})
	db.Create(&model.Modem{ICCID: "B", FirstSeenAt: &seen}) // vắng khay 9 ngày, chưa SMS nào
	db.Create(&model.SimSlotEvent{ICCID: "B", Event: model.SlotEventRemoved, DetectedAt: time.Now().Add(-9 * 24 * time.Hour)})

	s := NewService(db, nil, logic.NewWebhookService(repository.NewWebhookRepository(db)),
		config.SimHealthConfig{Enabled: true, NoSMSDays: 30, UnregisteredHours: 24, AbsentDays: 7, RemindDays: 7})

	items, err := s.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !items[0].InBay || *items[0].SlotNumber != 3 || len(items[0].Findings) != 1 || items[0].Findings[0].Kind != model.SimAlertNoSMS {
		t.Fatalf("items[0] = %+v", items[0])
	}
	if items[1].InBay || len(items[1].Findings) != 2 { // B: no_sms (thấy 60 ngày) + absent
		t.Fatalf("items[1] = %+v", items[1])
	}
	if got := alertText(items[0], items[0].Findings[0]); got != "🪦 SIM 0900000001 (khe 3): không nhận SMS nào 31 ngày — có thể bị thu hồi" {
		t.Fatalf("text = %q", got)
	}

	count := func() int64 {
		var n int64
		db.Model(&model.SimAlert{}).Count(&n)
		return n
	}
	if err := s.Evaluate(); err != nil || count() != 3 {
		t.Fatalf("lần 1: err=%v n=%d", err, count())
	}
	if err := s.Evaluate(); err != nil || count() != 3 {
		t.Fatalf("lần 2 (vẫn trong remind): err=%v n=%d", err, count())
	}
	db.Model(&model.SimAlert{}).Where("iccid = ? AND kind = ?", "A", model.SimAlertNoSMS).Update("sent_at", time.Now().Add(-8*24*time.Hour))
	if err := s.Evaluate(); err != nil || count() != 4 {
		t.Fatalf("lần 3 (8 ngày sau): err=%v n=%d", err, count())
	}
}
