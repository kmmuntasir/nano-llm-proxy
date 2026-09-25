package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Raw JSON-RPC over the mounted /mcp route: initialize → tools/list →
// tools/call with fake backends, plus auth/metering/disabled checks. No SDK
// client on purpose — this is exactly what Claude Code / opencode put on the
// wire.

// mcpRPC POSTs one JSON-RPC message and decodes the response envelope.
func mcpRPC(t *testing.T, g *gateway, key string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleMCP)(rec, req)

	ct := rec.Header().Get("Content-Type")
	payload := rec.Body.String()
	if strings.Contains(ct, "text/event-stream") {
		// SDK may answer SSE-framed even for JSONResponse requests on some
		// paths; unwrap the single data: line.
		for _, line := range strings.Split(payload, "\n") {
			if strings.HasPrefix(line, "data:") {
				payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				break
			}
		}
	}
	var out map[string]any
	if strings.TrimSpace(payload) == "" {
		return rec.Code, nil // notifications answer with an empty body
	}
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		t.Fatalf("response not JSON (code=%d ct=%s): %q", rec.Code, ct, payload)
	}
	return rec.Code, out
}

// fakeObscura writes a shell script that impersonates `obscura fetch`.
func fakeObscura(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "obscura")
	script := "#!/bin/sh\necho '# Rendered page'\necho 'body text from obscura'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func mcpTestGateway(t *testing.T, searxURL, obscuraPath string, enabled bool) (*gateway, *store.Store, string) {
	t.Helper()
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	rs := g.rs()
	rs.WebTools = settings.WebToolsSettings{
		Enabled:               enabled,
		SearXNGURL:            searxURL,
		ReaderMode:            settings.ReaderFast,
		ObscuraPath:           obscuraPath,
		ObscuraConcurrency:    2,
		FetchTimeoutSeconds:   5,
		ObscuraTimeoutSeconds: 10,
		MaxChars:              20000,
	}
	g.rsPtr.Store(rs)

	admin, err := st.UserByEmail("admin@example.com")
	if err != nil {
		t.Fatalf("UserByEmail: %v", err)
	}
	key := newClientKey(t, g, st, admin.ID, "mcp-test")
	return g, st, key
}

func TestMCPHandshakeAndTools(t *testing.T) {
	g, _, key := mcpTestGateway(t, "http://127.0.0.1:1", "obscura", true)

	// initialize
	code, res := mcpRPC(t, g, key, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "0"},
		},
	})
	if code != 200 {
		t.Fatalf("initialize: %d: %v", code, res)
	}
	if res["result"].(map[string]any)["serverInfo"] == nil {
		t.Fatalf("initialize missing serverInfo: %v", res)
	}

	// notifications/initialized (no id, no response body expected)
	code, _ = mcpRPC(t, g, key, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	})
	if code != http.StatusOK && code != http.StatusAccepted {
		t.Fatalf("notifications/initialized: %d", code)
	}

	// tools/list
	code, res = mcpRPC(t, g, key, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	if code != 200 {
		t.Fatalf("tools/list: %d: %v", code, res)
	}
	toolsJSON, _ := json.Marshal(res["result"])
	if !strings.Contains(string(toolsJSON), "web_search") || !strings.Contains(string(toolsJSON), "web_read") {
		t.Fatalf("tools/list missing tools: %s", toolsJSON)
	}
}

func TestMCPSearchCallMetersUsage(t *testing.T) {
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"results":[{"title":"Result One","url":"https://go.dev/1","content":"first snippet","engines":["ddg"]}]}`))
	}))
	defer searx.Close()

	g, st, key := mcpTestGateway(t, searx.URL, "obscura", true)

	code, res := mcpRPC(t, g, key, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name":      "web_search",
			"arguments": map[string]any{"query": "golang", "maxResults": 5},
		},
	})
	if code != 200 {
		t.Fatalf("tools/call web_search: %d: %v", code, res)
	}
	enc, _ := json.Marshal(res)
	if !strings.Contains(string(enc), "[Result One](https://go.dev/1)") {
		t.Fatalf("search result missing from response: %s", enc)
	}
	if isError, _ := res["result"].(map[string]any)["isError"].(bool); isError {
		t.Fatalf("unexpected isError: %s", enc)
	}

	// The usage event lands after the maintenance drain.
	events := g.usageBuf.drain()
	var found *store.UsageEvent
	for i := range events {
		if events[i].Provider == "web-tools" && events[i].Model == "web_search" {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("no web-tools usage event recorded; drained %d", len(events))
	}
	if found.Status != 200 || found.OutputTokens <= 0 || found.UserID == 0 {
		t.Fatalf("bad usage event: %+v", found)
	}
	_ = st
}

func TestMCPReadBlockedURLIsToolError(t *testing.T) {
	g, _, key := mcpTestGateway(t, "http://127.0.0.1:1", "obscura", true)

	code, res := mcpRPC(t, g, key, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{
			"name":      "web_read",
			"arguments": map[string]any{"url": "http://127.0.0.1:6379/"},
		},
	})
	if code != 200 {
		t.Fatalf("tools/call should be protocol-200: %d", code)
	}
	rmap, _ := res["result"].(map[string]any)
	if isError, _ := rmap["isError"].(bool); !isError {
		t.Fatalf("SSRF block must be a tool error: %v", res)
	}
	for _, e := range eventsOf(g) {
		if e.Model == "web_read" && e.Status != 400 {
			t.Fatalf("web_read SSRF block metered as %d, want 400", e.Status)
		}
	}
}

func TestMCPReadViaObscuraFallback(t *testing.T) {
	// A loopback URL can never reach the obscura leg (both SSRF guards
	// refuse private addresses), so exercise escalation with a TEST-NET
	// public URL: native dial fails fast, the fake obscura "renders" it.
	g, _, key := mcpTestGateway(t, "http://127.0.0.1:1", fakeObscura(t), true)
	rs := g.rs()
	rs.WebTools.FetchTimeoutSeconds = 1 // fail the native leg quickly
	g.rsPtr.Store(rs)

	code, res := mcpRPC(t, g, key, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{
			"name":      "web_read",
			"arguments": map[string]any{"url": "http://203.0.113.7/js-page"},
		},
	})
	if code != 200 {
		t.Fatalf("tools/call web_read: %d: %v", code, res)
	}
	enc, _ := json.Marshal(res)
	if !strings.Contains(string(enc), "Rendered page") {
		t.Fatalf("obscura output missing: %s", enc)
	}
}

func TestMCPDisabledAndUnauthorized(t *testing.T) {
	g, _, key := mcpTestGateway(t, "http://127.0.0.1:1", "obscura", false)

	// Disabled: 404 through the same clientOnly wrapper.
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleMCP)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled /mcp = %d, want 404", rec.Code)
	}

	// Enabled but bad key: 401 from clientOnly.
	enabled := settings.DefaultRuntimeSettings()
	enabled.WebTools.Enabled = true
	g.rsPtr.Store(enabled)
	req = httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer fg-not-a-real-key")
	rec = httptest.NewRecorder()
	g.clientOnly(g.handleMCP)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad key = %d, want 401", rec.Code)
	}
}

// eventsOf drains and returns the pending usage buffer (test-only).
func eventsOf(g *gateway) []store.UsageEvent {
	return g.usageBuf.drain()
}
