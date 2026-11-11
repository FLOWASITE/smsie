package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestInitSecretGeneratesOnceAndPersists(t *testing.T) {
	db := testDB(t)
	if err := InitSecret(db, ""); err != nil {
		t.Fatal(err)
	}
	first, _ := currentSecret()
	if len(first) < MinSecretLen {
		t.Fatalf("khoá sinh ra quá ngắn: %d", len(first))
	}
	SetSecret(nil)
	if err := InitSecret(db, ""); err != nil {
		t.Fatal(err)
	}
	second, _ := currentSecret()
	if string(first) != string(second) {
		t.Fatal("khởi động lại phải dùng lại khoá đã lưu, nếu không mọi phiên rớt mỗi lần restart")
	}
}

func TestInitSecretPrefersConfigAndRejectsShort(t *testing.T) {
	db := testDB(t)
	if err := InitSecret(db, "ngan"); err == nil {
		t.Fatal("khoá cấu hình ngắn phải bị từ chối")
	}
	want := strings.Repeat("k", MinSecretLen)
	if err := InitSecret(db, want); err != nil {
		t.Fatal(err)
	}
	got, _ := currentSecret()
	if string(got) != want {
		t.Fatalf("secret = %q", got)
	}
}

func TestLegacyHardcodedSecretIsRejected(t *testing.T) {
	db := testDB(t)
	if err := InitSecret(db, ""); err != nil {
		t.Fatal(err)
	}
	forged := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": 1, "role": "admin", "exp": time.Now().Add(time.Hour).Unix(),
	})
	raw, err := forged.SignedString([]byte("YOUR_SECRET_KEY_CHANGE_IN_PROD"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateToken(raw); err == nil {
		t.Fatal("token ký bằng khoá cứng cũ phải bị từ chối")
	}
}

func TestTokenRoundTripAndTamper(t *testing.T) {
	SetSecret([]byte(strings.Repeat("s", MinSecretLen)))
	raw, err := GenerateToken(&model.User{ID: 7, Role: "user", TokenVersion: 3})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ValidateToken(raw)
	if err != nil || claims.UserID != 7 || claims.Ver != 3 {
		t.Fatalf("claims = %+v, err = %v", claims, err)
	}
	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"user_id": 7, "exp": time.Now().Add(time.Hour).Unix()})
	unsigned, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := ValidateToken(unsigned); err == nil {
		t.Fatal("alg=none phải bị từ chối")
	}
	noExp := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"user_id": 7})
	rawNoExp, _ := noExp.SignedString([]byte(strings.Repeat("s", MinSecretLen)))
	if _, err := ValidateToken(rawNoExp); err == nil {
		t.Fatal("token không có exp phải bị từ chối")
	}
}
