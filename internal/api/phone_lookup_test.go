package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/internal/worker"
	"gorm.io/gorm"
)

func newPhoneTestDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.UserModemPermission{}, &model.PhoneNumberHistory{}); err != nil {
		t.Fatal(err)
	}
	const iccid = "89840509241455299254"
	if err := db.Create(&model.Modem{ICCID: iccid}).Error; err != nil {
		t.Fatal(err)
	}
	return db, iccid
}

func TestPhoneLookupOfflineModemIs409(t *testing.T) {
	db, iccid := newPhoneTestDB(t)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/modems/"+iccid+"/phone-lookup", nil)
	ctx.Params = gin.Params{{Key: "iccid", Value: iccid}}
	ctx.Set("user", &model.User{Role: "admin", AllowedModems: "*"})

	NewModemHandler(db, worker.NewManager(db), nil).PhoneLookup(ctx)
	if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("modem offline")) {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestPhoneHistoryReturnsManualChangesFromProfile(t *testing.T) {
	db, iccid := newPhoneTestDB(t)
	h := NewModemHandler(db, worker.NewManager(db), nil)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/modems/"+iccid+"/profile", bytes.NewBufferString(`{"phone_number":"0912345678"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "iccid", Value: iccid}}
	ctx.Set("user", &model.User{Role: "admin"})
	h.UpdateProfile(ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("profile status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/modems/"+iccid+"/phone-history", nil)
	ctx.Params = gin.Params{{Key: "iccid", Value: iccid}}
	ctx.Set("user", &model.User{Role: "admin", AllowedModems: "*"})
	h.PhoneHistory(ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("history status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []model.PhoneNumberHistory `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].NewPhone != "0912345678" || body.Data[0].Source != "manual" {
		t.Fatalf("history = %+v", body.Data)
	}
}
