package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/audit"
	"github.com/pccr10001/smsie/internal/auth"
	"github.com/pccr10001/smsie/internal/model"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func newUserEnv(t *testing.T, maxAttempts int) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.User{}, &model.UserModemPermission{}, &model.APIKey{}, &model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	auth.SetSecret([]byte(strings.Repeat("u", auth.MinSecretLen)))
	h := NewUserHandler(db)
	h.maxAttempts = maxAttempts

	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.POST("/api/v1/login", h.Login)
	authed := r.Group("/api/v1", AuthMiddleware(db), audit.Middleware(db))
	authed.POST("/change_password", h.ChangePassword)
	authed.GET("/whoami", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	admin := authed.Group("/", AdminOnly())
	admin.POST("/users", h.CreateUser)
	admin.DELETE("/users/:id", h.DeleteUser)
	return r, db
}

// addUser tạo người dùng với bcrypt cost thấp để test chạy nhanh (cost của hash quyết định thời gian so khớp).
func addUser(t *testing.T, db *gorm.DB, username, password, role string) model.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	u := model.User{Username: username, PasswordHash: string(hash), Role: role}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func call(r *gin.Engine, method, path, token string, body interface{}) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func loginToken(t *testing.T, r *gin.Engine, username, password string) string {
	t.Helper()
	w := call(r, http.MethodPost, "/api/v1/login", "", gin.H{"username": username, "password": password})
	if w.Code != http.StatusOK {
		t.Fatalf("login %s = %d %s", username, w.Code, w.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.Token
}

func TestLoginLocksOutAfterRepeatedFailures(t *testing.T) {
	r, db := newUserEnv(t, 3)
	addUser(t, db, "admin", "dung-mat-khau", "admin")

	for i := 0; i < 3; i++ {
		if w := call(r, http.MethodPost, "/api/v1/login", "", gin.H{"username": "admin", "password": "sai"}); w.Code != http.StatusUnauthorized {
			t.Fatalf("lần sai %d = %d", i+1, w.Code)
		}
	}
	// Đã đủ 3 lần sai: kể cả mật khẩu ĐÚNG cũng bị chặn, có Retry-After.
	w := call(r, http.MethodPost, "/api/v1/login", "", gin.H{"username": "admin", "password": "dung-mat-khau"})
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("sau 3 lần sai = %d, Retry-After = %q", w.Code, w.Header().Get("Retry-After"))
	}
	var failures int64
	db.Model(&model.AuditLog{}).Where("action = ? AND status = 401", "auth.login").Count(&failures)
	if failures != 3 {
		t.Fatalf("nhật ký đăng nhập sai = %d dòng", failures)
	}
}

func TestLoginSuccessResetsFailureCount(t *testing.T) {
	r, db := newUserEnv(t, 3)
	addUser(t, db, "admin", "dung-mat-khau", "admin")
	for round := 0; round < 3; round++ {
		for i := 0; i < 2; i++ {
			call(r, http.MethodPost, "/api/v1/login", "", gin.H{"username": "admin", "password": "sai"})
		}
		loginToken(t, r, "admin", "dung-mat-khau") // 2 sai rồi 1 đúng, lặp lại: không bao giờ chạm trần 3
	}
}

func TestLoginUnknownUserLooksLikeWrongPassword(t *testing.T) {
	r, db := newUserEnv(t, 5)
	addUser(t, db, "admin", "dung-mat-khau", "admin")
	wrong := call(r, http.MethodPost, "/api/v1/login", "", gin.H{"username": "admin", "password": "sai"})
	unknown := call(r, http.MethodPost, "/api/v1/login", "", gin.H{"username": "khong-co", "password": "sai"})
	if wrong.Code != unknown.Code || wrong.Body.String() != unknown.Body.String() {
		t.Fatalf("phản hồi khác nhau làm lộ tài khoản có thật: %d %s vs %d %s", wrong.Code, wrong.Body.String(), unknown.Code, unknown.Body.String())
	}
}

func TestChangePasswordRevokesOldSessions(t *testing.T) {
	r, db := newUserEnv(t, 10)
	addUser(t, db, "an", "mat-khau-cu", "user")
	oldToken := loginToken(t, r, "an", "mat-khau-cu")
	otherDevice := loginToken(t, r, "an", "mat-khau-cu")

	if w := call(r, http.MethodPost, "/api/v1/change_password", oldToken, gin.H{"old_password": "mat-khau-cu", "new_password": "ngan"}); w.Code != http.StatusBadRequest {
		t.Fatalf("mật khẩu mới quá ngắn = %d", w.Code)
	}
	w := call(r, http.MethodPost, "/api/v1/change_password", oldToken, gin.H{"old_password": "mat-khau-cu", "new_password": "mat-khau-moi-123"})
	if w.Code != http.StatusOK {
		t.Fatalf("đổi mật khẩu = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)

	if c := call(r, http.MethodGet, "/api/v1/whoami", otherDevice, nil); c.Code != http.StatusUnauthorized {
		t.Fatalf("phiên ở thiết bị khác vẫn sống sau khi đổi mật khẩu: %d", c.Code)
	}
	if c := call(r, http.MethodGet, "/api/v1/whoami", out.Token, nil); c.Code != http.StatusOK {
		t.Fatalf("token mới trả về không dùng được: %d", c.Code)
	}
	loginToken(t, r, "an", "mat-khau-moi-123")
}

func TestCreateUserValidation(t *testing.T) {
	r, db := newUserEnv(t, 10)
	addUser(t, db, "admin", "dung-mat-khau", "admin")
	token := loginToken(t, r, "admin", "dung-mat-khau")

	bad := []gin.H{
		{"username": "a", "password": ""},                                    // mật khẩu rỗng, không email
		{"username": "a", "password": "ngan"},                                // mật khẩu ngắn
		{"username": "", "password": ""},                                     // không có gì
		{"username": "a", "password": "du-dai-12345", "role": "root"},        // role lạ
		{"username": "a", "password": "du-dai-12345", "email": "khong-phai"}, // email sai
	}
	for _, body := range bad {
		if w := call(r, http.MethodPost, "/api/v1/users", token, body); w.Code != http.StatusBadRequest {
			t.Errorf("%v → %d, muốn 400", body, w.Code)
		}
	}

	// Người chỉ đăng nhập qua Jione: có email, không mật khẩu; tên đăng nhập lấy theo email.
	w := call(r, http.MethodPost, "/api/v1/users", token, gin.H{"email": " Lan@Cty.vn ", "role": "user", "allowed_modems": "8984A"})
	if w.Code != http.StatusOK {
		t.Fatalf("tạo người dùng Jione = %d %s", w.Code, w.Body.String())
	}
	var lan model.User
	db.Where("email = ?", "lan@cty.vn").First(&lan)
	if lan.Username != "lan@cty.vn" || lan.PasswordHash != noPasswordHash || lan.AllowedModems != "8984A" {
		t.Fatalf("lan = %+v", lan)
	}
	if l := call(r, http.MethodPost, "/api/v1/login", "", gin.H{"username": "lan@cty.vn", "password": ""}); l.Code != http.StatusUnauthorized {
		t.Fatalf("tài khoản không mật khẩu mà đăng nhập được bằng mật khẩu rỗng: %d", l.Code)
	}
	if dup := call(r, http.MethodPost, "/api/v1/users", token, gin.H{"username": "khac", "password": "du-dai-12345", "email": "lan@cty.vn"}); dup.Code != http.StatusConflict {
		t.Fatalf("trùng email = %d", dup.Code)
	}
}

func TestDeleteUserFreesNameAndBlocksSelfDelete(t *testing.T) {
	r, db := newUserEnv(t, 10)
	admin := addUser(t, db, "admin", "dung-mat-khau", "admin")
	token := loginToken(t, r, "admin", "dung-mat-khau")

	if w := call(r, http.MethodDelete, "/api/v1/users/1", token, nil); admin.ID != 1 || w.Code != http.StatusBadRequest {
		t.Fatalf("tự xoá mình = %d", w.Code)
	}

	body := gin.H{"username": "tam", "password": "du-dai-12345"}
	if w := call(r, http.MethodPost, "/api/v1/users", token, body); w.Code != http.StatusOK {
		t.Fatalf("tạo = %d %s", w.Code, w.Body.String())
	}
	var tam model.User
	db.Where("username = ?", "tam").First(&tam)
	db.Create(&model.APIKey{UserID: tam.ID, Name: "k", KeyHash: "h1"})
	tamToken := loginToken(t, r, "tam", "du-dai-12345")

	if w := call(r, http.MethodDelete, "/api/v1/users/2", token, nil); tam.ID != 2 || w.Code != http.StatusOK {
		t.Fatalf("xoá = %d", w.Code)
	}
	if c := call(r, http.MethodGet, "/api/v1/whoami", tamToken, nil); c.Code != http.StatusUnauthorized {
		t.Fatalf("người đã xoá vẫn dùng được token: %d", c.Code)
	}
	var keys int64
	db.Model(&model.APIKey{}).Where("user_id = ?", tam.ID).Count(&keys)
	if keys != 0 {
		t.Fatal("API key của người đã xoá phải đi theo")
	}
	if w := call(r, http.MethodPost, "/api/v1/users", token, body); w.Code != http.StatusOK {
		t.Fatalf("tạo lại cùng tên sau khi xoá = %d %s", w.Code, w.Body.String())
	}
}
