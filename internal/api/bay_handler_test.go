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
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/worker"
	"gorm.io/gorm"
)

func newBayHandlerTest(t *testing.T) (*BayHandler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Modem{}, &model.ModemBay{}, &model.SimSlotEvent{}, &model.UserModemPermission{}); err != nil {
		t.Fatal(err)
	}
	return NewBayHandler(db, worker.NewManager(db)), db
}

func intptr(n int) *int { return &n }

func TestListBaysIncludesRuntimeAndUnassigned(t *testing.T) {
	h, db := newBayHandlerTest(t)
	db.Create(&model.Modem{ICCID: "ICCID-1", IMEI: "IMEI-A", PhoneNumber: "0987654321", BalanceVND: 48500})
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intptr(16), CurrentICCID: "ICCID-1"})
	db.Create(&model.ModemBay{IMEI: "IMEI-Z"})
	db.Create(&model.SimSlotEvent{ICCID: "ICCID-1", Event: model.SlotEventInserted, DetectedAt: time.Now(), ToSlot: intptr(16)})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bays", nil)
	c.Set("user", &model.User{Role: "admin", AllowedModems: "*"})
	h.List(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body []bayView
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 || *body[0].SlotNumber != 16 || body[0].PhoneNumber != "0987654321" || body[0].BalanceVND != 48500 || body[0].Status != "offline" {
		t.Fatalf("body = %+v", body)
	}
	if body[0].LastEventAt == nil {
		t.Fatalf("last_event_at must be filled from sim_slot_events: %+v", body[0])
	}
	if body[1].SlotNumber != nil || body[1].Status != "empty" {
		t.Fatalf("unassigned bay must be last and empty: %+v", body[1])
	}
}

func TestListBaysHidesInaccessibleSimsFromNonAdmin(t *testing.T) {
	h, db := newBayHandlerTest(t)
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intptr(1), CurrentICCID: "ICCID-1"})
	db.Create(&model.ModemBay{IMEI: "IMEI-B", SlotNumber: intptr(2), CurrentICCID: "ICCID-2"})
	db.Create(&model.ModemBay{IMEI: "IMEI-C", SlotNumber: intptr(3)})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bays", nil)
	c.Set("user", &model.User{ID: 7, Role: "user", AllowedModems: "ICCID-1"})
	h.List(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body []bayView
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 || *body[0].SlotNumber != 1 || *body[1].SlotNumber != 3 {
		t.Fatalf("body = %+v", body)
	}
}

func TestAssignBaySlotConflict(t *testing.T) {
	h, db := newBayHandlerTest(t)
	db.Create(&model.ModemBay{IMEI: "IMEI-A", SlotNumber: intptr(15)})
	db.Create(&model.ModemBay{IMEI: "IMEI-B"})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/bays/IMEI-B", bytes.NewBufferString(`{"slot_number":15}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "imei", Value: "IMEI-B"}}
	c.Set("user", &model.User{Role: "admin"})
	h.Assign(c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListSlotEventsRespectsViewPermission(t *testing.T) {
	h, db := newBayHandlerTest(t)
	db.Create(&model.SimSlotEvent{ICCID: "ICCID-1", Event: model.SlotEventMoved, DetectedAt: time.Now(), FromSlot: intptr(15), ToSlot: intptr(16)})
	db.Create(&model.SimSlotEvent{ICCID: "ICCID-2", Event: model.SlotEventInserted, DetectedAt: time.Now(), ToSlot: intptr(3)})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/slot-events?iccid=ICCID-2", nil)
	c.Set("user", &model.User{ID: 7, Role: "user", AllowedModems: "ICCID-1"})
	h.ListEvents(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user without view_sms on ICCID-2 must get 403, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/slot-events?slot=16", nil)
	c.Set("user", &model.User{Role: "admin", AllowedModems: "*"})
	h.ListEvents(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data  []model.SimSlotEvent `json:"data"`
		Total int64                `json:"total"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Total != 1 || body.Data[0].ICCID != "ICCID-1" {
		t.Fatalf("body = %+v", body)
	}
}
