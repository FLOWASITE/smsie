package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/worker"
	"gorm.io/gorm"
)

func TestUpdateModemProfilePersistsPhoneHardwarePathAndBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}); err != nil {
		t.Fatal(err)
	}
	const iccid = "89840509241455299254"
	if err := db.Create(&model.Modem{ICCID: iccid, IMEI: "350730720214699"}).Error; err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/modems/"+iccid+"/profile", bytes.NewBufferString(`{"phone_number":"0924875662","hardware_path":"USB\\VID_04E2&PID_1414\\PORT16","balance_vnd":25000}`))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Params = gin.Params{{Key: "iccid", Value: iccid}}
	context.Set("user", &model.User{Role: "admin"})

	NewModemHandler(db, worker.NewManager(db), nil).UpdateProfile(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var modem model.Modem
	if err := db.First(&modem, "iccid = ?", iccid).Error; err != nil {
		t.Fatal(err)
	}
	if modem.PhoneNumber != "0924875662" || modem.HardwarePath == "" || modem.BalanceVND != 25000 {
		t.Fatalf("profile = %#v", modem)
	}
}

func TestCheckBalanceRejectsOfflineModem(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.UserModemPermission{}); err != nil {
		t.Fatal(err)
	}
	const iccid = "89840509241455299254"
	if err := db.Create(&model.Modem{ICCID: iccid}).Error; err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/modems/"+iccid+"/balance-check", nil)
	context.Params = gin.Params{{Key: "iccid", Value: iccid}}
	context.Set("user", &model.User{Role: "admin", AllowedModems: "*"})

	NewModemHandler(db, worker.NewManager(db), nil).CheckBalance(context)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateModemProfileLowBalanceVND(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}); err != nil {
		t.Fatal(err)
	}
	const iccid = "89840509241455299254"
	if err := db.Create(&model.Modem{ICCID: iccid}).Error; err != nil {
		t.Fatal(err)
	}
	h := NewModemHandler(db, worker.NewManager(db), nil)
	patch := func(body string) int {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/modems/"+iccid+"/profile", bytes.NewBufferString(body))
		context.Request.Header.Set("Content-Type", "application/json")
		context.Params = gin.Params{{Key: "iccid", Value: iccid}}
		context.Set("user", &model.User{Role: "admin"})
		h.UpdateProfile(context)
		return recorder.Code
	}
	low := func() *int64 {
		var m model.Modem
		db.First(&m, "iccid = ?", iccid)
		return m.LowBalanceVND
	}
	if code := patch(`{"low_balance_vnd":15000}`); code != http.StatusOK || low() == nil || *low() != 15000 {
		t.Fatalf("set: code=%d low=%v", code, low())
	}
	if code := patch(`{"phone_number":"0924875662"}`); code != http.StatusOK || low() == nil {
		t.Fatalf("absent key must keep value: code=%d low=%v", code, low())
	}
	if code := patch(`{"low_balance_vnd":-1}`); code != http.StatusBadRequest {
		t.Fatalf("negative: code=%d", code)
	}
	if code := patch(`{"low_balance_vnd":null}`); code != http.StatusOK || low() != nil {
		t.Fatalf("null must reset to NULL: code=%d low=%v", code, low())
	}
}
