package gateway

import (
	"encoding/json"
	"fmt"
	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// --- helpers ---

// boolPtr is a small convenience for store PATCH-style tests.
func boolPtr(b bool) *bool { return &b }

// testStoreGateway bootstraps a store-backed gateway whose builtin providers
// point at the given mock upstream (base_url lives in the DB in store mode),
// so /v1/models and chat work without network access.
func testStoreGateway(t *testing.T, upURL string) (*gateway, *store.Store) {
	t.Helper()
	st := openTestStore(t)
	cfg := testCfg()
	cfg.APIKeys = nil
	cfg.TrustedOrigins = []string{"gateway.example.com"}
	if err := st.Bootstrap(cfg, testKeyFile(), "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	provs, _ := st.ListProviders()
	for _, p := range provs {
		if p.Builtin {
			if err := st.UpdateProvider(p.ID, nil, &upURL, nil); err != nil {
				t.Fatalf("repoint builtin: %v", err)
			}
		}
	}
	g, err := NewGatewayFromStore(cfg, testRuntime(), st)
	if err != nil {
		t.Fatalf("newGatewayFromStore: %v", err)
	}
	return g, st
}

// addGenericProvider inserts a GUI-style openai provider and refreshes the
// hot path (the same thing applyMutation does after a real API call).
func addGenericProvider(t *testing.T, st *store.Store, name, baseURL string, keys ...string) *store.Provider {
	t.Helper()
	p := &store.Provider{Name: name, Type: "openai", BaseURL: baseURL, Enabled: true, SortOrder: 100}
	pks := make([]store.ProviderKey, 0, len(keys))
	for _, k := range keys {
		pks = append(pks, store.ProviderKey{Key: k})
	}
	if err := st.CreateProvider(p, pks); err != nil {
		t.Fatalf("CreateProvider(%s): %v", name, err)
	}
	return p
}

// newClientKey creates a client key and refreshes the cache, mirroring the
// post-write reload applyMutation performs in production.
func newClientKey(t *testing.T, g *gateway, st *store.Store, userID int64, alias string) string {
	t.Helper()
	plain, _, err := st.CreateClientKey(userID, alias)
	if err != nil {
		t.Fatalf("CreateClientKey: %v", err)
	}
	if err := g.ck.reload(st); err != nil {
		t.Fatalf("ck.reload: %v", err)
	}
	return plain
}

func loginAs(t *testing.T, g *gateway, email, pass string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(fmt.Sprintf(`{"email":%q,"password":%q}`, email, pass)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.9.9.9:5555"
	g.handleLogin(rec, req)
	if rec.Code != 200 {
		t.Fatalf("login %s: got %d: %s", email, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatalf("login %s: no %s cookie", email, sessionCookie)
	return nil
}

// sseOK writes a minimal streaming chat completion.
func sseOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
}

// --- generic (GUI-added openai) providers ---

func TestGenericProviderProxyRotation(t *testing.T) {
	var mu sync.Mutex
	var seenKeys, seenModels, seenUAs []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seenKeys = append(seenKeys, r.Header.Get("Authorization"))
		seenModels = append(seenModels, body.Model)
		seenUAs = append(seenUAs, r.Header.Get("User-Agent"))
		mu.Unlock()
		if r.Header.Get("Authorization") == "Bearer tk1" {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		sseOK(w)
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addGenericProvider(t, st, "together", up.URL, "tk1", "tk2")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}
	admin, _ := st.UserByEmail("admin@example.com")
	plain := newClientKey(t, g, st, admin.ID, "rot-test")

	call := func(model string) *httptest.ResponseRecorder {
		req := chatReq("together", model)
		req.Header.Set("Authorization", "Bearer "+plain)
		rec := httptest.NewRecorder()
		g.clientOnly(g.handleChat)(rec, req)
		return rec
	}

	// [1m] is stripped for generics too; the -4K part must survive untouched
	rec := call("foo-4K[1m]")
	if rec.Code != 200 {
		t.Fatalf("first request: got %d: %s", rec.Code, rec.Body.String())
	}
	rec = call("bar-4K")
	if rec.Code != 200 {
		t.Fatalf("second request: got %d: %s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	// request 1 hits tk1 (500) then tk2; request 2 goes straight to tk2
	if len(seenModels) != 3 || seenModels[0] != "foo-4K" || seenModels[1] != "foo-4K" || seenModels[2] != "bar-4K" {
		t.Fatalf("models mangled: %v", seenModels)
	}
	// rotation: tk1 fails once, tk2 serves both requests
	if len(seenKeys) != 3 || seenKeys[0] != "Bearer tk1" || seenKeys[1] != "Bearer tk2" || seenKeys[2] != "Bearer tk2" {
		t.Fatalf("rotation wrong: %v", seenKeys)
	}
	// generic providers must NOT get the zen fingerprint UA
	for i, ua := range seenUAs {
		if strings.Contains(ua, "opencode/") {
			t.Fatalf("request %d carried the zen fingerprint UA: %s", i, ua)
		}
	}

	// activity ring attributes both successes to the client key + owner
	recent := g.usage.recent(activityRingSize)
	if len(recent) != 2 {
		t.Fatalf("activity ring has %d entries, want 2", len(recent))
	}
	for _, e := range recent {
		if e.Provider != "together" || e.KeyAlias != "rot-test" || e.User != "admin@example.com" || e.Status != 200 {
			t.Fatalf("activity entry wrong: %+v", e)
		}
	}
}

func TestGenericProviderModelsAndCatalogInvalidation(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"m1","context_length":4096},{"id":"m2"}]}`))
			return
		}
		sseOK(w)
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addGenericProvider(t, st, "together", up.URL, "tk1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}

	rec := httptest.NewRecorder()
	g.handleModels(rec, anonReq())
	if rec.Code != 200 {
		t.Fatalf("models: got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"id":"together/m1"`) {
		t.Fatalf("generic passthrough missing together/m1 (no cosmetic suffix): %s", body)
	}
	if !strings.Contains(body, `"context_window":4096`) {
		t.Fatalf("generic context_length not passed through: %s", body)
	}
	g.catMu.Lock()
	cached := g.catalog != nil
	g.catMu.Unlock()
	if !cached {
		t.Fatal("catalog was not cached after first fetch")
	}

	// a CRUD write must invalidate the cache so the new provider shows up
	addGenericProvider(t, st, "glmweb", up.URL, "gk1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools 2: %v", err)
	}
	g.catMu.Lock()
	invalidated := g.catalog == nil
	g.catMu.Unlock()
	if !invalidated {
		t.Fatal("provider CRUD did not invalidate the catalog cache")
	}
	rec2 := httptest.NewRecorder()
	g.handleModels(rec2, anonReq())
	if rec2.Code != 200 {
		t.Fatalf("models 2: got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), `"id":"glmweb/m1"`) {
		t.Fatalf("stale catalog: glmweb missing: %s", rec2.Body.String())
	}
}

func TestDisabledProviderUnknownPrefix(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sseOK(w) }))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	gen := addGenericProvider(t, st, "together", up.URL, "tk1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}

	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("together", "m1"))
	if rec.Code != 200 {
		t.Fatalf("enabled provider should route: got %d: %s", rec.Code, rec.Body.String())
	}

	if err := st.UpdateProvider(gen.ID, nil, nil, boolPtr(false)); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools 2: %v", err)
	}
	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("together", "m1"))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Unknown provider") {
		t.Fatalf("disabled provider: got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- GUI auth: sessions, roles, origin, backoff, expiry ---

func insertPlainUser(t *testing.T, st *store.Store, email, pass, role string) *store.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), 10)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	u := &store.User{Name: "Pi", Email: email, PasswordHash: string(hash), Role: role}
	if err := st.InsertUser(u); err != nil {
		t.Fatalf("InsertUser: %v", err)
	}
	return u
}

func TestLoginSessionRoleGating(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1") // upstream never reached
	insertPlainUser(t, st, "pi@example.com", "pi-password-123", "user")

	// wrong password -> 401
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"email":"admin@example.com","password":"wrong-pass"}`))
	req.RemoteAddr = "10.9.9.9:5555"
	g.handleLogin(rec, req)
	if rec.Code != 401 {
		t.Fatalf("wrong password: got %d", rec.Code)
	}

	// 5 failures lock the ip|email pair, even for the right password
	for i := 0; i < 5; i++ {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("POST", "/api/auth/login",
			strings.NewReader(`{"email":"lock@example.com","password":"nope"}`))
		req.RemoteAddr = "10.9.9.9:5555"
		g.handleLogin(rec, req)
		if rec.Code != 401 {
			t.Fatalf("lock attempt %d: got %d", i, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"email":"lock@example.com","password":"nope"}`))
	req.RemoteAddr = "10.9.9.9:5555"
	g.handleLogin(rec, req)
	if rec.Code != 429 {
		t.Fatalf("locked login: got %d, want 429", rec.Code)
	}

	// superadmin login works, /api/auth/me reflects it
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	rec = httptest.NewRecorder()
	me := httptest.NewRequest("GET", "/api/auth/me", nil)
	me.AddCookie(adminCookie)
	g.requireSession(g.handleMe)(rec, me)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "admin@example.com") {
		t.Fatalf("me: got %d: %s", rec.Code, rec.Body.String())
	}

	// no cookie -> 401
	rec = httptest.NewRecorder()
	g.requireSession(g.handleMe)(rec, httptest.NewRequest("GET", "/api/auth/me", nil))
	if rec.Code != 401 {
		t.Fatalf("no cookie: got %d", rec.Code)
	}

	// plain user: me fine, superadmin endpoints 403
	piCookie := loginAs(t, g, "pi@example.com", "pi-password-123")
	rec = httptest.NewRecorder()
	lu := httptest.NewRequest("GET", "/api/users", nil)
	lu.AddCookie(piCookie)
	lu.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.requireSuperadmin(g.handleListUsers))(rec, lu)
	if rec.Code != 403 {
		t.Fatalf("user calling superadmin API: got %d, want 403", rec.Code)
	}

	// cross-site Origin on a mutation -> 403; trusted origin -> allowed
	rec = httptest.NewRecorder()
	mk := httptest.NewRequest("POST", "/api/me/keys", strings.NewReader(`{"alias":"evil"}`))
	mk.AddCookie(piCookie)
	mk.Header.Set("Origin", "https://evil.example")
	mk.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.handleCreateMyKey)(rec, mk)
	if rec.Code != 403 {
		t.Fatalf("evil origin: got %d, want 403", rec.Code)
	}
	rec = httptest.NewRecorder()
	mk = httptest.NewRequest("POST", "/api/me/keys", strings.NewReader(`{"alias":"ok"}`))
	mk.AddCookie(piCookie)
	mk.Header.Set("Origin", "https://gateway.example.com")
	mk.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.handleCreateMyKey)(rec, mk)
	if rec.Code != 201 {
		t.Fatalf("trusted origin key create: got %d: %s", rec.Code, rec.Body.String())
	}

	// expired session -> 401
	raw, _, err := st.CreateSession(1, -time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rec = httptest.NewRecorder()
	exp := httptest.NewRequest("GET", "/api/auth/me", nil)
	exp.AddCookie(&http.Cookie{Name: sessionCookie, Value: raw})
	g.requireSession(g.handleMe)(rec, exp)
	if rec.Code != 401 {
		t.Fatalf("expired session: got %d, want 401", rec.Code)
	}
}

func TestPasswordChangeKillsSessions(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	admin, _ := st.UserByEmail("admin@example.com")

	// superadmin changes their own password through the GUI API
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/users/"+strconv.FormatInt(admin.ID, 10),
		strings.NewReader(`{"password":"brand-new-password"}`))
	req.SetPathValue("id", strconv.FormatInt(admin.ID, 10))
	req.AddCookie(adminCookie)
	req.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.requireSuperadmin(g.handlePatchUser))(rec, req)
	if rec.Code != 200 {
		t.Fatalf("patch password: got %d: %s", rec.Code, rec.Body.String())
	}

	// the old session is dead
	rec = httptest.NewRecorder()
	me := httptest.NewRequest("GET", "/api/auth/me", nil)
	me.AddCookie(adminCookie)
	g.requireSession(g.handleMe)(rec, me)
	if rec.Code != 401 {
		t.Fatalf("old session survived password change: got %d", rec.Code)
	}

	// the new password logs in
	loginAs(t, g, "admin@example.com", "brand-new-password")
}

// --- upstream fingerprint decision is by provider type ---

func TestPostUpstreamFingerprintByType(t *testing.T) {
	var mu sync.Mutex
	var uas, sessions []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uas = append(uas, r.Header.Get("User-Agent"))
		sessions = append(sessions, r.Header.Get("x-opencode-session"))
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer up.Close()

	g, _ := testStoreGateway(t, up.URL)
	req := anonReq()

	// zen-style: UA floor + session header both present
	resp, ok := g.postUpstream(req, up.URL+"/chat/completions", "k", []byte("{}"), true)
	if !ok {
		t.Fatal("postUpstream failed")
	}
	resp.Body.Close()
	resp, ok = g.postUpstream(req, up.URL+"/chat/completions", "k", []byte("{}"), false)
	if !ok {
		t.Fatal("postUpstream 2 failed")
	}
	resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(uas) != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", len(uas))
	}
	if uas[0] != g.rs().Zen.UserAgent {
		t.Fatalf("zen call UA = %q, want %q", uas[0], g.rs().Zen.UserAgent)
	}
	if !strings.HasPrefix(sessions[0], "ses_") {
		t.Fatalf("zen call missing session header: %q", sessions[0])
	}
	if strings.Contains(uas[1], "opencode/") || sessions[1] != "" {
		t.Fatalf("plain call carried zen fingerprint: UA=%q session=%q", uas[1], sessions[1])
	}
}

// --- settings API ---

func TestSettingsAPIAuth(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1") // upstream never reached
	insertPlainUser(t, st, "pi@example.com", "pi-password-123", "user")

	// no cookie -> 401
	rec := httptest.NewRecorder()
	g.requireSession(g.requireSuperadmin(g.handleGetSettings))(rec, httptest.NewRequest("GET", "/api/settings", nil))
	if rec.Code != 401 {
		t.Fatalf("settings without cookie: got %d, want 401", rec.Code)
	}

	// plain user -> 403 on GET and PUT
	piCookie := loginAs(t, g, "pi@example.com", "pi-password-123")
	rec = httptest.NewRecorder()
	get := httptest.NewRequest("GET", "/api/settings", nil)
	get.AddCookie(piCookie)
	g.requireSession(g.requireSuperadmin(g.handleGetSettings))(rec, get)
	if rec.Code != 403 {
		t.Fatalf("plain user GET settings: got %d, want 403", rec.Code)
	}
	rec = httptest.NewRecorder()
	put := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{}`))
	put.AddCookie(piCookie)
	put.Header.Set("Origin", "https://gateway.example.com")
	put.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.requireSuperadmin(g.handlePutSettings))(rec, put)
	if rec.Code != 403 {
		t.Fatalf("plain user PUT settings: got %d, want 403", rec.Code)
	}

	// superadmin GET returns the effective defaults
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	rec = httptest.NewRecorder()
	get = httptest.NewRequest("GET", "/api/settings", nil)
	get.AddCookie(adminCookie)
	g.requireSession(g.requireSuperadmin(g.handleGetSettings))(rec, get)
	if rec.Code != 200 {
		t.Fatalf("admin GET settings: got %d: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"rotation":"priority"`, `"maxKeysPerRequest":3`, `"respectRetryAfter":true`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("GET settings missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestSettingsPutValidation(t *testing.T) {
	g, _ := testStoreGateway(t, "http://127.0.0.1:1")
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	for _, bad := range []string{
		`{"rotation":"random"}`,
		// note: an explicit maxKeysPerRequest:0 is indistinguishable from an
		// omitted field (both are the zero value) and defaults to 3; >100 is
		// the unambiguous bound violation
		`{"retry":{"maxKeysPerRequest":101}}`,
		`{"retry":{"cooldownSeconds":-1}}`,
		`{"retry":{"maxRequestsPerKeyPerDay":-5}}`,
		`{"anthropic":{"fallbackModel":"noslash"}}`,
		`{"zen":{"userAgent":"opencode/1.0.0"}}`,
		`{"zen":{"modelMeta":{"m":{"contextWindow":0}}}}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(bad))
		req.AddCookie(adminCookie)
		req.Header.Set("Origin", "https://gateway.example.com")
		req.RemoteAddr = "10.9.9.9:5555"
		g.requireSession(g.requireSuperadmin(g.handlePutSettings))(rec, req)
		if rec.Code != 400 {
			t.Fatalf("PUT %s: got %d, want 400", bad, rec.Code)
		}
		if err := g.store.SaveRuntimeSettings(nil); err == nil {
			// SaveRuntimeSettings(nil) would marshal fine — this guard is
			// about the DB never changing on a rejected PUT, asserted below
			_ = err
		}
	}
	rs, err := g.store.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load after rejects: %v", err)
	}
	if rs.Rotation != settings.RotationPriority || rs.Retry.MaxKeysPerRequest != 3 {
		t.Fatalf("rejected PUTs still changed the stored document: %+v", rs)
	}
}

func TestSettingsPutHotReload(t *testing.T) {
	var mu sync.Mutex
	var servedBy []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		servedBy = append(servedBy, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	ck := newClientKey(t, g, st, admin.ID, "settings-test")

	putSettings := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(body))
		req.AddCookie(adminCookie)
		req.Header.Set("Origin", "https://gateway.example.com")
		req.RemoteAddr = "10.9.9.9:5555"
		g.requireSession(g.requireSuperadmin(g.handlePutSettings))(rec, req)
		return rec
	}

	// switch to lru through the API; the pool rebuild inside applyMutation
	// must pick the new mode up before the response is written
	rec := putSettings(t, `{"rotation":"lru"}`)
	if rec.Code != 200 {
		t.Fatalf("PUT lru: got %d: %s", rec.Code, rec.Body.String())
	}
	if g.rs().Rotation != settings.RotationLRU {
		t.Fatalf("rs().Rotation = %q, want lru", g.rs().Rotation)
	}
	stored, err := st.LoadRuntimeSettings()
	if err != nil || stored.Rotation != settings.RotationLRU {
		t.Fatalf("stored rotation = %q err=%v, want lru", stored.Rotation, err)
	}

	// lru alternates across two requests (priority would stick to key a)
	chat := func() {
		t.Helper()
		rec := httptest.NewRecorder()
		req := chatReq("zen", "mimo-test-free")
		req.Header.Set("Authorization", "Bearer "+ck)
		g.clientOnly(g.handleChat)(rec, req)
		if rec.Code != 200 {
			t.Fatalf("chat: got %d: %s", rec.Code, rec.Body.String())
		}
	}
	chat()
	chat()
	mu.Lock()
	defer mu.Unlock()
	if len(servedBy) != 2 || servedBy[0] == servedBy[1] {
		t.Fatalf("lru should alternate keys, served: %v", servedBy)
	}
}

func TestSettingsPutCooldownTakesEffect(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer sk-zen-1" {
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"message":"limited"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	ck := newClientKey(t, g, st, admin.ID, "cooldown-test")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/settings",
		strings.NewReader(`{"retry":{"cooldownSeconds":61,"respectRetryAfter":false}}`))
	req.AddCookie(adminCookie)
	req.Header.Set("Origin", "https://gateway.example.com")
	req.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.requireSuperadmin(g.handlePutSettings))(rec, req)
	if rec.Code != 200 {
		t.Fatalf("PUT cooldown: got %d: %s", rec.Code, rec.Body.String())
	}

	// the 429 on key a must apply the new 61s default cooldown (not the
	// previous 30s, and no Retry-After since respect is off)
	rec = httptest.NewRecorder()
	chat := chatReq("zen", "mimo-test-free")
	chat.Header.Set("Authorization", "Bearer "+ck)
	g.clientOnly(g.handleChat)(rec, chat)
	if rec.Code != 200 {
		t.Fatalf("chat after failover: got %d: %s", rec.Code, rec.Body.String())
	}
	for _, k := range g.zen.snapshot() {
		if k.Label == "key-a" {
			if k.Status != statusCooling {
				t.Fatalf("key a status = %s, want cooling", k.Status)
			}
			if k.CooldownFor == "" || !strings.HasPrefix(k.CooldownFor, "1m") {
				t.Fatalf("cooldown remaining = %q, want ~1m", k.CooldownFor)
			}
		}
	}
}

func TestSettingsPutPreservesSyncStatus(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	if err := st.UpdateSettings(func(rs *settings.RuntimeSettings) *settings.RuntimeSettings {
		rs.Zen.ModelMetaSyncStatus = &settings.ModelMetaSyncStatus{At: 777, OK: true, Added: 3, Updated: 1}
		return rs
	}); err != nil {
		t.Fatalf("seed status: %v", err)
	}
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/settings",
		strings.NewReader(`{"rotation":"lru","zen":{"modelMetaSyncStatus":{"at":1,"ok":false,"error":"spoof"}}}`))
	req.AddCookie(adminCookie)
	req.Header.Set("Origin", "https://gateway.example.com")
	req.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.requireSuperadmin(g.handlePutSettings))(rec, req)
	if rec.Code != 200 {
		t.Fatalf("PUT: got %d: %s", rec.Code, rec.Body.String())
	}
	got, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Zen.ModelMetaSyncStatus == nil || got.Zen.ModelMetaSyncStatus.At != 777 || got.Zen.ModelMetaSyncStatus.Added != 3 {
		t.Fatalf("server-owned sync status was clobbered: %+v", got.Zen.ModelMetaSyncStatus)
	}
	if !strings.Contains(rec.Body.String(), `"at":777`) {
		t.Fatalf("response should echo the preserved status: %s", rec.Body.String())
	}
}

// --- modelMeta sync ---

func TestDecodeOpencodeModels(t *testing.T) {
	stream := strings.NewReader(`{
		"openai": {"models": {"gpt-x": {"limit":{"context":1,"output":1}}}},
		"anthropic": {"models": {}},
		"opencode": {"models": {
			"glm-5": {"limit":{"context":200000,"output":32000},"reasoning":true,"modalities":{"input":["text"]},"description":"big"}
		}}
	}`)
	models, err := decodeOpencodeModels(stream)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want only the opencode subtree", len(models))
	}
	if models["glm-5"].Limit.Context != 200000 || !models["glm-5"].Reasoning {
		t.Fatalf("glm-5 = %+v", models["glm-5"])
	}
	if _, err := decodeOpencodeModels(strings.NewReader(`{"openai":{}}`)); err == nil {
		t.Fatal("expected error when opencode provider is missing")
	}
	if _, err := decodeOpencodeModels(strings.NewReader(`[1,2]`)); err == nil {
		t.Fatal("expected error for non-object document")
	}
}

func TestSyncModelMetaMerge(t *testing.T) {
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"openai": {"models": {"gpt-x": {"limit":{"context":1,"output":1}}}},
			"opencode": {"models": {
				"glm-5": {"limit":{"context":200000,"output":32000},"reasoning":true,"modalities":{"input":["text","image"]},"description":"big"},
				"no-limits": {"reasoning":true}
			}}
		}`))
	}))
	zen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"glm-5"},{"id":"manual-only"}]}`))
			return
		}
		w.WriteHeader(404)
	}))
	g, st := testStoreGateway(t, zen.URL)

	// pre-seed: a stale entry (dead id -> pruned), a manual entry for a live
	// model models.dev doesn't know (survives), and a responses flag on the
	// glm-5 entry (survives the catalog overwrite)
	if err := st.UpdateSettings(func(rs *settings.RuntimeSettings) *settings.RuntimeSettings {
		rs.Zen.ModelMeta["stale-model"] = settings.ModelMeta{ContextWindow: 1, MaxOutputTokens: 1}
		rs.Zen.ModelMeta["manual-only"] = settings.ModelMeta{ContextWindow: 5000, MaxOutputTokens: 100}
		rs.Zen.ModelMeta["glm-5"] = settings.ModelMeta{ContextWindow: 9, MaxOutputTokens: 9, ResponsesAPI: true}
		return rs
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	status := g.syncModelMeta(dev.URL)
	if !status.OK {
		t.Fatalf("sync not ok: %+v", status)
	}
	if status.Added != 0 || status.Updated != 1 || status.Pruned != 1 {
		t.Fatalf("counts = %+v, want added=0 updated=1 pruned=1", status)
	}

	got, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Zen.ModelMeta) != 2 {
		t.Fatalf("meta keys = %+v, want glm-5 + manual-only", got.Zen.ModelMeta)
	}
	glm := got.Zen.ModelMeta["glm-5"]
	if glm.ContextWindow != 200000 || glm.MaxOutputTokens != 32000 || !glm.Reasoning || glm.Description != "big" || len(glm.InputModalities) != 2 {
		t.Fatalf("glm-5 imported wrong: %+v", glm)
	}
	if m := got.Zen.ModelMeta["manual-only"]; m.ContextWindow != 5000 || m.MaxOutputTokens != 100 {
		t.Fatalf("manual entry clobbered: %+v", m)
	}
	if _, exists := got.Zen.ModelMeta["stale-model"]; exists {
		t.Fatal("stale-model should have been pruned")
	}
	if !got.Zen.ModelMeta["glm-5"].ResponsesAPI {
		t.Fatal("responses flag lost after catalog overwrite")
	}
	// the in-memory snapshot the hot path reads must reflect the sync
	if g.rs().Zen.ModelMeta["glm-5"].ContextWindow != 200000 {
		t.Fatal("rsPtr was not refreshed after sync")
	}
	// the persisted status must record success (regression: it used to be
	// snapshotted before OK was flipped)
	if st, err := st.LoadRuntimeSettings(); err != nil || !st.Zen.ModelMetaSyncStatus.OK {
		t.Fatalf("persisted sync status wrong: %+v err=%v", st.Zen.ModelMetaSyncStatus, err)
	}
}

func TestSyncModelMetaFailureKeepsMeta(t *testing.T) {
	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	zen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"glm-5"}]}`))
	}))
	g, st := testStoreGateway(t, zen.URL)
	if err := st.UpdateSettings(func(rs *settings.RuntimeSettings) *settings.RuntimeSettings {
		rs.Zen.ModelMeta["glm-5"] = settings.ModelMeta{ContextWindow: 9, MaxOutputTokens: 9}
		return rs
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	status := g.syncModelMeta(dev.URL)
	if status.OK {
		t.Fatal("sync should have failed")
	}
	if !strings.Contains(status.Error, "500") {
		t.Fatalf("error = %q, want it to mention HTTP 500", status.Error)
	}
	got, _ := st.LoadRuntimeSettings()
	if got.Zen.ModelMeta["glm-5"].ContextWindow != 9 {
		t.Fatalf("failed sync mutated meta: %+v", got.Zen.ModelMeta)
	}
	if got.Zen.ModelMetaSyncStatus == nil || got.Zen.ModelMetaSyncStatus.OK {
		t.Fatalf("failure status not persisted: %+v", got.Zen.ModelMetaSyncStatus)
	}
}

func TestAnthropicFallbackModel(t *testing.T) {
	var mu sync.Mutex
	var seenModels []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seenModels = append(seenModels, body.Model)
		mu.Unlock()
		sseOK(w)
	}))
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	ck := newClientKey(t, g, st, admin.ID, "fallback-test")

	// configure the fallback through the settings API
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/settings",
		strings.NewReader(`{"anthropic":{"fallbackModel":"zen/mimo-v2.6-flash-free"}}`))
	req.AddCookie(adminCookie)
	req.Header.Set("Origin", "https://gateway.example.com")
	req.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.requireSuperadmin(g.handlePutSettings))(rec, req)
	if rec.Code != 200 {
		t.Fatalf("PUT: got %d: %s", rec.Code, rec.Body.String())
	}

	msg := func(model string) {
		t.Helper()
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/messages",
			strings.NewReader(fmt.Sprintf(`{"model":%q,"max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`, model)))
		r.Header.Set("Authorization", "Bearer "+ck)
		g.clientOnly(g.handleMessages)(rec, r)
		if rec.Code != 200 {
			t.Fatalf("messages %q: got %d: %s", model, rec.Code, rec.Body.String())
		}
	}

	// a literal claude-* name (no provider prefix) hits the fallback target
	msg("claude-haiku-4-5")
	// an explicit provider/model passes through untouched
	msg("zen/mimo-test-free")

	mu.Lock()
	defer mu.Unlock()
	if len(seenModels) != 2 || seenModels[0] != "mimo-v2.6-flash-free" || seenModels[1] != "mimo-test-free" {
		t.Fatalf("upstream models = %v, want [mimo-v2.6-flash-free mimo-test-free]", seenModels)
	}
}

// --- usage tracking ---

func TestUsageEventCaptureAndAggregation(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	ck := newClientKey(t, g, st, admin.ID, "usage-test")

	rec := httptest.NewRecorder()
	req := chatReq("zen", "mimo-test-free")
	req.Header.Set("Authorization", "Bearer "+ck)
	g.clientOnly(g.handleChat)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("chat: got %d: %s", rec.Code, rec.Body.String())
	}

	// the request streamed through passthroughSSE, which taps the usage chunk
	events := g.usageBuf.drain()
	if len(events) != 1 {
		t.Fatalf("drained %d events, want 1", len(events))
	}
	e := events[0]
	if e.UserID != admin.ID || e.Provider != "zen" || e.Model != "mimo-test-free" {
		t.Fatalf("event attribution wrong: %+v", e)
	}
	if e.InputTokens != 11 || e.OutputTokens != 7 || e.Status != 200 {
		t.Fatalf("tokens/status wrong: %+v", e)
	}

	now := time.Now().Unix()
	if err := st.InsertUsageEvents(events); err != nil {
		t.Fatalf("insert: %v", err)
	}
	totals, err := st.UsageTotals(now-60, now+60, nil)
	if err != nil || totals.Requests != 1 || totals.InputTokens != 11 || totals.OutputTokens != 7 {
		t.Fatalf("totals = %+v err=%v", totals, err)
	}
	models, _ := st.UsageByModel(now-60, now+60, nil, 5)
	if len(models) != 1 || models[0].Key != "mimo-test-free" || models[0].Requests != 1 {
		t.Fatalf("by-model = %+v", models)
	}
	// per-user scoping: another user sees nothing
	other := int64(admin.ID + 999)
	scoped, _ := st.UsageTotals(now-60, now+60, &other)
	if scoped.Requests != 0 {
		t.Fatalf("scoping leaked: %+v", scoped)
	}
}

func TestUsageAPIScoping(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sseOK(w) }))
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	insertPlainUser(t, st, "pi@example.com", "pi-password-123", "user")
	pi, _ := st.UserByEmail("pi@example.com")
	ckAdmin := newClientKey(t, g, st, admin.ID, "admin-key")
	ckPi := newClientKey(t, g, st, pi.ID, "pi-key")

	// one request per user
	for _, ck := range []string{ckAdmin, ckPi} {
		rec := httptest.NewRecorder()
		req := chatReq("zen", "mimo-test-free")
		req.Header.Set("Authorization", "Bearer "+ck)
		g.clientOnly(g.handleChat)(rec, req)
	}
	events := g.usageBuf.drain()
	if len(events) != 2 {
		t.Fatalf("drained %d events, want 2", len(events))
	}
	if err := st.InsertUsageEvents(events); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// superadmin sees both, pi sees only their own
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	piCookie := loginAs(t, g, "pi@example.com", "pi-password-123")
	get := func(cookie *http.Cookie) string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/usage/activity?limit=10", nil)
		req.AddCookie(cookie)
		g.requireSession(g.handleUsageActivity)(rec, req)
		if rec.Code != 200 {
			t.Fatalf("activity: got %d: %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	all := get(adminCookie)
	if !strings.Contains(all, "admin@example.com") || !strings.Contains(all, "pi@example.com") {
		t.Fatalf("superadmin should see all users: %s", all)
	}
	own := get(piCookie)
	if strings.Contains(own, "admin@example.com") || !strings.Contains(own, "pi@example.com") {
		t.Fatalf("user scoping broken: %s", own)
	}

	// superadmin-only per-user breakdown
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/usage/users", nil)
	req.AddCookie(piCookie)
	g.requireSession(g.requireSuperadmin(g.handleUsageUsers))(rec, req)
	if rec.Code != 403 {
		t.Fatalf("plain user /usage/users: got %d, want 403", rec.Code)
	}
}

func TestChangeMyPassword(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	insertPlainUser(t, st, "pw@example.com", "current-pass-123", "user")
	piCookie := loginAs(t, g, "pw@example.com", "current-pass-123")

	change := func(current, next string) int {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/me/password",
			strings.NewReader(fmt.Sprintf(`{"currentPassword":%q,"newPassword":%q}`, current, next)))
		req.AddCookie(piCookie)
		req.RemoteAddr = "10.9.9.9:5555"
		g.requireSession(g.handleChangeMyPassword)(rec, req)
		return rec.Code
	}

	// wrong current password -> 403
	if code := change("wrong-pass-1234", "brand-new-pass-1"); code != 403 {
		t.Fatalf("wrong current: got %d, want 403", code)
	}
	// too-short new password -> 400
	if code := change("current-pass-123", "short"); code != 400 {
		t.Fatalf("short new: got %d, want 400", code)
	}
	// happy path -> 200 and the old session is wiped
	if code := change("current-pass-123", "brand-new-pass-1"); code != 200 {
		t.Fatalf("happy path: got %d", code)
	}
	rec := httptest.NewRecorder()
	me := httptest.NewRequest("GET", "/api/auth/me", nil)
	me.AddCookie(piCookie)
	g.requireSession(g.handleMe)(rec, me)
	if rec.Code != 401 {
		t.Fatalf("old session survived password change: got %d", rec.Code)
	}
	// the new password logs in
	loginAs(t, g, "pw@example.com", "brand-new-pass-1")
}

func TestUsageTimeseries(t *testing.T) {
	st := openTestStore(t)
	admin := &store.User{Name: "a", Email: "a@x.test", PasswordHash: "h", Role: "superadmin"}
	if err := st.InsertUser(admin); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	day0 := int64(1790000000) // arbitrary midnight-aligned-ish base
	day0 = day0 - day0%86400  // snap to UTC midnight
	events := []store.UsageEvent{
		{TS: day0 + 3600, UserID: admin.ID, ClientKeyID: 1, Provider: "zen", Model: "m", InputTokens: 10, OutputTokens: 5, Status: 200},
		{TS: day0 + 7200, UserID: admin.ID, ClientKeyID: 1, Provider: "kilo", Model: "m", InputTokens: 20, OutputTokens: 8, Status: 200},
		{TS: day0 + 86400 + 60, UserID: admin.ID, ClientKeyID: 1, Provider: "zen", Model: "m", InputTokens: 100, OutputTokens: 50, Status: 200},
	}
	if err := st.InsertUsageEvents(events); err != nil {
		t.Fatalf("insert: %v", err)
	}

	pts, err := st.UsageTimeseries(day0, day0+2*86400, 86400, 0, nil)
	if err != nil {
		t.Fatalf("timeseries: %v", err)
	}
	if len(pts) != 2 {
		t.Fatalf("got %d points, want 2 daily buckets: %+v", len(pts), pts)
	}
	if pts[0].TS != day0 || pts[0].Requests != 2 || pts[0].InputTokens != 30 || pts[0].OutputTokens != 13 {
		t.Fatalf("bucket 1 wrong: %+v", pts[0])
	}
	if pts[1].TS != day0+86400 || pts[1].Requests != 1 {
		t.Fatalf("bucket 2 wrong: %+v", pts[1])
	}

	pp, err := st.UsageProviderTimeseries(day0, day0+2*86400, 86400, 0, nil)
	if err != nil || len(pp) != 3 {
		t.Fatalf("provider series: %d points err=%v, want 3", len(pp), err)
	}

	// tz shift moves the bucket boundary: +1h offset pulls the 00:00+1h event
	// of day 2 back into day 1's late bucket? No — it moves boundaries east;
	// the day0+3600 event lands in the previous local day when tz=+7200.
	shifted, err := st.UsageTimeseries(day0-7200, day0+2*86400, 86400, 7200, nil)
	if err != nil {
		t.Fatalf("shifted: %v", err)
	}
	if len(shifted) < 2 {
		t.Fatalf("shifted buckets = %d, want >= 2", len(shifted))
	}
}

func TestClientKeyLifecycleHotPath(t *testing.T) {
	st := openTestStore(t)
	if err := st.Bootstrap(testCfg(), testKeyFile(), "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	admin, _ := st.UserByEmail("admin@example.com")
	cfg := testCfg()
	g, err := NewGatewayFromStore(cfg, testRuntime(), st)
	if err != nil {
		t.Fatalf("newGatewayFromStore: %v", err)
	}

	plaintext, _, err := st.CreateClientKey(admin.ID, "test")
	if err != nil {
		t.Fatalf("CreateClientKey: %v", err)
	}

	// unknown key -> 401
	rec := httptest.NewRecorder()
	g.clientOnly(passThrough200)(rec, anonReq())
	if rec.Code != 401 {
		t.Fatalf("unknown key: got %d want 401", rec.Code)
	}

	// fresh key accepted only after a cache reload — direct store writes do not
	// touch the hot path; GUI mutations go through applyMutation which does.
	// This pins the documented hot-path contract.
	if err := g.ck.reload(st); err != nil {
		t.Fatalf("reload: %v", err)
	}
	rec = httptest.NewRecorder()
	g.clientOnly(passThrough200)(rec, bearerReq(plaintext))
	if rec.Code != 200 {
		t.Fatalf("fresh key: got %d want 200 (%s)", rec.Code, rec.Body.String())
	}

	// disabled key -> 401 after reload
	keyID := ckID(st, plaintext)
	if err := st.SetClientKeyDisabled(keyID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := g.ck.reload(st); err != nil {
		t.Fatalf("reload: %v", err)
	}
	rec = httptest.NewRecorder()
	g.clientOnly(passThrough200)(rec, bearerReq(plaintext))
	if rec.Code != 401 {
		t.Fatalf("disabled key: got %d want 401", rec.Code)
	}

	// re-enabled -> 200, deleted -> 401
	if err := st.SetClientKeyDisabled(keyID, false); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if err := g.ck.reload(st); err != nil {
		t.Fatalf("reload: %v", err)
	}
	rec = httptest.NewRecorder()
	g.clientOnly(passThrough200)(rec, bearerReq(plaintext))
	if rec.Code != 200 {
		t.Fatalf("re-enabled key: got %d want 200", rec.Code)
	}
	if err := st.DeleteClientKey(keyID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := g.ck.reload(st); err != nil {
		t.Fatalf("reload: %v", err)
	}
	rec = httptest.NewRecorder()
	g.clientOnly(passThrough200)(rec, bearerReq(plaintext))
	if rec.Code != 401 {
		t.Fatalf("deleted key: got %d want 401", rec.Code)
	}
}

func TestUsageFlushAndActivityRing(t *testing.T) {
	st := openTestStore(t)
	if err := st.Bootstrap(testCfg(), testKeyFile(), "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	admin, _ := st.UserByEmail("admin@example.com")
	_, ck, err := st.CreateClientKey(admin.ID, "flush-test")
	if err != nil {
		t.Fatalf("CreateClientKey: %v", err)
	}
	ut := newUsageTracker()
	for i := 0; i < 3; i++ {
		ut.record(ck.ID)
	}
	ut.flush(st)
	keys, _ := st.ListClientKeys(admin.ID)
	var fk *store.ClientKey
	for i := range keys {
		if keys[i].Alias == "flush-test" {
			fk = &keys[i]
		}
	}
	if fk == nil || fk.RequestCount != 3 {
		t.Fatalf("flush did not persist counts: %+v", keys)
	}
	if fk.LastUsedAt == 0 {
		t.Fatal("last_used_at not persisted")
	}

	// ring: 105 entries -> newest first, capped at 100
	for i := 0; i < 105; i++ {
		ut.addActivity(activityEntry{TS: int64(1000 + i), KeyAlias: fmt.Sprintf("k%d", i)})
	}
	recent := ut.recent(activityRingSize)
	if len(recent) != 100 {
		t.Fatalf("ring len = %d, want 100", len(recent))
	}
	if recent[0].KeyAlias != "k104" || recent[99].KeyAlias != "k5" {
		t.Fatalf("ring order wrong: first=%s last=%s", recent[0].KeyAlias, recent[99].KeyAlias)
	}
}
