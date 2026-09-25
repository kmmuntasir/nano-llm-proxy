package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Pin the exact wire-format contract for web_search's maxResults. LLM
// clients sometimes stringify scalar arguments ("10" instead of 10); the
// schema must keep advertising "integer" and reject those loudly (agents
// self-correct by omitting the param), while every legitimate numeric
// encoding — 10, 10.0 — reaches the handler. Regression guard for Pi's
// adapter report of a "must be integer" rejection: the offending call had
// sent a string.
func TestMCPWebSearchArgValidation(t *testing.T) {
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"results":[{"title":"t","url":"https://x.dev/1"}]}`))
	}))
	defer searx.Close()

	g, _, key := mcpTestGateway(t, searx.URL, "obscura", true)
	call := func(args string) string {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"web_search","arguments":` + args + `}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		g.clientOnly(g.handleMCP)(rec, req)
		return rec.Body.String()
	}

	for _, tc := range []struct {
		name, args string
		wantReject bool
	}{
		{"integer", `{"query":"q","maxResults":10}`, false},
		{"float with zero fraction", `{"query":"q","maxResults":10.0}`, false},
		{"omitted", `{"query":"q"}`, false},
		{"stringified", `{"query":"q","maxResults":"10"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := call(tc.args)
			if tc.wantReject {
				// the JSON-encoded body escapes the inner quotes
				if !strings.Contains(out, `want \"integer\"`) {
					t.Fatalf("stringified arg should be rejected, got: %.300s", out)
				}
				return
			}
			if strings.Contains(out, `want \"integer\"`) || !strings.Contains(out, "Web results") {
				t.Fatalf("numeric arg should reach the handler and search, got: %.300s", out)
			}
		})
	}

	// the advertised schema must keep saying integer
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleMCP)(rec, req)
	if !strings.Contains(rec.Body.String(), `"maxResults":{"type":"integer"`) {
		t.Fatalf("schema must advertise maxResults as integer, got: %.400s", rec.Body.String())
	}
}
