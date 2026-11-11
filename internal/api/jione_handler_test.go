package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/auth"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/jione/jionetest"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

const jioneTestOrigin = "https://smsie.example"

type jioneEnv struct {
	t      *testing.T
	db     *gorm.DB
	idp    *jionetest.IdP
	router *gin.Engine
}

func newJioneEnv(t *testing.T, mutate func(*config.JioneConfig)) *jioneEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // :memory: là riêng từng kết nối
	if err := db.AutoMigrate(&model.User{}, &model.UserModemPermission{}, &model.APIKey{}, &model.AuditLog{}, &model.Setting{}); err != nil {
		t.Fatal(err)
	}
	auth.SetSecret([]byte(strings.Repeat("t", auth.MinSecretLen)))

	idp := jionetest.New(t)
	cfg := config.JioneConfig{Enabled: true, Issuer: idp.Issuer(), ClientID: "smsie", RedirectOrigin: jioneTestOrigin}
	if mutate != nil {
		mutate(&cfg)
	}
	r := gin.New()
	RegisterJioneRoutes(r, NewJioneHandler(db, cfg))
	authed := r.Group("/api/v1", AuthMiddleware(db))
	authed.GET("/whoami", func(c *gin.Context) {
		role, _ := c.Get("role")
		c.JSON(http.StatusOK, gin.H{"role": role})
	})
	return &jioneEnv{t: t, db: db, idp: idp, router: r}
}

func (e *jioneEnv) do(method, target string, cookies []*http.Cookie, form url.Values, bearer string) *httptest.ResponseRecorder {
	e.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, ck := range cookies {
		if ck.MaxAge >= 0 && ck.Value != "" {
			req.AddCookie(ck)
		}
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

// signIn chạy trọn luồng: /oidc/login → (Jione cấp code) → /oidc/callback. Trả phản hồi của callback.
func (e *jioneEnv) signIn(claims map[string]interface{}) *httptest.ResponseRecorder {
	e.t.Helper()
	login := e.do(http.MethodGet, "/oidc/login", nil, nil, "")
	if login.Code != http.StatusFound {
		e.t.Fatalf("/oidc/login = %d %s", login.Code, login.Body.String())
	}
	loc, err := url.Parse(login.Header().Get("Location"))
	if err != nil || !strings.HasPrefix(loc.String(), e.idp.Issuer()+"/authorize?") {
		e.t.Fatalf("login phải chuyển sang Jione, lại ra %q", login.Header().Get("Location"))
	}
	q := loc.Query()
	if q.Get("redirect_uri") != jioneTestOrigin+"/oidc/callback" {
		e.t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	e.idp.Grant("code-ok", q.Get("client_id"), q.Get("redirect_uri"), q.Get("code_challenge"), q.Get("nonce"), claims)
	return e.do(http.MethodGet, "/oidc/callback?code=code-ok&state="+url.QueryEscape(q.Get("state")), login.Result().Cookies(), nil, "")
}

// token đổi cookie bàn giao lấy JWT phiên.
func (e *jioneEnv) token(cb *httptest.ResponseRecorder) (string, model.User) {
	e.t.Helper()
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != "/?sso=1" {
		e.t.Fatalf("callback = %d → %q", cb.Code, cb.Header().Get("Location"))
	}
	if strings.Contains(cb.Header().Get("Location"), "eyJ") {
		e.t.Fatal("JWT không được nằm trên URL")
	}
	sess := e.do(http.MethodPost, "/api/v1/auth/jione/session", cb.Result().Cookies(), nil, "")
	if sess.Code != http.StatusOK {
		e.t.Fatalf("session = %d %s", sess.Code, sess.Body.String())
	}
	var out struct {
		Token string     `json:"token"`
		User  model.User `json:"user"`
	}
	if err := json.Unmarshal(sess.Body.Bytes(), &out); err != nil || out.Token == "" {
		e.t.Fatalf("session body = %s", sess.Body.String())
	}
	return out.Token, out.User
}

func (e *jioneEnv) wantError(cb *httptest.ResponseRecorder, code string) {
	e.t.Helper()
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != "/?sso_error="+code {
		e.t.Fatalf("muốn sso_error=%s, lại ra %d → %q", code, cb.Code, cb.Header().Get("Location"))
	}
	for _, ck := range cb.Result().Cookies() {
		if ck.Name == jioneHandoffCookie && ck.Value != "" {
			e.t.Fatal("lỗi mà vẫn phát cookie phiên")
		}
	}
}

func (e *jioneEnv) userCount() int64 {
	var n int64
	e.db.Model(&model.User{}).Count(&n)
	return n
}

func chu(sub, email string) map[string]interface{} {
	return map[string]interface{}{"sub": sub, "email": email, "email_verified": true}
}

func TestJioneLoginAdminEmailEndToEnd(t *testing.T) {
	e := newJioneEnv(t, func(c *config.JioneConfig) { c.AdminEmails = []string{" Chu@Jione.vn "} })
	token, user := e.token(e.signIn(chu("sub-1", "chu@jione.vn")))
	if user.Username != "chu@jione.vn" || user.Role != "admin" || !user.JioneLinked {
		t.Fatalf("user = %+v", user)
	}
	who := e.do(http.MethodGet, "/api/v1/whoami", nil, nil, token)
	if who.Code != http.StatusOK || !strings.Contains(who.Body.String(), `"admin"`) {
		t.Fatalf("whoami = %d %s", who.Code, who.Body.String())
	}
	// Tài khoản chỉ có Jione: không mật khẩu nào đăng nhập được.
	var stored model.User
	e.db.First(&stored, user.ID)
	if checkPasswordHash("", stored.PasswordHash) || checkPasswordHash("!", stored.PasswordHash) {
		t.Fatal("tài khoản Jione không được có mật khẩu dùng được")
	}
	// Đăng nhập lần hai: vẫn là người đó, không tạo trùng.
	_, again := e.token(e.signIn(chu("sub-1", "chu@jione.vn")))
	if again.ID != user.ID || e.userCount() != 1 {
		t.Fatalf("lần hai tạo người mới: id %d vs %d, tổng %d", again.ID, user.ID, e.userCount())
	}
	var audits int64
	e.db.Model(&model.AuditLog{}).Where("action = ? AND status = 200", "auth.jione_login").Count(&audits)
	if audits != 2 {
		t.Fatalf("nhật ký đăng nhập Jione = %d dòng", audits)
	}
}

func TestJioneLoginUnknownAccountIsRefusedByDefault(t *testing.T) {
	e := newJioneEnv(t, nil)
	e.wantError(e.signIn(chu("sub-x", "la@example.com")), "not_allowed")
	if e.userCount() != 0 {
		t.Fatal("tài khoản lạ không được tạo người dùng")
	}
}

func TestJioneLoginLinksPrecreatedUserByEmail(t *testing.T) {
	e := newJioneEnv(t, nil)
	pre := model.User{Username: "lan", Email: "lan@cty.vn", PasswordHash: noPasswordHash, Role: "user", AllowedModems: "8984A,8984B"}
	e.db.Create(&pre)
	token, user := e.token(e.signIn(chu("sub-lan", "Lan@Cty.vn")))
	if user.ID != pre.ID || user.Role != "user" || user.AllowedModems != "8984A,8984B" || !user.JioneLinked {
		t.Fatalf("user = %+v", user)
	}
	if who := e.do(http.MethodGet, "/api/v1/whoami", nil, nil, token); who.Code != http.StatusOK {
		t.Fatalf("whoami = %d", who.Code)
	}
	// Tài khoản Jione KHÁC khai cùng email không chiếm được người đã gắn.
	e.wantError(e.signIn(chu("sub-khac", "lan@cty.vn")), "not_allowed")
	if e.userCount() != 1 {
		t.Fatalf("tổng người dùng = %d", e.userCount())
	}
}

func TestJioneLoginAutoCreateGivesNoModemAccess(t *testing.T) {
	e := newJioneEnv(t, func(c *config.JioneConfig) { c.AutoCreate = true })
	_, user := e.token(e.signIn(chu("sub-new", "moi@example.com")))
	if user.Role != "user" || user.AllowedModems != "" {
		t.Fatalf("người tự tạo phải là user, chưa có SIM nào: %+v", user)
	}
	if userCanAccessICCID(&user, "8984A") {
		t.Fatal("người tự tạo không được thấy SIM nào")
	}
}

func TestJioneLoginRejectsUnverifiedEmail(t *testing.T) {
	e := newJioneEnv(t, func(c *config.JioneConfig) { c.AutoCreate = true })
	e.wantError(e.signIn(map[string]interface{}{"sub": "s", "email": "a@b.vn", "email_verified": false}), "email_unverified")
	if e.userCount() != 0 {
		t.Fatal("email chưa xác minh không được tạo người dùng")
	}
}

func TestJioneCallbackStateAndReplay(t *testing.T) {
	e := newJioneEnv(t, func(c *config.JioneConfig) { c.AutoCreate = true })
	login := e.do(http.MethodGet, "/oidc/login", nil, nil, "")
	q, _ := url.Parse(login.Header().Get("Location"))
	state := q.Query().Get("state")
	cookies := login.Result().Cookies()
	for _, ck := range cookies {
		if ck.Name == jioneFlowCookie && (!ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/oidc") {
			t.Fatalf("cookie lượt đăng nhập thiếu thuộc tính bảo vệ: %+v", ck)
		}
	}
	e.idp.Grant("code-ok", "smsie", jioneTestOrigin+"/oidc/callback", q.Query().Get("code_challenge"), q.Query().Get("nonce"), chu("s1", "a@b.vn"))

	// state lệch (CSRF): từ chối, và code chưa bị tiêu.
	e.wantError(e.do(http.MethodGet, "/oidc/callback?code=code-ok&state=sai", cookies, nil, ""), "session")
	// Không có cookie (kẻ khác dán link callback): từ chối.
	e.wantError(e.do(http.MethodGet, "/oidc/callback?code=code-ok&state="+state, nil, nil, ""), "session")
	// Jione báo người dùng bị từ chối quyền vào app.
	e.wantError(e.do(http.MethodGet, "/oidc/callback?error=access_denied&state="+state, cookies, nil, ""), "denied")
	// Đúng state + cookie: vào được.
	ok := e.do(http.MethodGet, "/oidc/callback?code=code-ok&state="+state, cookies, nil, "")
	e.token(ok)
	// Dùng lại đúng URL đó: code đã tiêu ở Jione → thất bại, không phát phiên.
	e.wantError(e.do(http.MethodGet, "/oidc/callback?code=code-ok&state="+state, cookies, nil, ""), "failed")
}

func TestJioneSessionHandoffIsSingleUseCookie(t *testing.T) {
	e := newJioneEnv(t, func(c *config.JioneConfig) { c.AutoCreate = true })
	cb := e.signIn(chu("s1", "a@b.vn"))
	var handoff *http.Cookie
	for _, ck := range cb.Result().Cookies() {
		if ck.Name == jioneHandoffCookie {
			handoff = ck
		}
	}
	if handoff == nil || !handoff.HttpOnly || !handoff.Secure || handoff.MaxAge != jioneHandoffTTL || handoff.Path != "/api/v1/auth/jione" {
		t.Fatalf("cookie bàn giao = %+v", handoff)
	}
	sess := e.do(http.MethodPost, "/api/v1/auth/jione/session", []*http.Cookie{handoff}, nil, "")
	cleared := false
	for _, ck := range sess.Result().Cookies() {
		if ck.Name == jioneHandoffCookie && ck.MaxAge < 0 {
			cleared = true
		}
	}
	if sess.Code != http.StatusOK || !cleared {
		t.Fatalf("session = %d, cookie đã xoá = %v", sess.Code, cleared)
	}
	if none := e.do(http.MethodPost, "/api/v1/auth/jione/session", nil, nil, ""); none.Code != http.StatusUnauthorized {
		t.Fatalf("không cookie mà vẫn được: %d", none.Code)
	}
}

func TestJioneBackchannelLogoutRevokesSessions(t *testing.T) {
	e := newJioneEnv(t, func(c *config.JioneConfig) { c.AutoCreate = true })
	token, _ := e.token(e.signIn(chu("sub-out", "a@b.vn")))
	if who := e.do(http.MethodGet, "/api/v1/whoami", nil, nil, token); who.Code != http.StatusOK {
		t.Fatalf("trước đăng xuất: %d", who.Code)
	}
	claims := map[string]interface{}{
		"iss": e.idp.Issuer(), "aud": "smsie", "sub": "sub-out", "jti": "j1",
		"events": map[string]interface{}{"http://schemas.openid.net/event/backchannel-logout": map[string]interface{}{}},
	}
	// id_token (typ JWT) không được chấp nhận làm logout_token.
	bad := e.do(http.MethodPost, "/oidc/backchannel-logout", nil, url.Values{"logout_token": {e.idp.Sign(t, claims, "JWT", time.Minute)}}, "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("token sai loại = %d", bad.Code)
	}
	if who := e.do(http.MethodGet, "/api/v1/whoami", nil, nil, token); who.Code != http.StatusOK {
		t.Fatal("token sai loại không được làm rớt phiên")
	}
	good := e.do(http.MethodPost, "/oidc/backchannel-logout", nil, url.Values{"logout_token": {e.idp.Sign(t, claims, "logout+jwt", 2*time.Minute)}}, "")
	if good.Code != http.StatusOK {
		t.Fatalf("backchannel logout = %d %s", good.Code, good.Body.String())
	}
	if who := e.do(http.MethodGet, "/api/v1/whoami", nil, nil, token); who.Code != http.StatusUnauthorized {
		t.Fatalf("sau khi Jione báo đăng xuất, phiên cũ vẫn dùng được: %d", who.Code)
	}
	// Đăng nhập lại thì có phiên mới dùng được.
	fresh, _ := e.token(e.signIn(chu("sub-out", "a@b.vn")))
	if who := e.do(http.MethodGet, "/api/v1/whoami", nil, nil, fresh); who.Code != http.StatusOK {
		t.Fatalf("phiên mới = %d", who.Code)
	}
}

func TestJioneDisabled(t *testing.T) {
	e := newJioneEnv(t, func(c *config.JioneConfig) { c.Enabled = false })
	e.wantError(e.do(http.MethodGet, "/oidc/login", nil, nil, ""), "disabled")
	cfg := e.do(http.MethodGet, "/api/v1/auth/config", nil, nil, "")
	if cfg.Code != http.StatusOK || !strings.Contains(cfg.Body.String(), `"enabled":false`) {
		t.Fatalf("auth config = %s", cfg.Body.String())
	}
	if out := e.do(http.MethodPost, "/oidc/backchannel-logout", nil, url.Values{"logout_token": {"x"}}, ""); out.Code != http.StatusNotFound {
		t.Fatalf("backchannel khi tắt = %d", out.Code)
	}
}

func TestMiddlewareUsesDatabaseRoleNotTokenRole(t *testing.T) {
	e := newJioneEnv(t, nil)
	u := model.User{Username: "an", PasswordHash: noPasswordHash, Role: "admin"}
	e.db.Create(&u)
	token, err := auth.GenerateToken(&u)
	if err != nil {
		t.Fatal(err)
	}
	e.db.Model(&u).Update("role", "user") // hạ quyền sau khi đã phát token
	who := e.do(http.MethodGet, "/api/v1/whoami", nil, nil, token)
	if who.Code != http.StatusOK || !strings.Contains(who.Body.String(), `"user"`) {
		t.Fatalf("hạ quyền phải có hiệu lực ngay: %d %s", who.Code, who.Body.String())
	}
}
