package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Cached-input-token tracking: internally (and in usage_events) inputTokens
// is the TOTAL prompt and cachedTokens the cache-read subset (OpenAI
// semantics). Anthropic-native usage is normalized on the way in and split
// back out on /v1/messages responses.

// fakeResp wraps a canned upstream body for the response adapters.
func fakeResp(contentType, body string) *http.Response {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

const chatSSEWithCache = "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
	"data: {\"id\":\"c1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":20,\"prompt_tokens_details\":{\"cached_tokens\":800}}}\n\n" +
	"data: [DONE]\n\n"

const responsesSSEWithCache = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"usage\":null}}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":500,\"output_tokens\":9,\"input_tokens_details\":{\"cached_tokens\":300}}}}\n\n"

func wantTU(t *testing.T, name string, got tokenUsage, in, out, cached int64) {
	t.Helper()
	if got.in != in || got.out != out || got.cached != cached {
		t.Errorf("%s: usage = {in:%d out:%d cached:%d}, want {in:%d out:%d cached:%d}",
			name, got.in, got.out, got.cached, in, out, cached)
	}
}

func TestCachedTokensChatPassthroughCapture(t *testing.T) {
	rec := httptest.NewRecorder()
	tu := passthroughSSE(rec, fakeResp("text/event-stream", chatSSEWithCache))
	wantTU(t, "passthroughSSE", tu, 1000, 20, 800)
	// the raw chunk (with its cached_tokens) reaches the client untouched
	if !strings.Contains(rec.Body.String(), `"prompt_tokens_details":{"cached_tokens":800}`) {
		t.Errorf("passthrough altered the usage chunk: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	wantTU(t, "streamKilo SSE", streamKilo(rec, fakeResp("text/event-stream", chatSSEWithCache)), 1000, 20, 800)

	rec = httptest.NewRecorder()
	nonStream := `{"id":"c2","choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":40,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":32}}}`
	wantTU(t, "streamKilo JSON", streamKilo(rec, fakeResp("application/json", nonStream)), 40, 2, 32)

	rec = httptest.NewRecorder()
	wantTU(t, "aggregateChatSSE", aggregateChatSSE(rec, fakeResp("text/event-stream", chatSSEWithCache), "m"), 1000, 20, 800)
}

func TestCachedTokensChatCapturedIntoUsageEvent(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(chatSSEWithCache))
	}))
	defer up.Close()
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	ck := newClientKey(t, g, st, admin.ID, "cache")

	rec := httptest.NewRecorder()
	req := chatReq("zen", "mimo-test-free")
	req.Header.Set("Authorization", "Bearer "+ck)
	g.clientOnly(g.handleChat)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("chat: %d: %s", rec.Code, rec.Body.String())
	}
	events := g.usageBuf.drain()
	if len(events) != 1 || events[0].InputTokens != 1000 || events[0].OutputTokens != 20 || events[0].CachedTokens != 800 {
		t.Fatalf("usage event = %+v, want in=1000 out=20 cached=800", events)
	}
}

func TestCachedTokensResponsesCapture(t *testing.T) {
	// native Responses passthrough (stream client)
	rec := httptest.NewRecorder()
	wantTU(t, "responses passthrough", streamKilo(rec, fakeResp("text/event-stream", responsesSSEWithCache)), 500, 9, 300)

	// Responses aggregated for a stream:false client
	rec = httptest.NewRecorder()
	wantTU(t, "aggregateResponsesSSE", aggregateResponsesSSE(rec, fakeResp("text/event-stream", responsesSSEWithCache), "m"), 500, 9, 300)
	var agg struct {
		Usage struct {
			InputTokens        int64 `json:"input_tokens"`
			InputTokensDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &agg); err != nil || agg.Usage.InputTokens != 500 || agg.Usage.InputTokensDetails.CachedTokens != 300 {
		t.Errorf("aggregated responses usage wrong (%v): %s", err, rec.Body.String())
	}

	// Responses -> chat translation, both client modes
	for _, stream := range []bool{true, false} {
		rec = httptest.NewRecorder()
		tu := translateResponses(rec, fakeResp("text/event-stream", responsesSSEWithCache), "m", stream)
		wantTU(t, fmt.Sprintf("translateResponses stream=%v", stream), tu, 500, 9, 300)
		if !strings.Contains(rec.Body.String(), `"prompt_tokens_details":{"cached_tokens":300}`) {
			t.Errorf("translated chat usage lacks cached_tokens (stream=%v): %s", stream, rec.Body.String())
		}
	}
}

// anthropicSSEUsage pulls the usage objects off message_start and
// message_delta in an emitted Anthropic stream.
func anthropicSSEUsage(t *testing.T, body string) (start, delta map[string]any) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var ev struct {
			Type    string         `json:"type"`
			Usage   map[string]any `json:"usage"`
			Message struct {
				Usage map[string]any `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(payload), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			start = ev.Message.Usage
		case "message_delta":
			delta = ev.Usage
		}
	}
	if start == nil || delta == nil {
		t.Fatalf("message_start/message_delta usage missing: %s", body)
	}
	return start, delta
}

func wantAnthropicUsage(t *testing.T, name string, u map[string]any, in, cacheRead, cacheCreate, out float64) {
	t.Helper()
	if u["input_tokens"] != in || u["cache_read_input_tokens"] != cacheRead ||
		u["cache_creation_input_tokens"] != cacheCreate || u["output_tokens"] != out {
		t.Errorf("%s: usage = %v, want input=%v cache_read=%v cache_creation=%v output=%v",
			name, u, in, cacheRead, cacheCreate, out)
	}
}

func TestCachedTokensMessagesTranslation(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if s, _ := body["stream"].(bool); s {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(chatSSEWithCache))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c9","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1000,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":800}}}`))
	}))
	defer up.Close()
	g, st := testStoreGateway(t, up.URL)
	addDualProvider(t, st, "acme", up.URL, "", "k1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}
	admin, _ := st.UserByEmail("admin@example.com")
	plain := newClientKey(t, g, st, admin.ID, "msgs")

	for _, stream := range []bool{false, true} {
		req := messagesBody(t, fmt.Sprintf(`{"model":"acme/m1","max_tokens":64,"stream":%v,"messages":[{"role":"user","content":"hi"}]}`, stream))
		req.Header.Set("Authorization", "Bearer "+plain)
		rec := httptest.NewRecorder()
		g.clientOnly(g.handleMessages)(rec, req)
		if rec.Code != 200 {
			t.Fatalf("messages stream=%v: %d: %s", stream, rec.Code, rec.Body.String())
		}
		if stream {
			start, delta := anthropicSSEUsage(t, rec.Body.String())
			wantAnthropicUsage(t, "message_start", start, 0, 0, 0, 0)
			// input + cache_read + cache_creation sums back to the 1000 total
			wantAnthropicUsage(t, "message_delta", delta, 200, 800, 0, 20)
		} else {
			var msg struct {
				Usage map[string]any `json:"usage"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
				t.Fatalf("decode: %v", err)
			}
			wantAnthropicUsage(t, "non-stream", msg.Usage, 200, 800, 0, 20)
		}
		events := g.usageBuf.drain()
		if len(events) != 1 || events[0].InputTokens != 1000 || events[0].CachedTokens != 800 || events[0].OutputTokens != 20 {
			t.Errorf("stream=%v usage event = %+v, want total in=1000 cached=800 out=20", stream, events)
		}
	}
}

func TestCachedTokensZenMessagesAggregated(t *testing.T) {
	// stream:false client on a stream-forced upstream (chat and Responses)
	msg := chatSSEToAnthropicMessage(strings.NewReader(chatSSEWithCache), "m")
	wantAnthropicUsage(t, "chat aggregate", roundTrip(t, msg)["usage"].(map[string]any), 200, 800, 0, 20)
	wantTU(t, "chat aggregate tap", usageFromAnthropicMsg(roundTrip(t, msg)), 1000, 20, 800)

	msg = responsesSSEToAnthropicMessage(strings.NewReader(responsesSSEWithCache), "m")
	wantAnthropicUsage(t, "responses aggregate", roundTrip(t, msg)["usage"].(map[string]any), 200, 300, 0, 9)
	wantTU(t, "responses aggregate tap", usageFromAnthropicMsg(roundTrip(t, msg)), 500, 9, 300)

	// Responses stream -> Anthropic stream
	rec := httptest.NewRecorder()
	a := &anthropicStreamWriter{w: rec, model: "m"}
	a.start()
	streamResponsesSSEToAnthropic(a, strings.NewReader(responsesSSEWithCache))
	wantTU(t, "responses stream tap", a.tu, 500, 9, 300)
	_, delta := anthropicSSEUsage(t, rec.Body.String())
	wantAnthropicUsage(t, "responses message_delta", delta, 200, 300, 0, 9)
}

// roundTrip re-decodes a map through JSON so numbers are float64, as they are
// for every decoded upstream body.
func roundTrip(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// anthropicCachedStream is a native Anthropic stream with cache usage: the
// prompt is 10 uncached + 900 read from cache + 90 written to cache.
const anthropicCachedStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_c","usage":{"input_tokens":10,"cache_read_input_tokens":900,"cache_creation_input_tokens":90,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}

`

const anthropicCachedMessage = `{"id":"msg_n","type":"message","role":"assistant","model":"glm",
	"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn",
	"usage":{"input_tokens":10,"cache_read_input_tokens":900,"cache_creation_input_tokens":90,"output_tokens":15}}`

func TestCachedTokensNativeAnthropicNormalization(t *testing.T) {
	// /v1/messages passthrough: bytes untouched, usage normalized for the log
	rec := httptest.NewRecorder()
	tu := passthroughAnthropic(rec, fakeResp("text/event-stream", anthropicCachedStream))
	wantTU(t, "native SSE passthrough", tu, 1000, 15, 900)
	if rec.Body.String() != anthropicCachedStream {
		t.Errorf("native passthrough altered the stream")
	}
	rec = httptest.NewRecorder()
	wantTU(t, "native JSON passthrough", passthroughAnthropic(rec, fakeResp("application/json", anthropicCachedMessage)), 1000, 15, 900)

	// /v1/chat/completions on an anthropic-only upstream: OpenAI semantics out
	rec = httptest.NewRecorder()
	tu = streamAnthropicSSEToChat(rec, strings.NewReader(anthropicCachedStream), "m")
	wantTU(t, "anthropic->chat stream", tu, 1000, 15, 900)
	if !strings.Contains(rec.Body.String(), `"prompt_tokens":1000,"prompt_tokens_details":{"cached_tokens":900}`) {
		t.Errorf("chat stream usage not normalized: %s", rec.Body.String())
	}
	var msg map[string]any
	if err := json.Unmarshal([]byte(anthropicCachedMessage), &msg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantTU(t, "anthropic message tap", usageFromAnthropicMsg(msg), 1000, 15, 900)
	chat := roundTrip(t, anthropicJSONToChat(msg, "m"))
	wantTU(t, "anthropic->chat JSON", usageFromChatJSON(chat), 1000, 15, 900)
}

func TestCachedTokensNativeAnthropicEndToEnd(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(anthropicCachedStream))
	}))
	defer up.Close()
	g, st := testStoreGateway(t, up.URL)
	addDualProvider(t, st, "zai", up.URL, up.URL, "ak1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}
	admin, _ := st.UserByEmail("admin@example.com")
	plain := newClientKey(t, g, st, admin.ID, "native")
	req := messagesBody(t, `{"model":"zai/glm","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleMessages)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("messages: %d: %s", rec.Code, rec.Body.String())
	}
	events := g.usageBuf.drain()
	if len(events) != 1 || events[0].InputTokens != 1000 || events[0].CachedTokens != 900 || events[0].OutputTokens != 15 {
		t.Fatalf("usage event = %+v, want in=1000 cached=900 out=15", events)
	}
}

// --- reporting API ---

func TestUsageReportCachedTokens(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	admin, _ := st.UserByEmail("admin@example.com")
	_, ak, err := st.CreateClientKey(admin.ID, "k")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	day0 := int64(1790000000)
	day0 -= day0 % 86400
	ev := func(off int64, model string, in, out, cached int64) store.UsageEvent {
		return store.UsageEvent{
			TS: day0 + off, UserID: admin.ID, ClientKeyID: ak.ID, Provider: "zen", Model: model,
			InputTokens: in, OutputTokens: out, CachedTokens: cached, Status: 200, DurationMs: 10, Surface: "chat",
		}
	}
	if err := st.InsertUsageEvents([]store.UsageEvent{
		ev(100, "a", 1000, 10, 900), // most cached, fewer total tokens than b
		ev(200, "b", 5000, 10, 100),
		ev(3700, "a", 300, 5, 200),
		ev(3800, "c", 50, 5, 0),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f := &usageFixture{g: g, st: st, day0: day0, adminCookie: loginAs(t, g, "admin@example.com", "super-secret-pass")}
	rng := fmt.Sprintf("from=%d&to=%d", day0, day0+2*3600)

	var ov struct {
		Totals store.UsageOverviewTotals
		Points []store.UsageOverviewPoint
	}
	if code := f.get(t, g.handleUsageOverview, f.adminCookie, "/api/usage/overview?"+rng, &ov); code != 200 {
		t.Fatalf("overview: %d", code)
	}
	if ov.Totals.CachedTokens != 1200 || ov.Totals.InputTokens != 6350 {
		t.Errorf("overview totals = %+v, want cached 1200 of 6350", ov.Totals)
	}
	if len(ov.Points) != 3 || ov.Points[0].CachedTokens != 1000 || ov.Points[1].CachedTokens != 200 || ov.Points[2].CachedTokens != 0 {
		t.Errorf("overview points = %+v", ov.Points)
	}

	var bd struct {
		Totals store.UsageBreakdownTotals
		Rows   []store.UsageBreakdownRow
	}
	if code := f.get(t, g.handleUsageBreakdown, f.adminCookie, "/api/usage/breakdown?"+rng+"&groupBy=model&sort=cachedTokens", &bd); code != 200 {
		t.Fatalf("breakdown: %d", code)
	}
	if bd.Totals.CachedTokens != 1200 || bd.Totals.InputTokens != 6350 {
		t.Errorf("breakdown totals = %+v", bd.Totals)
	}
	if len(bd.Rows) != 3 || bd.Rows[0].Model != "a" || bd.Rows[0].CachedTokens != 1100 || bd.Rows[1].Model != "b" || bd.Rows[2].CachedTokens != 0 {
		t.Errorf("breakdown sort=cachedTokens desc: %+v", bd.Rows)
	}

	var rq struct{ Rows []store.UsageRequestRow }
	if code := f.get(t, g.handleUsageRequests, f.adminCookie, "/api/usage/requests?"+rng+"&sort=cachedTokens&order=asc", &rq); code != 200 {
		t.Fatalf("requests: %d", code)
	}
	if len(rq.Rows) != 4 || rq.Rows[0].CachedTokens != 0 || rq.Rows[3].CachedTokens != 900 || rq.Rows[3].InputTokens != 1000 {
		t.Errorf("requests sort=cachedTokens asc: %+v", rq.Rows)
	}

	var se struct {
		Metric string
		Series []store.UsageSeries
	}
	if code := f.get(t, g.handleUsageSeries, f.adminCookie, "/api/usage/series?"+rng+"&groupBy=model&metric=cachedTokens", &se); code != 200 {
		t.Fatalf("series: %d", code)
	}
	// c has no cached tokens, so only a and b rank
	if se.Metric != "cachedTokens" || len(se.Series) != 2 || se.Series[0].Key != "zen/a" ||
		se.Series[0].Points[0].Value != 900 || se.Series[0].Points[1].Value != 200 || se.Series[1].Points[0].Value != 100 {
		t.Errorf("cachedTokens series: %+v", se.Series)
	}

	// the raw JSON carries the camelCase field on every surface
	for _, path := range []string{
		"/api/usage/overview?" + rng,
		"/api/usage/breakdown?" + rng,
		"/api/usage/requests?" + rng,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(f.adminCookie)
		h := map[string]http.HandlerFunc{
			"overview": g.handleUsageOverview, "breakdown": g.handleUsageBreakdown, "requests": g.handleUsageRequests,
		}[strings.TrimPrefix(strings.SplitN(path, "?", 2)[0], "/api/usage/")]
		g.requireSession(h)(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"cachedTokens":`) {
			t.Errorf("%s: %d lacks cachedTokens: %s", path, rec.Code, rec.Body.String())
		}
	}
}
