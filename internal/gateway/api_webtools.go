package gateway

import (
	"context"
	"net/http"
	"time"
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
			res.NextStep = "install/start SearXNG: sudo ./scripts/install-web-tools.sh, then check 'sudo ./scripts/install-web-tools.sh --check'"
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
			NextStep:  "install obscura: sudo ./scripts/install-web-tools.sh --skip-searxng",
		}
		if err != nil {
			res.OK = false
			res.Detail = firstLineOf(err.Error())
		} else {
			res.OK = true
		}
		writeJSON(w, http.StatusOK, res)

	default:
		apiErr(w, http.StatusBadRequest, `target must be "searxng" or "obscura"`)
	}
}

func firstLineOf(s string) string {
	for i, r := range s {
		if r == '\n' || r == '\r' {
			return s[:i]
		}
	}
	return s
}
