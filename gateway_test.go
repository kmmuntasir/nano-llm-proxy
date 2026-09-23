package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testRuntime returns the runtime settings the gateway boots with when the
// database holds no document; individual tests tweak fields on it.
func testRuntime() *RuntimeSettings { return DefaultRuntimeSettings() }

// testGateway wires a gateway at a mock upstream for one provider.
func testGateway(t *testing.T, handler http.Handler) *gateway {
	t.Helper()
	up := httptest.NewServer(handler)
	t.Cleanup(up.Close)
	cfg := &Config{
		Port: 0, Bind: "127.0.0.1",
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	rs.Zen.ResponsesModels = []string{"muse-test-free"}
	kf := &keyFile{
		Zen:  []keyFileEntry{{"a", "sk-zen-a"}, {"b", "sk-zen-b"}, {"c", "sk-zen-c"}},
		Kilo: []keyFileEntry{{"a", "sk-kilo-a"}, {"b", "sk-kilo-b"}},
	}
	return newGateway(cfg, rs, kf)
}

func chatReq(provider, model string) *http.Request {
	body := fmt.Sprintf(`{"model":"%s/%s","messages":[{"role":"user","content":"hi"}],"stream":true}`,
		provider, model)
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func sseResponse(chunks ...string) http.Response {
	return http.Response{StatusCode: 200}
}

// --- pool rotation ---

func TestRotation429FallsBackToNextKey(t *testing.T) {
	var mu = make(chan struct{}, 1)
	seen := map[string]int{}
	var g *gateway
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		seen[key]++
		if key == "Bearer sk-zen-a" {
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"message":"limited"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	_ = mu
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Zen: []keyFileEntry{
		{"a", "sk-zen-a"}, {"b", "sk-zen-b"},
	}}
	g = newGateway(cfg, rs, kf)

	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free"))
	if rec.Code != 200 {
		t.Fatalf("expected 200 after rotation, got %d: %s", rec.Code, rec.Body.String())
	}
	if seen["Bearer sk-zen-a"] != 1 {
		t.Fatalf("key a should have been tried once, got %d", seen["Bearer sk-zen-a"])
	}
	if seen["Bearer sk-zen-b"] != 1 {
		t.Fatalf("key b should have been tried once, got %d", seen["Bearer sk-zen-b"])
	}
}

func TestAuthErrorDisablesKey(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"type":"AuthError","message":"Invalid API key."}}`))
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Kilo: []keyFileEntry{{"a", "sk-a"}, {"b", "sk-b"}}}
	g := newGateway(cfg, rs, kf)
	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("kilo", "some-model"))
	// both keys get disabled -> no healthy keys -> 502
	if rec.Code != 502 {
		t.Fatalf("expected 502 when all keys auth-fail, got %d", rec.Code)
	}
	snap := g.kilo.snapshot()
	for _, k := range snap {
		if k.Status != statusDisabled {
			t.Fatalf("key %s should be disabled, is %s", k.Label, k.Status)
		}
	}
}

func TestFreeTierErrorFailsFastWithoutBurningPool(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(403)
		w.Write([]byte(`{"type":"error","error":{"type":"FreeTierError","message":"nope"}}`))
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}, {"b", "sk-b"}, {"c", "sk-c"}}}
	g := newGateway(cfg, rs, kf)
	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free"))
	if rec.Code != 502 {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "client-shape") {
		t.Fatalf("expected client-shape hint in body, got: %s", rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("FreeTierError must fail fast (1 call), got %d calls", calls)
	}
}

func TestZenFingerprintInjection(t *testing.T) {
	var gotSession, gotUA string
	var gotBody map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession = r.Header.Get("x-opencode-session")
		gotUA = r.Header.Get("User-Agent")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)
	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free"))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	if gotUA != "opencode/1.18.32" {
		t.Fatalf("UA not set: %q", gotUA)
	}
	if !validZenSession(gotSession) {
		t.Fatalf("session id malformed: %q", gotSession)
	}
	bodyStream, _ := gotBody["stream"].(bool)
	if !bodyStream {
		t.Fatalf("stream must be forced true upstream")
	}
	tools, _ := gotBody["tools"].([]any)
	if len(tools) < 3 {
		t.Fatalf("expected >=3 injected tools, got %d", len(tools))
	}
}

func validZenSession(s string) bool {
	if len(s) != 30 || !strings.HasPrefix(s, "ses_") {
		return false
	}
	for i, c := range s[4:] {
		hexPart := i < 12
		switch {
		case hexPart && !strings.ContainsRune("0123456789abcdef", c):
			return false
		case !hexPart && !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", c):
			return false
		}
	}
	return true
}

func TestSurfaceFlipOn503(t *testing.T) {
	surface := "chat"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" && surface == "chat" {
			// first chat attempt fails with the signature 503
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"type":"server_error","message":"Upstream request failed: Endpoint is unavailable."}}`))
			surface = "responses"
			return
		}
		if r.URL.Path == "/responses" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
			w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"))
			return
		}
		w.WriteHeader(404)
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)

	// model NOT in responsesModels — must still flip and succeed
	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "muse-test-free"))
	if rec.Code != 200 {
		t.Fatalf("expected 200 after surface flip, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("expected translated text, got: %s", rec.Body.String())
	}
	if got := g.surfaceFor("muse-test-free"); got != "responses" {
		t.Fatalf("surface should be learned as responses, got %s", got)
	}
}

func TestResponsesTranslationNonStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["input"]; !ok {
			t.Errorf("responses body must carry input, got %v", body)
		}
		if _, ok := body["messages"]; ok {
			t.Errorf("responses body must not carry messages")
		}
		if body["max_output_tokens"] == nil {
			t.Errorf("max_tokens must map to max_output_tokens")
		}
		// flat tools?
		tools, _ := body["tools"].([]any)
		if len(tools) > 0 {
			tm := tools[0].(map[string]any)
			if _, ok := tm["function"]; ok {
				t.Errorf("responses tools must be flat")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"abc\"}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"def\"}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":7}}}\n\n"))
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	rs.Zen.ResponsesModels = []string{"muse-test-free"}
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)

	// client asks non-stream; upstream forced to stream; response aggregated
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"zen/muse-test-free","messages":[{"role":"user","content":"hi"}],"max_tokens":50,"stream":false}`))
	rec := httptest.NewRecorder()
	g.handleChat(rec, r)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if out["object"] != "chat.completion" {
		t.Fatalf("expected chat.completion, got %v", out["object"])
	}
	choices := out["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "abcdef" {
		t.Fatalf("expected aggregated content abcdef, got %v", msg["content"])
	}
	usage := out["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(5) || usage["completion_tokens"] != float64(7) {
		t.Fatalf("usage not mapped: %v", usage)
	}
}

func TestResponsesEndpointAggregation(t *testing.T) {
	var gotUA, gotSession string
	var gotBody map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotSession = r.Header.Get("x-opencode-session")
		json.NewDecoder(r.Body).Decode(&gotBody)
		if r.URL.Path != "/responses" {
			t.Errorf("expected /responses path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_x\",\"status\":\"in_progress\"}}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"he\"}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"llo\"}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_x\",\"status\":\"completed\",\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\n"))
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	rs.Zen.ResponsesModels = []string{"muse-test-free"}
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)

	r := httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"zen/muse-test-free","input":"hi","stream":false,"max_output_tokens":50}`))
	rec := httptest.NewRecorder()
	g.handleResponses(rec, r)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if out["object"] != "response" || out["status"] != "completed" {
		t.Fatalf("unexpected shape: %v", out)
	}
	output := out["output"].([]any)
	msg := output[0].(map[string]any)
	content := msg["content"].([]any)
	text := content[0].(map[string]any)["text"]
	if text != "hello" {
		t.Fatalf("expected aggregated text hello, got %v", text)
	}
	usage := out["usage"].(map[string]any)
	if usage["input_tokens"] != float64(4) || usage["output_tokens"] != float64(2) {
		t.Fatalf("usage not mapped: %v", usage)
	}
	// fingerprint checks
	if gotUA != "opencode/1.18.32" || !validZenSession(gotSession) {
		t.Fatalf("fingerprint missing: ua=%q session=%q", gotUA, gotSession)
	}
	if s, _ := gotBody["stream"].(bool); !s {
		t.Fatalf("stream must be forced true upstream")
	}
	tools := gotBody["tools"].([]any)
	tm := tools[0].(map[string]any)
	if _, nested := tm["function"]; nested {
		t.Fatalf("tools must stay flat on /responses, got %v", tm)
	}
}

func TestResponsesEndpointRejectsChatSurfaceModel(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream must not be called for chat-surface models")
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)

	// chat-surface zen model (not in ResponsesModels)
	r := httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"zen/mimo-test-free","input":"hi"}`))
	rec := httptest.NewRecorder()
	g.handleResponses(rec, r)
	if rec.Code != 400 {
		t.Fatalf("expected 400 for chat-surface model, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/v1/chat/completions") {
		t.Fatalf("expected hint to chat endpoint, got: %s", rec.Body.String())
	}

	// kilo model
	r = httptest.NewRequest("POST", "/v1/responses",
		strings.NewReader(`{"model":"kilo/some-model","input":"hi"}`))
	rec = httptest.NewRecorder()
	g.handleResponses(rec, r)
	if rec.Code != 400 {
		t.Fatalf("expected 400 for kilo model, got %d", rec.Code)
	}
}

func TestResponsesToolCallTranslation(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_t\",\"status\":\"in_progress\"}}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_9\",\"name\":\"bash\",\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\"}}\n\n"))
		w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_t\",\"status\":\"completed\",\"usage\":{\"input_tokens\":9,\"output_tokens\":5}}}\n\n"))
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	rs.Zen.ResponsesModels = []string{"muse-test-free"}
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)

	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"zen/muse-test-free","messages":[{"role":"user","content":"ls"}],"stream":false}`))
	rec := httptest.NewRecorder()
	g.handleChat(rec, r)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	choices := out["choices"].([]any)
	ch := choices[0].(map[string]any)
	if ch["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason should be tool_calls, got %v", ch["finish_reason"])
	}
	msg := ch["message"].(map[string]any)
	tcs := msg["tool_calls"].([]any)
	tc := tcs[0].(map[string]any)
	if tc["id"] != "call_9" {
		t.Fatalf("tool call id mismatch: %v", tc)
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "bash" || fn["arguments"] != "{\"cmd\":\"ls\"}" {
		t.Fatalf("tool call payload mismatch: %v", fn)
	}
}

func TestChatToResponsesInputConversion(t *testing.T) {
	chat := []any{
		map[string]any{"role": "system", "content": "be brief"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "run ls"},
		}},
		map[string]any{"role": "assistant", "tool_calls": []any{
			map[string]any{"id": "call_1", "type": "function",
				"function": map[string]any{"name": "bash", "arguments": "{\"cmd\":\"ls\"}"}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "file.txt"},
	}
	out := chatMessagesToResponsesInput(chat)
	if len(out) != 4 {
		t.Fatalf("expected 4 input items, got %d: %v", len(out), out)
	}
	// user content parts retyped text -> input_text
	user := out[1].(map[string]any)
	part := user["content"].([]any)[0].(map[string]any)
	if part["type"] != "input_text" {
		t.Fatalf("user part should be input_text, got %v", part["type"])
	}
	// assistant tool_calls become function_call items
	fc := out[2].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "call_1" || fc["name"] != "bash" {
		t.Fatalf("bad function_call item: %v", fc)
	}
	// tool result becomes function_call_output
	fo := out[3].(map[string]any)
	if fo["type"] != "function_call_output" || fo["call_id"] != "call_1" || fo["output"] != "file.txt" {
		t.Fatalf("bad function_call_output item: %v", fo)
	}
}

func TestPriorityModeSticksToFirstKey(t *testing.T) {
	var mu = make(chan struct{}, 1)
	seen := map[string]int{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		seen[key]++
		if key == "Bearer sk-zen-a" && seen[key] <= 1 {
			// primary rate-limits on first request
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	_ = mu
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-zen-a"}, {"b", "sk-zen-b"}}}
	g := newGateway(cfg, rs, kf)

	// request 1: a 429s, fails over to b
	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free"))
	if rec.Code != 200 {
		t.Fatalf("expected 200 after failover, got %d", rec.Code)
	}
	// request 2 (cooldown not expired): still on b
	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free"))
	if seen["Bearer sk-zen-b"] != 2 {
		t.Fatalf("expected key b to serve while a cools, seen: %v", seen)
	}
	// after cooldown expiry: back to primary a
	time.Sleep(1100 * time.Millisecond)
	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free"))
	if rec.Code != 200 || seen["Bearer sk-zen-a"] != 2 {
		t.Fatalf("expected return to primary a, seen: %v code %d", seen, rec.Code)
	}
	// and now a stays primary for subsequent requests
	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free"))
	if seen["Bearer sk-zen-a"] != 3 {
		t.Fatalf("expected primary stickiness, seen: %v", seen)
	}
}

func TestClientAuth(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	cfg := &Config{
		APIKeys: []string{"sk-good"},
		Zen:     ZenProviderConfig{BaseURL: up.URL},
		Kilo:    KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)
	handler := g.clientOnly(g.handleChat)

	// missing key
	rec := httptest.NewRecorder()
	handler(rec, chatReq("zen", "mimo-test-free"))
	if rec.Code != 401 {
		t.Fatalf("expected 401 without key, got %d", rec.Code)
	}
	// wrong key
	r := chatReq("zen", "mimo-test-free")
	r.Header.Set("Authorization", "Bearer sk-wrong")
	rec = httptest.NewRecorder()
	handler(rec, r)
	if rec.Code != 401 {
		t.Fatalf("expected 401 with wrong key, got %d", rec.Code)
	}
	// good key
	r = chatReq("zen", "mimo-test-free")
	r.Header.Set("Authorization", "Bearer sk-good")
	rec = httptest.NewRecorder()
	handler(rec, r)
	if rec.Code != 200 {
		t.Fatalf("expected 200 with good key, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestModelIDSuffix(t *testing.T) {
	cases := []struct {
		ctx  int64
		mods []string
		want string
	}{
		{1048576, []string{"text", "image", "video", "pdf", "audio"}, "-1M-txt-img-vid-pdf-aud"},
		{200000, []string{"text", "image", "audio", "video"}, "-200K-txt-img-aud-vid"},
		{262144, []string{"text", "image", "video"}, "-262K-txt-img-vid"},
		{262144, nil, "-262K"},
		{1500000, []string{"text"}, "-1.5M-txt"},
	}
	for _, c := range cases {
		got := buildSuffixedID("x", c.ctx, c.mods)
		if got != "x"+c.want {
			t.Errorf("buildSuffixedID(ctx=%d) = %q, want %q", c.ctx, got, "x"+c.want)
		}
	}
	if got := formatCtx(1048576); got != "1M" {
		t.Errorf("formatCtx(1048576) = %q, want 1M", got)
	}
	if got := formatCtx(200000); got != "200K" {
		t.Errorf("formatCtx(200000) = %q, want 200K", got)
	}
	if got := formatCtx(262144); got != "262K" {
		t.Errorf("formatCtx(262144) = %q, want 262K", got)
	}
	if got := formatCtx(1500000); got != "1.5M" {
		t.Errorf("formatCtx(1500000) = %q, want 1.5M", got)
	}

	suffix := buildSuffixedID("muse-free", 1048576, []string{"text", "image"})
	if suffix != "muse-free-1M-txt-img" {
		t.Fatalf("buildSuffixedID = %q", suffix)
	}
	if got := stripModelSuffix(suffix); got != "muse-free" {
		t.Fatalf("stripModelSuffix = %q", got)
	}
	// bare ids pass through untouched
	for _, bare := range []string{"mimo-v2.6-flash-free", "qwen/qwen3.8-27b:free", "big-pickle"} {
		if got := stripModelSuffix(bare); got != bare {
			t.Fatalf("stripModelSuffix(%q) mutated bare id: %q", bare, got)
		}
	}
	// claude code [1m] opt-in suffix is stripped (alone and combined)
	if got := stripModelSuffix("zen/muse-spark-1.3-contributor-free-1M-txt-img-vid-pdf-aud[1m]"); got != "zen/muse-spark-1.3-contributor-free" {
		t.Fatalf("combined strip failed: %q", got)
	}
	if got := stripModelSuffix("glm-5.3-flash[1m]"); got != "glm-5.3-flash" {
		t.Fatalf("[1m]-only strip failed: %q", got)
	}
	if got := stripModelSuffix("zen/big-pickle[1M]"); got != "zen/big-pickle" {
		t.Fatalf("uppercase [1M] strip failed: %q", got)
	}
}

func TestRoutingWithSuffixedID(t *testing.T) {
	var gotModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)

	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-v2.6-flash-free-200K-txt-img-aud-vid"))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotModel != "mimo-v2.6-flash-free" {
		t.Fatalf("upstream must receive bare id, got %q", gotModel)
	}
}

func TestAnthropicConversion(t *testing.T) {
	// request: system + tool_use round-trip
	req := map[string]any{
		"model":      "zen/x",
		"max_tokens": float64(100),
		"system":     "be brief",
		"messages": []any{
			map[string]any{"role": "user", "content": "run ls"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": "running"},
				map[string]any{"type": "tool_use", "id": "tu_1", "name": "bash",
					"input": map[string]any{"cmd": "ls"}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "tu_1",
					"content": []any{map[string]any{"type": "text", "text": "file.txt"}}},
			}},
		},
		"tools": []any{
			map[string]any{"name": "bash", "description": "run cmd",
				"input_schema": map[string]any{"type": "object"}},
		},
	}
	chat := anthropicToChatBody(req)
	msgs := chat["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("expected 4 chat messages (system,user,assistant+tc,tool), got %d", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("system must become first message")
	}
	asst := msgs[2].(map[string]any)
	tcs := asst["tool_calls"].([]any)
	if tcs[0].(map[string]any)["id"] != "tu_1" {
		t.Fatalf("tool_use not converted: %v", asst)
	}
	toolMsg := msgs[3].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "tu_1" || toolMsg["content"] != "file.txt" {
		t.Fatalf("tool_result not converted: %v", toolMsg)
	}
	ct := chat["tools"].([]any)
	fn := ct[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "bash" || fn["parameters"] == nil {
		t.Fatalf("input_schema not mapped to parameters: %v", fn)
	}
}

func TestAnthropicSSEShape(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"tc1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{}\"}}]}}]}\n\n"))
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)

	r := httptest.NewRequest("POST", "/v1/messages",
		strings.NewReader(`{"model":"zen/x-free","max_tokens":100,"stream":true,
			"messages":[{"role":"user","content":"ls"}]}`))
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleMessages)(rec, r)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"event: message_start", "event: content_block_start",
		"event: content_block_delta", `"type":"text_delta"`,
		`"type":"tool_use"`, `"type":"input_json_delta"`,
		"event: message_delta", `"stop_reason":"tool_use"`, "event: message_stop",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in anthropic SSE:\n%s", want, body)
		}
	}
}

func TestAnthropicXApiKeyAuth(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	cfg := &Config{
		APIKeys: []string{"sk-good"},
		Zen:     ZenProviderConfig{BaseURL: up.URL},
		Kilo:    KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Kilo: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)

	r := httptest.NewRequest("POST", "/v1/messages",
		strings.NewReader(`{"model":"kilo/m","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("x-api-key", "sk-good") // anthropic-style auth
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleMessages)(rec, r)
	if rec.Code != 200 {
		t.Fatalf("x-api-key auth failed: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["type"] != "message" || out["stop_reason"] != "end_turn" {
		t.Fatalf("bad anthropic message: %v", out)
	}
}

func TestAnthropicToolCallFragmentsMerged(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// chunk 1: call header with partial args (as real upstreams stream it)
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"tc9\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"cm\"}}]}}]}\n\n"))
		// chunk 2: fragment carries only index + argument continuation
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"d\\\":\\\"ls\\\"}\"}}]}}]}\n\n"))
		w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	cfg := &Config{
		Zen:  ZenProviderConfig{BaseURL: up.URL},
		Kilo: KiloProviderConfig{BaseURL: up.URL},
	}
	rs := testRuntime()
	kf := &keyFile{Zen: []keyFileEntry{{"a", "sk-a"}}}
	g := newGateway(cfg, rs, kf)

	r := httptest.NewRequest("POST", "/v1/messages",
		strings.NewReader(`{"model":"zen/x-free","max_tokens":100,"stream":true,
			"messages":[{"role":"user","content":"ls"}]}`))
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleMessages)(rec, r)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	// exactly ONE tool_use block, with the merged full arguments
	if n := strings.Count(body, `"type":"tool_use"`); n != 1 {
		t.Fatalf("expected 1 tool_use block, got %d:\n%s", n, body)
	}
	// the delta payload carries JSON-escaped args
	if !strings.Contains(body, `{\"cmd\":\"ls\"}`) {
		t.Fatalf("arguments not merged, body:\n%s", body)
	}
	if !strings.Contains(body, `"stop_reason":"tool_use"`) {
		t.Fatalf("stop_reason should be tool_use:\n%s", body)
	}
}

func TestUAVersionFloor(t *testing.T) {
	cases := map[string]bool{
		"opencode/1.0.0": false, "opencode/1.17.9": false, "opencode/1.18.0": true,
		"opencode/1.18.32": true, "opencode/2.0.0": true, "opencode/9.9.9": true,
		"opencode": false, "myapp/1.0": false, "opencode/1.18": true,
	}
	for ua, want := range cases {
		if got := uaVersionOK(ua); got != want {
			t.Errorf("uaVersionOK(%q) = %v, want %v", ua, got, want)
		}
	}
}

var _ = sseResponse
