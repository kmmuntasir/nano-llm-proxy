package gateway

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

const testGoogleClientID = "test-client.apps.googleusercontent.com"

// fakeGoogle is a JWKS endpoint plus a token minter for one signing key.
type fakeGoogle struct {
	key     *rsa.PrivateKey
	kid     string
	fetches atomic.Int32
	srv     *httptest.Server
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGoogle{key: k, kid: "kid-1"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.fetches.Add(1)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": f.kid, "kty": "RSA", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
		}}})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) verifier() *googleVerifier {
	v := newGoogleVerifier(testGoogleClientID, f.srv.Client())
	v.certsURL = f.srv.URL
	return v
}

// claims returns a valid claim set for email; tests mutate it.
func validGoogleClaims(email, hd string) map[string]any {
	now := time.Now().Unix()
	c := map[string]any{
		"iss": "https://accounts.google.com", "aud": testGoogleClientID, "sub": "1234",
		"email": email, "email_verified": true, "name": "Test Person",
		"iat": now, "exp": now + 3600,
	}
	if hd != "" {
		c["hd"] = hd
	}
	return c
}

func (f *fakeGoogle) sign(t *testing.T, header, claims map[string]any, key *rsa.PrivateKey) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(header) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fakeGoogle) token(t *testing.T, claims map[string]any) string {
	return f.sign(t, map[string]any{"alg": "RS256", "kid": f.kid, "typ": "JWT"}, claims, f.key)
}

func TestGoogleVerifierAcceptsValidAndRejectsTampered(t *testing.T) {
	f := newFakeGoogle(t)
	v := f.verifier()
	ctx := context.Background()

	c, err := v.verify(ctx, f.token(t, validGoogleClaims("Person@Example.com", "example.com")))
	if err != nil || c.Email != "person@example.com" || c.HD != "example.com" {
		t.Fatalf("valid token: claims=%+v err=%v", c, err)
	}

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	mut := func(edit func(map[string]any)) map[string]any {
		c := validGoogleClaims("p@example.com", "example.com")
		edit(c)
		return c
	}
	cases := map[string]string{
		"wrong audience":       f.token(t, mut(func(c map[string]any) { c["aud"] = "someone-else" })),
		"wrong issuer":         f.token(t, mut(func(c map[string]any) { c["iss"] = "https://evil.example" })),
		"expired":              f.token(t, mut(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() })),
		"issued in the future": f.token(t, mut(func(c map[string]any) { c["iat"] = time.Now().Add(time.Hour).Unix() })),
		"email unverified":     f.token(t, mut(func(c map[string]any) { c["email_verified"] = false })),
		"no email":             f.token(t, mut(func(c map[string]any) { delete(c, "email") })),
		"foreign signature":    f.sign(t, map[string]any{"alg": "RS256", "kid": f.kid}, validGoogleClaims("p@example.com", ""), other),
		"alg none":             f.sign(t, map[string]any{"alg": "none", "kid": f.kid}, validGoogleClaims("p@example.com", ""), f.key),
		"alg HS256":            f.sign(t, map[string]any{"alg": "HS256", "kid": f.kid}, validGoogleClaims("p@example.com", ""), f.key),
		"malformed":            "a.b",
	}
	for name, tok := range cases {
		if _, err := v.verify(ctx, tok); !errors.Is(err, errGoogleToken) {
			t.Errorf("%s: err = %v, want errGoogleToken", name, err)
		}
	}
	// a body whose signature belonged to different claims
	good := f.token(t, validGoogleClaims("p@example.com", ""))
	parts := strings.Split(good, ".")
	forged, _ := json.Marshal(validGoogleClaims("admin@example.com", ""))
	if _, err := v.verify(ctx, parts[0]+"."+base64.RawURLEncoding.EncodeToString(forged)+"."+parts[2]); !errors.Is(err, errGoogleToken) {
		t.Errorf("swapped claims: err = %v", err)
	}
	// string "true" (older tokens) counts as verified
	c2 := validGoogleClaims("p@example.com", "")
	c2["email_verified"] = "true"
	if _, err := v.verify(ctx, f.token(t, c2)); err != nil {
		t.Errorf(`email_verified "true": %v`, err)
	}
}

// TestGoogleVerifierKeyCache: keys are fetched once and cached; an unknown
// kid refetches at most once a minute (garbage kids can't hammer Google); a
// rotated key is picked up by that refetch.
func TestGoogleVerifierKeyCache(t *testing.T) {
	f := newFakeGoogle(t)
	v := f.verifier()
	now := time.Now()
	v.now = func() time.Time { return now }
	ctx := context.Background()
	for range 3 {
		if _, err := v.verify(ctx, f.token(t, validGoogleClaims("p@example.com", ""))); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.fetches.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1 (cached)", n)
	}
	unknown := f.sign(t, map[string]any{"alg": "RS256", "kid": "kid-unknown"}, validGoogleClaims("p@example.com", ""), f.key)
	for range 3 {
		if _, err := v.verify(ctx, unknown); !errors.Is(err, errGoogleToken) {
			t.Fatalf("unknown kid: %v", err)
		}
	}
	if n := f.fetches.Load(); n != 1 {
		t.Fatalf("fetches after unknown kids = %d, want 1 (rate-limited within a minute of the last fetch)", n)
	}
	// Google rotates keys; a minute later the new kid is fetched and accepted
	f.kid = "kid-2"
	now = now.Add(2 * time.Minute)
	if _, err := v.verify(ctx, f.token(t, validGoogleClaims("p@example.com", ""))); err != nil {
		t.Fatalf("rotated key: %v", err)
	}
	if n := f.fetches.Load(); n != 2 {
		t.Fatalf("fetches after rotation = %d, want 2", n)
	}
}

func TestGoogleDomainAllowed(t *testing.T) {
	cases := []struct {
		email, hd, domain string
		want              bool
	}{
		{"a@example.com", "example.com", "example.com", true},
		{"a@example.com", "EXAMPLE.com", "example.com", true},
		{"a@example.com", "", "example.com", false},          // consumer account with a look-alike address
		{"a@other.com", "example.com", "example.com", false}, // alias outside the domain
		{"a@gmail.com", "", "", true},                        // no restriction
	}
	for _, c := range cases {
		if got := googleDomainAllowed(&googleClaims{Email: c.email, HD: c.hd}, c.domain); got != c.want {
			t.Errorf("%s hd=%q domain=%q: got %v, want %v", c.email, c.hd, c.domain, got, c.want)
		}
	}
}

// googleGateway is a store-backed gateway with Google sign-in configured
// against a fake Google.
func googleGateway(t *testing.T, domain string) (*gateway, *fakeGoogle) {
	t.Helper()
	g, _ := testStoreGateway(t, "http://127.0.0.1:1")
	cfg := *g.conf()
	cfg.GoogleClientID, cfg.GoogleWorkspaceDomain = testGoogleClientID, domain
	g.cfgPtr.Store(&cfg)
	f := newFakeGoogle(t)
	g.google = f.verifier()
	return g, f
}

func googleLogin(g *gateway, token, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/auth/google", strings.NewReader(`{"credential":"`+token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.RemoteAddr = "10.1.1.1:4444"
	rec := httptest.NewRecorder()
	g.handleGoogleLogin(rec, req)
	return rec
}

func TestGoogleLoginFlows(t *testing.T) {
	t.Run("existing account signs in and keeps its role", func(t *testing.T) {
		g, f := googleGateway(t, "")
		rec := googleLogin(g, f.token(t, validGoogleClaims("Admin@example.com", "")), "")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"role":"superadmin"`) {
			t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
		}
		if c := rec.Result().Cookies(); len(c) == 0 || c[0].Name != sessionCookie || !c[0].HttpOnly {
			t.Fatalf("no session cookie: %+v", c)
		}
	})
	t.Run("no domain: unknown email is refused, not created", func(t *testing.T) {
		g, f := googleGateway(t, "")
		rec := googleLogin(g, f.token(t, validGoogleClaims("stranger@gmail.com", "")), "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
		}
		if u, _ := g.store.UserByEmail("stranger@gmail.com"); u != nil {
			t.Fatal("user was created without a domain restriction")
		}
	})
	t.Run("domain: first sign-in creates a regular user, second reuses it", func(t *testing.T) {
		g, f := googleGateway(t, "corp.example")
		tok := f.token(t, validGoogleClaims("new.person@corp.example", "corp.example"))
		for range 2 {
			if rec := googleLogin(g, tok, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"role":"user"`) {
				t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
			}
		}
		u, _ := g.store.UserByEmail("new.person@corp.example")
		if u == nil || u.Role != "user" || u.Name != "Test Person" || u.PasswordHash != "" {
			t.Fatalf("provisioned user = %+v", u)
		}
		users, _ := g.store.ListUsers()
		if len(users) != 2 {
			t.Fatalf("users = %d, want 2 (admin + one provisioned)", len(users))
		}
	})
	t.Run("domain: outside accounts are refused", func(t *testing.T) {
		g, f := googleGateway(t, "corp.example")
		for _, c := range []map[string]any{
			validGoogleClaims("x@gmail.com", ""),
			validGoogleClaims("x@corp.example", ""), // look-alike consumer account, no hd
		} {
			if rec := googleLogin(g, f.token(t, c), ""); rec.Code != http.StatusForbidden {
				t.Fatalf("%v: got %d: %s", c["email"], rec.Code, rec.Body.String())
			}
		}
	})
	t.Run("disabled account is refused", func(t *testing.T) {
		g, f := googleGateway(t, "corp.example")
		googleLogin(g, f.token(t, validGoogleClaims("p@corp.example", "corp.example")), "")
		u, _ := g.store.UserByEmail("p@corp.example")
		disabled := true
		if err := g.store.UpdateUser(u.ID, nil, nil, nil, nil, &disabled); err != nil {
			t.Fatal(err)
		}
		if rec := googleLogin(g, f.token(t, validGoogleClaims("p@corp.example", "corp.example")), ""); rec.Code != http.StatusForbidden {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("bad token and cross-origin are refused", func(t *testing.T) {
		g, f := googleGateway(t, "")
		if rec := googleLogin(g, "not.a.token", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("bad token: got %d", rec.Code)
		}
		if rec := googleLogin(g, f.token(t, validGoogleClaims("admin@example.com", "")), "https://evil.example"); rec.Code != http.StatusForbidden {
			t.Fatalf("cross-origin: got %d", rec.Code)
		}
	})
	t.Run("not configured is 404", func(t *testing.T) {
		g, _ := testStoreGateway(t, "http://127.0.0.1:1")
		if rec := googleLogin(g, "x.y.z", ""); rec.Code != http.StatusNotFound {
			t.Fatalf("got %d", rec.Code)
		}
	})
}

func authConfig(t *testing.T, g *gateway) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handleAuthConfig(rec, httptest.NewRequest("GET", "/api/auth/config", nil))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func passwordLogin(g *gateway) int {
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"email":"admin@example.com","password":"super-secret-pass"}`))
	req.RemoteAddr = "10.2.2.2:5555"
	rec := httptest.NewRecorder()
	g.handleLogin(rec, req)
	return rec.Code
}

// TestCredentialLoginSwitch: password sign-in can be turned off only when
// Google is configured, and a stored "off" without Google stays effectively
// on — no configuration can lock every account out.
func TestCredentialLoginSwitch(t *testing.T) {
	put := func(g *gateway, cookie *http.Cookie, on bool) *httptest.ResponseRecorder {
		rs := g.rs().Clone()
		rs.Auth.CredentialLogin = on
		body, _ := json.Marshal(rs)
		req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(string(body)))
		req.AddCookie(cookie)
		req.Header.Set("Origin", "https://gateway.example.com")
		rec := httptest.NewRecorder()
		g.requireSession(g.requireSuperadmin(g.handlePutSettings))(rec, req)
		return rec
	}

	t.Run("without Google", func(t *testing.T) {
		g, _ := testStoreGateway(t, "http://127.0.0.1:1")
		cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
		if rec := put(g, cookie, false); rec.Code != http.StatusBadRequest {
			t.Fatalf("disable without Google: got %d: %s", rec.Code, rec.Body.String())
		}
		if cfg := authConfig(t, g); cfg["credentialLogin"] != true || cfg["google"] != nil {
			t.Fatalf("auth config = %v", cfg)
		}
		// a stored false (e.g. Google env removed later) is overridden
		rs := g.rs().Clone()
		rs.Auth.CredentialLogin = false
		g.rsPtr.Store(rs)
		if code := passwordLogin(g); code != 200 {
			t.Fatalf("password login with stored false and no Google: got %d", code)
		}
	})

	t.Run("with Google", func(t *testing.T) {
		g, _ := googleGateway(t, "corp.example")
		cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
		cfg := authConfig(t, g)
		google, _ := cfg["google"].(map[string]any)
		if cfg["credentialLogin"] != true || google["clientId"] != testGoogleClientID || google["domain"] != "corp.example" {
			t.Fatalf("auth config = %v", cfg)
		}
		// the acting admin (admin@example.com) can't pass corp.example Google
		// sign-in, so turning passwords off would lock them out
		if rec := put(g, cookie, false); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "lock you out") {
			t.Fatalf("self-lockout: got %d: %s", rec.Code, rec.Body.String())
		}
		g.cfgPtr.Load().GoogleWorkspaceDomain = "example.com"
		if rec := put(g, cookie, false); rec.Code != 200 {
			t.Fatalf("disable with Google: got %d: %s", rec.Code, rec.Body.String())
		}
		if code := passwordLogin(g); code != http.StatusForbidden {
			t.Fatalf("password login while disabled: got %d", code)
		}
		if cfg := authConfig(t, g); cfg["credentialLogin"] != false {
			t.Fatalf("auth config after disable = %v", cfg)
		}
		stored, _ := g.store.LoadRuntimeSettings()
		if stored.Auth.CredentialLogin {
			t.Fatal("disable was not persisted")
		}
		if rec := put(g, cookie, true); rec.Code != 200 || passwordLogin(g) != 200 {
			t.Fatalf("re-enable: got %d", rec.Code)
		}
	})

	t.Run("omitted field keeps the default", func(t *testing.T) {
		rs := settings.DefaultRuntimeSettings()
		if err := json.Unmarshal([]byte(`{"rotation":"lru"}`), rs); err != nil || !rs.Auth.CredentialLogin {
			t.Fatalf("omitted auth: %+v err=%v", rs.Auth, err)
		}
	})
}
