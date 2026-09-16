package balance

import (
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
	if err := db.AutoMigrate(&model.Modem{}, &model.Webhook{}, &model.BalanceSnapshot{}, &model.BalanceAlert{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	db.Create(&model.Modem{ICCID: "A", PhoneNumber: "0900000001", BalanceVND: 10000, BalanceUpdatedAt: &now})
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
	if got := alertText(items[0]); got != "⚠️ SIM 0900000001: số dư 10.000 đ, dưới ngưỡng 20.000 đ" {
		t.Fatalf("text = %q", got)
	}
}
