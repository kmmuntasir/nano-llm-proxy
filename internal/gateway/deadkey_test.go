package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
)

// kiloGateway is a store-less gateway whose kilo pool holds the given keys
// and points at up (rotation "priority": keys are tried in order).
func kiloGateway(t *testing.T, up *httptest.Server, keys ...string) *gateway {
	t.Helper()
	cfg := &config.Config{
		Zen:  config.ZenProviderConfig{BaseURL: up.URL},
		Kilo: config.KiloProviderConfig{BaseURL: up.URL},
	}
	kf := &config.KeyFile{}
	for _, k := range keys {
		kf.Kilo = append(kf.Kilo, config.KeyFileEntry{Label: "label-" + k, Key: k})
	}
	return newGateway(cfg, testRuntime(), kf)
}

func keyStatus(t *testing.T, g *gateway, label string) string {
	t.Helper()
	for _, k := range g.kilo.snapshot() {
		if k.Label == label {
			return k.Status
		}
	}
	t.Fatalf("no key %s", label)
	return ""
}

// A 401 disables a key only when the upstream blames the key — in any of the
// wordings providers use — while zen's 401-wrapped upstream flakes just cool.
func TestAuthRejectionWordingsDisableKey(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"zen AuthError", `{"error":{"type":"AuthError","message":"Invalid API key."}}`, statusDisabled},
		{"zen invalid credential", `{"error":{"type":"server_error","message":"Invalid credential"}}`, statusDisabled},
		{"kilo invalid token", `{"error":{"code":"INVALID_TOKEN","message":"Your authentication token is invalid. Please sign in again."}}`, statusDisabled},
		{"zen-wrapped flake", `{"error":{"type":"server_error","message":"Upstream request failed"}}`, statusCooling},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(c.body)) //nolint:errcheck
			}))
			defer up.Close()
			g := kiloGateway(t, up, "sk-a")
			g.handleChat(httptest.NewRecorder(), chatReq("kilo", "m"))
			if got := keyStatus(t, g, "label-sk-a"); got != c.want {
				t.Fatalf("key status = %s, want %s", got, c.want)
			}
		})
	}
}

// One dead key must not empty the catalog (or fail the models.dev sync): the
// fetch moves on to the next key and the dead one is disabled on the way.
func TestCatalogFailsOverPastDeadKey(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") == "Bearer sk-dead" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"type":"server_error","message":"Invalid credential"}}`)) //nolint:errcheck
			return
		}
		w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`)) //nolint:errcheck
	}))
	defer up.Close()
	g := kiloGateway(t, up, "sk-dead", "sk-good")
	ref, _ := g.provider("kilo")
	models, err := g.fetchModelsOpenAI(ref)
	if err != nil || len(models) != 2 {
		t.Fatalf("catalog = %v, %v", models, err)
	}
	if hits.Load() != 2 || keyStatus(t, g, "label-sk-dead") != statusDisabled || keyStatus(t, g, "label-sk-good") != statusHealthy {
		t.Fatalf("hits=%d dead=%s good=%s", hits.Load(), keyStatus(t, g, "label-sk-dead"), keyStatus(t, g, "label-sk-good"))
	}
}

func TestCatalogFailoverOnlyForKeyErrors(t *testing.T) {
	for _, c := range []struct {
		status   int
		wantHits int32
	}{
		{http.StatusTooManyRequests, 3}, // key-specific: try the next keys
		{http.StatusForbidden, 3},
		{http.StatusInternalServerError, 1}, // the endpoint's problem: same for every key
		{http.StatusNotFound, 1},
	} {
		var hits atomic.Int32
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			http.Error(w, "nope", c.status)
		}))
		g := kiloGateway(t, up, "sk-a", "sk-b", "sk-c", "sk-d")
		ref, _ := g.provider("kilo")
		_, err := g.fetchModelsOpenAI(ref)
		up.Close()
		if err == nil || !strings.Contains(err.Error(), "/models HTTP") {
			t.Fatalf("%d: err = %v", c.status, err)
		}
		// maxKeysPerRequest (3) bounds the failover
		if hits.Load() != c.wantHits {
			t.Fatalf("%d: %d upstream calls, want %d", c.status, hits.Load(), c.wantHits)
		}
		// a catalog failure never cools or disables a key
		for _, k := range g.kilo.snapshot() {
			if k.Status != statusHealthy {
				t.Fatalf("%d: key %s is %s", c.status, k.Label, k.Status)
			}
		}
	}
}
