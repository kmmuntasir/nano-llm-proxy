package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// --- helpers ---

// boolPtr is a small convenience for store PATCH-style tests.
func boolPtr(b bool) *bool { return &b }

// testStoreGateway bootstraps a store-backed gateway whose builtin providers
// point at the given mock upstream (base_url lives in the DB in store mode),
// so /v1/models and chat work without network access.
func testStoreGateway(t *testing.T, upURL string) (*gateway, *Store) {
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
	g, err := newGatewayFromStore(cfg, st)
	if err != nil {
		t.Fatalf("newGatewayFromStore: %v", err)
	}
	return g, st
}

// addGenericProvider inserts a GUI-style openai provider and refreshes the
// hot path (the same thing applyMutation does after a real API call).
func addGenericProvider(t *testing.T, st *Store, name, baseURL string, keys ...string) *Provider {
	t.Helper()
	p := &Provider{Name: name, Type: "openai", BaseURL: baseURL, Enabled: true, SortOrder: 100}
	pks := make([]ProviderKey, 0, len(keys))
	for _, k := range keys {
		pks = append(pks, ProviderKey{Key: k})
	}
	if err := st.CreateProvider(p, pks); err != nil {
		t.Fatalf("CreateProvider(%s): %v", name, err)
	}
	return p
}

// newClientKey creates a client key and refreshes the cache, mirroring the
// post-write reload applyMutation performs in production.
func newClientKey(t *testing.T, g *gateway, st *Store, userID int64, alias string) string {
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
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "unknown provider") {
		t.Fatalf("disabled provider: got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- GUI auth: sessions, roles, origin, backoff, expiry ---

func insertPlainUser(t *testing.T, st *Store, email, pass, role string) *User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), 10)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	u := &User{Name: "Pi", Email: email, PasswordHash: string(hash), Role: role}
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
	if uas[0] != g.cfg.Zen.UserAgent {
		t.Fatalf("zen call UA = %q, want %q", uas[0], g.cfg.Zen.UserAgent)
	}
	if !strings.HasPrefix(sessions[0], "ses_") {
		t.Fatalf("zen call missing session header: %q", sessions[0])
	}
	if strings.Contains(uas[1], "opencode/") || sessions[1] != "" {
		t.Fatalf("plain call carried zen fingerprint: UA=%q session=%q", uas[1], sessions[1])
	}
}
