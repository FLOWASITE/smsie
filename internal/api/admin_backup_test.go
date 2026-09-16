package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/backup"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/pkg/logger"
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

	NewAdminBackupHandler(db, "sqlite", "", config.BackupConfig{}).Download(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Body.String(), "SQLite format 3") {
		t.Fatal("response is not a SQLite database")
	}
}

func newBackupHandlerTest(t *testing.T) (*AdminBackupHandler, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger.InitLogger("error")
	tmp := t.TempDir()
	dsn := filepath.Join(tmp, "live.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}, &model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.Modem{ICCID: "sim-1"})
	t.Cleanup(func() { s, _ := db.DB(); s.Close() }) // Windows: TempDir không xoá được file đang mở
	return NewAdminBackupHandler(db, "sqlite", dsn, config.BackupConfig{Enabled: true, Dir: filepath.Join(tmp, "backups"), Hour: 3, Keep: 14}), dsn
}

func adminReq(h gin.HandlerFunc, method, url string, body io.Reader, contentType string, params ...gin.Param) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, url, body)
	if contentType != "" {
		c.Request.Header.Set("Content-Type", contentType)
	}
	c.Params = params
	c.Set("user", &model.User{Role: "admin"})
	h(c)
	return rec
}

func TestBackupRunListGet(t *testing.T) {
	h, _ := newBackupHandlerTest(t)
	rec := adminReq(h.RunNow, http.MethodPost, "/api/v1/admin/backups/run", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("run: %d %s", rec.Code, rec.Body.String())
	}
	var res backup.Result
	json.Unmarshal(rec.Body.Bytes(), &res)
	rec = adminReq(h.List, http.MethodGet, "/api/v1/admin/backups", nil, "")
	var list struct {
		Items    []backup.Info       `json:"items"`
		Schedule config.BackupConfig `json:"schedule"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list.Items) != 1 || list.Items[0].Name != res.Name || list.Schedule.Hour != 3 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{"../live.db", "smsie-2020.db", "x.db", "smsie-20200101-000000.db.tmp"} {
		if rec = adminReq(h.Get, http.MethodGet, "/api/v1/admin/backups/x", nil, "", gin.Param{Key: "name", Value: bad}); rec.Code != http.StatusBadRequest {
			t.Fatalf("tên %q phải 400, được %d", bad, rec.Code)
		}
	}
	if rec = adminReq(h.Get, http.MethodGet, "/api/v1/admin/backups/x", nil, "", gin.Param{Key: "name", Value: "smsie-20200101-000000.db"}); rec.Code != http.StatusNotFound {
		t.Fatalf("không có file phải 404, được %d", rec.Code)
	}
	rec = adminReq(h.Get, http.MethodGet, "/api/v1/admin/backups/x", nil, "", gin.Param{Key: "name", Value: res.Name})
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "SQLite format 3") {
		t.Fatalf("get: %d", rec.Code)
	}
	var n int64
	h.db.Model(&model.AuditLog{}).Where("action = ?", "backup.ok").Count(&n)
	if n != 1 {
		t.Fatalf("audit backup.ok = %d", n)
	}
}

func multipartDB(t *testing.T, content []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "up.db")
	part.Write(content)
	w.Close()
	return &buf, w.FormDataContentType()
}

func TestRestoreRejectsGarbageAndStagesValid(t *testing.T) {
	h, dsn := newBackupHandlerTest(t)
	body, ct := multipartDB(t, []byte("SQLite format 3\x00 rác"))
	if rec := adminReq(h.Restore, http.MethodPost, "/api/v1/admin/restore", body, ct); rec.Code != http.StatusBadRequest {
		t.Fatalf("file rác phải 400: %d %s", rec.Code, rec.Body.String())
	}
	if rec := adminReq(h.Restore, http.MethodPost, "/api/v1/admin/restore", strings.NewReader(`{"name":"../live.db"}`), "application/json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("tên bẩn phải 400: %d", rec.Code)
	}
	if _, err := os.Stat(backup.StagePath(dsn)); err == nil {
		t.Fatal("không được tạo staging khi lỗi")
	}

	rec := adminReq(h.RunNow, http.MethodPost, "/api/v1/admin/backups/run", nil, "")
	var res backup.Result
	json.Unmarshal(rec.Body.Bytes(), &res)
	rec = adminReq(h.Restore, http.MethodPost, "/api/v1/admin/restore", strings.NewReader(`{"name":"`+res.Name+`"}`), "application/json")
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"restart_required":true`) {
		t.Fatalf("restore theo tên: %d %s", rec.Code, rec.Body.String())
	}
	if err := backup.Integrity(backup.StagePath(dsn)); err != nil {
		t.Fatalf("staging phải là DB hợp lệ: %v", err)
	}

	raw, _ := os.ReadFile(res.Path)
	body, ct = multipartDB(t, raw)
	if rec = adminReq(h.Restore, http.MethodPost, "/api/v1/admin/restore", body, ct); rec.Code != http.StatusAccepted {
		t.Fatalf("restore upload: %d %s", rec.Code, rec.Body.String())
	}
}
