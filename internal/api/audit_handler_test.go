package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/audit"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func auditList(t *testing.T, db *gorm.DB, user *model.User, query string) (int, map[string]interface{}) {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/audit"+query, nil)
	ctx.Set("user", user)
	NewAuditHandler(db).List(ctx)
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestAuditListAdminOnlyAndFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 1, 5, 10, 0, 0, 0, time.Local)
	for _, e := range []audit.Entry{
		{Username: "alice", Action: "sms.send", ICCID: "A", Status: 200, At: old},
		{Username: "alice", Action: "sms.send", ICCID: "B", Status: 500},
		{Username: "apikey:n8n", Action: "call.dial", ICCID: "A", Status: 200},
		{Username: "system", Action: "keepalive.send", ICCID: "B", Status: 200},
	} {
		if err := audit.Record(db, e); err != nil {
			t.Fatal(err)
		}
	}
	if code, _ := auditList(t, db, &model.User{Role: "user"}, ""); code != http.StatusForbidden {
		t.Fatalf("non-admin = %d", code)
	}
	admin := &model.User{Role: "admin"}
	check := func(query string, want int, wantTotal float64) {
		t.Helper()
		code, body := auditList(t, db, admin, query)
		if code != 200 || len(body["data"].([]interface{})) != want || body["total"].(float64) != wantTotal {
			t.Fatalf("%s → %d %v", query, code, body)
		}
	}
	check("", 4, 4)
	check("?iccid=A", 2, 2)
	check("?username=alice&action=sms.send", 2, 2)
	check("?action=keepalive.send", 1, 1)
	check("?from=2026-01-05&to=2026-01-05", 1, 1)
	check("?to=2026-01-04", 0, 0)
	check("?page_size=1&page=2", 1, 4)
	if code, _ := auditList(t, db, admin, "?from=bad"); code != http.StatusBadRequest {
		t.Fatalf("bad from = %d", code)
	}
}
