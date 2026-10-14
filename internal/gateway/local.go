package gateway

import (
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"github.com/kmmuntasir/nano-llm-proxy/internal/localmode"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Local mode: one person's machine. There is no login — the GUI runs as the
// implicit owner and keyless /v1 traffic is billed to the implicit client key
// (store.BootstrapLocal). With no login, the browser itself is the attack
// surface, so local mode adds three guards server mode doesn't need:
//
//   - a Host check on every route (DNS rebinding: a hostile site resolving
//     its own name to 127.0.0.1 would otherwise drive the API);
//   - the usual Origin check on GUI mutations;
//   - on /v1 and /mcp, no foreign Origin and a JSON Content-Type on POSTs,
//     which forces a CORS preflight the gateway never approves — a web page
//     can't spend the owner's credits with a "simple" request.
//
// Server mode never reaches any of this: every entry point checks isLocal.

// LocalServiceEnv is set in the environment of the background service the
// gateway registers for itself, so it knows a restart will bring it back.
const LocalServiceEnv = "NANO_LOCAL_SERVICE"

// serviceManager is the per-user background-service mechanism (localmode);
// an interface only so tests can run without systemd/launchd.
type serviceManager interface {
	Supported() bool
	Supervised() bool
	Register(s localmode.Service) error
	Unregister(name string) error
	Status(name string) (installed, running bool, err error)
	Restart(name string) error
}

type osServices struct{}

func (osServices) Supervised() bool                       { return localmode.Supervised() }
func (osServices) Supported() bool                        { return localmode.Supported() }
func (osServices) Register(s localmode.Service) error     { return localmode.Register(s) }
func (osServices) Unregister(name string) error           { return localmode.Unregister(name) }
func (osServices) Status(name string) (bool, bool, error) { return localmode.Status(name) }
func (osServices) Restart(name string) error              { return localmode.Restart(name) }

// localState is the local-mode wiring; nil in server mode.
type localState struct {
	id         store.LocalIdentity
	gatewaySvc localmode.Service // what "start at login" registers
	svc        serviceManager
	exit       func(code int) // os.Exit; swapped in tests
}

// EnableLocal switches the gateway into local mode: the GUI runs as the
// owner, keyless requests use the implicit key, and gatewaySvc is what the
// Settings "start at login" switch registers. Called once by main before
// serving, only when the config is in local mode.
func (g *gateway) EnableLocal(id store.LocalIdentity, gatewaySvc localmode.Service) {
	g.local = &localState{id: id, gatewaySvc: gatewaySvc, svc: osServices{}, exit: os.Exit}
}

func (g *gateway) isLocal() bool { return g.local != nil }

// Handler wraps the mux with the local-mode Host guard; in server mode it
// returns the mux unchanged.
func (g *gateway) Handler(mux http.Handler) http.Handler {
	if !g.isLocal() {
		return mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHostAllowed(r.Host, g.conf().Port) {
			http.Error(w, "nano-llm-proxy: this address is not allowed — open http://localhost:"+
				strconv.Itoa(g.conf().Port), http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// localHostAllowed: the Host header must name this computer on our port.
func localHostAllowed(host string, port int) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil || p != strconv.Itoa(port) {
		return false
	}
	return isLoopbackName(h)
}

func isLoopbackName(h string) bool {
	switch strings.ToLower(h) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// localSession is requireSession for local mode: no cookie, the owner is
// read fresh each request; the Origin check on mutations stays.
func (g *gateway) localSession(next http.HandlerFunc, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !g.originAllowed(r) {
		apiErr(w, http.StatusForbidden, "origin not allowed")
		return
	}
	user, err := g.store.User(g.local.id.OwnerID)
	if err != nil || user == nil {
		apiErr(w, http.StatusInternalServerError, "local owner lookup failed")
		return
	}
	next(w, r.WithContext(withUser(r.Context(), user)))
}

// localClient is clientOnly for local mode. It returns false when it has
// already answered the request.
func (g *gateway) localClient(w http.ResponseWriter, r *http.Request, key string) (clientKeyEntry, bool) {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !isLoopbackName(u.Hostname()) {
			writeErr(w, http.StatusForbidden, "Requests from web pages are not allowed")
			return clientKeyEntry{}, false
		}
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); r.Method == http.MethodPost && mt != "application/json" {
		writeErr(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return clientKeyEntry{}, false
	}
	if key != "" {
		if ck, ok := g.ck.lookup(key); ok {
			return ck, true
		}
	}
	if g.rs().Local.RequireAPIKey {
		g.rejectClient(w)
		return clientKeyEntry{}, false
	}
	// any key or none: the implicit key (a placeholder like "local" is what
	// most agents insist on sending)
	if ck, ok := g.ck.byID(g.local.id.KeyID); ok {
		return ck, true
	}
	writeErr(w, http.StatusUnauthorized, "The built-in local key is disabled — enable \""+store.LocalKeyAlias+
		"\" in My API Keys or use one of your keys")
	return clientKeyEntry{}, false
}

// --- /api/local ---

type localAutostartView struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	Error     string `json:"error"`
}

type localSearXNGView struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	URL       string `json:"url"`
}

type localInfoView struct {
	Platform  string             `json:"platform"` // runtime.GOOS: the GUI words OS-specific hints
	Port      int                `json:"port"`
	DataDir   string             `json:"dataDir"`
	LogFile   string             `json:"logFile"`
	Binary    string             `json:"binary"`
	Autostart localAutostartView `json:"autostart"`
	SearXNG   localSearXNGView   `json:"searxng"`
}

// LocalLogFile is where local mode writes its log.
func LocalLogFile(dataDir string) string { return filepath.Join(dataDir, "nano-llm-proxy.log") }

func (g *gateway) localInfo() localInfoView {
	cfg, ls := g.conf(), g.local
	out := localInfoView{
		Platform: runtime.GOOS, Port: cfg.Port, DataDir: cfg.DataDir, LogFile: LocalLogFile(cfg.DataDir),
		Binary:    ls.gatewaySvc.Exec,
		Autostart: localAutostartView{Supported: ls.svc.Supported()},
		SearXNG:   localSearXNGView{URL: g.rs().WebTools.SearXNGURL},
	}
	if out.Autostart.Supported {
		installed, running, err := ls.svc.Status("gateway")
		out.Autostart.Enabled, out.Autostart.Running = installed, running
		if err != nil {
			out.Autostart.Error = err.Error()
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "searxng", "venv")); err == nil {
		out.SearXNG.Installed = true
		if _, running, err := ls.svc.Status("searxng"); err == nil {
			out.SearXNG.Running = running
		}
	}
	return out
}

// localOnly 404s the /api/local routes in server mode.
func (g *gateway) localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.isLocal() {
			apiErr(w, http.StatusNotFound, "not found")
			return
		}
		next(w, r)
	}
}

// handleLocalInfo serves GET /api/local.
func (g *gateway) handleLocalInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.localInfo())
}

// handleLocalAutostart serves PUT /api/local/autostart: add or remove the
// start-at-sign-in entry. Only the next sign-in changes — the instance
// answering this request (often that very background service) keeps
// running, which is why this registers rather than enables.
func (g *gateway) handleLocalAutostart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		apiErr(w, http.StatusBadRequest, "enabled is required")
		return
	}
	svc := g.local.svc
	if !svc.Supported() {
		apiErr(w, http.StatusBadRequest, "Starting at sign-in isn't supported on this system")
		return
	}
	var err error
	if *req.Enabled {
		err = svc.Register(g.local.gatewaySvc)
	} else {
		err = svc.Unregister("gateway")
	}
	if err != nil {
		log.Printf("local autostart enabled=%v: %v", *req.Enabled, err)
		apiErr(w, http.StatusInternalServerError, "Couldn't change start at sign-in: "+err.Error())
		return
	}
	log.Printf("local autostart enabled=%v", *req.Enabled)
	writeJSON(w, http.StatusOK, g.localInfo())
}

// handleLocalPort serves PUT /api/local/port: the port is a bootstrap value,
// so it goes to <datadir>/.env and takes effect on the next start. A gateway
// running as the background service restarts itself; otherwise the owner is
// told to restart it.
func (g *gateway) handleLocalPort(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Port int `json:"port"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Port < 1024 || req.Port > 65535 {
		apiErr(w, http.StatusBadRequest, "Port must be between 1024 and 65535")
		return
	}
	if req.Port != g.conf().Port && !portFree(g.conf().Bind, req.Port) {
		apiErr(w, http.StatusConflict, fmt.Sprintf("Port %d is already in use", req.Port))
		return
	}
	if err := config.SetDotEnv(filepath.Join(g.conf().DataDir, ".env"), "NANO_PORT", strconv.Itoa(req.Port)); err != nil {
		apiErr(w, http.StatusInternalServerError, "Couldn't save the port: "+err.Error())
		return
	}
	newURL := "http://localhost:" + strconv.Itoa(req.Port)
	log.Printf("local port set to %d", req.Port)
	managed := false
	if os.Getenv(LocalServiceEnv) == "1" && g.local.svc.Supervised() {
		// started by a service manager that restarts what exits, and still
		// registered with it — so exiting brings us back on the new port
		managed, _, _ = g.local.svc.Status("gateway")
	}
	if !managed {
		writeJSON(w, http.StatusOK, map[string]any{"restarting": false, "url": newURL,
			"message": "Saved — restart nano-llm-proxy to use the new port"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restarting": true, "url": newURL})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	// exiting lets the service manager (Restart=always / KeepAlive) start a
	// fresh process that reads the new port; a moment's delay lets the
	// response reach the browser first
	go func() {
		time.Sleep(500 * time.Millisecond)
		log.Printf("restarting for the new port")
		g.local.exit(0)
	}()
}

func portFree(bind string, port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close() //nolint:errcheck
	return true
}
