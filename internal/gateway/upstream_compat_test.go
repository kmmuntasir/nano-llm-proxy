package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// TestUpstreamFailureStatusNeverBadGateway: an upstream refusal of the
// request itself reaches the client as 400; every other upstream failure as
// 503 — never 502, whose body Cloudflare replaces with its own page.
func TestUpstreamFailureStatusNeverBadGateway(t *testing.T) {
	cases := map[int]int{
		0:   http.StatusServiceUnavailable, // no upstream answer / no trace
		400: http.StatusBadRequest,
		404: http.StatusBadRequest,
		422: http.StatusBadRequest,
		401: http.StatusServiceUnavailable,
		403: http.StatusServiceUnavailable, // e.g. zen's free-tier gate
		426: http.StatusServiceUnavailable,
		429: http.StatusServiceUnavailable,
		500: http.StatusServiceUnavailable,
		502: http.StatusServiceUnavailable,
	}
	for upstream, want := range cases {
		r := withTrace(httptest.NewRequest("POST", "/v1/chat/completions", nil))
		if upstream != 0 {
			traceFrom(r).attempt("k")
			traceFrom(r).upstream(upstream)
		}
		if got := upstreamFailureStatus(r); got != want {
			t.Errorf("upstream %d: client status %d, want %d", upstream, got, want)
		}
	}
	if got := upstreamFailureStatus(httptest.NewRequest("POST", "/", nil)); got != http.StatusServiceUnavailable {
		t.Errorf("no trace: %d, want 503", got)
	}
}

// presetUpstream is a mock OpenAI-compatible upstream that records the
// instruction role it received and answers 400 when told to.
type presetUpstream struct {
	mu    sync.Mutex
	roles []string
}

func (u *presetUpstream) handler(reject bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body) //nolint:errcheck
		u.mu.Lock()
		if len(body.Messages) > 0 {
			u.roles = append(u.roles, body.Messages[0].Role)
		}
		u.mu.Unlock()
		if reject {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"code":"1214","message":"Incorrect role information"}}`))
			return
		}
		sseOK(w)
	}
}

func (u *presetUpstream) lastRole() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.roles) == 0 {
		return ""
	}
	return u.roles[len(u.roles)-1]
}

// TestDeveloperRoleRenamedForStrictUpstreams: Pi sends role "developer" for
// reasoning models; a Z.ai-style upstream must receive "system", while an
// OpenAI upstream keeps "developer". An upstream 400 reaches the client as a
// readable 400, not a 502.
func TestDeveloperRoleRenamedForStrictUpstreams(t *testing.T) {
	zai, openai := &presetUpstream{}, &presetUpstream{}
	zaiSrv := httptest.NewServer(zai.handler(false))
	t.Cleanup(zaiSrv.Close)
	openaiSrv := httptest.NewServer(openai.handler(false))
	t.Cleanup(openaiSrv.Close)
	rejectSrv := httptest.NewServer((&presetUpstream{}).handler(true))
	t.Cleanup(rejectSrv.Close)

	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	for _, p := range []*store.Provider{
		{Name: "zai", Type: "openai", BaseURL: zaiSrv.URL, Preset: "zai", Enabled: true, SortOrder: 100},
		{Name: "oai", Type: "openai", BaseURL: openaiSrv.URL, Preset: "openai", Enabled: true, SortOrder: 101},
		{Name: "strict", Type: "openai", BaseURL: rejectSrv.URL, Enabled: true, SortOrder: 102},
	} {
		if err := st.CreateProvider(p, []store.ProviderKey{{Key: "sk-" + p.Name, Label: p.Name}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.rebuildPools(); err != nil {
		t.Fatal(err)
	}
	admin, _ := st.UserByEmail("admin@example.com")
	key := newClientKey(t, g, st, admin.ID, "pi")
	chat := func(model string) *httptest.ResponseRecorder {
		body := `{"model":"` + model + `","stream":true,"messages":[{"role":"developer","content":"be terse"},{"role":"user","content":"hi"}]}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		g.clientOnly(g.handleChat)(rec, req)
		return rec
	}

	if rec := chat("zai/glm-5.3-flash"); rec.Code != 200 || zai.lastRole() != "system" {
		t.Fatalf("zai: status %d, upstream role %q, want 200 + system", rec.Code, zai.lastRole())
	}
	if rec := chat("oai/gpt-6"); rec.Code != 200 || openai.lastRole() != "developer" {
		t.Fatalf("openai: status %d, upstream role %q, want 200 + developer", rec.Code, openai.lastRole())
	}
	rec := chat("strict/m")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Incorrect role information") {
		t.Fatalf("upstream 400 should reach the client as a readable 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestZenGateStubsCarryRealSchemas: zen's free-tier gate refuses stub tools
// with empty parameter schemas, so both stub shapes carry opencode's real
// shell/read/edit schemas — and every request gets its own copy.
func TestZenGateStubsCarryRealSchemas(t *testing.T) {
	want := map[string]string{"shell": "command", "read": "path", "edit": "oldString"}
	for name, prop := range want {
		for shape, params := range map[string]map[string]any{
			"chat": stubToolChat(name)["function"].(map[string]any)["parameters"].(map[string]any),
			"flat": stubToolFlat(name)["parameters"].(map[string]any),
		} {
			props, _ := params["properties"].(map[string]any)
			if _, ok := props[prop]; !ok || len(props) < 2 {
				t.Errorf("%s stub (%s) lacks the real schema: %v", name, shape, params)
			}
		}
	}
	a := zenStubParams("shell")
	a["properties"].(map[string]any)["command"] = "mutated"
	if b := zenStubParams("shell"); b["properties"].(map[string]any)["command"] == "mutated" {
		t.Fatal("stub schemas share state across requests")
	}
	// a client tool of the same name is kept (its schema is non-empty), the
	// missing gate tools are added
	body := map[string]any{"tools": []any{map[string]any{"type": "function", "function": map[string]any{
		"name": "read", "parameters": map[string]any{"type": "object", "properties": map[string]any{"file": map[string]any{"type": "string"}}},
	}}}}
	injectChatFingerprint(body, true)
	if names := toolNamesChat(body["tools"].([]any)); len(names) != 3 || names[0] != "read" {
		t.Fatalf("tools after injection = %v, want client read + shell + edit stubs", names)
	}
}
