package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
)

// zenExpiredBody is OpenCode's answer for an expired API key (2026-10).
const zenExpiredBody = `{"error":{"type":"authentication_error","code":"api_key_expired","message":"This API key has expired. Create a new API key in OpenCode Console and replace it."}}`

// modelCooldownFor returns how long the key with hash still cools for model.
func modelCooldownFor(p *Pool, hash, model string) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, k := range p.keys {
		if k.Hash == hash {
			return time.Until(k.modelCooldown[model])
		}
	}
	return 0
}

// keyCalls counts upstream calls per Authorization header, race-safe.
type keyCalls struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *keyCalls) add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[key]++
}

func (c *keyCalls) get(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[key]
}

func (c *keyCalls) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := 0
	for _, v := range c.n {
		t += v
	}
	return t
}

// requestModel reads the "model" of an upstream request body.
func requestModel(r *http.Request) string {
	var b struct {
		Model string `json:"model"`
	}
	raw, _ := io.ReadAll(r.Body)
	json.Unmarshal(raw, &b) //nolint:errcheck
	return b.Model
}

// An expired key (OpenCode's 401 api_key_expired) is disabled with reason
// "expired" — not cooled and retried — and the request fails over.
func TestExpiredKeyDisabledAndNextKeyServes(t *testing.T) {
	var calls keyCalls
	g := testGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		calls.add(key)
		if key == "Bearer sk-zen-a" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(zenExpiredBody)) //nolint:errcheck
			return
		}
		sseOK(w)
	}))
	for i := range 2 {
		rec := httptest.NewRecorder()
		g.handleChat(rec, chatReq("zen", "mimo-test-free"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	if n := calls.get("Bearer sk-zen-a"); n != 1 {
		t.Fatalf("expired key called %d times, want 1 (disabled after the first)", n)
	}
	if n := calls.get("Bearer sk-zen-b"); n != 2 {
		t.Fatalf("next key served %d requests, want 2", n)
	}
	for _, k := range g.zen.snapshot() {
		switch k.Label {
		case "a":
			if k.Status != statusDisabled || k.DisabledReason != disabledExpired {
				t.Fatalf("key a = %s/%q, want disabled/expired", k.Status, k.DisabledReason)
			}
		default:
			if k.DisabledReason != "" {
				t.Fatalf("key %s has disabled reason %q", k.Label, k.DisabledReason)
			}
		}
	}
}

// The structured expiry code disables on 401 and 403; the client hint
// never names the key.
func TestExpiredKeyStatusesAndHint(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(zenExpiredBody)) //nolint:errcheck
		}))
		g := kiloGateway(t, up, "sk-a")
		rec := httptest.NewRecorder()
		g.handleChat(rec, chatReq("kilo", "m"))
		up.Close()
		if k := g.kilo.snapshot()[0]; k.Status != statusDisabled || k.DisabledReason != disabledExpired {
			t.Fatalf("HTTP %d: key %s/%q, want disabled/expired", status, k.Status, k.DisabledReason)
		}
		body := rec.Body.String()
		if !strings.Contains(body, keyExpiredHint) || strings.Contains(body, "label-sk-a") {
			t.Fatalf("HTTP %d: client body %s", status, body)
		}
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("HTTP %d: client status %d, want 503", status, rec.Code)
		}
	}
}

// Expiry wording a client can plant — an echoed prompt in the error
// message, a 400, the code inside free text — must never disable a key.
func TestPlantedExpiryWordingDoesNotDisable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"400 echo", http.StatusBadRequest, `{"error":{"message":"bad input: 'This API key has expired' api_key_expired"}}`},
		{"400 with code", http.StatusBadRequest, `{"error":{"code":"api_key_expired","message":"x"}}`},
		{"401 echo", http.StatusUnauthorized, `{"error":{"message":"prompt said: your key has expired (api_key_expired)"}}`},
		{"401 not json", http.StatusUnauthorized, `key has expired api_key_expired`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				w.Write([]byte(c.body)) //nolint:errcheck
			}))
			defer up.Close()
			g := kiloGateway(t, up, "sk-a")
			g.handleChat(httptest.NewRecorder(), chatReq("kilo", "m"))
			if k := g.kilo.snapshot()[0]; k.Status == statusDisabled || k.DisabledReason != "" {
				t.Fatalf("key %s/%q, must not be disabled", k.Status, k.DisabledReason)
			}
			if saysKeyExpired(c.status, []byte(c.body)) {
				t.Fatal("saysKeyExpired matched planted wording")
			}
		})
	}
}

// The admin provider list exposes why a key was disabled.
func TestProviderListShowsDisabledReason(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer sk-zen-1" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(zenExpiredBody)) //nolint:errcheck
			return
		}
		sseOK(w)
	}))
	defer up.Close()
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	ck := newClientKey(t, g, st, admin.ID, "expiry")
	chat := chatReq("zen", "mimo-test-free")
	chat.Header.Set("Authorization", "Bearer "+ck)
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleChat)(rec, chat)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat: got %d: %s", rec.Code, rec.Body.String())
	}

	cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	rec = httptest.NewRecorder()
	list := httptest.NewRequest("GET", "/api/providers", nil)
	list.AddCookie(cookie)
	g.requireSession(g.requireAdmin(g.handleListProviders))(rec, list)
	var out struct {
		Providers []struct {
			Name string           `json:"name"`
			Keys []map[string]any `json:"keys"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body.String())
	}
	seen := 0
	for _, p := range out.Providers {
		if p.Name != "zen" {
			continue
		}
		for _, k := range p.Keys {
			switch k["label"] {
			case "key-a":
				seen++
				if k["status"] != statusDisabled || k["disabledReason"] != "expired" {
					t.Fatalf("key-a view = %v, want disabled + disabledReason expired", k)
				}
			case "key-b":
				seen++
				if _, ok := k["disabledReason"]; ok {
					t.Fatalf("healthy key carries disabledReason: %v", k)
				}
			}
		}
	}
	if seen != 2 {
		t.Fatalf("zen keys missing from list: %s", rec.Body.String())
	}
}

// A 429 cools (key, model): the same key keeps serving other models, and the
// rate-limited model answers 429 + Retry-After without an upstream call.
func Test429CoolsKeyForModelOnly(t *testing.T) {
	var calls keyCalls
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := requestModel(r)
		calls.add(m)
		if m == "hot" {
			w.Header().Set("Retry-After", "20")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		sseOK(w)
	}))
	defer up.Close()
	g := kiloGateway(t, up, "sk-a")

	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("kilo", "hot"))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "20" {
		t.Fatalf("hot model: got %d Retry-After %q: %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("kilo", "cool"))
	if rec.Code != http.StatusOK {
		t.Fatalf("other model on the same key: got %d: %s", rec.Code, rec.Body.String())
	}
	if got := keyStatus(t, g, "label-sk-a"); got != statusHealthy {
		t.Fatalf("key status %s, want healthy", got)
	}
	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("kilo", "hot"))
	if rec.Code != http.StatusTooManyRequests || calls.get("hot") != 1 {
		t.Fatalf("cooling model: got %d after %d upstream calls, want 429 after 1", rec.Code, calls.get("hot"))
	}
	if ra := rec.Header().Get("Retry-After"); ra != "20" && ra != "19" {
		t.Fatalf("Retry-After %q, want ~20", ra)
	}
	// the failure is in the usage log as a 429 with the reason
	var found bool
	for _, e := range g.usage.recent(10) {
		if e.Status == http.StatusTooManyRequests && strings.Contains(e.FailReason, "rate-limiting hot") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no 429 usage entry: %+v", g.usage.recent(10))
	}
}

// trippedState reports whether the breaker for (provider, model) is in
// single-attempt mode.
func trippedState(g *gateway, provider, model string) bool {
	return g.breaker.tripped(provider, model, time.Now())
}

// 429s from two distinct keys for one model trip the breaker: the request
// stops rotating (the third key isn't tried) and answers 429. The next
// request makes a single attempt on the key that never 429'd — exactly one
// upstream call — and its success clears the trip.
func TestBreakerTripsAndHealthyKeyStillServes(t *testing.T) {
	var calls keyCalls
	g := testGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		calls.add(key)
		if key != "Bearer sk-zen-c" {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"rate limited"}}`)) //nolint:errcheck
			return
		}
		sseOK(w)
	}))

	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free"))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("trip: got %d Retry-After %q: %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
	var errBody struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &errBody) //nolint:errcheck
	if errBody.Error.Type != "rate_limit_error" || !strings.Contains(errBody.Error.Message, "zen is rate-limiting mimo-test-free — retry after 1s") {
		t.Fatalf("OpenAI 429 shape: %s", rec.Body.String())
	}
	if calls.get("Bearer sk-zen-a") != 1 || calls.get("Bearer sk-zen-b") != 1 || calls.get("Bearer sk-zen-c") != 0 {
		t.Fatalf("calls after trip: %v (key c must not be tried)", calls.n)
	}
	if !trippedState(g, "zen", "mimo-test-free") {
		t.Fatal("breaker not tripped after 429s from two keys")
	}

	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free"))
	if rec.Code != http.StatusOK || calls.total() != 3 || calls.get("Bearer sk-zen-c") != 1 {
		t.Fatalf("single attempt: got %d after %d calls (%v), want 200 on key c with 1 call", rec.Code, calls.total()-2, calls.n)
	}
	if trippedState(g, "zen", "mimo-test-free") {
		t.Fatal("success did not clear the trip")
	}
}

// Tripped, the single attempt doesn't rotate on a 429; once every key is
// cooling for the model, requests fail fast with no upstream call.
func TestBreakerSingleAttemptThenFailFast(t *testing.T) {
	var calls keyCalls
	g := testGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.add(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	g.handleChat(httptest.NewRecorder(), chatReq("zen", "mimo-test-free")) // a, b → trip
	if calls.total() != 2 {
		t.Fatalf("trip used %d calls, want 2", calls.total())
	}

	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free")) // single attempt: c
	if calls.total() != 3 || calls.get("Bearer sk-zen-c") != 1 {
		t.Fatalf("single attempt used %d calls (%v), want 1 on key c", calls.total()-2, calls.n)
	}
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("single attempt 429: got %d Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}

	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("zen", "mimo-test-free")) // all cooled: no call
	if calls.total() != 3 {
		t.Fatalf("fail-fast made %d upstream calls", calls.total()-3)
	}
	ra := rec.Header().Get("Retry-After")
	if rec.Code != http.StatusTooManyRequests || (ra != "30" && ra != "29") {
		t.Fatalf("fail-fast: got %d Retry-After %q, want 429 / ~30", rec.Code, ra)
	}
	// other models on the same keys are unaffected by the trip
	if !trippedState(g, "zen", "mimo-test-free") || trippedState(g, "zen", "other") {
		t.Fatal("trip state leaked across models")
	}
}

// Backoff: max(Retry-After seen, cooldownSeconds), doubled by a later 429
// while tripped (at most once per window), capped; an expired trip counts
// afresh; success clears.
func TestBreakerBackoffMath(t *testing.T) {
	var b rateBreaker
	now := time.Now()
	floor := 30 * time.Second
	if b.record429("p", "m", "k1", 10*time.Second, floor, now) {
		t.Fatal("one key must not trip")
	}
	if !b.record429("p", "m", "k2", 40*time.Second, floor, now.Add(time.Second)) {
		t.Fatal("two distinct keys within the window must trip")
	}
	e := b.m[breakerKey("p", "m")]
	if e.backoff != 40*time.Second {
		t.Fatalf("backoff %v, want 40s (longest Retry-After)", e.backoff)
	}
	b.record429("p", "m", "k3", 10*time.Second, floor, now.Add(2*time.Second))
	if e.backoff != 40*time.Second {
		t.Fatalf("backoff grew within the window: %v", e.backoff)
	}
	b.record429("p", "m", "k3", 10*time.Second, floor, now.Add(20*time.Second))
	if e.backoff != 80*time.Second || !e.openUntil.Equal(now.Add(100*time.Second)) {
		t.Fatalf("backoff %v until +%v, want 80s until +100s", e.backoff, e.openUntil.Sub(now))
	}
	for i := range 10 {
		b.record429("p", "m", "k3", 0, floor, now.Add(time.Duration(40+20*i)*time.Second))
	}
	if e.backoff != breakerMaxBackoff {
		t.Fatalf("backoff %v, want the 5m cap", e.backoff)
	}
	if b.tripped("p", "m", now.Add(time.Hour)) {
		t.Fatal("trip outlived its backoff")
	}
	if b.record429("p", "m", "k1", 0, floor, now.Add(time.Hour)) {
		t.Fatal("an expired trip must count afresh")
	}
	b.success("p", "m")
	if _, ok := b.m[breakerKey("p", "m")]; ok {
		t.Fatal("success did not clear the entry")
	}
}

// /v1/messages answers a rate-limited model in Anthropic's error shape.
func TestRateLimitAnthropicShape(t *testing.T) {
	g := testGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(
		`{"model":"zen/mimo-test-free","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()
	g.handleMessages(rec, req)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d Retry-After %q: %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body) //nolint:errcheck
	if body.Type != "error" || body.Error.Type != "rate_limit_error" || !strings.Contains(body.Error.Message, "rate-limiting mimo-test-free") {
		t.Fatalf("Anthropic 429 shape: %s", rec.Body.String())
	}
}

// /v1/responses uses the OpenAI rate-limit shape.
func TestRateLimitResponsesShape(t *testing.T) {
	g := testGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(
		`{"model":"zen/muse-test-free","input":"hi"}`))
	rec := httptest.NewRecorder()
	g.handleResponses(rec, req)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"rate_limit_error"`) {
		t.Fatalf("OpenAI 429 shape: %s", rec.Body.String())
	}
}

// 5xx still cools the whole key, and a pool down for non-429 reasons keeps
// the 503 "no healthy keys" answer.
func Test5xxStillCoolsWholeKey(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer up.Close()
	g := kiloGateway(t, up, "sk-a")
	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("kilo", "m"))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "" {
		t.Fatalf("got %d Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if got := keyStatus(t, g, "label-sk-a"); got != statusCooling {
		t.Fatalf("key status %s, want cooling", got)
	}
	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("kilo", "other"))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "No healthy kilo keys") {
		t.Fatalf("other model: got %d: %s", rec.Code, rec.Body.String())
	}
}

// Key verification reports an expired key as rejected, saying so.
func TestKeyCheckReportsExpiredKey(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(zenExpiredBody)) //nolint:errcheck
	}))
	defer up.Close()
	g := kiloGateway(t, up, "sk-a")
	err := g.verifyUpstreamKey(context.Background(), up.URL, "", "sk-expired")
	status, msg := classifyKeyCheck(err, "zen", `Key "work-1"`)
	if status != keyCheckInvalid || !strings.Contains(msg, "key has expired") {
		t.Fatalf("verdict %s: %s", status, msg)
	}
}

// The breaker is shared by concurrent requests: exercise it under -race.
func TestBreakerConcurrent(t *testing.T) {
	var b rateBreaker
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			now := time.Now()
			model := []string{"m1", "m2"}[i%2]
			b.tripped("p", model, now)
			b.record429("p", model, string(rune('a'+i%4)), time.Second, 0, now)
			if i%5 == 0 {
				b.success("p", model)
			}
		}()
	}
	wg.Wait()
}

// A runtime disable (expired) survives the pool rebuild every GUI write
// does; the admin switching the key on clears it.
func TestRuntimeDisableSurvivesRebuild(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer sk-zen-1" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(zenExpiredBody)) //nolint:errcheck
			return
		}
		sseOK(w)
	}))
	defer up.Close()
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	ck := newClientKey(t, g, st, admin.ID, "rebuild")
	chat := chatReq("zen", "mimo-test-free")
	chat.Header.Set("Authorization", "Bearer "+ck)
	g.clientOnly(g.handleChat)(httptest.NewRecorder(), chat)

	keyA := func() KeyView {
		t.Helper()
		for _, k := range g.zen.snapshot() {
			if k.Label == "key-a" {
				return k
			}
		}
		t.Fatal("no key-a")
		return KeyView{}
	}
	if k := keyA(); k.Status != statusDisabled || k.DisabledReason != disabledExpired {
		t.Fatalf("before rebuild: %s/%q", k.Status, k.DisabledReason)
	}
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if k := keyA(); k.Status != statusDisabled || k.DisabledReason != disabledExpired {
		t.Fatalf("after rebuild: %s/%q, want still disabled/expired", k.Status, k.DisabledReason)
	}

	// the admin switches it on: PATCH {"disabled": false}
	provs, keyLists, err := st.ListProvidersWithKeys()
	if err != nil {
		t.Fatal(err)
	}
	var provID, keyID int64
	for i, p := range provs {
		for _, k := range keyLists[i] {
			if k.Label == "key-a" {
				provID, keyID = p.ID, k.ID
			}
		}
	}
	cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/providers/%d/keys/%d", provID, keyID), strings.NewReader(`{"disabled":false}`))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "https://gateway.example.com")
	req.SetPathValue("id", fmt.Sprintf("%d", provID))
	req.SetPathValue("keyId", fmt.Sprintf("%d", keyID))
	rec := httptest.NewRecorder()
	g.requireSession(g.requireAdmin(g.handlePatchProviderKey))(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	if k := keyA(); k.Status != statusHealthy || k.DisabledReason != "" {
		t.Fatalf("after admin re-enable: %s/%q, want healthy", k.Status, k.DisabledReason)
	}
}

// Pool-level: newPoolPreserving carries the disable and its reason.
func TestPoolPreservesRuntimeDisable(t *testing.T) {
	p := newPool("zen", "priority", 0, []config.KeyFileEntry{mkEntry("a")})
	p.disable(p.keys[0], disabledAuth, "test")
	prev := map[string]*KeyState{}
	for _, k := range p.stateCopies() {
		prev[k.Hash] = k
	}
	p2 := newPoolPreserving("zen", "priority", 0, []config.KeyFileEntry{mkEntry("a")}, prev)
	if k := p2.snapshot()[0]; k.Status != statusDisabled || k.DisabledReason != disabledAuth {
		t.Fatalf("rebuilt key %s/%q, want disabled/auth", k.Status, k.DisabledReason)
	}
	if p2.pick(nil, "") != nil {
		t.Fatal("rebuilt pool served a disabled key")
	}
}

// zai's 429 with a stated reset is account-wide: the whole key cools, so
// other models don't burn calls on it either, and doesn't feed the breaker.
func TestZaiAccountWide429CoolsWholeKey(t *testing.T) {
	var calls keyCalls
	reset := time.Now().Add(2 * time.Hour).In(zaiCST).Format("2006-01-02 15:04:05")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.add(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"code":"1308","message":"已达到 5 小时的使用上限。您的限额将在 ` + reset + ` 重置。"}`)) //nolint:errcheck
	}))
	defer up.Close()
	g := kiloGateway(t, up, "sk-a")
	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("kilo", "glm-a"))
	if got := keyStatus(t, g, "label-sk-a"); got != statusCooling {
		t.Fatalf("key status %s, want cooling (whole key)", got)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if ra, _ := strconv.Atoi(rec.Header().Get("Retry-After")); ra < 7100 || ra > 7201 {
		t.Fatalf("Retry-After %q, want ~2h", rec.Header().Get("Retry-After"))
	}
	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("kilo", "glm-b"))
	if calls.total() != 1 || rec.Code != http.StatusTooManyRequests {
		t.Fatalf("other model: %d upstream calls, status %d; want 1 call total and 429", calls.total(), rec.Code)
	}
	if trippedState(g, "kilo", "glm-a") {
		t.Fatal("account-wide 429 fed the model breaker")
	}
}

// retryAfterSeconds rounds up and never answers 0.
func TestRetryAfterSeconds(t *testing.T) {
	cases := map[time.Duration]int{0: 1, time.Millisecond: 1, time.Second: 1, 1500 * time.Millisecond: 2, 30 * time.Second: 30}
	for d, want := range cases {
		if got := retryAfterSeconds(d); got != want {
			t.Errorf("retryAfterSeconds(%v) = %d, want %d", d, got, want)
		}
	}
}
