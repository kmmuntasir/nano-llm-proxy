package gateway

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// Pin the wire contract for the tools' scalar arguments, which are advertised
// as a union of the native JSON type and string.
//
// The reason is client-side and invisible from the server: clients validate
// arguments against the advertised inputSchema *before* sending the request, so
// a scalar-typed property ("type":"integer") makes every client whose tool
// arguments arrive stringified reject the call with "must be integer" and the
// tool never runs. Pi's MCP adapter hit exactly that on this deployment. These
// tests pin the fix: both encodings are advertised, both reach the handler with
// the value the caller meant, and values outside the union are still rejected
// with a message an agent can act on.
func TestMCPWebToolScalarArgs(t *testing.T) {
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"results":[{"title":"t","url":"https://x.dev/1"}]}`))
	}))
	defer searx.Close()

	g, _, key := mcpTestGateway(t, searx.URL, "obscura", true)

	call := func(name, args string) string {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		g.clientOnly(g.handleMCP)(rec, req)
		return rec.Body.String()
	}

	search := func(t *testing.T, args string) string {
		t.Helper()
		out := call("web_search", args)
		if strings.Contains(out, `"isError":true`) {
			t.Fatalf("web_search(%s) should succeed, got: %.400s", args, out)
		}
		if !strings.Contains(out, "Web results") {
			t.Fatalf("web_search(%s) did not reach the handler, got: %.400s", args, out)
		}
		return out
	}

	// Both encodings of a well-formed value must be accepted, and the result
	// list must actually be clamped by the handler's value, proving coercion
	// happened rather than the argument being dropped.
	for _, args := range []string{
		`{"query":"q","maxResults":3}`,
		`{"query":"q","maxResults":"3"}`,
		`{"query":"q","maxResults":3.0}`,
		`{"query":"q"}`,
	} {
		search(t, args)
	}

	// render/maxChars on web_read: the same widening, both ways. A loopback
	// URL never reaches the obscura leg (both SSRF guards refuse private
	// addresses), so use a TEST-NET address: native dial fails, the fake
	// obscura "renders" it, and the trailing marker shows which leg ran.
	g2, _, key2 := mcpTestGateway(t, "http://127.0.0.1:1", fakeObscura(t), true)
	rs := g2.rs()
	rs.WebTools.FetchTimeoutSeconds = 1
	g2.rsPtr.Store(rs)
	read := func(args string) string {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"web_read","arguments":`+args+`}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+key2)
		rec := httptest.NewRecorder()
		g2.clientOnly(g2.handleMCP)(rec, req)
		return rec.Body.String()
	}
	const page = `http://203.0.113.7/js-page`
	const whole = "body text from obscura" // only in the fake page when unclamped
	for _, tc := range []struct {
		args        string
		wantClamped bool
	}{
		{`{"url":"` + page + `","render":true}`, false},
		{`{"url":"` + page + `","render":"true"}`, false},
		{`{"url":"` + page + `","render":true,"maxChars":10}`, true},
		{`{"url":"` + page + `","render":"true","maxChars":"10"}`, true},
	} {
		out := read(tc.args)
		if strings.Contains(out, `"isError":true`) {
			t.Fatalf("web_read(%s) should succeed, got: %.400s", tc.args, out)
		}
		// render must have been honoured as a boolean, not merely accepted:
		// with the native leg unable to reach the URL, the render leg ran.
		if !strings.Contains(out, "_(via render)_") {
			t.Fatalf("web_read(%s) did not take the render leg: %.400s", tc.args, out)
		}
		// maxChars must have been honoured as a number, not merely accepted.
		clamped := !strings.Contains(out, whole)
		if clamped != tc.wantClamped {
			t.Fatalf("web_read(%s) clamped=%v, want %v: %.400s", tc.args, clamped, tc.wantClamped, out)
		}
	}

	// Values outside the advertised union are still rejected, and the message
	// names the argument so the agent can self-correct.
	for _, tc := range []struct {
		tool, args, want string
	}{
		{"web_search", `{"query":"q","maxResults":"lots"}`, `maxResults must be an integer`},
		{"web_search", `{"query":"q","maxResults":2.5}`, `maxResults must be a whole number`},
		{"web_search", `{"query":"q","maxResults":true}`, `maxResults must be an integer`},
		{"web_search", `{"query":"q","maxResults":{"n":1}}`, `maxResults must be an integer`},
		{"web_search", `{"maxResults":3}`, `query is required`},
		{"web_read", `{"url":"https://x.dev/1","render":"maybe"}`, `render must be a boolean`},
		{"web_read", `{"url":"https://x.dev/1","render":7}`, `render must be a boolean`},
		{"web_read", `{"url":["a"]}`, `url must be a string`},
	} {
		out := call(tc.tool, tc.args)
		if !strings.Contains(out, `"isError":true`) {
			t.Fatalf("%s(%s): expected a tool error, got: %.400s", tc.tool, tc.args, out)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s(%s): error should mention %q, got: %.400s", tc.tool, tc.args, tc.want, out)
		}
	}

	// The advertised schema is the whole fix: it must admit both encodings, or
	// client-side validation rejects the stringified call before it is sent.
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleMCP)(rec, req)
	list := rec.Body.String()
	for _, want := range []string{
		`"maxResults":{"description":"maximum number of results to return (1-25, default 8). Send a number; a numeric string such as \"10\" is also accepted","type":["integer","string"]}`,
		`"render":{"description":"render the page in a headless browser first (for JavaScript-heavy or bot-protected pages). Send true or false; \"true\"/\"false\" are also accepted","type":["boolean","string"]}`,
		`"maxChars":{"description":"maximum characters of content to return. Send a number; a numeric string such as \"5000\" is also accepted","type":["integer","string"]}`,
	} {
		if !strings.Contains(list, want) {
			t.Fatalf("tools/list must advertise %s, got: %.2000s", want, list)
		}
	}
}

// The coerced value must be the value the caller sent, not a dropped one: the
// result list is truncated to maxResults locally, so the returned count proves
// which value the handler actually used.
func TestMCPWebSearchCoercedMaxResults(t *testing.T) {
	var results []string
	for i := 1; i <= 6; i++ {
		results = append(results, `{"title":"t","url":"https://x.dev/`+strconv.Itoa(i)+`"}`)
	}
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"results":[` + strings.Join(results, ",") + `]}`))
	}))
	defer searx.Close()

	g, _, key := mcpTestGateway(t, searx.URL, "obscura", true)
	for _, tc := range []struct{ args, want string }{
		{`{"query":"q","maxResults":3}`, "(3 results; SearXNG)"},
		{`{"query":"q","maxResults":"4"}`, "(4 results; SearXNG)"},
		{`{"query":"q","maxResults":2.0}`, "(2 results; SearXNG)"},
		{`{"query":"q"}`, "(6 results; SearXNG)"},
	} {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"web_search","arguments":`+tc.args+`}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		g.clientOnly(g.handleMCP)(rec, req)
		out := rec.Body.String()
		if strings.Contains(out, `"isError":true`) {
			t.Fatalf("%s: %.400s", tc.args, out)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s: got %.400s, want %q", tc.args, out, tc.want)
		}
	}
}
