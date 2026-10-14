package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"github.com/kmmuntasir/nano-llm-proxy/internal/localmode"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// fakeServices records service-manager calls instead of touching
// systemd/launchd.
type fakeServices struct {
	mu         sync.Mutex
	supported  bool
	supervised bool
	installed  map[string]bool
	calls      []string
}

func (f *fakeServices) Supported() bool  { return f.supported }
func (f *fakeServices) Supervised() bool { return f.supervised }
func (f *fakeServices) Register(s localmode.Service) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installed[s.Name] = true
	f.calls = append(f.calls, "register "+s.Name+" "+s.Exec+" "+strings.Join(s.Args, " "))
	return nil
}
func (f *fakeServices) Unregister(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.installed, name)
	f.calls = append(f.calls, "unregister "+name)
	return nil
}
func (f *fakeServices) Status(name string) (bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installed[name], f.installed[name], nil
}
func (f *fakeServices) Restart(name string) error { return nil }

const testLocalPort = 8787

// testLocalGateway is a local-mode gateway over a temp data dir, wrapped in
// the full mux + Host guard exactly as main serves it.
func testLocalGateway(t *testing.T) (*gateway, http.Handler, *fakeServices, store.LocalIdentity) {
	t.Helper()
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Mode, cfg.DataDir, cfg.Port = config.ModeLocal, dir, testLocalPort
	st := openTestStore(t)
	id, err := st.BootstrapLocal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewGatewayFromStore(cfg, rs, st)
	if err != nil {
		t.Fatal(err)
	}
	g.keyCheck = nil
	g.EnableLocal(id, localmode.Service{Name: "gateway", Exec: "/opt/nano/nano-llm-proxy", Args: []string{"local"}})
	fs := &fakeServices{supported: true, supervised: true, installed: map[string]bool{}}
	g.local.svc = fs
	mux := http.NewServeMux()
	g.RegisterRoutes(mux, nil)
	return g, g.Handler(mux), fs, id
}

func localReq(method, target, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Host = "localhost:" + strconv.Itoa(testLocalPort)
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestLocalHostGuard(t *testing.T) {
	_, h, _, _ := testLocalGateway(t)
	for host, want := range map[string]int{
		"localhost:8787":      200,
		"LOCALHOST:8787":      200,
		"127.0.0.1:8787":      200,
		"[::1]:8787":          200,
		"localhost":           403, // no port
		"localhost:9999":      403,
		"evil.example:8787":   403, // DNS rebinding
		"127.0.0.1.nip.io:87": 403,
		"192.168.1.5:8787":    403,
	} {
		r := localReq("GET", "/api/auth/me", "")
		r.Host = host
		if rec := serve(h, r); rec.Code != want {
			t.Errorf("Host %q: %d, want %d", host, rec.Code, want)
		}
	}
	// the guard covers /health and /v1 too, not just the GUI API
	r := localReq("GET", "/health", "")
	r.Host = "evil.example:8787"
	if rec := serve(h, r); rec.Code != 403 {
		t.Errorf("/health from a foreign host: %d", rec.Code)
	}
}

func TestLocalSessionIsTheOwner(t *testing.T) {
	g, h, _, _ := testLocalGateway(t)
	rec := serve(h, localReq("GET", "/api/auth/me", ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), store.LocalOwnerEmail) {
		t.Fatalf("me: %d %s", rec.Code, rec.Body.String())
	}
	// admin routes work without a cookie
	if rec := serve(h, localReq("GET", "/api/settings", "")); rec.Code != 200 {
		t.Fatalf("settings: %d", rec.Code)
	}
	// mutations still need a same-origin Origin (CSRF)
	r := localReq("PATCH", "/api/me", `{"name":"x"}`)
	r.Header.Set("Origin", "https://evil.example")
	if rec := serve(h, r); rec.Code != 403 {
		t.Fatalf("cross-origin mutation: %d", rec.Code)
	}
	// NANO_TRUSTED_ORIGINS widens nothing in local mode: there's no session
	// for a trusted proxy to carry
	g.conf().TrustedOrigins = []string{"gateway.example.com"}
	r = localReq("PATCH", "/api/me", `{"name":"x"}`)
	r.Header.Set("Origin", "https://gateway.example.com")
	if rec := serve(h, r); rec.Code != 403 {
		t.Fatalf("trusted origin in local mode: %d", rec.Code)
	}
	// no sign-in endpoints; logout is a harmless no-op
	if rec := serve(h, localReq("POST", "/api/auth/login", `{"email":"owner@localhost","password":"x"}`)); rec.Code != 404 {
		t.Fatalf("login: %d", rec.Code)
	}
	if rec := serve(h, localReq("POST", "/api/auth/google", `{"credential":"x"}`)); rec.Code != 404 {
		t.Fatalf("google: %d", rec.Code)
	}
	if rec := serve(h, localReq("POST", "/api/auth/logout", "")); rec.Code != 200 {
		t.Fatalf("logout: %d", rec.Code)
	}
	var cfg map[string]any
	rec = serve(h, localReq("GET", "/api/auth/config", ""))
	if json.Unmarshal(rec.Body.Bytes(), &cfg); cfg["mode"] != "local" {
		t.Fatalf("auth config: %s", rec.Body.String())
	}
}

func TestLocalClientAuth(t *testing.T) {
	g, _, _, id := testLocalGateway(t)
	user, _ := g.store.UserByEmail(store.LocalOwnerEmail)
	ownKey, _, err := g.store.CreateClientKey(user.ID, "mine")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.rebuildPools(); err != nil {
		t.Fatal(err)
	}
	ownCK, _ := g.ck.lookup(ownKey)
	var gotKey int64
	h := g.clientOnly(func(w http.ResponseWriter, r *http.Request) {
		ck, _ := contextClientKey(r)
		gotKey = ck.ID
		w.WriteHeader(200)
	})
	call := func(key, origin, ctype string) int {
		gotKey = 0
		r := localReq("POST", "/v1/chat/completions", "{}")
		r.Header.Set("Content-Type", ctype)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return serve(h, r).Code
	}
	const js = "application/json"
	if c := call("", "", js); c != 200 || gotKey != id.KeyID {
		t.Fatalf("keyless: %d key=%d want %d", c, gotKey, id.KeyID)
	}
	if c := call("local", "", js); c != 200 || gotKey != id.KeyID {
		t.Fatalf("placeholder key: %d key=%d", c, gotKey)
	}
	if c := call(ownKey, "", js); c != 200 || gotKey != ownCK.ID {
		t.Fatalf("real key not attributed: %d key=%d want %d", c, gotKey, ownCK.ID)
	}
	// browser-driven requests are refused
	if c := call("", "https://evil.example", js); c != 403 {
		t.Fatalf("foreign origin: %d", c)
	}
	if c := call("", "null", js); c != 403 {
		t.Fatalf("null origin: %d", c)
	}
	if c := call("", "http://localhost:5173", js); c != 200 {
		t.Fatalf("loopback origin: %d", c)
	}
	for _, ct := range []string{"text/plain", "text/plain; x=application/json", ""} {
		if c := call("", "", ct); c != 415 {
			t.Fatalf("content type %q: %d", ct, c)
		}
	}
	if c := call("", "", "Application/JSON; charset=utf-8"); c != 200 {
		t.Fatalf("json with params: %d", c)
	}
	// GET /v1/models has no body to type-check
	r := localReq("GET", "/v1/models", "")
	if rec := serve(h, r); rec.Code != 200 {
		t.Fatalf("keyless GET: %d", rec.Code)
	}

	// requireApiKey on: today's behaviour
	rs := g.rs().Clone()
	rs.Local.RequireAPIKey = true
	g.rsPtr.Store(rs)
	if c := call("", "", js); c != 401 {
		t.Fatalf("required, keyless: %d", c)
	}
	if c := call("local", "", js); c != 401 {
		t.Fatalf("required, placeholder: %d", c)
	}
	if c := call(ownKey, "", js); c != 200 || gotKey != ownCK.ID {
		t.Fatalf("required, real key: %d", c)
	}

	// a disabled implicit key stops keyless traffic
	rs = rs.Clone()
	rs.Local.RequireAPIKey = false
	g.rsPtr.Store(rs)
	if err := g.store.SetClientKeyDisabled(id.KeyID, true); err != nil {
		t.Fatal(err)
	}
	g.rebuildPools() //nolint:errcheck
	if c := call("", "", js); c != 401 {
		t.Fatalf("implicit key disabled: %d", c)
	}
}

func TestLocalInfoAndAutostart(t *testing.T) {
	g, h, fs, _ := testLocalGateway(t)
	var info localInfoView
	rec := serve(h, localReq("GET", "/api/local", ""))
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	dir := g.conf().DataDir
	if info.Port != testLocalPort || info.DataDir != dir || info.LogFile != filepath.Join(dir, "nano-llm-proxy.log") ||
		info.Binary != "/opt/nano/nano-llm-proxy" || !info.Autostart.Supported || info.Autostart.Enabled ||
		info.SearXNG.Installed || info.SearXNG.URL != "http://127.0.0.1:8899" {
		t.Fatalf("info = %+v", info)
	}

	rec = serve(h, localReq("PUT", "/api/local/autostart", `{"enabled":true}`))
	if json.Unmarshal(rec.Body.Bytes(), &info); rec.Code != 200 || !info.Autostart.Enabled {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body.String())
	}
	rec = serve(h, localReq("PUT", "/api/local/autostart", `{"enabled":false}`))
	if json.Unmarshal(rec.Body.Bytes(), &info); rec.Code != 200 || info.Autostart.Enabled {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Join(fs.calls, "|") != "register gateway /opt/nano/nano-llm-proxy local|unregister gateway" {
		t.Fatalf("calls = %q", fs.calls)
	}
	if rec := serve(h, localReq("PUT", "/api/local/autostart", `{}`)); rec.Code != 400 {
		t.Fatalf("missing enabled: %d", rec.Code)
	}

	// SearXNG counts as installed once the installer made its venv
	os.MkdirAll(filepath.Join(dir, "searxng", "venv"), 0o700) //nolint:errcheck
	rec = serve(h, localReq("GET", "/api/local", ""))
	if json.Unmarshal(rec.Body.Bytes(), &info); !info.SearXNG.Installed {
		t.Fatalf("searxng = %+v", info.SearXNG)
	}
}

func TestLocalPort(t *testing.T) {
	g, h, fs, _ := testLocalGateway(t)
	envPath := filepath.Join(g.conf().DataDir, ".env")
	os.WriteFile(envPath, []byte("# mine\nNANO_PORT=8787\n"), 0o600) //nolint:errcheck
	exited := make(chan int, 1)
	g.local.exit = func(code int) { exited <- code }

	for _, bad := range []string{`{"port":80}`, `{"port":70000}`, `{}`} {
		if rec := serve(h, localReq("PUT", "/api/local/port", bad)); rec.Code != 400 {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}

	// not running as the background service: saved, restart by hand
	t.Setenv(LocalServiceEnv, "")
	rec := serve(h, localReq("PUT", "/api/local/port", `{"port":8787}`))
	var out map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != 200 || out["restarting"] != false || out["url"] != "http://localhost:8787" {
		t.Fatalf("manual: %d %s", rec.Code, rec.Body.String())
	}

	// started by the manager but no longer registered: exiting would leave
	// nothing running, so it's a manual restart too
	t.Setenv(LocalServiceEnv, "1")
	rec = serve(h, localReq("PUT", "/api/local/port", `{"port":8787}`))
	if json.Unmarshal(rec.Body.Bytes(), &out); out["restarting"] != false {
		t.Fatalf("unregistered service: %s", rec.Body.String())
	}

	// registered, but nothing restarts what exits (Windows Run key, desktop
	// autostart): manual again
	fs.installed["gateway"] = true
	fs.supervised = false
	rec = serve(h, localReq("PUT", "/api/local/port", `{"port":8787}`))
	if json.Unmarshal(rec.Body.Bytes(), &out); out["restarting"] != false {
		t.Fatalf("unsupervised service: %s", rec.Body.String())
	}

	// as the registered, supervised background service: answer, then exit
	// so the manager restarts us
	fs.supervised = true
	rec = serve(h, localReq("PUT", "/api/local/port", `{"port":8787}`))
	if json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != 200 || out["restarting"] != true {
		t.Fatalf("service: %d %s", rec.Code, rec.Body.String())
	}
	if code := <-exited; code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if b, _ := os.ReadFile(envPath); string(b) != "# mine\nNANO_PORT=8787\n" {
		t.Fatalf(".env = %q", b)
	}
}

func TestServerModeHasNoLocalRoutes(t *testing.T) {
	g, _ := testStoreGateway(t, "http://127.0.0.1:1")
	mux := http.NewServeMux()
	g.RegisterRoutes(mux, nil)
	h := g.Handler(mux)
	cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/local"}, {"PUT", "/api/local/autostart"}, {"PUT", "/api/local/port"},
	} {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
		r.AddCookie(cookie)
		if rec := serve(h, r); rec.Code != 404 {
			t.Errorf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
	// keyless /v1 is still refused, any Host is still served
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	r.Host = "gateway.example.com"
	if rec := serve(h, r); rec.Code != 401 {
		t.Fatalf("server keyless: %d", rec.Code)
	}
	var cfg map[string]any
	rec := serve(h, httptest.NewRequest("GET", "/api/auth/config", nil))
	if json.Unmarshal(rec.Body.Bytes(), &cfg); cfg["mode"] != "server" {
		t.Fatalf("auth config: %s", rec.Body.String())
	}
}
