package audit

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	sql.SetMaxOpenConns(1) // :memory: mỗi kết nối là một DB riêng; goroutine ghi phải dùng chung
	return db
}

func router(db *gorm.DB, user *model.User, key *model.APIKey) *gin.Engine {
	r := gin.New()
	g := r.Group("/api/v1")
	g.Use(func(c *gin.Context) {
		if user != nil {
			c.Set("user", user)
		}
		if key != nil {
			c.Set("api_key", key)
		}
	})
	g.Use(Middleware(db))
	g.POST("/modems/:iccid/send", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.String(http.StatusOK, string(body)) // handler vẫn đọc được body sau middleware
	})
	g.GET("/modems", func(c *gin.Context) { c.Status(200) })
	g.POST("/change_password", func(c *gin.Context) { c.Status(http.StatusBadRequest) })
	g.PATCH("/modems/:iccid/profile", func(c *gin.Context) { c.Status(200) })
	g.DELETE("/users/:id", func(c *gin.Context) { c.Status(200) })
	return r
}

func waitRows(t *testing.T, db *gorm.DB, n int64) []model.AuditLog {
	t.Helper()
	var rows []model.AuditLog
	for i := 0; i < 50; i++ {
		db.Order("id").Find(&rows)
		if int64(len(rows)) >= n {
			return rows
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("có %d dòng audit, chờ %d", len(rows), n)
	return nil
}

func TestMiddlewareRecordsPostNotGet(t *testing.T) {
	db := newDB(t)
	r := router(db, &model.User{ID: 7, Username: "alice"}, nil)
	body := `{"phone":"0912345678","message":"` + strings.Repeat("x", 100) + `","password":"s3cret"}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/modems/8984/send", bytes.NewBufferString(body)))
	if rec.Code != 200 || rec.Body.String() != body {
		t.Fatalf("handler không nhận lại body: %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/modems", nil))

	rows := waitRows(t, db, 1)
	time.Sleep(20 * time.Millisecond)
	db.Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("GET không được ghi: %d dòng", len(rows))
	}
	e := rows[0]
	if e.Action != "sms.send" || e.ICCID != "8984" || e.Target != "0912345678" || e.Username != "alice" || e.UserID == nil || *e.UserID != 7 || e.Status != 200 {
		t.Fatalf("entry = %+v", e)
	}
	if strings.Contains(e.Detail, "s3cret") || strings.Contains(e.Detail, "password") || strings.Contains(e.Detail, strings.Repeat("x", 61)) {
		t.Fatalf("detail chưa rút gọn: %s", e.Detail)
	}
}

func TestMiddlewareKeepsFullBodyOver4KB(t *testing.T) {
	db := newDB(t)
	r := router(db, &model.User{ID: 1, Username: "a"}, nil)
	body := strings.Repeat("y", 3*maxBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/modems/1/send", bytes.NewBufferString(body)))
	if rec.Body.String() != body {
		t.Fatalf("handler nhận %d byte, gửi %d", rec.Body.Len(), len(body))
	}
	if e := waitRows(t, db, 1)[0]; e.Detail != "" {
		t.Fatalf("body cắt 4 KB không phải JSON → detail phải rỗng: %q", e.Detail)
	}
}

func TestMiddlewareStatusApiKeyAndFallback(t *testing.T) {
	db := newDB(t)
	r := router(db, &model.User{ID: 1, Username: "bob"}, &model.APIKey{ID: 3, Name: "n8n"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/change_password", bytes.NewBufferString(`{"old_password":"a","new_password":"b"}`)))
	waitRows(t, db, 1) // Record chạy trong goroutine: chờ từng dòng để thứ tự id ổn định
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, "/api/v1/users/42", nil))
	waitRows(t, db, 2)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPatch, "/api/v1/modems/89/profile", bytes.NewBufferString(`{"phone_number":"09","balance_vnd":5}`)))
	rows := waitRows(t, db, 3)
	if rows[0].Action != "auth.password" || rows[0].Status != 400 || rows[0].Detail != "" || rows[0].Username != "apikey:n8n" || rows[0].APIKeyID == nil || *rows[0].APIKeyID != 3 {
		t.Fatalf("password entry = %+v", rows[0])
	}
	if rows[1].Action != "user.delete" || rows[1].Target != "42" {
		t.Fatalf("delete entry = %+v", rows[1])
	}
	if rows[2].Action != "modem.profile" || rows[2].Detail != `{"balance_vnd":5,"phone_number":"09"}` {
		t.Fatalf("profile entry = %+v", rows[2])
	}
}

func TestActionFor(t *testing.T) {
	cases := map[[2]string]string{
		{"POST", "/api/v1/modems/:iccid/send"}:          "sms.send",
		{"POST", "/api/v1/modems/:iccid/call/dial"}:     "call.dial",
		{"POST", "/api/v1/modems/:iccid/call/hangup"}:   "call.hangup",
		{"POST", "/api/v1/modems/:iccid/at"}:            "at.exec",
		{"POST", "/api/v1/modems/:iccid/balance-check"}: "balance.check",
		{"POST", "/api/v1/modems/:iccid/phone-lookup"}:  "phone.lookup",
		{"POST", "/api/v1/modems/:iccid/reboot"}:        "modem.reboot",
		{"PATCH", "/api/v1/modems/:iccid/profile"}:      "modem.profile",
		{"PATCH", "/api/v1/bays/:imei"}:                 "bay.assign",
		{"POST", "/api/v1/balance/run"}:                 "balance.run",
		{"POST", "/api/v1/keepalive/run"}:               "keepalive.run",
		{"POST", "/api/v1/users"}:                       "user.create",
		{"DELETE", "/api/v1/users/:id"}:                 "user.delete",
		{"PUT", "/api/v1/users/:id/permissions"}:        "user.permissions",
		{"POST", "/api/v1/apikeys"}:                     "apikey.create",
		{"POST", "/api/v1/apikeys/:id/rotate"}:          "apikey.rotate",
		{"DELETE", "/api/v1/apikeys/:id"}:               "apikey.delete",
		{"POST", "/api/v1/webhooks"}:                    "webhook.create",
		{"DELETE", "/api/v1/webhooks/:id"}:              "webhook.delete",
		{"POST", "/api/v1/change_password"}:             "auth.password",
		{"POST", "/api/v1/modems/:iccid/scan"}:          "POST /api/v1/modems/:iccid/scan",
	}
	for in, want := range cases {
		if got := actionFor(in[0], in[1]); got != want {
			t.Errorf("actionFor(%s %s) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestScrub(t *testing.T) {
	got := scrub([]byte(`{"name":"k","api_key":"x","Token":"y","client_secret":"z","password":"p","content":"` + strings.Repeat("a", 70) + `","n":1}`))
	want := `{"content":"` + strings.Repeat("a", 60) + `…","n":1,"name":"k"}`
	if got != want {
		t.Fatalf("scrub = %s", got)
	}
	if scrub([]byte("not json")) != "" || scrub(nil) != "" {
		t.Fatal("non-JSON phải trả rỗng")
	}
}

func TestRecordAndPrune(t *testing.T) {
	db := newDB(t)
	if err := Record(db, Entry{Username: "system", Action: "keepalive.send", ICCID: "89", Target: "0912", Status: 200}); err != nil {
		t.Fatal(err)
	}
	db.Model(&model.AuditLog{}).Where("1=1").Update("at", time.Now().AddDate(0, 0, -400))
	Record(db, Entry{Username: "system", Action: "keepalive.send", Status: 500})
	if err := Prune(db, 365); err != nil {
		t.Fatal(err)
	}
	var rows []model.AuditLog
	db.Find(&rows)
	if len(rows) != 1 || rows[0].Status != 500 || rows[0].At.IsZero() {
		t.Fatalf("rows = %+v", rows)
	}
}
