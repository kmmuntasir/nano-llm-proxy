package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

func TestBenchKeyNormalizesCatalogSpellings(t *testing.T) {
	cases := []struct{ in, want string }{
		{"glm-5.3-flash", "glm-5-3-flash"},
		{"z-ai/GLM-5.3-Flash", "z-ai/glm-5-3-flash"},
		{"deepseek/deepseek-v4.1-flash:free", "deepseek/deepseek-v4-1-flash"},
		{"deepseek/deepseek-v4.1-flash:batch", "deepseek/deepseek-v4-1-flash"},
		{"kimi-k2.5-free", "kimi-k2-5"},
		{"deepseek/deepseek-v4.1-flash-20260910", "deepseek/deepseek-v4-1-flash"},
		{"openai/gpt-6-2026-05-12", "openai/gpt-6"},
		{"claude-haiku-4-5", "claude-haiku-4-5"},
		{"anthropic/claude-haiku-4.5", "anthropic/claude-haiku-4-5"},
		{"~x-ai/grok-latest", "x-ai/grok-latest"},
		{"qwen3_coder", "qwen3-coder"},
	}
	for _, c := range cases {
		if got := benchKey(c.in); got != c.want {
			t.Errorf("benchKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func f64(v float64) *float64 { return &v }

func TestBenchIndexLookupPrefersBaseIDsAndExactVendor(t *testing.T) {
	snap := &store.BenchmarkSnapshot{Models: []store.ModelBenchmark{
		// the ":batch" twin listed first must not shadow the base model
		{ID: "deepseek/deepseek-v4.1-flash:batch", Intelligence: f64(1)},
		{ID: "deepseek/deepseek-v4.1-flash", CanonicalSlug: "deepseek/deepseek-v4.1-flash-20260910", Intelligence: f64(39.5)},
		// two vendors share a tail: an exact vendor match must win
		{ID: "meta/llama-9-8b", Intelligence: f64(10)},
		{ID: "acme/llama-9-8b", Intelligence: f64(20)},
		// reachable only through its canonical slug
		{ID: "vendor/alias-name", CanonicalSlug: "vendor/real-name-20260101", Coding: f64(5)},
	}}
	ix := newBenchIndex(snap)
	cases := []struct {
		id   string
		want string // matched source id, "" = no match
	}{
		{"deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash"},
		{"deepseek/deepseek-v4.1-flash:free", "deepseek/deepseek-v4.1-flash"},
		{"deepseek-v4.1-flash-free", "deepseek/deepseek-v4.1-flash"},
		{"acme/llama-9-8b", "acme/llama-9-8b"},
		{"meta/llama-9-8b", "meta/llama-9-8b"},
		{"real-name", "vendor/alias-name"},
		{"big-pickle", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := ix.lookup(c.id)
		gotID := ""
		if got != nil {
			gotID = got.ID
		}
		if gotID != c.want {
			t.Errorf("lookup(%q) = %q, want %q", c.id, gotID, c.want)
		}
	}
	var nilIx *benchIndex
	if nilIx.lookup("x") != nil || nilIx.status() != (benchmarksStatus{}) {
		t.Error("nil index must be inert")
	}
}

func TestParseOpenRouterBenchmarksIsLenient(t *testing.T) {
	doc := `{"data":[
		{"id":"a/full","canonical_slug":"a/full-20260101","name":"Full",
		 "benchmarks":{"artificial_analysis":{"intelligence_index":39.5,"coding_index":null,"agentic_index":28.7},
		               "design_arena":[{"arena":"models","category":"website","elo":1358,"win_rate":61.8,"rank":2},{"arena":"models","category":""}]}},
		{"id":"a/bad-arena","benchmarks":{"artificial_analysis":{"intelligence_index":12},"design_arena":{"unexpected":"shape"}}},
		{"id":"a/arena-only","canonical_slug":"a/arena-only","benchmarks":{"design_arena":[{"arena":"models","category":"svg","elo":1288,"win_rate":90,"rank":1}]}},
		{"id":"a/empty","benchmarks":{"design_arena":[],"artificial_analysis":{"intelligence_index":null}}},
		{"id":"a/none"}
	]}`
	got, err := parseOpenRouterBenchmarks(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d models, want 3 (full, bad-arena, arena-only): %+v", len(got), got)
	}
	full := got[0]
	if full.ID != "a/full" || full.Name != "Full" || full.CanonicalSlug != "a/full-20260101" ||
		*full.Intelligence != 39.5 || full.Coding != nil || *full.Agentic != 28.7 ||
		len(full.DesignArena) != 1 || full.DesignArena[0].Category != "website" || full.DesignArena[0].Rank != 2 {
		t.Fatalf("full parsed wrong: %+v", full)
	}
	if bad := got[1]; *bad.Intelligence != 12 || bad.DesignArena != nil {
		t.Fatalf("a malformed arena block should drop only the arena: %+v", bad)
	}
	if only := got[2]; only.CanonicalSlug != "" || len(only.DesignArena) != 1 {
		t.Fatalf("arena-only parsed wrong (canonical equal to id should be dropped): %+v", only)
	}

	for name, doc := range map[string]string{
		"no models":     `{"data":[]}`,
		"no benchmarks": `{"data":[{"id":"a/b"}]}`,
		"not json":      `<html>`,
	} {
		if _, err := parseOpenRouterBenchmarks(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// TestBenchmarksSyncAttachesToGUICatalogOnly drives the whole path: fetch,
// persist, the /api/models decoration (through zen's cosmetic id suffix and a
// generic provider's ":free" variant), /v1/models untouched, and a failed
// refresh that must keep the last good snapshot.
func TestBenchmarksSyncAttachesToGUICatalogOnly(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"glm-5.3-flash-free"},{"id":"big-pickle"},{"id":"z-ai/glm-5.3-flash:free"}]}`))
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(up.Close)
	var failing atomic.Bool
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"data":[{"id":"z-ai/glm-5.3-flash","name":"Z.ai: GLM 5.3 Flash",
			"benchmarks":{"artificial_analysis":{"intelligence_index":41,"coding_index":33.3,"agentic_index":null}}}]}`))
	}))
	t.Cleanup(src.Close)

	g, st := testStoreGateway(t, up.URL)
	addGenericProvider(t, st, "acme", up.URL, "sk-acme")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}
	cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")

	type entry struct {
		ID         string                `json:"id"`
		Benchmarks *store.ModelBenchmark `json:"benchmarks"`
	}
	apiModels := func() (map[string]entry, benchmarksStatus) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/models", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		g.requireSession(g.handleAllModels)(rec, req)
		if rec.Code != 200 {
			t.Fatalf("/api/models: got %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Data   []entry          `json:"data"`
			Status benchmarksStatus `json:"benchmarks_status"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := map[string]entry{}
		for _, e := range body.Data {
			out[strings.SplitN(e.ID, "/", 2)[0]+"|"+benchKey(stripModelSuffix(strings.SplitN(e.ID, "/", 2)[1]))] = e
		}
		return out, body.Status
	}

	// never synced: no decoration, zero status
	if models, status := apiModels(); status != (benchmarksStatus{}) || models["zen|glm-5-3-flash"].Benchmarks != nil {
		t.Fatalf("unsynced gateway should carry no benchmarks: %+v %+v", status, models)
	}

	status := g.syncBenchmarks(src.URL)
	if status.Count != 1 || status.Error != "" || status.FetchedAt == 0 {
		t.Fatalf("sync status = %+v", status)
	}
	models, _ := apiModels()
	for _, key := range []string{"zen|glm-5-3-flash", "acme|z-ai/glm-5-3-flash"} {
		b := models[key].Benchmarks
		if b == nil || b.ID != "z-ai/glm-5.3-flash" || *b.Intelligence != 41 || *b.Coding != 33.3 || b.Agentic != nil {
			t.Fatalf("%s: benchmarks = %+v (entries %+v)", key, b, models)
		}
	}
	if models["zen|big-pickle"].Benchmarks != nil {
		t.Fatal("an unknown model must not be decorated")
	}

	// /v1/models is the client contract — it never carries benchmarks
	key := newClientKey(t, g, st, 1, "bench")
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleModels)(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "benchmarks") {
		t.Fatalf("/v1/models leaked benchmarks (%d): %s", rec.Code, rec.Body.String())
	}

	// persisted: a fresh load restores the same index
	snap, err := st.LoadModelBenchmarks()
	if err != nil || len(snap.Models) != 1 || snap.FetchedAt != status.FetchedAt {
		t.Fatalf("persisted snapshot = %+v, %v", snap, err)
	}

	// a failed refresh keeps the last good data and reports the error
	failing.Store(true)
	failed := g.syncBenchmarks(src.URL)
	if failed.Count != 1 || failed.FetchedAt != status.FetchedAt || !strings.Contains(failed.Error, "503") {
		t.Fatalf("failed sync status = %+v", failed)
	}
	if models, bst := apiModels(); models["zen|glm-5-3-flash"].Benchmarks == nil || bst.Error == "" {
		t.Fatalf("failed sync dropped data or hid the error: %+v", bst)
	}
	snap, _ = st.LoadModelBenchmarks()
	if len(snap.Models) != 1 || snap.Error == "" || snap.AttemptAt < snap.FetchedAt {
		t.Fatalf("persisted snapshot after failure = %+v", snap)
	}
	g.loadBenchmarks()
	if g.bench.Load().lookup("glm-5.3-flash") == nil {
		t.Fatal("reloaded index lost the model")
	}
}
