package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/audit"
	"github.com/pccr10001/smsie/internal/auth"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/jione"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/pkg/logger"
	"gorm.io/gorm"
)

// Đăng nhập bằng tài khoản Jione (OpenID Connect, Authorization Code + PKCE):
//
//	GET  /oidc/login                  → sinh state/nonce/PKCE, giữ trong cookie mã hoá, chuyển sang id.jione.vn
//	GET  /oidc/callback               → kiểm state, đổi code, kiểm id_token, tìm/gắn người dùng, phát phiên smsie
//	POST /api/v1/auth/jione/session   → giao diện đổi cookie bàn giao (60 giây, HttpOnly) lấy JWT như đăng nhập thường
//	POST /oidc/backchannel-logout     → Jione báo đăng xuất: thu hồi mọi phiên smsie của người đó
//
// Đường dẫn /oidc/* theo quy ước chung của các sản phẩm Jione (FinAI, BizScan, Flowa).
const (
	jioneFlowCookie    = "smsie_oidc"
	jioneHandoffCookie = "smsie_sso"
	jioneFlowPath      = "/oidc"
	jioneHandoffPath   = "/api/v1/auth/jione"
	jioneCallbackPath  = "/oidc/callback"
	jioneHandoffTTL    = 60 // giây
)

var errJioneNotAllowed = errors.New("jione account is not allowed to use smsie")

type JioneHandler struct {
	db     *gorm.DB
	cfg    config.JioneConfig
	client *jione.Client
}

func NewJioneHandler(db *gorm.DB, cfg config.JioneConfig) *JioneHandler {
	return &JioneHandler{db: db, cfg: cfg, client: jione.New(jione.Config{Issuer: cfg.Issuer, ClientID: cfg.ClientID})}
}

func (h *JioneHandler) enabled() bool {
	return h.cfg.Enabled && strings.TrimSpace(h.cfg.ClientID) != ""
}

// AuthConfig — GET /api/v1/auth/config (công khai): giao diện dùng để biết có hiện nút "Đăng nhập với Jione" không.
func (h *JioneHandler) AuthConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"jione": gin.H{"enabled": h.enabled(), "login_url": "/oidc/login"}})
}

// publicOrigin: origin công khai của smsie. Ưu tiên cấu hình; không có thì suy từ request (sau Cloudflare Tunnel
// là Host công khai + X-Forwarded-Proto). Host do client gửi, nhưng redirect_uri sai sẽ bị Jione từ chối vì phải
// khớp chính xác URI đã đăng ký.
func (h *JioneHandler) publicOrigin(c *gin.Context) string {
	if o := strings.TrimRight(strings.TrimSpace(h.cfg.RedirectOrigin), "/"); o != "" {
		return o
	}
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

func (h *JioneHandler) setCookie(c *gin.Context, name, value, path string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   strings.HasPrefix(h.publicOrigin(c), "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *JioneHandler) fail(c *gin.Context, code string) {
	h.setCookie(c, jioneFlowCookie, "", jioneFlowPath, -1)
	c.Redirect(http.StatusFound, "/?sso_error="+url.QueryEscape(code))
}

// Login — GET /oidc/login.
func (h *JioneHandler) Login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !h.enabled() {
		h.fail(c, "disabled")
		return
	}
	key, err := auth.DeriveKey("jione-oidc-flow")
	if err != nil {
		h.fail(c, "failed")
		return
	}
	flow, err := h.client.NewFlow()
	if err != nil {
		h.fail(c, "failed")
		return
	}
	target, err := h.client.AuthorizeURL(c.Request.Context(), h.publicOrigin(c)+jioneCallbackPath, flow)
	if err != nil {
		logger.Log.Warnf("jione: không dựng được URL đăng nhập: %v", err)
		h.fail(c, "unavailable")
		return
	}
	sealed, err := jione.Seal(key, flow)
	if err != nil {
		h.fail(c, "failed")
		return
	}
	h.setCookie(c, jioneFlowCookie, sealed, jioneFlowPath, int(jione.FlowTTL.Seconds()))
	c.Redirect(http.StatusFound, target)
}

// Callback — GET /oidc/callback.
func (h *JioneHandler) Callback(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !h.enabled() {
		h.fail(c, "disabled")
		return
	}
	// Cookie lượt đăng nhập chỉ dùng một lần: xoá trước mọi nhánh trả về.
	sealed, _ := c.Cookie(jioneFlowCookie)
	h.setCookie(c, jioneFlowCookie, "", jioneFlowPath, -1)

	key, err := auth.DeriveKey("jione-oidc-flow")
	if err != nil {
		h.fail(c, "failed")
		return
	}
	var flow jione.Flow
	state := c.Query("state")
	if sealed == "" || state == "" || jione.Open(key, sealed, &flow) != nil ||
		flow.Expired(time.Now()) || flow.State == "" || flow.State != state {
		h.fail(c, "session")
		return
	}
	if idpErr := c.Query("error"); idpErr != "" {
		if idpErr == "access_denied" {
			h.fail(c, "denied")
		} else {
			h.fail(c, "failed")
		}
		return
	}
	code := c.Query("code")
	if code == "" {
		h.fail(c, "failed")
		return
	}

	ctx := c.Request.Context()
	idToken, err := h.client.Exchange(ctx, code, h.publicOrigin(c)+jioneCallbackPath, flow.Verifier)
	if err != nil {
		logger.Log.Warnf("jione: đổi code thất bại: %v", err)
		h.fail(c, jioneErrorCode(err))
		return
	}
	identity, err := h.client.VerifyIDToken(ctx, idToken, flow.Nonce)
	if err != nil {
		logger.Log.Warnf("jione: id_token không hợp lệ: %v", err)
		h.fail(c, jioneErrorCode(err))
		return
	}

	ip := c.ClientIP()
	user, err := h.resolveUser(identity)
	if err != nil {
		code := "failed"
		if errors.Is(err, errJioneNotAllowed) {
			code = "not_allowed"
		} else {
			logger.Log.Errorf("jione: không gắn được người dùng: %v", err)
		}
		_ = audit.Record(h.db, audit.Entry{Username: truncateForAudit(identity.Email), Action: "auth.jione_login", Detail: code, Status: http.StatusForbidden, IP: ip})
		h.fail(c, code)
		return
	}

	token, err := auth.GenerateToken(user)
	if err != nil {
		h.fail(c, "failed")
		return
	}
	_ = audit.Record(h.db, audit.Entry{UserID: &user.ID, Username: user.Username, Action: "auth.jione_login", Status: http.StatusOK, IP: ip})

	// Không đưa JWT lên URL (lọt vào lịch sử trình duyệt/log). Trao qua cookie HttpOnly sống 60 giây,
	// giao diện đổi lấy token bằng POST cùng origin rồi cookie bị xoá.
	h.setCookie(c, jioneHandoffCookie, token, jioneHandoffPath, jioneHandoffTTL)
	c.Redirect(http.StatusFound, "/?sso=1")
}

func jioneErrorCode(err error) string {
	switch {
	case errors.Is(err, jione.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, jione.ErrEmailUnverified):
		return "email_unverified"
	case errors.Is(err, jione.ErrToken):
		return "token"
	default:
		return "failed"
	}
}

func (h *JioneHandler) isAdminEmail(email string) bool {
	for _, e := range h.cfg.AdminEmails {
		if jione.NormalizeEmail(e) == email {
			return true
		}
	}
	return false
}

// resolveUser quyết định tài khoản Jione này là người dùng smsie nào:
//  1. đã gắn (jione_sub khớp) → chính người đó;
//  2. chưa gắn nhưng admin đã tạo sẵn người dùng có email (hoặc tên đăng nhập) trùng email đã xác minh → gắn vào;
//  3. email nằm trong jione.admin_emails → tạo admin;
//  4. jione.auto_create bật → tạo user thường, chưa có quyền SIM nào;
//  5. còn lại → từ chối.
//
// smsie giữ OTP nên mặc định KHÔNG tự mở cửa cho mọi tài khoản Jione: ai cũng đăng ký được Jione.
func (h *JioneHandler) resolveUser(id *jione.Identity) (*model.User, error) {
	var user model.User
	err := h.db.Transaction(func(tx *gorm.DB) error {
		admin := h.isAdminEmail(id.Email)

		err := tx.Where("jione_sub = ?", id.Sub).First(&user).Error
		if err == nil {
			updates := map[string]interface{}{}
			if user.Email != id.Email {
				updates["email"] = id.Email
			}
			if admin && user.Role != "admin" {
				updates["role"] = "admin"
			}
			if len(updates) == 0 {
				return nil
			}
			if err := tx.Model(&user).Updates(updates).Error; err != nil {
				return err
			}
			return tx.First(&user, user.ID).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		err = tx.Where("jione_sub IS NULL AND (email = ? OR LOWER(username) = ?)", id.Email, id.Email).
			Order("id asc").First(&user).Error
		if err == nil {
			updates := map[string]interface{}{"jione_sub": id.Sub, "email": id.Email}
			if admin {
				updates["role"] = "admin"
			}
			if err := tx.Model(&user).Updates(updates).Error; err != nil {
				return err
			}
			return tx.First(&user, user.ID).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if !admin && !h.cfg.AutoCreate {
			return errJioneNotAllowed
		}
		// Email này đang thuộc một người đã gắn tài khoản Jione KHÁC → không tạo trùng, không chiếm.
		var taken int64
		if err := tx.Model(&model.User{}).Where("email = ? OR LOWER(username) = ?", id.Email, id.Email).Count(&taken).Error; err != nil {
			return err
		}
		if taken > 0 {
			return errJioneNotAllowed
		}
		sub := id.Sub
		user = model.User{Username: id.Email, Email: id.Email, PasswordHash: noPasswordHash, Role: "user", JioneSub: &sub}
		if admin {
			user.Role = "admin"
		}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		return tx.First(&user, user.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// Session — POST /api/v1/auth/jione/session: đổi cookie bàn giao lấy {token, user} (cùng dạng với /login).
func (h *JioneHandler) Session(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	raw, _ := c.Cookie(jioneHandoffCookie)
	h.setCookie(c, jioneHandoffCookie, "", jioneHandoffPath, -1)
	if raw == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no pending Jione sign-in"})
		return
	}
	claims, err := auth.ValidateToken(raw)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Jione sign-in expired"})
		return
	}
	var user model.User
	if err := h.db.First(&user, claims.UserID).Error; err != nil || claims.Ver != user.TokenVersion {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Jione sign-in expired"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": raw, "user": user})
}

// BackchannelLogout — POST /oidc/backchannel-logout (OIDC Back-Channel Logout 1.0). Jione gửi logout_token khi
// phiên SSO của người dùng kết thúc; smsie tăng token_version để mọi JWT đã phát cho người đó hết hiệu lực.
func (h *JioneHandler) BackchannelLogout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !h.enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	raw := c.PostForm("logout_token")
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	sub, err := h.client.VerifyLogoutToken(c.Request.Context(), raw)
	if err != nil {
		if errors.Is(err, jione.ErrUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily_unavailable"})
			return
		}
		logger.Log.Warnf("jione: logout_token không hợp lệ: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	var user model.User
	if err := h.db.Where("jione_sub = ?", sub).First(&user).Error; err != nil {
		// Người chưa từng vào smsie: không có gì để thu hồi, vẫn là thành công.
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	if err := h.db.Model(&model.User{}).Where("id = ?", user.ID).
		UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
		return
	}
	_ = audit.Record(h.db, audit.Entry{UserID: &user.ID, Username: user.Username, Action: "auth.jione_logout", Status: http.StatusOK, IP: c.ClientIP()})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// RegisterJioneRoutes gắn các route đăng nhập Jione (đều công khai — chưa có phiên smsie nào ở các bước này).
func RegisterJioneRoutes(r gin.IRoutes, h *JioneHandler) {
	r.GET("/oidc/login", h.Login)
	r.GET(jioneCallbackPath, h.Callback)
	r.POST("/oidc/backchannel-logout", h.BackchannelLogout)
	r.GET("/api/v1/auth/config", h.AuthConfig)
	r.POST(jioneHandoffPath+"/session", h.Session)
}
