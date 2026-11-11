package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// MinSecretLen — khoá ký cấu hình tay phải dài ít nhất chừng này ký tự.
	MinSecretLen = 32
	// TokenTTL — thời hạn JWT phiên đăng nhập.
	TokenTTL = 24 * time.Hour

	settingJWTSecret = "jwt_secret"
)

var (
	secretMu  sync.RWMutex
	secretKey []byte
)

type Claims struct {
	UserID uint   `json:"user_id"`
	Role   string `json:"role"`
	// Ver phải khớp users.token_version; lệch = token đã bị thu hồi.
	Ver uint `json:"ver"`
	jwt.RegisteredClaims
}

// SetSecret đặt khoá ký JWT của tiến trình.
func SetSecret(secret []byte) {
	secretMu.Lock()
	defer secretMu.Unlock()
	secretKey = append([]byte(nil), secret...)
}

func currentSecret() ([]byte, error) {
	secretMu.RLock()
	defer secretMu.RUnlock()
	if len(secretKey) == 0 {
		return nil, errors.New("jwt secret is not initialised")
	}
	return secretKey, nil
}

// InitSecret chọn khoá ký JWT: ưu tiên giá trị cấu hình (auth.jwt_secret / AUTH_JWT_SECRET); không có thì
// đọc khoá đã sinh trong bảng settings, chưa có thì sinh 32 byte ngẫu nhiên và lưu lại. Không bao giờ dùng
// một hằng số trong mã nguồn — repo công khai nên hằng số đồng nghĩa với ai cũng ký được token admin.
func InitSecret(db *gorm.DB, configured string) error {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		if len(configured) < MinSecretLen {
			return fmt.Errorf("auth.jwt_secret phải dài ít nhất %d ký tự", MinSecretLen)
		}
		SetSecret([]byte(configured))
		return nil
	}

	var row model.Setting
	err := db.First(&row, "name = ?", settingJWTSecret).Error
	if err == nil && len(row.Value) >= MinSecretLen {
		SetSecret([]byte(row.Value))
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	value := base64.RawURLEncoding.EncodeToString(buf)
	// Hai tiến trình cùng khởi động trên một DB: bên nào ghi trước thắng, bên sau đọc lại.
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Setting{Name: settingJWTSecret, Value: value}).Error; err != nil {
		return err
	}
	if err := db.First(&row, "name = ?", settingJWTSecret).Error; err != nil {
		return err
	}
	if len(row.Value) < MinSecretLen {
		row.Value = value
		if err := db.Save(&row).Error; err != nil {
			return err
		}
	}
	SetSecret([]byte(row.Value))
	return nil
}

// DeriveKey trả khoá 32 byte tách riêng theo mục đích (vd. mã hoá cookie OIDC) từ khoá ký JWT.
func DeriveKey(purpose string) ([]byte, error) {
	secret, err := currentSecret()
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write([]byte("smsie:" + purpose + ":"))
	h.Write(secret)
	return h.Sum(nil), nil
}

func GenerateToken(user *model.User) (string, error) {
	secret, err := currentSecret()
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := &Claims{
		UserID: user.ID,
		Role:   user.Role,
		Ver:    user.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func ValidateToken(tokenString string) (*Claims, error) {
	secret, err := currentSecret()
	if err != nil {
		return nil, err
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
