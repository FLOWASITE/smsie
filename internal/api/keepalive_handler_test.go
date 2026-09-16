package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/keepalive"
	"github.com/pccr10001/smsie/internal/logic"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/repository"
	"gorm.io/gorm"
)

type recSender struct{ calls int }

func (r *recSender) SendSMS(iccid, phone, msg string) error { r.calls++; return nil }

func newKeepaliveHandlerTest(t *testing.T, enabled bool) (*KeepaliveHandler, *recSender) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Modem{}, &model.ModemBay{}, &model.SMS{}, &model.Webhook{}, &model.UserModemPermission{}, &model.SimAlert{}, &model.KeepaliveRun{}); err != nil {
		t.Fatal(err)
	}
	seen := time.Now().Add(-40 * 24 * time.Hour)
	db.Create(&model.Modem{ICCID: "ICCID-1", PhoneNumber: "0900000001", FirstSeenAt: &seen, KeepaliveEnabled: true})
	db.Create(&model.Modem{ICCID: "ICCID-2", PhoneNumber: "0900000002", FirstSeenAt: &seen})
	s1, s2 := 1, 2
	db.Create(&model.ModemBay{IMEI: "I-1", SlotNumber: &s1, CurrentICCID: "ICCID-1"})
	db.Create(&model.ModemBay{IMEI: "I-2", SlotNumber: &s2, CurrentICCID: "ICCID-2"})
	rs := &recSender{}
	svc := keepalive.NewService(db, nil, rs, logic.NewWebhookService(repository.NewWebhookRepository(db)),
		config.KeepaliveConfig{Enabled: enabled, IntervalDays: 25, MaxPerMonth: 3, Message: "keepalive {{.Date}}", RunHour: 7})
	return NewKeepaliveHandler(db, svc), rs
}

func keepaliveReq(h func(*gin.Context), method, path, body string, user *model.User) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)
	h(c)
	return rec
}

func TestKeepaliveStatusFiltersByPermissionAndReturnsConfig(t *testing.T) {
	h, _ := newKeepaliveHandlerTest(t, false)
	var resp struct {
		Config struct {
			Enabled      bool `json:"enabled"`
			RunHour      int  `json:"run_hour"`
			IntervalDays int  `json:"interval_days"`
			MaxPerMonth  int  `json:"max_per_month"`
		} `json:"config"`
		Items []keepalive.Item `json:"items"`
	}
	rec := keepaliveReq(h.Status, http.MethodGet, "/api/v1/keepalive/status", "", &model.User{ID: 7, Role: "user", AllowedModems: "ICCID-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ICCID != "ICCID-1" || !resp.Items[0].Enabled || *resp.Items[0].SlotNumber != 1 || resp.Items[0].IntervalDays != 25 || resp.Items[0].NextDueAt == nil {
		t.Fatalf("items = %+v", resp.Items)
	}
	if resp.Config.Enabled || resp.Config.RunHour != 7 || resp.Config.IntervalDays != 25 || resp.Config.MaxPerMonth != 3 {
		t.Fatalf("config = %+v", resp.Config)
	}
	rec = keepaliveReq(h.Status, http.MethodGet, "/api/v1/keepalive/status", "", &model.User{Role: "admin", AllowedModems: "*"})
	resp.Items = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 2 || resp.Items[1].Enabled {
		t.Fatalf("admin items = %+v", resp.Items)
	}
}

func TestKeepaliveRunsAndRunNowAdminOnly(t *testing.T) {
	h, _ := newKeepaliveHandlerTest(t, true)
	user := &model.User{ID: 7, Role: "user", AllowedModems: "*"}
	if rec := keepaliveReq(h.Runs, http.MethodGet, "/api/v1/keepalive/runs", "", user); rec.Code != http.StatusForbidden {
		t.Fatalf("runs non-admin status %d", rec.Code)
	}
	if rec := keepaliveReq(h.RunNow, http.MethodPost, "/api/v1/keepalive/run", "{}", user); rec.Code != http.StatusForbidden {
		t.Fatalf("run non-admin status %d", rec.Code)
	}
}

func TestKeepaliveRunNowDisabledIs409(t *testing.T) {
	h, rs := newKeepaliveHandlerTest(t, false)
	rec := keepaliveReq(h.RunNow, http.MethodPost, "/api/v1/keepalive/run", `{"iccid":"ICCID-1"}`, &model.User{Role: "admin"})
	if rec.Code != http.StatusConflict || rs.calls != 0 {
		t.Fatalf("status %d calls %d: %s", rec.Code, rs.calls, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "keepalive.enabled=false trong config.yaml" {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestKeepaliveRunNowWithICCIDReturnsRun(t *testing.T) {
	h, rs := newKeepaliveHandlerTest(t, true)
	admin := &model.User{Role: "admin"}
	rec := keepaliveReq(h.RunNow, http.MethodPost, "/api/v1/keepalive/run", `{"iccid":"ICCID-1"}`, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var run model.KeepaliveRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != model.KeepaliveSent || run.TargetICCID != "ICCID-2" || run.TargetPhone != "0900000002" || rs.calls != 1 {
		t.Fatalf("run = %+v calls = %d", run, rs.calls)
	}
	if rec := keepaliveReq(h.RunNow, http.MethodPost, "/api/v1/keepalive/run", `{"iccid":"NOPE"}`, admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown iccid status %d", rec.Code)
	}
	if rec := keepaliveReq(h.RunNow, http.MethodPost, "/api/v1/keepalive/run", `{"iccid":"ICCID-2"}`, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("SIM chưa bật: status %d: %s", rec.Code, rec.Body.String())
	}
	rec = keepaliveReq(h.Runs, http.MethodGet, "/api/v1/keepalive/runs?iccid=ICCID-1", "", admin)
	var list struct {
		Data  []model.KeepaliveRun `json:"data"`
		Total int64                `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || list.Total != 1 || len(list.Data) != 1 || list.Data[0].ICCID != "ICCID-1" {
		t.Fatalf("runs: %d %s", rec.Code, rec.Body.String())
	}
	if rec := keepaliveReq(h.RunNow, http.MethodPost, "/api/v1/keepalive/run", "", admin); rec.Code != http.StatusAccepted {
		t.Fatalf("run all status %d: %s", rec.Code, rec.Body.String())
	}
}
