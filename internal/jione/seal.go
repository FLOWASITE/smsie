package jione

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// Seal mã hoá (AES-256-GCM) một giá trị JSON thành chuỗi base64url để đặt vào cookie.
func Seal(key []byte, v interface{}) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, plain, nil)), nil
}

// Open giải mã chuỗi do Seal tạo; sai khoá hoặc bị sửa đều trả lỗi.
func Open(key []byte, sealed string, out interface{}) error {
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	if len(raw) < gcm.NonceSize() {
		return errors.New("sealed value too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, out)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
