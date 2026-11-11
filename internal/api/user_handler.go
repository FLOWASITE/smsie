package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/audit"
	"github.com/pccr10001/smsie/internal/auth"
	"github.com/pccr10001/smsie/internal/config"
	"github.com/pccr10001/smsie/internal/jione"
	"github.com/pccr10001/smsie/internal/model"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	minPasswordLen = 8
	// noPasswordHash — không phải hash bcrypt hợp lệ nên không mật khẩu nào khớp: tài khoản chỉ vào được qua Jione.
	noPasswordHash = "!"
)

type UserHandler struct {
	db *gorm.DB
	// limiter đếm lần đăng nhập sai; maxAttempts áp cho mỗi IP, gấp đôi cho mỗi tên đăng nhập.
	limiter     *attemptLimiter
	maxAttempts int
}

func NewUserHandler(db *gorm.DB) *UserHandler {
	cfg := config.AppConfig.Auth
	max := cfg.LoginMaxAttempts
	if max == 0 {
		max = 10
	}
	window := time.Duration(cfg.LoginWindowMin) * time.Minute
	return &UserHandler{db: db, limiter: newAttemptLimiter(window), maxAttempts: max}
}

var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

// burnPasswordCheck tiêu đúng một lượt bcrypt khi tên đăng nhập không tồn tại, để thời gian trả lời
// không tiết lộ tài khoản nào có thật.
func burnPasswordCheck(password string) {
	dummyHashOnce.Do(func() { dummyHash, _ = bcrypt.GenerateFromPassword([]byte("smsie-dummy"), 14) })
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}

type permissionInput struct {
	ICCID       string `json:"iccid"`
	CanMakeCall bool   `json:"can_make_call"`
	CanViewSMS  bool   `json:"can_view_sms"`
	CanSendSMS  bool   `json:"can_send_sms"`
	CanSendAT   bool   `json:"can_send_at"`
}

// Use bcrypt for secure hashing
func hashPassword(password string) (string, error) {
	return hashPasswordStrict(password)
}

func checkPasswordHash(password, hash string) bool {
	return checkPasswordStrict(password, hash)
}

func (h *UserHandler) Login(c *gin.Context) {
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&creds); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	username := strings.TrimSpace(creds.Username)
	ip := c.ClientIP()
	ipKey, userKey := "ip:"+ip, "user:"+strings.ToLower(username)

	// Chặn TRƯỚC khi chạy bcrypt: vừa chống dò mật khẩu, vừa không để ai đốt CPU bằng cách bắn /login.
	for _, k := range []struct {
		key string
		max int
	}{{ipKey, h.maxAttempts}, {userKey, h.maxAttempts * 2}} {
		if blocked, wait := h.limiter.blocked(k.key, k.max); blocked {
			c.Header("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many failed login attempts", "retry_after_seconds": int(wait.Seconds()) + 1})
			return
		}
	}

	fail := func() {
		h.limiter.fail(ipKey)
		h.limiter.fail(userKey)
		_ = audit.Record(h.db, audit.Entry{Username: truncateForAudit(username), Action: "auth.login", Status: http.StatusUnauthorized, IP: ip})
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
	}

	var user model.User
	if err := h.db.Where("username = ?", username).First(&user).Error; err != nil {
		burnPasswordCheck(creds.Password)
		fail()
		return
	}
	if !checkPasswordHash(creds.Password, user.PasswordHash) {
		fail()
		return
	}

	token, err := auth.GenerateToken(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	h.limiter.reset(ipKey)
	h.limiter.reset(userKey)
	_ = audit.Record(h.db, audit.Entry{UserID: &user.ID, Username: user.Username, Action: "auth.login", Status: http.StatusOK, IP: ip})

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user":  user,
	})
}

func truncateForAudit(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

func (h *UserHandler) ListUsers(c *gin.Context) {
	var users []model.User
	if err := h.db.Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, users)
}

func (h *UserHandler) CreateUser(c *gin.Context) {
	var req struct {
		Username      string            `json:"username"`
		Password      string            `json:"password"`
		Email         string            `json:"email"`
		Role          string            `json:"role"`
		AllowedModems string            `json:"allowed_modems"`
		Permissions   []permissionInput `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	username := strings.TrimSpace(req.Username)
	email := jione.NormalizeEmail(req.Email)
	if email != "" && !looksLikeEmail(email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is not valid"})
		return
	}
	if username == "" {
		username = email // người chỉ đăng nhập qua Jione: lấy email làm tên
	}
	if username == "" || len(username) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username is required (or give an email)"})
		return
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = "user"
	}
	if role != "user" && role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be user or admin"})
		return
	}

	// Mật khẩu chỉ được bỏ trống khi có email (tài khoản đăng nhập qua Jione). Trước đây mật khẩu rỗng vẫn được
	// băm và lưu, tức ai cũng đăng nhập được bằng mật khẩu rỗng.
	hash := noPasswordHash
	switch {
	case req.Password == "" && email == "":
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is required unless an email is given for Jione login"})
		return
	case req.Password != "":
		if len(req.Password) < minPasswordLen {
			c.JSON(http.StatusBadRequest, gin.H{"error": "password must be at least 8 characters"})
			return
		}
		var err error
		if hash, err = hashPassword(req.Password); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}
	}

	var clash int64
	q := h.db.Model(&model.User{}).Where("username = ?", username)
	if email != "" {
		q = q.Or("email = ?", email)
	}
	if err := q.Count(&clash).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check existing users"})
		return
	}
	if clash > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "username or email already exists"})
		return
	}

	user := model.User{
		Username:      username,
		PasswordHash:  hash,
		Email:         email,
		Role:          role,
		AllowedModems: req.AllowedModems,
	}
	if err := h.db.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	if err := h.replaceUserPermissions(user.ID, req.AllowedModems, req.Permissions); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save permissions: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, user)
}

func looksLikeEmail(s string) bool {
	at := strings.LastIndex(s, "@")
	return at > 0 && at < len(s)-3 && strings.Contains(s[at+1:], ".") && !strings.ContainsAny(s, " \t\r\n<>") && len(s) <= 254
}

func (h *UserHandler) UpdateUserPermissions(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	var user model.User
	if err := h.db.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	if user.Role == "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "admin permissions are always full"})
		return
	}

	var req struct {
		AllowedModems string            `json:"allowed_modems"`
		Permissions   []permissionInput `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user.AllowedModems = req.AllowedModems
	if err := h.db.Model(&user).Update("allowed_modems", req.AllowedModems).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if err := h.replaceUserPermissions(user.ID, req.AllowedModems, req.Permissions); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save permissions: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *UserHandler) ListUserPermissions(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	var user model.User
	if err := h.db.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var perms []model.UserModemPermission
	if err := h.db.Where("user_id = ?", user.ID).Order("iccid asc").Find(&perms).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id":        user.ID,
		"username":       user.Username,
		"allowed_modems": user.AllowedModems,
		"permissions":    perms,
	})
}

func (h *UserHandler) replaceUserPermissions(userID uint, allowedModems string, inputs []permissionInput) error {
	allowedSet := map[string]struct{}{}
	for _, iccid := range splitAndTrimAllowed(allowedModems) {
		allowedSet[iccid] = struct{}{}
	}
	_, hasWildcard := allowedSet["*"]

	if err := h.db.Where("user_id = ?", userID).Delete(&model.UserModemPermission{}).Error; err != nil {
		return err
	}

	for _, item := range inputs {
		iccid := strings.TrimSpace(item.ICCID)
		if iccid == "" {
			continue
		}
		if !hasWildcard {
			if _, ok := allowedSet[iccid]; !ok {
				continue
			}
		}

		rec := model.UserModemPermission{
			UserID:      userID,
			ICCID:       iccid,
			CanMakeCall: item.CanMakeCall,
			CanViewSMS:  item.CanViewSMS,
			CanSendSMS:  item.CanSendSMS,
			CanSendAT:   item.CanSendAT,
		}
		if err := h.db.Create(&rec).Error; err != nil {
			return err
		}
	}

	return nil
}

// DeleteUser xoá hẳn người dùng cùng quyền SIM và API key của họ. Xoá hẳn (không xoá mềm) để tên đăng nhập,
// email và liên kết Jione được giải phóng — tạo lại người cùng tên không còn vướng unique index.
func (h *UserHandler) DeleteUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	if actor, ok := getActor(c); ok && actor.User != nil && actor.User.ID == uint(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete your own account"})
		return
	}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&model.UserModemPermission{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&model.APIKey{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(&model.User{}, id).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *UserHandler) ChangePassword(c *gin.Context) {
	// Got User from Middleware
	userObj, exists := c.Get("user")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	user := userObj.(*model.User)

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify old password
	if !checkPasswordHash(req.OldPassword, user.PasswordHash) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Incorrect old password"})
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new password must be at least 8 characters"})
		return
	}

	// Update
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Đổi mật khẩu thu hồi mọi phiên cũ (tăng token_version); phiên đang dùng nhận token mới ngay trong phản hồi.
	user.PasswordHash = hash
	user.TokenVersion++
	if err := h.db.Model(user).Select("PasswordHash", "TokenVersion").Updates(user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}
	token, err := auth.GenerateToken(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "Password updated", "token": token})
}
