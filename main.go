// Command nano-llm-proxy is a tiny single-binary LLM gateway: it pools
// upstream providers behind one endpoint that speaks OpenAI chat, OpenAI
// Responses, and Anthropic Messages, with health-tracking key rotation and
// an embedded admin GUI.
//
// The runtime lives in internal/ — this file only wires bootstrap config,
// the store, and the gateway together, then serves.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"github.com/kmmuntasir/nano-llm-proxy/internal/gateway"
	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

func main() {
	keysPath := "keys.json"
	var sawKeys bool
	resetPassword := false
	for _, arg := range os.Args[1:] {
		switch {
		case arg == "-reset-admin-password":
			resetPassword = true
		case !sawKeys && !strings.HasPrefix(arg, "-"):
			keysPath, sawKeys = arg, true
		}
	}

	if err := config.LoadDotEnv(".env"); err != nil {
		log.Fatalf("env: %v", err)
	}
	cfg, err := config.LoadEnvConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	st, err := store.OpenStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

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

	// keys.json is only needed for first-boot seeding; later boots run fine
	// without it (the store holds the keys).
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

	g, err := gateway.NewGatewayFromStore(cfg, rs, st)
	if err != nil {
		log.Fatalf("gateway: %v", err)
	}
	log.Printf("nano-llm-proxy starting: providers=%d upstream_keys_healthy=%d ua=%s bind=%s:%d db=%s",
		g.ProviderCount(), g.HealthyUpstreamKeys(), rs.Zen.UserAgent, cfg.Bind, cfg.Port, cfg.DBPath)
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

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
