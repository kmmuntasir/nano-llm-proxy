package gateway

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/webtools"
)

// Diagnostics for the web-tools backends, driven by the "Test" buttons in
// Settings → Web tools and by scripts/install-web-tools.sh --check.

type webToolsTestRequest struct {
	Target string `json:"target"` // "searxng" | "obscura"
	Deep   bool   `json:"deep"`   // obscura: also fetch example.com, not just --version
}

type webToolsTestResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
	Version   string `json:"version,omitempty"`
	Detail    string `json:"detail,omitempty"`
	NextStep  string `json:"nextStep,omitempty"`
}

func (g *gateway) handleWebToolsTest(w http.ResponseWriter, r *http.Request) {
	var req webToolsTestRequest
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()

	switch req.Target {
	case "searxng":
		start := time.Now()
		out, err := g.webtools.TestSearxng(ctx)
		res := webToolsTestResult{LatencyMs: time.Since(start).Milliseconds()}
		if err != nil {
			res.OK = false
			res.Detail = err.Error()
			res.NextStep = g.searxngNextStep(err)
		} else {
			res.OK = true
			res.Detail = out
		}
		writeJSON(w, http.StatusOK, res)

	case "obscura":
		start := time.Now()
		version, detail, err := g.webtools.TestObscura(ctx, req.Deep)
		res := webToolsTestResult{
			Version:   version,
			Detail:    detail,
			LatencyMs: time.Since(start).Milliseconds(),
		}
		if err != nil {
			res.OK = false
			res.Detail = firstLineOf(err.Error())
			res.NextStep = g.obscuraNextStep()
		} else {
			res.OK = true
		}
		writeJSON(w, http.StatusOK, res)

	default:
		apiErr(w, http.StatusBadRequest, `target must be "searxng" or "obscura"`)
	}
}

// searxngNextStep says what to do about a failed SearXNG test. Server mode
// points at the root installer; local mode at what the one-line installer
// set up (or didn't, when Python/git were missing or --no-search was used).
func (g *gateway) searxngNextStep(err error) string {
	noResults := errors.Is(err, webtools.ErrSearxngNoResults)
	if !g.isLocal() {
		if noResults {
			return "SearXNG is running but its engines are blocked or rate-limited from this server's IP — retry in a few minutes, or trim the engines in /etc/searxng/settings.yml (docs/deployment.md)"
		}
		return "install/start SearXNG: sudo ./scripts/install-web-tools.sh, then check 'sudo ./scripts/install-web-tools.sh --check'"
	}
	home := filepath.Join(g.conf().DataDir, "searxng")
	if noResults {
		return "SearXNG is running, but search engines are blocking or rate-limiting your IP (common for a few minutes after many searches) — retry later, or change the engine list in " +
			filepath.Join(home, "settings.yml") + " and restart nano-llm-proxy's SearXNG"
	}
	if _, statErr := os.Stat(filepath.Join(home, "venv")); statErr != nil {
		if runtime.GOOS == "windows" {
			return "there's no SearXNG on this computer — set the URL of a SearXNG you can reach (with its JSON API on) above"
		}
		return "SearXNG isn't set up on this computer — re-run the installer without --no-search (it needs Python 3.10+ and git), or set the URL of another SearXNG above"
	}
	return "SearXNG is installed but not answering — check its status under This computer and its log at " +
		filepath.Join(home, "searxng.log") + "; `nano-llm-proxy local status` shows the service, re-running the installer restarts it"
}

// obscuraNextStep says where obscura comes from in each mode. It is
// optional: web_read falls back to a plain fetch without it.
func (g *gateway) obscuraNextStep() string {
	if !g.isLocal() {
		return "install obscura: sudo ./scripts/install-web-tools.sh --skip-searxng"
	}
	return "obscura is optional (web_read uses it only for JavaScript-heavy pages) — re-run the installer to set it up, or install it from https://github.com/h4ckf0r0day/obscura/releases and set its absolute path above"
}

func firstLineOf(s string) string {
	for i, r := range s {
		if r == '\n' || r == '\r' {
			return s[:i]
		}
	}
	return s
}
