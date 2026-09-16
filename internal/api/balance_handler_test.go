package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/balance"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/logic"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"github.com/pccr10001/smsie/internal/worker"
	"gorm.io/gorm"
)

func newBalanceHandlerTest(t *testing.T) (*BalanceHandler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Modem{}, &model.ModemBay{}, &model.Webhook{}, &model.UserModemPermission{}, &model.BalanceSnapshot{}, &model.BalanceAlert{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	db.Create(&model.Modem{ICCID: "ICCID-1", BalanceVND: 5000, BalanceUpdatedAt: &now})
	db.Create(&model.Modem{ICCID: "ICCID-2", BalanceVND: 50000, BalanceUpdatedAt: &now})
	sched := balance.NewScheduler(db, worker.NewManager(db), logic.NewWebhookService(repository.NewWebhookRepository(db)), config.BalanceConfig{LowThresholdVND: 20000, ForecastDays: 3})
	h := NewBalanceHandler(db, sched)
	h.evalDelay = 0
	return h, db
}

func balanceGet(h func(*gin.Context), path string, user *model.User) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Set("user", user)
	h(c)
	return rec
}

func TestBalanceStatusFiltersByPermission(t *testing.T) {
	h, _ := newBalanceHandlerTest(t)
	rec := balanceGet(h.Status, "/api/v1/balance/status", &model.User{ID: 7, Role: "user", AllowedModems: "ICCID-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var items []balance.StatusItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ICCID != "ICCID-1" || items[0].Level != balance.LevelLow || items[0].ThresholdVND != 20000 {
		t.Fatalf("items = %+v", items)
	}

	rec = balanceGet(h.Status, "/api/v1/balance/status", &model.User{Role: "admin", AllowedModems: "*"})
	items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].Level != balance.LevelOK {
		t.Fatalf("admin items = %+v", items)
	}
}

func TestBalanceAlertsAndRunNowAdminOnly(t *testing.T) {
	h, db := newBalanceHandlerTest(t)
	user := &model.User{ID: 7, Role: "user", AllowedModems: "*"}
	if rec := balanceGet(h.Alerts, "/api/v1/balance/alerts", user); rec.Code != http.StatusForbidden {
		t.Fatalf("alerts non-admin status %d", rec.Code)
	}
	if rec := balanceGet(h.RunNow, "/api/v1/balance/run", user); rec.Code != http.StatusForbidden {
		t.Fatalf("run non-admin status %d", rec.Code)
	}
	db.Create(&model.BalanceAlert{ICCID: "ICCID-1", Kind: model.BalanceAlertLow, BalanceVND: 5000, SentAt: time.Now()})
	rec := balanceGet(h.Alerts, "/api/v1/balance/alerts?page_size=10", &model.User{Role: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("alerts admin status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data  []model.BalanceAlert `json:"data"`
		Total int64                `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Data) != 1 || body.Data[0].Kind != model.BalanceAlertLow {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if rec := balanceGet(h.RunNow, "/api/v1/balance/run", &model.User{Role: "admin"}); rec.Code != http.StatusAccepted || rec.Body.String() != `{"requested":0}` {
		t.Fatalf("run admin status %d: %s", rec.Code, rec.Body.String())
	}
	time.Sleep(50 * time.Millisecond) // để goroutine Evaluate (evalDelay=0) xong trước khi DB in-memory đóng
}
