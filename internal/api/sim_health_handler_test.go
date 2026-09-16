package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/logic"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"github.com/pccr10001/smsie/internal/simhealth"
	"gorm.io/gorm"
)

func newSimHealthHandlerTest(t *testing.T) (*SimHealthHandler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Modem{}, &model.SMS{}, &model.ModemBay{}, &model.SimSlotEvent{}, &model.Webhook{}, &model.UserModemPermission{}, &model.SimAlert{}); err != nil {
		t.Fatal(err)
	}
	seen := time.Now().Add(-40 * 24 * time.Hour)
	slot := 1
	db.Create(&model.Modem{ICCID: "ICCID-1", FirstSeenAt: &seen}) // im 40 ngày, đang cắm → no_sms
	db.Create(&model.ModemBay{IMEI: "I-1", SlotNumber: &slot, CurrentICCID: "ICCID-1"})
	db.Create(&model.Modem{ICCID: "ICCID-2", FirstSeenAt: &seen}) // rút 10 ngày → absent
	db.Create(&model.SimSlotEvent{ICCID: "ICCID-2", Event: model.SlotEventRemoved, DetectedAt: time.Now().Add(-10 * 24 * time.Hour)})
	svc := simhealth.NewService(db, nil, logic.NewWebhookService(repository.NewWebhookRepository(db)),
		config.SimHealthConfig{Enabled: true, NoSMSDays: 30, UnregisteredHours: 24, AbsentDays: 7, RemindDays: 7})
	return NewSimHealthHandler(db, svc), db
}

func TestSimHealthStatusFiltersByPermission(t *testing.T) {
	h, _ := newSimHealthHandlerTest(t)
	rec := balanceGet(h.Status, "/api/v1/sim-health", &model.User{ID: 7, Role: "user", AllowedModems: "ICCID-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var items []simhealth.Item
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ICCID != "ICCID-1" || !items[0].InBay || len(items[0].Findings) != 1 || items[0].Findings[0].Kind != model.SimAlertNoSMS {
		t.Fatalf("items = %+v", items)
	}

	rec = balanceGet(h.Status, "/api/v1/sim-health", &model.User{Role: "admin", AllowedModems: "*"})
	items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].InBay || len(items[1].Findings) != 1 || items[1].Findings[0].Kind != model.SimAlertAbsent {
		t.Fatalf("admin items = %+v", items)
	}
}

func TestSimHealthAlertsAdminOnly(t *testing.T) {
	h, db := newSimHealthHandlerTest(t)
	if rec := balanceGet(h.Alerts, "/api/v1/sim-health/alerts", &model.User{ID: 7, Role: "user", AllowedModems: "*"}); rec.Code != http.StatusForbidden {
		t.Fatalf("alerts non-admin status %d", rec.Code)
	}
	db.Create(&model.SimAlert{ICCID: "ICCID-1", Kind: model.SimAlertNoSMS, Detail: "x", SentAt: time.Now()})
	rec := balanceGet(h.Alerts, "/api/v1/sim-health/alerts?page_size=10", &model.User{Role: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("alerts admin status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data  []model.SimAlert `json:"data"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Data) != 1 || body.Data[0].Kind != model.SimAlertNoSMS {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
