package balance

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/logic"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"github.com/pccr10001/smsie/internal/worker"
	"gorm.io/gorm"
)

func TestEvaluateAlertsOnceAndSkipsUnknown(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.ModemBay{}, &model.Webhook{}, &model.BalanceSnapshot{}, &model.BalanceAlert{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	db.Create(&model.Modem{ICCID: "A", PhoneNumber: "0900000001", BalanceVND: 10000, BalanceUpdatedAt: &now})
	slot7 := 7
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: &slot7, CurrentICCID: "A"}) // modems.slot_number NULL → lấy từ modem_bays
	db.Create(&model.Modem{ICCID: "B"})
	s := NewScheduler(db, worker.NewManager(db), logic.NewWebhookService(repository.NewWebhookRepository(db)),
		config.BalanceConfig{LowThresholdVND: 20000, ForecastDays: 3})

	items, err := s.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Level != LevelLow || items[1].Level != LevelUnknown {
		t.Fatalf("items = %+v", items)
	}
	if _, err := s.Evaluate(); err != nil {
		t.Fatal(err)
	}
	var alerts []model.BalanceAlert
	db.Find(&alerts)
	if len(alerts) != 1 || alerts[0].ICCID != "A" || alerts[0].Kind != model.BalanceAlertLow {
		t.Fatalf("alerts = %+v", alerts)
	}
	if items[0].SlotNumber == nil || *items[0].SlotNumber != 7 {
		t.Fatalf("slot = %v, want 7 from modem_bays", items[0].SlotNumber)
	}
	if got := alertText(items[0]); got != "⚠️ SIM 0900000001 (khe 7): số dư 10.000 đ, dưới ngưỡng 20.000 đ" {
		t.Fatalf("text = %q", got)
	}
}

func TestEvaluateForecastAlert(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.ModemBay{}, &model.Webhook{}, &model.BalanceSnapshot{}, &model.BalanceAlert{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	db.Create(&model.Modem{ICCID: "C", BalanceVND: 30000, BalanceUpdatedAt: &now})
	for i, v := range []int64{60000, 45000, 30000} {
		db.Create(&model.BalanceSnapshot{ICCID: "C", BalanceVND: v, ReadAt: now.Add(time.Duration(i-2) * 24 * time.Hour)})
	}
	s := NewScheduler(db, worker.NewManager(db), logic.NewWebhookService(repository.NewWebhookRepository(db)),
		config.BalanceConfig{LowThresholdVND: 20000, ForecastDays: 3})
	items, err := s.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Level != LevelForecast || items[0].DaysLeft == nil {
		t.Fatalf("items = %+v", items)
	}
	var alerts []model.BalanceAlert
	db.Find(&alerts)
	if len(alerts) != 1 || alerts[0].Kind != model.BalanceAlertForecast || alerts[0].DaysLeft == nil {
		t.Fatalf("alerts = %+v", alerts)
	}
	if got := alertText(items[0]); !strings.Contains(got, "dự kiến hết tiền sau ≈2 ngày") {
		t.Fatalf("text = %q", got)
	}
}

func TestNextCheck(t *testing.T) {
	now := time.Date(2026, 9, 16, 10, 30, 0, 0, time.Local)
	if got := nextCheck(now, 8); got != time.Date(2026, 9, 17, 8, 0, 0, 0, time.Local) {
		t.Fatalf("passed hour: %v", got)
	}
	if got := nextCheck(now, 15); got != time.Date(2026, 9, 16, 15, 0, 0, 0, time.Local) {
		t.Fatalf("ahead hour: %v", got)
	}
	if got := nextCheck(now, 0); got != time.Date(2026, 9, 17, 0, 0, 0, 0, time.Local) {
		t.Fatalf("midnight: %v", got)
	}
}
