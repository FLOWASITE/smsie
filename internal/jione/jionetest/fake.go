// Package jionetest dựng một IdP Jione giả (discovery, JWKS, /authorize rút gọn, /oidc/token có kiểm PKCE)
// để test luồng đăng nhập mà không cần id.jione.vn thật. Hành vi bám theo server/routes/oidc.js của jione-platform.
package jionetest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const Kid = "test-kid-1"

type grant struct {
	challenge, redirectURI, clientID, nonce string
	claims                                  map[string]interface{}
}

type IdP struct {
	Server *httptest.Server
	Key    *rsa.PrivateKey

	mu     sync.Mutex
	codes  map[string]grant
	Hits   map[string]int
	NoKeys bool // true: /jwks trả rỗng
}

var verifierRe = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func New(t testing.TB) *IdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &IdP{Key: key, codes: map[string]grant{}, Hits: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		idp.hit("discovery")
		writeJSON(w, map[string]interface{}{
			"issuer":                 idp.Issuer(),
			"authorization_endpoint": idp.Issuer() + "/authorize",
			"token_endpoint":         idp.Issuer() + "/oidc/token",
			"jwks_uri":               idp.Issuer() + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		idp.hit("jwks")
		keys := []map[string]string{}
		if !idp.NoKeys {
			keys = append(keys, map[string]string{
				"kty": "RSA", "use": "sig", "alg": "RS256", "kid": Kid,
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			})
		}
		writeJSON(w, map[string]interface{}{"keys": keys})
	})
	mux.HandleFunc("/oidc/token", func(w http.ResponseWriter, r *http.Request) {
		idp.hit("token")
		if r.Method != http.MethodPost || r.ParseForm() != nil {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		verifier := r.PostForm.Get("code_verifier")
		if r.PostForm.Get("grant_type") != "authorization_code" || !verifierRe.MatchString(verifier) {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		idp.mu.Lock()
		g, ok := idp.codes[r.PostForm.Get("code")]
		delete(idp.codes, r.PostForm.Get("code")) // dùng một lần
		idp.mu.Unlock()
		sum := sha256.Sum256([]byte(verifier))
		if !ok || g.clientID != r.PostForm.Get("client_id") || g.redirectURI != r.PostForm.Get("redirect_uri") ||
			g.challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		claims := map[string]interface{}{"iss": idp.Issuer(), "aud": g.clientID, "nonce": g.nonce}
		for k, v := range g.claims {
			claims[k] = v
		}
		writeJSON(w, map[string]interface{}{
			"id_token": idp.Sign(t, claims, "JWT", 15*time.Minute), "token_type": "Bearer", "expires_in": 900,
		})
	})
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Server.Close)
	return idp
}

func (i *IdP) Issuer() string { return i.Server.URL }

func (i *IdP) hit(name string) {
	i.mu.Lock()
	i.Hits[name]++
	i.mu.Unlock()
}

// Grant đóng vai /authorize sau khi người dùng đăng nhập ở Jione: cấp một code gắn với challenge + nonce.
func (i *IdP) Grant(code, clientID, redirectURI, challenge, nonce string, claims map[string]interface{}) {
	i.mu.Lock()
	i.codes[code] = grant{challenge: challenge, redirectURI: redirectURI, clientID: clientID, nonce: nonce, claims: claims}
	i.mu.Unlock()
}

// Sign ký một JWT RS256 bằng khoá của IdP giả; tự thêm iat/exp nếu thiếu.
func (i *IdP) Sign(t testing.TB, claims map[string]interface{}, typ string, ttl time.Duration) string {
	t.Helper()
	return SignWith(t, i.Key, Kid, claims, typ, ttl)
}

func SignWith(t testing.TB, key *rsa.PrivateKey, kid string, claims map[string]interface{}, typ string, ttl time.Duration) string {
	t.Helper()
	mc := jwt.MapClaims{}
	for k, v := range claims {
		mc[k] = v
	}
	if _, ok := mc["iat"]; !ok {
		mc["iat"] = time.Now().Unix()
	}
	if _, ok := mc["exp"]; !ok {
		mc["exp"] = time.Now().Add(ttl).Unix()
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, mc)
	token.Header["kid"] = kid
	token.Header["typ"] = typ
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
