// Command nano-llm-proxy is a tiny single-binary LLM gateway: it pools
// upstream providers behind one endpoint that speaks OpenAI chat, OpenAI
// Responses, and Anthropic Messages, with health-tracking key rotation and
// an embedded admin GUI.
//
// The runtime lives in internal/ — this file only wires bootstrap config,
// the store, and the gateway together, then serves. `nano-llm-proxy local`
// runs the same gateway in local mode (one person's machine, no login);
// its subcommands live in localcmd.go.
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"github.com/kmmuntasir/nano-llm-proxy/internal/gateway"
	"github.com/kmmuntasir/nano-llm-proxy/internal/localmode"
	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Build-stamped by the linker. Overridden in .goreleaser.yaml:
//
//	-ldflags "-X main.version=... -X main.commit=... -X main.date=..."
//
// A plain `go build` leaves these at the dev defaults below, which is why they
// are deliberately obvious rather than blank.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	args := os.Args[1:]
	mode, err := config.ParseMode(os.Getenv("NANO_MODE"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if len(args) > 0 && args[0] == "local" {
		mode, args = config.ModeLocal, args[1:]
		if localCommand(args) {
			return
		}
	}

	keysPath := "keys.json"
	var sawKeys bool
	resetPassword := false
	for _, arg := range args {
		switch {
		case arg == "-reset-admin-password":
			resetPassword = true
		case arg == "-version" || arg == "--version":
			fmt.Printf("nano-llm-proxy %s (commit %s, built %s)\n", version, commit, date)
			return
		case !sawKeys && !strings.HasPrefix(arg, "-"):
			keysPath, sawKeys = arg, true
		}
	}

	var cfg *config.Config
	if mode == config.ModeLocal {
		// local mode: everything lives in the per-user data dir
		dataDir, err := localmode.DataDir()
		if err != nil {
			log.Fatalf("data dir: %v", err)
		}
		if err := setupLocalLog(dataDir); err != nil {
			log.Fatalf("log: %v", err)
		}
		if cfg, err = loadLocalConfig(dataDir); err != nil {
			log.Fatalf("config: %v", err)
		}
	} else {
		if err := config.LoadDotEnv(".env"); err != nil {
			log.Fatalf("env: %v", err)
		}
		if cfg, err = config.LoadEnvConfig(); err != nil {
			log.Fatalf("config: %v", err)
		}
	}
	st, err := store.OpenStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	// local mode seeds its implicit owner, key and settings defaults before
	// the settings are read
	var localID store.LocalIdentity
	if cfg.IsLocal() {
		if localID, err = st.BootstrapLocal(cfg); err != nil {
			log.Fatalf("bootstrap: %v", err)
		}
	}

	// Runtime settings live in the DB; a corrupted document is fatal rather
	// than silently running on defaults. The UA floor stays a boot check even
	// though PUT /api/settings validates it — hand-edited databases exist.
	rs, err := st.LoadRuntimeSettings()
	if err != nil {
		log.Fatalf("settings: %v", err)
	}
	if !settings.UAVersionOK(rs.Zen.UserAgent) {
		log.Fatalf("zen.userAgent %q fails the 1.18.0 floor — the whole pool would 426; "+
			"reset to defaults with: sqlite3 %s \"DELETE FROM settings WHERE key='%s'\"",
			rs.Zen.UserAgent, cfg.DBPath, store.RuntimeSettingsKey)
	}

	if !cfg.IsLocal() {
		// keys.json is only needed for first-boot seeding; later boots run
		// fine without it (the store holds the keys).
		kf, kfErr := config.LoadKeys(keysPath)
		if kfErr != nil {
			kf = nil
		}
		if err := st.Bootstrap(cfg, kf, os.Getenv("ADMIN_EMAIL"), os.Getenv("ADMIN_PASSWORD")); err != nil {
			log.Fatalf("bootstrap: %v", err)
		}
		if resetPassword {
			gateway.ResetAdminPassword(st)
			return
		}
	}

	g, err := gateway.NewGatewayFromStore(cfg, rs, st)
	if err != nil {
		log.Fatalf("gateway: %v", err)
	}
	g.SetAbout(version, commit, date, changelogMD)
	if cfg.IsLocal() {
		exe, err := executable()
		if err != nil {
			log.Fatalf("executable: %v", err)
		}
		g.EnableLocal(localID, gatewayService(exe, cfg.DataDir))
	}
	log.Printf("nano-llm-proxy %s starting: mode=%s providers=%d upstream_keys_healthy=%d ua=%s bind=%s:%d db=%s",
		version, cfg.Mode, g.ProviderCount(), g.HealthyUpstreamKeys(), rs.Zen.UserAgent, cfg.Bind, cfg.Port, cfg.DBPath)
	go g.MaintenanceLoop()

	mux := http.NewServeMux()
	fsys, ok := webFS()
	g.RegisterRoutes(mux, fsys)
	if !ok {
		// untagged builds serve a stub instead of the GUI
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "nano-llm-proxy: admin GUI not built into this binary (build with -tags prod)")
		}))
	}

	addr := net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           g.Handler(mux), // local mode adds the Host guard
		ReadHeaderTimeout: 15 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
