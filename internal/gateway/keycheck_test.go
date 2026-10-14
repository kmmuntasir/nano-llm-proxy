package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

const goodKey = "sk-good-key-123456"

// fakeUpstream reproduces one provider's measured key-check behaviour.
type fakeUpstream struct {
	publicModels bool // GET models answers 200 whatever the key (kilo, zai anthropic)
	modelFirst   bool // chat validates the model before the key → 400 for any key (zen)
	okChatStatus int  // what a valid key gets for the nonexistent model (400, 402, …)
	all          int  // non-zero: answer every request with this status
	body         string
}

func (f fakeUpstream) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" {
			key = r.Header.Get("x-api-key")
		}
		reply := func(status int, body string) {
			w.WriteHeader(status)
			w.Write([]byte(body))
		}
		if f.all != 0 {
			reply(f.all, f.body)
			return
		}
		isModels := r.Method == http.MethodGet
		switch {
		case isModels && (f.publicModels || strings.HasPrefix(key, "sk-good")):
			reply(200, `{"data":[]}`)
		case isModels:
			reply(401, `{"error":{"message":"invalid key `+key+`"}}`)
		case f.modelFirst:
			reply(400, `{"error":{"message":"model not found"}}`)
		case strings.HasPrefix(key, "sk-good"):
			reply(f.okChatStatus, `{"error":{"message":"no such model"}}`)
		default:
			reply(401, `{"error":{"message":"Incorrect API key provided: `+key+`"}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVerifyUpstreamKeyAcrossProviderShapes(t *testing.T) {
	g := newGateway(testCfg(), testRuntime(), &config.KeyFile{})
	zen := fakeUpstream{modelFirst: true}.server(t)
	kilo := fakeUpstream{publicModels: true, okChatStatus: 402}.server(t)
	zaiOpenAI := fakeUpstream{okChatStatus: 400}.server(t)
	zaiAnthropic := fakeUpstream{publicModels: true, okChatStatus: 400}.server(t)
	broken := fakeUpstream{all: 500, body: "boom"}.server(t)
	freeTier := fakeUpstream{all: 403, body: `{"type":"error","error":{"type":"FreeTierError"}}`}.server(t)

	cases := []struct {
		name           string
		base, anthBase string
		key            string
		want           string // "ok" | "rejected" | "unverifiable"
	}{
		{"zen-like good", zen.URL, "", goodKey, "ok"},
		{"zen-like bad (only /models notices)", zen.URL, "", "sk-bad", "rejected"},
		{"kilo-like good (402 = authenticated)", kilo.URL, "", goodKey, "ok"},
		{"kilo-like bad (only chat notices)", kilo.URL, "", "sk-bad", "rejected"},
		{"zai both surfaces good", zaiOpenAI.URL, zaiAnthropic.URL, goodKey, "ok"},
		{"zai anthropic-only bad (only messages notices)", "", zaiAnthropic.URL, "sk-bad", "rejected"},
		{"upstream 5xx", broken.URL, "", goodKey, "unverifiable"},
		{"unreachable", "http://127.0.0.1:1", "", goodKey, "unverifiable"},
		{"FreeTierError is not a key problem", freeTier.URL, "", goodKey, "ok"},
	}
	for _, c := range cases {
		err := g.verifyUpstreamKey(context.Background(), c.base, c.anthBase, c.key)
		var rej *keyRejectedError
		got := "ok"
		switch {
		case errors.As(err, &rej):
			got = "rejected"
		case err != nil:
			got = "unverifiable"
		}
		if got != c.want {
			t.Errorf("%s: got %s (%v), want %s", c.name, got, err, c.want)
		}
	}

	// an upstream that echoes the key never gets it into the error text
	err := g.verifyUpstreamKey(context.Background(), kilo.URL, "", "sk-secret-echoed-999")
	if err == nil || strings.Contains(err.Error(), "sk-secret-echoed-999") || !strings.Contains(err.Error(), "<key>") {
		t.Fatalf("echoed key not redacted: %v", err)
	}
}

// TestProviderKeysVerifiedBeforeSave drives the real handlers: a bad key is
// refused with 400 and nothing is saved, an unverifiable one with 503, a
// good key (whitespace trimmed) is saved; provider creation verifies every
// initial key and creates nothing when one fails.
func TestProviderKeysVerifiedBeforeSave(t *testing.T) {
	upstream := fakeUpstream{publicModels: true, okChatStatus: 402}.server(t)
	broken := fakeUpstream{all: 503}.server(t)
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	g.keyCheck = g.verifyUpstreamKey
	cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	call := func(method, path, pathID, body string, h http.HandlerFunc) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if pathID != "" {
			req.SetPathValue("id", pathID)
		}
		req.AddCookie(cookie)
		req.Header.Set("Origin", "https://gateway.example.com")
		rec := httptest.NewRecorder()
		g.requireSession(g.requireAdmin(h))(rec, req)
		return rec
	}
	create := func(name, base, key string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"name":%q,"baseUrl":%q,"keys":[{"key":%q,"label":"first"}]}`, name, base, key)
		return call("POST", "/api/providers", "", body, g.handleCreateProvider)
	}
	providerNamed := func(name string) *store.Provider {
		ps, _ := st.ListProviders()
		for _, p := range ps {
			if p.Name == name {
				return &p
			}
		}
		return nil
	}

	if rec := create("acme", upstream.URL, "sk-bad"); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), `Key \"first\" is invalid`) {
		t.Fatalf("create with bad key: %d %s", rec.Code, rec.Body.String())
	}
	if providerNamed("acme") != nil {
		t.Fatal("provider created despite an invalid key")
	}
	if rec := create("acme", upstream.URL, goodKey); rec.Code != http.StatusCreated {
		t.Fatalf("create with good key: %d %s", rec.Code, rec.Body.String())
	}
	p := providerNamed("acme")
	addKey := func(key string) *httptest.ResponseRecorder {
		return call("POST", "/api/providers/x/keys", fmt.Sprint(p.ID), fmt.Sprintf(`{"key":%q,"label":"second"}`, key), g.handleAddProviderKey)
	}
	keyCount := func() int {
		keys, _ := st.ListProviderKeys(p.ID)
		return len(keys)
	}
	if rec := addKey("sk-also-bad"); rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "sk-also-bad") {
		t.Fatalf("add bad key: %d %s", rec.Code, rec.Body.String())
	}
	if n := keyCount(); n != 1 {
		t.Fatalf("bad key was saved: %d keys", n)
	}
	if rec := addKey("  sk-good-second\n"); rec.Code != http.StatusCreated {
		t.Fatalf("add good key (with whitespace): %d %s", rec.Code, rec.Body.String())
	}
	if n := keyCount(); n != 2 {
		t.Fatalf("good key not saved: %d keys", n)
	}

	// the same key again is a 409 before any upstream probe, and not saved
	if rec := addKey("sk-good-second"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already on this provider") {
		t.Fatalf("duplicate key: %d %s", rec.Code, rec.Body.String())
	}
	if n := keyCount(); n != 2 {
		t.Fatalf("duplicate key was saved: %d keys", n)
	}
	dupBody := fmt.Sprintf(`{"name":"twins","baseUrl":%q,"keys":[{"key":%q,"label":"a"},{"key":%q,"label":"b"}]}`, upstream.URL, goodKey, goodKey)
	if rec := call("POST", "/api/providers", "", dupBody, g.handleCreateProvider); rec.Code != http.StatusBadRequest {
		t.Fatalf("create with the same key twice: %d %s", rec.Code, rec.Body.String())
	}

	// an unverifiable key is refused with 503 and not saved
	if rec := create("flaky", broken.URL, goodKey); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "Nothing was saved") {
		t.Fatalf("create against a broken upstream: %d %s", rec.Code, rec.Body.String())
	}
	if providerNamed("flaky") != nil {
		t.Fatal("provider created though its key couldn't be verified")
	}
}
