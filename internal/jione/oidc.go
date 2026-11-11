// Package jione là client OpenID Connect cho tài khoản Jione (id.jione.vn):
// Authorization Code + PKCE S256, client công khai (không client_secret), id_token RS256 kiểm qua JWKS.
// Cùng hợp đồng với các sản phẩm Jione khác (1Web, FinAI, MapScan…).
package jione

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	DefaultIssuer = "https://id.jione.vn"

	// FlowTTL — thời hạn của một lượt đăng nhập đang dở (state + PKCE).
	FlowTTL = 10 * time.Minute

	discoveryTTL    = time.Hour
	jwksMinRefresh  = time.Minute
	maxResponseSize = 1 << 20
	clockLeeway     = 30 * time.Second

	backchannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"
)

var (
	// ErrUnavailable — không đọc được cấu hình/khoá của Jione (mạng, cấu hình sai).
	ErrUnavailable = errors.New("jione: identity provider unavailable")
	// ErrExchange — Jione từ chối đổi mã (code sai, hết hạn, đã dùng, lệch PKCE).
	ErrExchange = errors.New("jione: code exchange rejected")
	// ErrToken — token sai chữ ký hoặc sai iss/aud/exp/nonce/sub/email.
	ErrToken = errors.New("jione: invalid token")
	// ErrEmailUnverified — tài khoản Jione chưa xác minh email.
	ErrEmailUnverified = errors.New("jione: email not verified")
)

type Config struct {
	Issuer     string
	ClientID   string
	HTTPClient *http.Client
	Now        func() time.Time
}

type Client struct {
	issuer   string
	clientID string
	http     *http.Client
	now      func() time.Time

	mu          sync.Mutex
	disc        *discovery
	discAt      time.Time
	keys        map[string]*rsa.PublicKey
	keysFetched time.Time
}

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// Flow — trạng thái một lượt đăng nhập, giữ ở cookie mã hoá giữa /oidc/login và /oidc/callback.
type Flow struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Expires  int64  `json:"e"`
}

// Identity — danh tính đã kiểm từ id_token. Sub là khoá bất biến; Email đã trim + chữ thường.
type Identity struct {
	Sub   string
	Email string
}

func New(cfg Config) *Client {
	issuer := strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	if issuer == "" {
		issuer = DefaultIssuer
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
			// Không theo redirect: endpoint của IdP phải trả lời thẳng.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Client{issuer: issuer, clientID: strings.TrimSpace(cfg.ClientID), http: httpClient, now: now}
}

func (c *Client) Issuer() string   { return c.issuer }
func (c *Client) ClientID() string { return c.clientID }

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewFlow sinh state, nonce và PKCE verifier (43 ký tự base64url — đúng khuôn Jione yêu cầu).
func (c *Client) NewFlow() (Flow, error) {
	var f Flow
	var err error
	if f.State, err = randomURLSafe(32); err != nil {
		return Flow{}, err
	}
	if f.Nonce, err = randomURLSafe(32); err != nil {
		return Flow{}, err
	}
	if f.Verifier, err = randomURLSafe(32); err != nil {
		return Flow{}, err
	}
	f.Expires = c.now().Add(FlowTTL).Unix()
	return f, nil
}

// Expired báo lượt đăng nhập đã quá hạn.
func (f Flow) Expired(now time.Time) bool { return f.Expires <= now.Unix() }

// Challenge = BASE64URL(SHA256(verifier)) — PKCE S256.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (c *Client) getJSON(ctx context.Context, rawURL string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", rawURL, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(out)
}

// endpointOK: endpoint phải là URL tuyệt đối https; http chỉ được khi chính issuer là http (chạy thử cục bộ).
func (c *Client) endpointOK(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return u.Scheme == "http" && strings.HasPrefix(c.issuer, "http://")
}

func (c *Client) discover(ctx context.Context) (*discovery, error) {
	c.mu.Lock()
	if c.disc != nil && c.now().Sub(c.discAt) < discoveryTTL {
		d := c.disc
		c.mu.Unlock()
		return d, nil
	}
	c.mu.Unlock()

	var d discovery
	if err := c.getJSON(ctx, c.issuer+"/.well-known/openid-configuration", &d); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	// Issuer trong tài liệu phải khớp đúng chuỗi cấu hình: chống tài liệu discovery của bên khác.
	if strings.TrimRight(d.Issuer, "/") != c.issuer ||
		!c.endpointOK(d.AuthorizationEndpoint) || !c.endpointOK(d.TokenEndpoint) || !c.endpointOK(d.JWKSURI) {
		return nil, fmt.Errorf("%w: discovery document không hợp lệ", ErrUnavailable)
	}

	c.mu.Lock()
	c.disc, c.discAt = &d, c.now()
	c.mu.Unlock()
	return &d, nil
}

// AuthorizeURL dựng URL chuyển người dùng sang Jione.
func (c *Client) AuthorizeURL(ctx context.Context, redirectURI string, f Flow) (string, error) {
	if c.clientID == "" {
		return "", fmt.Errorf("%w: thiếu client_id", ErrUnavailable)
	}
	d, err := c.discover(ctx)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(d.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "openid email")
	q.Set("state", f.State)
	q.Set("nonce", f.Nonce)
	q.Set("code_challenge", Challenge(f.Verifier))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Exchange đổi authorization code lấy id_token (kèm PKCE verifier).
func (c *Client) Exchange(ctx context.Context, code, redirectURI, verifier string) (string, error) {
	d, err := c.discover(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {c.clientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return "", fmt.Errorf("%w: token endpoint status %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: status %d", ErrExchange, resp.StatusCode)
	}
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&body); err != nil || body.IDToken == "" {
		return "", fmt.Errorf("%w: thiếu id_token", ErrToken)
	}
	return body.IDToken, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (k jwk) rsaKey() (*rsa.PublicKey, bool) {
	if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
		return nil, false
	}
	nb, err1 := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.N, "="))
	eb, err2 := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.E, "="))
	if err1 != nil || err2 != nil || len(nb) < 256 || len(eb) == 0 || len(eb) > 4 { // tối thiểu RSA-2048
		return nil, false
	}
	e := 0
	for _, b := range eb {
		e = e<<8 | int(b)
	}
	if e < 3 {
		return nil, false
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, true
}

// keyFor tìm khoá công khai theo kid; kid lạ thì tải lại JWKS (không quá một lần mỗi phút) — Jione xoay khoá
// bằng cách công bố khoá mới trước rồi mới ký.
func (c *Client) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	keys, fetched := c.keys, c.keysFetched
	c.mu.Unlock()
	if key, ok := pickKey(keys, kid); ok {
		return key, nil
	}
	if keys != nil && c.now().Sub(fetched) < jwksMinRefresh {
		return nil, fmt.Errorf("%w: không có khoá kid=%q", ErrToken, kid)
	}

	d, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := c.getJSON(ctx, d.JWKSURI, &set); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	fresh := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if key, ok := k.rsaKey(); ok {
			fresh[k.Kid] = key
		}
	}
	c.mu.Lock()
	c.keys, c.keysFetched = fresh, c.now()
	c.mu.Unlock()
	if key, ok := pickKey(fresh, kid); ok {
		return key, nil
	}
	return nil, fmt.Errorf("%w: không có khoá kid=%q", ErrToken, kid)
}

func pickKey(keys map[string]*rsa.PublicKey, kid string) (*rsa.PublicKey, bool) {
	if key, ok := keys[kid]; ok {
		return key, true
	}
	if kid == "" && len(keys) == 1 {
		for _, key := range keys {
			return key, true
		}
	}
	return nil, false
}

// parse kiểm chữ ký RS256 + iss + aud + exp và trả claims cùng header typ.
func (c *Client) parse(ctx context.Context, raw string) (jwt.MapClaims, string, error) {
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}), // chỉ đúng một thuật toán: chặn alg=none / HS256 ký bằng khoá công khai
		jwt.WithIssuer(c.issuer),
		jwt.WithAudience(c.clientID),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockLeeway),
		jwt.WithTimeFunc(c.now),
	)
	var lookupErr error
	token, err := parser.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		key, err := c.keyFor(ctx, kid)
		if err != nil {
			lookupErr = err
		}
		return key, err
	})
	if err != nil || token == nil || !token.Valid {
		if errors.Is(lookupErr, ErrUnavailable) {
			return nil, "", lookupErr
		}
		return nil, "", fmt.Errorf("%w: %v", ErrToken, err)
	}
	typ, _ := token.Header["typ"].(string)
	return claims, typ, nil
}

// VerifyIDToken kiểm id_token và trả danh tính. Bất biến chung của hệ Jione: sub là khoá định danh,
// email phải có và đã xác minh, nonce phải khớp lượt đăng nhập này.
func (c *Client) VerifyIDToken(ctx context.Context, raw, nonce string) (*Identity, error) {
	claims, typ, err := c.parse(ctx, raw)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(typ, "logout+jwt") {
		return nil, fmt.Errorf("%w: logout_token không dùng làm id_token được", ErrToken)
	}
	sub, _ := claims["sub"].(string)
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return nil, fmt.Errorf("%w: thiếu sub", ErrToken)
	}
	if got, _ := claims["nonce"].(string); nonce == "" || got != nonce {
		return nil, fmt.Errorf("%w: sai nonce", ErrToken)
	}
	email, _ := claims["email"].(string)
	email = NormalizeEmail(email)
	if email == "" {
		return nil, fmt.Errorf("%w: thiếu email", ErrToken)
	}
	switch v := claims["email_verified"].(type) {
	case bool:
		if !v {
			return nil, ErrEmailUnverified
		}
	case string:
		if v != "true" {
			return nil, ErrEmailUnverified
		}
	default:
		return nil, ErrEmailUnverified
	}
	return &Identity{Sub: sub, Email: email}, nil
}

// VerifyLogoutToken kiểm logout_token của Back-Channel Logout 1.0 và trả sub cần đăng xuất.
// Jione gửi token typ "logout+jwt", sống 2 phút, có sub, không có sid.
func (c *Client) VerifyLogoutToken(ctx context.Context, raw string) (string, error) {
	claims, typ, err := c.parse(ctx, raw)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(typ, "logout+jwt") {
		return "", fmt.Errorf("%w: typ phải là logout+jwt", ErrToken)
	}
	if _, hasNonce := claims["nonce"]; hasNonce {
		return "", fmt.Errorf("%w: logout_token không được có nonce", ErrToken)
	}
	events, _ := claims["events"].(map[string]interface{})
	if _, ok := events[backchannelLogoutEvent]; !ok {
		return "", fmt.Errorf("%w: thiếu sự kiện backchannel-logout", ErrToken)
	}
	sub, _ := claims["sub"].(string)
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return "", fmt.Errorf("%w: thiếu sub", ErrToken)
	}
	return sub, nil
}

// NormalizeEmail: trim + chữ thường — cùng quy ước với các sản phẩm Jione khác.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
