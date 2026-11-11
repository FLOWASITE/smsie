package jione_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pccr10001/smsie/internal/jione"
	"github.com/pccr10001/smsie/internal/jione/jionetest"
)

const (
	clientID    = "smsie"
	redirectURI = "https://smsie.example/oidc/callback"
)

func setup(t *testing.T) (*jionetest.IdP, *jione.Client) {
	idp := jionetest.New(t)
	return idp, jione.New(jione.Config{Issuer: idp.Issuer(), ClientID: clientID})
}

func idClaims(idp *jionetest.IdP, nonce string) map[string]interface{} {
	return map[string]interface{}{
		"iss": idp.Issuer(), "aud": clientID, "sub": "user-1", "nonce": nonce,
		"email": "  Chu@Jione.VN ", "email_verified": true,
	}
}

func TestAuthorizeURLCarriesPKCE(t *testing.T) {
	_, c := setup(t)
	f, err := c.NewFlow()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.AuthorizeURL(context.Background(), redirectURI, f)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if u.Path != "/authorize" || q.Get("response_type") != "code" || q.Get("client_id") != clientID ||
		q.Get("redirect_uri") != redirectURI || q.Get("state") != f.State || q.Get("nonce") != f.Nonce ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != jione.Challenge(f.Verifier) {
		t.Fatalf("authorize url = %s", raw)
	}
	// Jione chỉ nhận code_challenge đúng 43 ký tự base64url và verifier 43–128 ký tự.
	if len(q.Get("code_challenge")) != 43 || len(f.Verifier) != 43 || strings.ContainsAny(f.Verifier, "+/=") {
		t.Fatalf("challenge/verifier sai khuôn: %q %q", q.Get("code_challenge"), f.Verifier)
	}
	if strings.Contains(raw, f.Verifier) {
		t.Fatal("verifier không được lộ trên URL")
	}
}

func TestExchangeAndVerify(t *testing.T) {
	idp, c := setup(t)
	f, _ := c.NewFlow()
	idp.Grant("code-1", clientID, redirectURI, jione.Challenge(f.Verifier), f.Nonce,
		map[string]interface{}{"sub": "user-1", "email": "Chu@Jione.vn", "email_verified": true})

	raw, err := c.Exchange(context.Background(), "code-1", redirectURI, f.Verifier)
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.VerifyIDToken(context.Background(), raw, f.Nonce)
	if err != nil || id.Sub != "user-1" || id.Email != "chu@jione.vn" {
		t.Fatalf("identity = %+v, err = %v", id, err)
	}
	// Code chỉ dùng được một lần.
	if _, err := c.Exchange(context.Background(), "code-1", redirectURI, f.Verifier); !errors.Is(err, jione.ErrExchange) {
		t.Fatalf("đổi lại code: err = %v", err)
	}
}

func TestExchangeRejectsWrongVerifier(t *testing.T) {
	idp, c := setup(t)
	f, _ := c.NewFlow()
	other, _ := c.NewFlow()
	idp.Grant("code-2", clientID, redirectURI, jione.Challenge(f.Verifier), f.Nonce, nil)
	if _, err := c.Exchange(context.Background(), "code-2", redirectURI, other.Verifier); !errors.Is(err, jione.ErrExchange) {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyIDTokenRejections(t *testing.T) {
	idp, c := setup(t)
	ctx := context.Background()
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mut := func(f func(m map[string]interface{})) map[string]interface{} {
		m := idClaims(idp, "n1")
		f(m)
		return m
	}
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"sai nonce", idp.Sign(t, idClaims(idp, "khac"), "JWT", time.Minute), jione.ErrToken},
		{"sai aud", idp.Sign(t, mut(func(m map[string]interface{}) { m["aud"] = "app-khac" }), "JWT", time.Minute), jione.ErrToken},
		{"sai iss", idp.Sign(t, mut(func(m map[string]interface{}) { m["iss"] = "https://gia.example" }), "JWT", time.Minute), jione.ErrToken},
		{"hết hạn", idp.Sign(t, mut(func(m map[string]interface{}) { m["exp"] = time.Now().Add(-time.Hour).Unix() }), "JWT", time.Minute), jione.ErrToken},
		{"thiếu exp", signNoExp(t, idp, idClaims(idp, "n1")), jione.ErrToken},
		{"thiếu sub", idp.Sign(t, mut(func(m map[string]interface{}) { delete(m, "sub") }), "JWT", time.Minute), jione.ErrToken},
		{"thiếu email", idp.Sign(t, mut(func(m map[string]interface{}) { delete(m, "email") }), "JWT", time.Minute), jione.ErrToken},
		{"email chưa xác minh", idp.Sign(t, mut(func(m map[string]interface{}) { m["email_verified"] = false }), "JWT", time.Minute), jione.ErrEmailUnverified},
		{"thiếu email_verified", idp.Sign(t, mut(func(m map[string]interface{}) { delete(m, "email_verified") }), "JWT", time.Minute), jione.ErrEmailUnverified},
		{"khoá khác cùng kid", jionetest.SignWith(t, other, jionetest.Kid, idClaims(idp, "n1"), "JWT", time.Minute), jione.ErrToken},
		{"logout_token giả làm id_token", idp.Sign(t, idClaims(idp, "n1"), "logout+jwt", time.Minute), jione.ErrToken},
		{"HS256 ký bằng chuỗi", signHS256(t, idClaims(idp, "n1")), jione.ErrToken},
		{"rác", "khong.phai.jwt", jione.ErrToken},
	}
	for _, tc := range cases {
		if _, err := c.VerifyIDToken(ctx, tc.raw, "n1"); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	// Đối chứng: cùng claims, ký đúng thì qua — và chấp nhận email_verified dạng chuỗi "true".
	ok := mut(func(m map[string]interface{}) { m["email_verified"] = "true" })
	if id, err := c.VerifyIDToken(ctx, idp.Sign(t, ok, "JWT", time.Minute), "n1"); err != nil || id.Email != "chu@jione.vn" {
		t.Fatalf("token hợp lệ bị từ chối: %+v %v", id, err)
	}
}

func TestUnknownKidRefetchIsRateLimited(t *testing.T) {
	idp, c := setup(t)
	ctx := context.Background()
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	bad := jionetest.SignWith(t, other, "kid-la", idClaims(idp, "n1"), "JWT", time.Minute)
	for i := 0; i < 5; i++ {
		if _, err := c.VerifyIDToken(ctx, bad, "n1"); !errors.Is(err, jione.ErrToken) {
			t.Fatalf("err = %v", err)
		}
	}
	if idp.Hits["jwks"] != 1 {
		t.Fatalf("kid lạ không được kéo JWKS liên tục: %d lần", idp.Hits["jwks"])
	}
}

func TestVerifyLogoutToken(t *testing.T) {
	idp, c := setup(t)
	ctx := context.Background()
	base := func() map[string]interface{} {
		return map[string]interface{}{
			"iss": idp.Issuer(), "aud": clientID, "sub": "user-1", "jti": "j1",
			"events": map[string]interface{}{"http://schemas.openid.net/event/backchannel-logout": map[string]interface{}{}},
		}
	}
	sub, err := c.VerifyLogoutToken(ctx, idp.Sign(t, base(), "logout+jwt", 2*time.Minute))
	if err != nil || sub != "user-1" {
		t.Fatalf("sub = %q, err = %v", sub, err)
	}
	asID := base()
	if _, err := c.VerifyLogoutToken(ctx, idp.Sign(t, asID, "JWT", time.Minute)); !errors.Is(err, jione.ErrToken) {
		t.Fatalf("id_token không được dùng làm logout_token: %v", err)
	}
	noEvent := base()
	delete(noEvent, "events")
	if _, err := c.VerifyLogoutToken(ctx, idp.Sign(t, noEvent, "logout+jwt", time.Minute)); !errors.Is(err, jione.ErrToken) {
		t.Fatalf("thiếu events: %v", err)
	}
	withNonce := base()
	withNonce["nonce"] = "x"
	if _, err := c.VerifyLogoutToken(ctx, idp.Sign(t, withNonce, "logout+jwt", time.Minute)); !errors.Is(err, jione.ErrToken) {
		t.Fatalf("có nonce: %v", err)
	}
	wrongAud := base()
	wrongAud["aud"] = "finai"
	if _, err := c.VerifyLogoutToken(ctx, idp.Sign(t, wrongAud, "logout+jwt", time.Minute)); !errors.Is(err, jione.ErrToken) {
		t.Fatalf("sai aud: %v", err)
	}
}

func TestDiscoveryIssuerMismatchIsUnavailable(t *testing.T) {
	idp := jionetest.New(t)
	// Client cấu hình issuer có đường dẫn khác → tài liệu discovery (nếu có) không khớp issuer.
	c := jione.New(jione.Config{Issuer: idp.Issuer() + "/khac", ClientID: clientID})
	f, _ := c.NewFlow()
	if _, err := c.AuthorizeURL(context.Background(), redirectURI, f); !errors.Is(err, jione.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestSealRoundTripAndTamper(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	in := jione.Flow{State: "s", Nonce: "n", Verifier: "v", Expires: 42}
	sealed, err := jione.Seal(key, in)
	if err != nil {
		t.Fatal(err)
	}
	var out jione.Flow
	if err := jione.Open(key, sealed, &out); err != nil || out != in {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	tampered := []byte(sealed)
	tampered[len(tampered)-1] ^= 1
	if err := jione.Open(key, string(tampered), &out); err == nil {
		t.Fatal("cookie bị sửa phải bị từ chối")
	}
	otherKey := make([]byte, 32)
	if err := jione.Open(otherKey, sealed, &out); err == nil {
		t.Fatal("sai khoá phải bị từ chối")
	}
}

func signNoExp(t *testing.T, idp *jionetest.IdP, claims map[string]interface{}) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(claims))
	token.Header["kid"] = jionetest.Kid
	raw, err := token.SignedString(idp.Key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signHS256(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	mc := jwt.MapClaims(claims)
	mc["exp"] = time.Now().Add(time.Minute).Unix()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, mc)
	token.Header["kid"] = jionetest.Kid
	raw, err := token.SignedString([]byte("bat-ky"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
