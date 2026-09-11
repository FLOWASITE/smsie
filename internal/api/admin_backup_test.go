package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func TestDownloadBackupReturnsValidSQLiteSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Modem{ICCID: "sim-1"}).Error; err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/backup", nil)
	context.Set("user", &model.User{Role: "admin"})

	NewAdminBackupHandler(db, "sqlite").Download(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Body.String(), "SQLite format 3") {
		t.Fatal("response is not a SQLite database")
	}
}
