// Package audit ghi nhật ký hành động: middleware cho request ghi (POST/PATCH/PUT/DELETE)
// và Record() cho việc hệ thống tự làm (keepalive, backup).
package audit

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pccr10001/smsie/internal/model"
	"github.com/pccr10001/smsie/pkg/logger"
	"gorm.io/gorm"
)

const maxBody = 4 << 10

type Entry = model.AuditLog

// Record ghi một dòng; At rỗng → bây giờ.
func Record(db *gorm.DB, e Entry) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	return db.Create(&e).Error
}

// Prune xoá dòng cũ hơn keepDays.
func Prune(db *gorm.DB, keepDays int) error {
	if keepDays <= 0 {
		return nil
	}
	return db.Where("at < ?", time.Now().AddDate(0, 0, -keepDays)).Delete(&model.AuditLog{}).Error
}

// actions: method + FullPath → mã hành động ổn định. Route ngoài bảng → "<METHOD> <path>".
var actions = map[string]string{
	"POST /api/v1/modems/:iccid/send":          "sms.send",
	"POST /api/v1/modems/:iccid/call/dial":     "call.dial",
	"POST /api/v1/modems/:iccid/call/hangup":   "call.hangup",
	"POST /api/v1/modems/:iccid/at":            "at.exec",
	"POST /api/v1/modems/:iccid/balance-check": "balance.check",
	"POST /api/v1/modems/:iccid/phone-lookup":  "phone.lookup",
	"POST /api/v1/modems/:iccid/reboot":        "modem.reboot",
	"PATCH /api/v1/modems/:iccid/profile":      "modem.profile",
	"PATCH /api/v1/bays/:imei":                 "bay.assign",
	"POST /api/v1/balance/run":                 "balance.run",
	"POST /api/v1/keepalive/run":               "keepalive.run",
	"POST /api/v1/users":                       "user.create",
	"DELETE /api/v1/users/:id":                 "user.delete",
	"PUT /api/v1/users/:id/permissions":        "user.permissions",
	"POST /api/v1/apikeys":                     "apikey.create",
	"POST /api/v1/apikeys/:id/rotate":          "apikey.rotate",
	"DELETE /api/v1/apikeys/:id":               "apikey.delete",
	"POST /api/v1/webhooks":                    "webhook.create",
	"DELETE /api/v1/webhooks/:id":              "webhook.delete",
	"POST /api/v1/change_password":             "auth.password",
}

func actionFor(method, fullPath string) string {
	if a, ok := actions[method+" "+fullPath]; ok {
		return a
	}
	return method + " " + fullPath
}

// scrub: JSON → bỏ key chứa password/secret/key/token (không phân biệt hoa thường), cắt
// content/message còn 60 ký tự. Không phải JSON object → "".
func scrub(body []byte) string {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil || m == nil {
		return ""
	}
	for k, v := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "password") || strings.Contains(lk, "secret") || strings.Contains(lk, "key") || strings.Contains(lk, "token") {
			delete(m, k)
			continue
		}
		if s, ok := v.(string); ok && (lk == "content" || lk == "message") {
			if r := []rune(s); len(r) > 60 {
				m[k] = string(r[:60]) + "…"
			}
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(out)
}

// Middleware ghi request POST/PATCH/PUT/DELETE sau khi handler chạy (lấy status thật).
// Đặt SAU AuthMiddleware để c.Get("user") / c.Get("api_key") có giá trị.
func Middleware(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case "POST", "PATCH", "PUT", "DELETE":
		default:
			c.Next()
			return
		}
		var body []byte
		if c.Request.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(c.Request.Body, maxBody))
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
		}
		c.Next()

		e := Entry{
			At:     time.Now(),
			Action: actionFor(c.Request.Method, c.FullPath()),
			ICCID:  c.Param("iccid"),
			Status: c.Writer.Status(),
			IP:     c.ClientIP(),
		}
		if e.Action != "auth.password" {
			e.Detail = scrub(body)
		}
		var fields map[string]interface{}
		_ = json.Unmarshal(body, &fields)
		for _, k := range []string{"phone", "number", "to"} {
			if s, ok := fields[k].(string); ok && s != "" {
				e.Target = s
				break
			}
		}
		if e.Target == "" {
			for _, p := range []string{"id", "imei"} {
				if v := c.Param(p); v != "" {
					e.Target = v
					break
				}
			}
		}
		if u, ok := c.Get("user"); ok {
			if user, ok := u.(*model.User); ok && user != nil {
				e.Username, e.UserID = user.Username, &user.ID
			}
		}
		if k, ok := c.Get("api_key"); ok {
			if key, ok := k.(*model.APIKey); ok && key != nil {
				e.Username, e.APIKeyID = "apikey:"+key.Name, &key.ID
			}
		}
		go func() {
			if err := Record(db, e); err != nil && logger.Log != nil {
				logger.Log.Warnf("audit: không ghi được %s: %v", e.Action, err)
			}
		}()
	}
}
