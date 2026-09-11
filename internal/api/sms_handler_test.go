package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func TestListSMSFiltersByType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SMS{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create([]model.SMS{
		{ICCID: "sim-1", Phone: "+841", Content: "in", Timestamp: now, Type: "received"},
		{ICCID: "sim-1", Phone: "+842", Content: "out", Timestamp: now, Type: "sent"},
	}).Error; err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/sms?type=sent", nil)
	context.Set("user", &model.User{Role: "admin"})
	NewSMSHandler(db).ListSMS(context)

	var response struct {
		Data []model.SMS `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].Type != "sent" {
		t.Fatalf("filtered messages = %#v", response.Data)
	}
}
