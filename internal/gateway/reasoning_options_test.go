package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

// oh-my-pi (and other agents that follow the models.dev shape) read two facts
// from /v1/models that a bare "reasoning": true cannot express: the context
// window under the context_length spelling, and the reasoning effort ladder
// itself. These tests pin all three: the models.dev parse, the fallback for
// models the catalog doesn't know, and the rendered entry.

func TestCleanReasoningOptions(t *testing.T) {
	dev := func(reasoning bool, opts ...settings.ReasoningOption) modelsDevModel {
		var m modelsDevModel
		m.Reasoning = reasoning
		m.ReasoningOptions = opts
		return m
	}
	min, max := int64(1024), int64(32768)

	cases := []struct {
		name string
		in   modelsDevModel
		want []settings.ReasoningOption
	}{
		{
			name: "effort ladder passes through",
			in:   dev(true, settings.ReasoningOption{Type: "effort", Values: []string{"low", "medium", "high"}}),
			want: []settings.ReasoningOption{{Type: "effort", Values: []string{"low", "medium", "high"}}},
		},
		{
			name: "toggle keeps no values",
			in:   dev(true, settings.ReasoningOption{Type: "toggle"}),
			want: []settings.ReasoningOption{{Type: "toggle"}},
		},
		{
			name: "budget_tokens keeps its bounds",
			in:   dev(true, settings.ReasoningOption{Type: "budget_tokens", Min: &min, Max: &max}),
			want: []settings.ReasoningOption{{Type: "budget_tokens", Min: &min, Max: &max}},
		},
		{
			// models.dev has shipped a couple of these; a null level decodes to
			// an empty string and would advertise a blank reasoning level.
			name: "null holes in the ladder are dropped",
			in:   dev(true, settings.ReasoningOption{Type: "effort", Values: []string{"", "low", "  ", "high"}}),
			want: []settings.ReasoningOption{{Type: "effort", Values: []string{"low", "high"}}},
		},
		{
			name: "typeless option is dropped",
			in:   dev(true, settings.ReasoningOption{Type: "", Values: []string{"low"}}),
			want: nil,
		},
		{
			name: "effort with no levels is dropped",
			in:   dev(true, settings.ReasoningOption{Type: "effort"}),
			want: nil,
		},
		{
			// the two facts would contradict each other in the catalog
			name: "non-reasoning model keeps nothing",
			in:   dev(false, settings.ReasoningOption{Type: "effort", Values: []string{"low", "high"}}),
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanReasoningOptions(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				a, _ := json.Marshal(got[i])
				b, _ := json.Marshal(tc.want[i])
				if string(a) != string(b) {
					t.Errorf("option %d = %s, want %s", i, a, b)
				}
			}
		})
	}
}

// The sync is the only source of these ladders; a regression here silently
// degrades every zen/zai entry back to reasoning:true with no levels.
func TestSyncModelMetaStoresReasoningOptions(t *testing.T) {
	zen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(`{"data":[{"id":"ling-3.1-flash-free"},{"id":"plain-chat"},{"id":"glm-5.3"}]}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer zen.Close()

	dev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"huge-provider-we-skip": {"models": {"x": {"limit": {"context": 1, "output": 1}}}},
			"opencode": {"models": {
				"ling-3.1-flash-free": {
					"reasoning": true,
					"reasoning_options": [{"type": "toggle"}, {"type": "effort", "values": ["low", "medium", "high"]}],
					"modalities": {"input": ["text"]},
					"limit": {"context": 262144, "output": 32768},
					"description": "Efficient model for low-latency assistance"
				},
				"plain-chat": {
					"reasoning": false,
					"reasoning_options": [{"type": "effort", "values": ["low", "high"]}],
					"modalities": {"input": ["text"]},
					"limit": {"context": 128000, "output": 8192}
				}
			}},
			"zai-coding-plan": {"models": {
				"glm-5.3": {
					"reasoning": true,
					"reasoning_options": [{"type": "effort", "values": ["none", "low", "medium", "high", "max"]},
						{"type": "budget_tokens", "min": 1024, "max": 131072}],
					"modalities": {"input": ["text"]},
					"limit": {"context": 1000000, "output": 131072}
				}
			}}
		}`))
	}))
	defer dev.Close()

	g, _ := testStoreGateway(t, zen.URL)
	if st := g.syncModelMeta(dev.URL); !st.OK {
		t.Fatalf("sync failed: %+v", st)
	}

	rs := g.rs()
	effort := rs.Zen.ModelMeta["ling-3.1-flash-free"].ReasoningOptions
	if len(effort) != 2 || effort[0].Type != "toggle" || effort[1].Type != "effort" {
		t.Fatalf("zen ladder not stored: %+v", effort)
	}
	if strings.Join(effort[1].Values, ",") != "low,medium,high" {
		t.Errorf("zen effort levels = %v", effort[1].Values)
	}
	if got := rs.Zen.ModelMeta["plain-chat"].ReasoningOptions; len(got) != 0 {
		t.Errorf("reasoning:false must not keep a ladder: %+v", got)
	}
	glm := rs.Zai.ModelMeta["glm-5.3"].ReasoningOptions
	if len(glm) != 2 || glm[1].Type != "budget_tokens" ||
		glm[1].Min == nil || *glm[1].Min != 1024 || glm[1].Max == nil || *glm[1].Max != 131072 {
		t.Fatalf("zai options not stored: %+v", glm)
	}
}

// The served entry is what oh-my-pi actually reads: context_length mirroring
// context_window, and the catalog ladder for known models / the default one
// for ids models.dev has never heard of.
func TestZenCatalogAdvertisesContextLengthAndReasoningOptions(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(`{"data":[{"id":"ling-3.1-flash-free"},{"id":"brand-new"}]}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer up.Close()

	g, _ := testStoreGateway(t, up.URL)
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(
		`{"zen":{"modelMeta":{"ling-3.1-flash-free":{"contextWindow":262144,"maxOutputTokens":32768,
		 "reasoning":true,"reasoningOptions":[{"type":"effort","values":["low","medium","high"]}],
		 "inputModalities":["text"],"description":"efficient"}}}}`))
	req.AddCookie(adminCookie)
	req.Header.Set("Origin", "https://gateway.example.com")
	req.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.requireSuperadmin(g.handlePutSettings))(rec, req)
	if rec.Code != 200 {
		t.Fatalf("PUT settings: got %d: %s", rec.Code, rec.Body.String())
	}

	ref, ok := g.provider("zen")
	if !ok {
		t.Fatal("no zen pool")
	}
	models, err := g.fetchUpstreamModels(ref)
	if err != nil {
		t.Fatalf("fetchUpstreamModels: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, m := range models {
		e := m.(map[string]any)
		byID[e["id"].(string)] = e
	}

	known, ok := byID["zen/ling-3.1-flash-free-262K-txt"]
	if !ok {
		t.Fatalf("enriched zen entry missing: %v", byID)
	}
	if known["context_window"] != int64(262144) || known["context_length"] != int64(262144) {
		t.Errorf("context_length must mirror context_window: %v", known)
	}
	opts, _ := known["reasoning_options"].([]settings.ReasoningOption)
	if len(opts) != 1 || strings.Join(opts[0].Values, ",") != "low,medium,high" {
		t.Errorf("catalog ladder not advertised: %v", known["reasoning_options"])
	}

	// unknown id: still a reasoning model, so it still needs levels to offer
	unknown, ok := byID["zen/brand-new-262K"]
	if !ok {
		t.Fatalf("fallback zen entry missing: %v", byID)
	}
	opts, _ = unknown["reasoning_options"].([]settings.ReasoningOption)
	if len(opts) != 1 || opts[0].Type != "effort" || strings.Join(opts[0].Values, ",") != "low,medium,high" {
		t.Errorf("fallback ladder wrong: %v", unknown["reasoning_options"])
	}

	// the served JSON must use the catalog's own field names
	body, _ := json.Marshal(known)
	for _, want := range []string{`"reasoning_options"`, `"type":"effort"`, `"values":["low","medium","high"]`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("served entry missing %s: %s", want, body)
		}
	}
}

func TestZaiCatalogAdvertisesReasoningOptions(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(`{"data":[{"id":"glm-5.3"},{"id":"brand-new-model"}]}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addPresetProvider(t, st, "zai", "glm", up.URL, "zk1")
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(
		`{"zai":{"modelMeta":{"glm-5.3":{"contextWindow":1000000,"maxOutputTokens":131072,
		 "reasoning":true,"reasoningOptions":[{"type":"effort","values":["none","low","medium","high","max"]}]}}}}`))
	req.AddCookie(adminCookie)
	req.Header.Set("Origin", "https://gateway.example.com")
	req.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.requireSuperadmin(g.handlePutSettings))(rec, req)
	if rec.Code != 200 {
		t.Fatalf("PUT settings: got %d: %s", rec.Code, rec.Body.String())
	}
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}
	ref, ok := g.provider("glm")
	if !ok {
		t.Fatal("zai preset provider not in registry")
	}
	models, err := g.fetchUpstreamModels(ref)
	if err != nil {
		t.Fatalf("fetchUpstreamModels: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, m := range models {
		e := m.(map[string]any)
		byID[e["id"].(string)] = e
	}
	known, ok := byID["glm/glm-5.3-1M"]
	if !ok {
		t.Fatalf("zai entry missing: %v", byID)
	}
	opts, _ := known["reasoning_options"].([]settings.ReasoningOption)
	if len(opts) != 1 || strings.Join(opts[0].Values, ",") != "none,low,medium,high,max" {
		t.Errorf("zai ladder not advertised: %v", known["reasoning_options"])
	}
	unknown, ok := byID["glm/brand-new-model-1M"]
	if !ok {
		t.Fatalf("zai fallback entry missing: %v", byID)
	}
	if _, has := unknown["reasoning_options"]; !has {
		t.Errorf("unknown zai model should still offer a ladder: %v", unknown)
	}
}

// Generic passthrough providers keep upstream's own numbers; the mirror must
// still hold so an agent reading context_length sees the same window.
func TestGenericCatalogMirrorsContextLength(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(`{"data":[{"id":"m1","context_length":4096}]}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addGenericProvider(t, st, "together", up.URL, "tk1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}
	ref, ok := g.provider("together")
	if !ok {
		t.Fatal("generic provider not in registry")
	}
	models, err := g.fetchUpstreamModels(ref)
	if err != nil {
		t.Fatalf("fetchUpstreamModels: %v", err)
	}
	e := models[0].(map[string]any)
	if e["context_window"] != int64(4096) || e["context_length"] != int64(4096) {
		t.Errorf("generic context mirror wrong: %v", e)
	}
}
