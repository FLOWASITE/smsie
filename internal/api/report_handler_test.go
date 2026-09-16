package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/report"
	"gorm.io/gorm"
)

func newReportHandlerTest(t *testing.T) *ReportHandler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.UserModemPermission{}, &model.Modem{}, &model.ModemBay{}, &model.SMS{}, &model.CallRecording{}, &model.BalanceSnapshot{}, &model.SimSlotEvent{}, &model.KeepaliveRun{}, &model.BalanceAlert{}, &model.SimAlert{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.Modem{ICCID: "ICCID-1"})
	db.Create(&model.Modem{ICCID: "ICCID-2"})
	db.Create(&model.SMS{ICCID: "ICCID-2", Phone: "1", Type: "received", Timestamp: time.Date(2026, 9, 5, 0, 0, 0, 0, time.Local)})
	return NewReportHandler(db)
}

func TestReportMonthlyFiltersByPermission(t *testing.T) {
	h := newReportHandlerTest(t)
	rec := balanceGet(h.Monthly, "/api/v1/reports/monthly?month=2026-09", &model.User{ID: 7, Role: "user", AllowedModems: "ICCID-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var rep report.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Month != "2026-09" || len(rep.Rows) != 1 || rep.Rows[0].ICCID != "ICCID-1" || rep.Totals.SMSReceived != 0 {
		t.Fatalf("rep = %+v", rep)
	}
	rec = balanceGet(h.Monthly, "/api/v1/reports/monthly?month=2026-09", &model.User{ID: 1, Role: "admin"})
	json.Unmarshal(rec.Body.Bytes(), &rep)
	if len(rep.Rows) != 2 || rep.Totals.SMSReceived != 1 {
		t.Fatalf("admin rep = %+v", rep)
	}
	if rec = balanceGet(h.Monthly, "/api/v1/reports/monthly?month=2026-13", &model.User{ID: 1, Role: "admin"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("tháng sai phải 400, được %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/reports/monthly", nil)
	c.Set("user", &model.User{ID: 1, Role: "admin"})
	c.Set("api_key", &model.APIKey{ID: 1, CanViewSMS: false})
	h.Monthly(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("api key không có view_sms phải 403, được %d", rec.Code)
	}
}

func TestReportMonthlyCSV(t *testing.T) {
	h := newReportHandlerTest(t)
	rec := balanceGet(h.Monthly, "/api/v1/reports/monthly?month=2026-09&format=csv", &model.User{ID: 1, Role: "admin"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Disposition") != "attachment; filename=smsie-bao-cao-2026-09.csv" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	if !strings.HasPrefix(rec.Body.String(), "\ufeffICCID,") || !strings.Contains(rec.Body.String(), "ICCID-2,,,1,0,0") {
		t.Fatalf("csv: %q", rec.Body.String())
	}
}
