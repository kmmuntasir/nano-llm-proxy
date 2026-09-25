package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Tests for dual-endpoint providers: native Anthropic passthrough on
// /v1/messages, reverse chat→Messages translation on /v1/chat/completions for
// anthropic-only providers, and catalog fallback across the two roots. Shapes
// mirror the captured Z.ai fixtures in testdata/zai/.

// addDualProvider inserts a GUI-style provider with either or both endpoint
// roots set and refreshes the hot path.
func addDualProvider(t *testing.T, st *store.Store, name, baseURL, anthropicBaseURL string, keys ...string) *store.Provider {
	t.Helper()
	p := &store.Provider{
		Name: name, Type: "openai", BaseURL: baseURL,
		AnthropicBaseURL: anthropicBaseURL, Enabled: true, SortOrder: 100,
	}
	pks := make([]store.ProviderKey, 0, len(keys))
	for _, k := range keys {
		pks = append(pks, store.ProviderKey{Key: k})
	}
	if err := st.CreateProvider(p, pks); err != nil {
		t.Fatalf("CreateProvider(%s): %v", name, err)
	}
	return p
}

// chatBody builds a non-streaming chat request with arbitrary JSON.
func chatBody(t *testing.T, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// messagesBody builds a /v1/messages request with arbitrary JSON.
func messagesBody(t *testing.T, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// glmAnthropicMessage is a minimal non-streaming Messages response (GLM shape:
// thinking + text blocks, cache-aware usage).
const glmAnthropicMessage = `{
	"id":"msg_1","type":"message","role":"assistant","model":"glm-5.3",
	"content":[
		{"type":"thinking","thinking":" pondering","signature":"sig-abc"},
		{"type":"text","text":"Hello!"}
	],
	"stop_reason":"end_turn","stop_sequence":null,
	"usage":{"input_tokens":12,"output_tokens":34,"cache_read_input_tokens":0}
}`

// anthropicStream is a Messages SSE stream with thinking, text, and a tool_use
// block — the full delta vocabulary passthrough must survive.
const anthropicStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_s","usage":{"input_tokens":21,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" step 1"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hi "}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"there"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"Dhaka\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}

event: message_stop
data: {"type":"message_stop"}

`

func TestAnthropicOnlyProviderChatNonStream(t *testing.T) {
	var got struct {
		path    string
		apiKey  string
		version string
		body    map[string]any
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.apiKey = r.Header.Get("x-api-key")
		got.version = r.Header.Get("anthropic-version")
		json.NewDecoder(r.Body).Decode(&got.body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(glmAnthropicMessage))
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addDualProvider(t, st, "zai", "", up.URL, "ak1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}

	req := chatBody(t, `{
		"model":"zai/glm-5.3","stream":false,"temperature":0.5,"stop":"END",
		"messages":[
			{"role":"system","content":"You are terse."},
			{"role":"user","content":"hi"}
		]
	}`)
	rec := httptest.NewRecorder()
	g.handleChat(rec, req)
	if rec.Code != 200 {
		t.Fatalf("chat via anthropic: got %d: %s", rec.Code, rec.Body.String())
	}

	if got.path != "/v1/messages" {
		t.Fatalf("upstream path = %q, want /v1/messages", got.path)
	}
	if got.apiKey != "ak1" {
		t.Fatalf("upstream x-api-key = %q, want ak1", got.apiKey)
	}
	if got.version != "2023-06-01" {
		t.Fatalf("anthropic-version = %q, want 2023-06-01", got.version)
	}

	// request translation: prefix stripped, system extracted, max_tokens
	// defaulted, stop mapped, strict user/assistant messages
	if got.body["model"] != "glm-5.3" {
		t.Fatalf("upstream model = %v, want glm-5.3", got.body["model"])
	}
	if got.body["stream"] != false {
		t.Fatalf("upstream stream = %v, want false", got.body["stream"])
	}
	if mt, _ := got.body["max_tokens"].(float64); mt != 8192 {
		t.Fatalf("max_tokens = %v, want default 8192", got.body["max_tokens"])
	}
	if got.body["system"] != "You are terse." {
		t.Fatalf("system = %v, want extracted string", got.body["system"])
	}
	if ss, ok := got.body["stop_sequences"].([]any); !ok || len(ss) != 1 || ss[0] != "END" {
		t.Fatalf("stop_sequences = %v, want [END]", got.body["stop_sequences"])
	}
	if got.body["temperature"] != 0.5 {
		t.Fatalf("temperature = %v, want 0.5 passthrough", got.body["temperature"])
	}
	msgs, _ := got.body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %d entries, want 1 user message", len(msgs))
	}
	m0, _ := msgs[0].(map[string]any)
	if m0["role"] != "user" {
		t.Fatalf("messages[0].role = %v, want user", m0["role"])
	}

	// response translation: thinking -> reasoning_content, anthropic usage
	// spellings -> chat spellings, end_turn -> stop
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if out["model"] != "glm-5.3" {
		t.Fatalf("response model = %v, want glm-5.3", out["model"])
	}
	choices, _ := out["choices"].([]any)
	c0, _ := choices[0].(map[string]any)
	msg, _ := c0["message"].(map[string]any)
	if msg["content"] != "Hello!" {
		t.Fatalf("content = %v, want Hello!", msg["content"])
	}
	if msg["reasoning_content"] != " pondering" {
		t.Fatalf("reasoning_content = %v, want thinking block text", msg["reasoning_content"])
	}
	if c0["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v, want stop", c0["finish_reason"])
	}
	usage, _ := out["usage"].(map[string]any)
	if usage["prompt_tokens"] != 12.0 || usage["completion_tokens"] != 34.0 || usage["total_tokens"] != 46.0 {
		t.Fatalf("usage = %v, want 12/34/46", usage)
	}
}

func TestAnthropicOnlyProviderChatStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "ak1" {
			t.Errorf("upstream x-api-key = %q", r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(anthropicStream))
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addDualProvider(t, st, "zai", "", up.URL, "ak1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}
	admin, _ := st.UserByEmail("admin@example.com")
	plain := newClientKey(t, g, st, admin.ID, "stream-test")

	req := chatBody(t, `{"model":"zai/glm-5.3","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleChat)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("stream chat: got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content type = %q, want text/event-stream", ct)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"role":"assistant"`) {
		t.Fatalf("missing leading role chunk: %s", body)
	}
	if !strings.Contains(body, `"reasoning_content":" step 1"`) {
		t.Fatalf("thinking delta not surfaced as reasoning_content: %s", body)
	}
	if !strings.Contains(body, `"content":"Hi "`) || !strings.Contains(body, `"content":"there"`) {
		t.Fatalf("text deltas missing: %s", body)
	}
	// tool_use: first fragment carries id+name, later fragments only arguments
	if !strings.Contains(body, `"tool_calls":[{"function":{"arguments":"","name":"get_weather"},"id":"toolu_1","index":0,"type":"function"}]`) {
		t.Fatalf("tool_calls start fragment missing: %s", body)
	}
	if !strings.Contains(body, `"arguments":"{\"city\":"},"index":0,"type":"function"`) {
		t.Fatalf("input_json_delta fragment 1 missing: %s", body)
	}
	if !strings.Contains(body, `"arguments":"\"Dhaka\"}"},"index":0,"type":"function"`) {
		t.Fatalf("input_json_delta fragment 2 missing: %s", body)
	}
	if !strings.Contains(body, `"finish_reason":"tool_calls"`) {
		t.Fatalf("stop_reason tool_use not mapped to finish_reason: %s", body)
	}
	if !strings.Contains(body, `"usage":{"completion_tokens":7,"prompt_tokens":21,"total_tokens":28}`) {
		t.Fatalf("final usage chunk missing: %s", body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
		t.Fatalf("stream must end with [DONE]: %q", body[len(body)-40:])
	}

	// usage tapped from message_start + message_delta for the usage log
	events := g.usageBuf.drain()
	if len(events) != 1 || events[0].InputTokens != 21 || events[0].OutputTokens != 7 {
		t.Fatalf("usage events = %+v, want 21 in / 7 out", events)
	}
}

func TestAnthropicOnlyProviderToolsRoundTrip(t *testing.T) {
	var got map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id":"msg_t","type":"message","role":"assistant","model":"glm-5.3",
			"content":[{"type":"tool_use","id":"toolu_9","name":"get_weather","input":{"city":"Dhaka"}}],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":50,"output_tokens":10}
		}`))
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addDualProvider(t, st, "zai", "", up.URL, "ak1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}

	req := chatBody(t, `{
		"model":"zai/glm-5.3","stream":false,"tool_choice":"required",
		"messages":[
			{"role":"user","content":"weather?"},
			{"role":"assistant","content":"","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Dhaka\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_1","content":"Sunny 30C"},
			{"role":"tool","tool_call_id":"call_2","content":"Rainy 22C"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]
	}`)
	rec := httptest.NewRecorder()
	g.handleChat(rec, req)
	if rec.Code != 200 {
		t.Fatalf("tools chat: got %d: %s", rec.Code, rec.Body.String())
	}

	// tools -> {name, description, input_schema}; required -> any
	tools, _ := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want 1 entry", got["tools"])
	}
	t0, _ := tools[0].(map[string]any)
	if t0["name"] != "get_weather" || t0["description"] != "Get weather" {
		t.Fatalf("tool shape = %v, want name+description preserved", t0)
	}
	if _, ok := t0["input_schema"].(map[string]any); !ok {
		t.Fatalf("parameters not mapped to input_schema: %v", t0)
	}
	tc, _ := got["tool_choice"].(map[string]any)
	if tc["type"] != "any" {
		t.Fatalf("tool_choice = %v, want {type:any}", got["tool_choice"])
	}

	// history: assistant tool_calls -> tool_use; the two role:"tool" results
	// group into ONE user message (anthropic alternation rule)
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d entries, want [user, assistant, user(tool_results)]: %v", len(msgs), got["messages"])
	}
	a1, _ := msgs[1].(map[string]any)
	if a1["role"] != "assistant" {
		t.Fatalf("messages[1].role = %v, want assistant", a1["role"])
	}
	blocks, _ := a1["content"].([]any)
	b0, _ := blocks[0].(map[string]any)
	if b0["type"] != "tool_use" || b0["id"] != "call_1" || b0["name"] != "get_weather" {
		t.Fatalf("tool_use block = %v, want call_1/get_weather", b0)
	}
	if input, _ := b0["input"].(map[string]any); input["city"] != "Dhaka" {
		t.Fatalf("tool_use input = %v, want decoded {city:Dhaka}", b0["input"])
	}
	u2, _ := msgs[2].(map[string]any)
	if u2["role"] != "user" {
		t.Fatalf("messages[2].role = %v, want user (tool_result carrier)", u2["role"])
	}
	results, _ := u2["content"].([]any)
	if len(results) != 2 {
		t.Fatalf("tool results = %d, want both grouped in one user message", len(results))
	}
	r0, _ := results[0].(map[string]any)
	r1, _ := results[1].(map[string]any)
	if r0["type"] != "tool_result" || r0["tool_use_id"] != "call_1" || r0["content"] != "Sunny 30C" {
		t.Fatalf("tool_result[0] = %v", r0)
	}
	if r1["tool_use_id"] != "call_2" || r1["content"] != "Rainy 22C" {
		t.Fatalf("tool_result[1] = %v", r1)
	}

	// response: tool_use -> tool_calls with stringified arguments
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	choices, _ := out["choices"].([]any)
	c0, _ := choices[0].(map[string]any)
	if c0["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %v, want tool_calls", c0["finish_reason"])
	}
	msg, _ := c0["message"].(map[string]any)
	tcs, _ := msg["tool_calls"].([]any)
	tc0, _ := tcs[0].(map[string]any)
	if tc0["id"] != "toolu_9" || tc0["type"] != "function" {
		t.Fatalf("tool_calls[0] = %v, want toolu_9/function", tc0)
	}
	fn, _ := tc0["function"].(map[string]any)
	if fn["name"] != "get_weather" || fn["arguments"] != `{"city":"Dhaka"}` {
		t.Fatalf("function = %v, want name+stringified arguments", fn)
	}
}

func TestDualProviderMessagesPassthrough(t *testing.T) {
	var mu sync.Mutex
	chatHits := 0
	var got struct {
		path      string
		apiKey    string
		auth      string
		beta      string
		ua        string
		version   string
		body      map[string]any
		rawSSE    bool
		eventLine bool
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/chat/completions" {
			chatHits++
			sseOK(w)
			return
		}
		got.path = r.URL.Path
		got.apiKey = r.Header.Get("x-api-key")
		got.auth = r.Header.Get("Authorization")
		got.beta = r.Header.Get("anthropic-beta")
		got.ua = r.Header.Get("User-Agent")
		got.version = r.Header.Get("anthropic-version")
		json.NewDecoder(r.Body).Decode(&got.body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(anthropicStream))
		got.rawSSE = true
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addDualProvider(t, st, "zai", up.URL, up.URL, "ak1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}
	admin, _ := st.UserByEmail("admin@example.com")
	plain := newClientKey(t, g, st, admin.ID, "pass-test")

	// a Claude Code-shaped request: protocol extensions an OpenAI round-trip
	// would destroy, sent verbatim
	body := `{
		"model":"zai/glm-5.3","stream":true,"max_tokens":1024,
		"thinking":{"type":"adaptive","display":"omitted"},
		"context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]},
		"messages":[
			{"role":"system","content":"sys prompt"},
			{"role":"user","content":[{"type":"text","text":"hi"}]},
			{"role":"assistant","content":"hey"},
			{"role":"user","content":[{"type":"text","text":"go on"}]}
		]
	}`
	req := messagesBody(t, body)
	req.Header.Set("Authorization", "Bearer "+plain)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "context-1m-2025-08-07")
	req.Header.Set("User-Agent", "claude-cli/2.0.0 (external)")
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleMessages)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("messages passthrough: got %d: %s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if chatHits != 0 {
		t.Fatalf("openai root was hit %d times — /v1/messages must stay native", chatHits)
	}
	if got.path != "/v1/messages" {
		t.Fatalf("upstream path = %q", got.path)
	}
	if got.apiKey != "ak1" {
		t.Fatalf("x-api-key = %q, want pooled key ak1", got.apiKey)
	}
	if got.auth != "" {
		t.Fatalf("client Authorization leaked upstream: %q", got.auth)
	}
	if got.beta != "context-1m-2025-08-07" {
		t.Fatalf("anthropic-beta not forwarded: %q", got.beta)
	}
	if got.ua != "claude-cli/2.0.0 (external)" {
		t.Fatalf("User-Agent not forwarded: %q", got.ua)
	}
	if got.version != "2023-06-01" {
		t.Fatalf("anthropic-version = %q", got.version)
	}

	// body verbatim: thinking/context_management survive, interleaved
	// role:"system" message untouched, prefix stripped, stream forced on
	if got.body["model"] != "glm-5.3" {
		t.Fatalf("model = %v, want stripped glm-5.3", got.body["model"])
	}
	if got.body["stream"] != true {
		t.Fatalf("stream = %v, want true", got.body["stream"])
	}
	if _, ok := got.body["thinking"].(map[string]any); !ok {
		t.Fatalf("thinking block dropped: %v", got.body["thinking"])
	}
	if _, ok := got.body["context_management"].(map[string]any); !ok {
		t.Fatalf("context_management dropped: %v", got.body["context_management"])
	}
	msgs, _ := got.body["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4 verbatim (system message included)", len(msgs))
	}
	m0, _ := msgs[0].(map[string]any)
	if m0["role"] != "system" {
		t.Fatalf("messages[0].role = %v, want system kept verbatim", m0["role"])
	}

	// SSE copied with event framing intact
	if !got.rawSSE || got.eventLine {
		t.Fatalf("upstream handler misconfigured")
	}
	out := rec.Body.String()
	for _, want := range []string{
		"event: message_start", "event: content_block_delta",
		`"type":"thinking_delta"`, "event: message_stop",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("SSE framing lost, missing %q in: %.400s", want, out)
		}
	}

	// usage tapped for the log despite the verbatim copy
	events := g.usageBuf.drain()
	if len(events) != 1 || events[0].InputTokens != 21 || events[0].OutputTokens != 7 {
		t.Fatalf("usage events = %+v, want 21 in / 7 out", events)
	}
}

func TestDualProviderMessagesDefaultsVersion(t *testing.T) {
	var version string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(glmAnthropicMessage))
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addDualProvider(t, st, "zai", "", up.URL, "ak1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}

	// client sends no anthropic-version — the gateway defaults it
	req := messagesBody(t, `{"model":"zai/glm-5.3","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	g.handleMessages(rec, req)
	if rec.Code != 200 {
		t.Fatalf("messages: got %d: %s", rec.Code, rec.Body.String())
	}
	if version != "2023-06-01" {
		t.Fatalf("anthropic-version = %q, want default 2023-06-01", version)
	}
	// non-stream client on non-SSE upstream: body passes through as JSON
	if !strings.Contains(rec.Body.String(), `"stop_reason":"end_turn"`) {
		t.Fatalf("non-stream passthrough mangled: %s", rec.Body.String())
	}
}

func TestDualProviderChatPrefersOpenAIRoot(t *testing.T) {
	messagesHits := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			messagesHits++
		}
		sseOK(w)
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addDualProvider(t, st, "zai", up.URL, up.URL, "ak1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}

	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("zai", "glm-5.3"))
	if rec.Code != 200 {
		t.Fatalf("chat on dual provider: got %d: %s", rec.Code, rec.Body.String())
	}
	if messagesHits != 0 {
		t.Fatalf("/v1/messages hit %d times — chat must prefer the OpenAI root", messagesHits)
	}
}

func TestAnthropicCatalogAndOpenAIFallback(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"glm-5.3"},{"id":"glm-5.3-flash"}]}`))
		default: // openai root: /models is broken here
			http.Error(w, "nope", http.StatusInternalServerError)
		}
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addDualProvider(t, st, "zai", "", up.URL, "ak1")        // anthropic-only
	addDualProvider(t, st, "dualfb", up.URL, up.URL, "ak2") // openai root broken
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}

	rec := httptest.NewRecorder()
	g.handleModels(rec, anonReq())
	if rec.Code != 200 {
		t.Fatalf("models: got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"id":"zai/glm-5.3"`, `"id":"zai/glm-5.3-flash"`, `"id":"dualfb/glm-5.3"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("catalog missing %s: %s", want, body)
		}
	}
}

func TestProviderAPIAnthropicEndpointValidation(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1") // upstream never reached
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/providers", strings.NewReader(body))
		req.AddCookie(adminCookie)
		rec := httptest.NewRecorder()
		g.requireSession(g.requireSuperadmin(g.handleCreateProvider))(rec, req)
		return rec
	}

	// both endpoints empty -> 400
	rec := post(`{"name":"zai","keys":["k1"]}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "At least one endpoint is required") {
		t.Fatalf("empty endpoints: got %d: %s", rec.Code, rec.Body.String())
	}

	// anthropic-only provider -> 201
	rec = post(fmt.Sprintf(`{"name":"zai","anthropicBaseUrl":%q,"keys":["ak1"]}`, "https://api.z.ai/api/anthropic"))
	if rec.Code != 201 {
		t.Fatalf("anthropic-only create: got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)

	// invalid anthropic URL -> 400
	rec = post(`{"name":"bad","anthropicBaseUrl":"ftp://x","keys":["k1"]}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "anthropicBaseUrl") {
		t.Fatalf("bad anthropic URL: got %d: %s", rec.Code, rec.Body.String())
	}

	// list shows the anthropic root
	rec = httptest.NewRecorder()
	list := httptest.NewRequest("GET", "/api/providers", nil)
	list.AddCookie(adminCookie)
	g.requireSession(g.requireSuperadmin(g.handleListProviders))(rec, list)
	if rec.Code != 200 {
		t.Fatalf("list: got %d", rec.Code)
	}
	var listed struct {
		Providers []struct {
			Name             string `json:"name"`
			BaseURL          string `json:"baseUrl"`
			AnthropicBaseURL string `json:"anthropicBaseUrl"`
		} `json:"providers"`
	}
	json.Unmarshal(rec.Body.Bytes(), &listed)
	var zai *struct {
		Name             string `json:"name"`
		BaseURL          string `json:"baseUrl"`
		AnthropicBaseURL string `json:"anthropicBaseUrl"`
	}
	for i := range listed.Providers {
		if listed.Providers[i].Name == "zai" {
			zai = &listed.Providers[i]
		}
	}
	if zai == nil || zai.AnthropicBaseURL != "https://api.z.ai/api/anthropic" || zai.BaseURL != "" {
		t.Fatalf("zai row wrong: %+v", listed.Providers)
	}

	// PATCH: clearing both endpoints -> 400
	patch := func(id int64, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/providers/%d", id), strings.NewReader(body))
		req.AddCookie(adminCookie)
		req.SetPathValue("id", fmt.Sprintf("%d", id))
		rec := httptest.NewRecorder()
		g.requireSession(g.requireSuperadmin(g.handlePatchProvider))(rec, req)
		return rec
	}
	rec = patch(created.ID, `{"baseUrl":"","anthropicBaseUrl":""}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Cannot clear both endpoints") {
		t.Fatalf("clear both: got %d: %s", rec.Code, rec.Body.String())
	}

	// PATCH: invalid anthropic URL -> 400
	rec = patch(created.ID, `{"anthropicBaseUrl":"not a url"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "anthropicBaseUrl") {
		t.Fatalf("patch bad URL: got %d: %s", rec.Code, rec.Body.String())
	}

	// PATCH: switching to openai-only is allowed (clears the anthropic root)
	rec = patch(created.ID, fmt.Sprintf(`{"baseUrl":%q,"anthropicBaseUrl":""}`, "https://api.z.ai/api/coding/paas/v4"))
	if rec.Code != 200 {
		t.Fatalf("patch to openai-only: got %d: %s", rec.Code, rec.Body.String())
	}
	p, err := st.Provider(created.ID)
	if err != nil || p == nil || p.BaseURL != "https://api.z.ai/api/coding/paas/v4" || p.AnthropicBaseURL != "" {
		t.Fatalf("patched provider = %+v, err=%v", p, err)
	}
}
