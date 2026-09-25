package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// --- helpers ---

// addPresetProvider inserts a store.Provider carrying a preset id and (with
// rebuildPools) refreshes the hot path — the same thing a preset create via
// the API does.
func addPresetProvider(t *testing.T, st *store.Store, preset, name, baseURL string, keys ...string) *store.Provider {
	t.Helper()
	p := &store.Provider{Name: name, Type: "openai", BaseURL: baseURL, Enabled: true, Preset: preset, SortOrder: 100}
	pks := make([]store.ProviderKey, 0, len(keys))
	for _, k := range keys {
		pks = append(pks, store.ProviderKey{Key: k})
	}
	if err := st.CreateProvider(p, pks); err != nil {
		t.Fatalf("CreateProvider(%s): %v", name, err)
	}
	return p
}

// --- registry invariants ---

func TestPresetRegistryInvariants(t *testing.T) {
	var prevLabel string
	for i, p := range presetRegistry {
		if !providerNameRe.MatchString(p.ID) {
			t.Errorf("preset %q violates providerNameRe", p.ID)
		}
		if p.BaseURL == "" && p.AnthropicBaseURL == "" {
			t.Errorf("preset %q has no endpoint root", p.ID)
		}
		if i > 0 && strings.ToLower(p.Label) < strings.ToLower(prevLabel) {
			t.Errorf("registry not sorted by label at %q (after %q)", p.Label, prevLabel)
		}
		prevLabel = p.Label
	}
	// every preset is lookable, unknown ids are not
	if _, ok := lookupPreset("zai"); !ok {
		t.Error("lookupPreset(zai) missing")
	}
	if p, ok := lookupPreset("nope"); ok || p.ID != "" {
		t.Error("lookupPreset(nope) should miss")
	}
	// kilo and zai carry the suffixed-catalog treatment; plain presets do not
	if !presetSuffixed("kilo") || !presetSuffixed("zai") || presetSuffixed("") {
		t.Error("suffixed flags wrong: want kilo+zai true, others false")
	}
	if presetCatalogHook("kilo") == nil {
		t.Error("kilo must keep its catalog hook")
	}
	if presetCatalogHook("zai") == nil {
		t.Error("zai must keep its catalog hook")
	}
}

// --- preset catalog enrichment (kilo hook) ---

func TestKiloPresetCatalogEnrichment(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[
			{"id":"qwen3.8-27b","context_length":262144,
			 "top_provider":{"max_completion_tokens":16384},
			 "architecture":{"input_modalities":["text","image"]},
			 "output_modalities":["text"],
			 "supported_parameters":["tools","temperature"],
			 "description":"big model","isFree":true},
			{"id":"paid-model","context_length":131072,"isFree":false}
		]}`))
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addPresetProvider(t, st, "kilo", "mykilo", up.URL, "kk1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}
	ref, ok := g.provider("mykilo")
	if !ok {
		t.Fatal("kilo preset provider not in registry")
	}

	models, err := g.fetchUpstreamModels(ref)
	if err != nil {
		t.Fatalf("fetchUpstreamModels: %v", err)
	}
	var byID = map[string]map[string]any{}
	for _, m := range models {
		e := m.(map[string]any)
		byID[e["id"].(string)] = e
	}
	free, ok := byID["mykilo/qwen3.8-27b-262K-txt-img"]
	if !ok {
		t.Fatalf("suffixed kilo entry missing: %v", byID)
	}
	if free["context_window"] != int64(262144) || free["max_output_tokens"] != int64(16384) {
		t.Errorf("context/output not enriched: %v", free)
	}
	if free["description"] != "big model" || free["free"] != true {
		t.Errorf("description/free not mapped: %v", free)
	}
	if _, ok := free["supported_parameters"]; !ok {
		t.Errorf("supported_parameters not passed through: %v", free)
	}
	if _, ok := byID["mykilo/paid-model-131K"]; !ok {
		t.Errorf("non-free entry dropped with freeOnly off: %v", byID)
	}

	// freeOnly=true (Settings → Kilo) filters through the same hook
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(`{"kilo":{"freeOnly":true}}`))
	req.AddCookie(adminCookie)
	req.Header.Set("Origin", "https://gateway.example.com")
	req.RemoteAddr = "10.9.9.9:5555"
	g.requireSession(g.requireSuperadmin(g.handlePutSettings))(rec, req)
	if rec.Code != 200 {
		t.Fatalf("PUT settings: got %d: %s", rec.Code, rec.Body.String())
	}

	models, err = g.fetchUpstreamModels(ref)
	if err != nil {
		t.Fatalf("refetch: %v", err)
	}
	for _, m := range models {
		id, _ := m.(map[string]any)["id"].(string)
		if strings.Contains(id, "paid-model") {
			t.Fatalf("freeOnly left a paid model in the catalog: %v", models)
		}
	}
	if len(models) != 1 {
		t.Fatalf("got %d models with freeOnly on, want 1", len(models))
	}
}

// --- zai catalog enrichment (models.dev-backed meta + unknown-id fallback) ---

func TestZaiPresetCatalogEnrichment(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"glm-5.3"},{"id":"brand-new-model"}]}`))
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addPresetProvider(t, st, "zai", "glm", up.URL, "zk1")
	// seed the meta through the PUT handler (it refreshes the in-memory
	// snapshot the hot path reads, like a real admin edit would)
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/settings",
		strings.NewReader(`{"zai":{"modelMeta":{"glm-5.3":{"contextWindow":1000000,"maxOutputTokens":131072,
			"reasoning":true,"inputModalities":["text","image","video","pdf"],"description":"flagship"}}}}`))
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

	known, ok := byID["glm/glm-5.3-1M-txt-img-vid-pdf"]
	if !ok {
		t.Fatalf("suffixed known entry missing: %v", byID)
	}
	if known["context_window"] != int64(1_000_000) || known["max_output_tokens"] != int64(131_072) {
		t.Errorf("known entry limits wrong: %v", known)
	}
	if known["reasoning"] != true || known["description"] != "flagship" || known["responses_api"] != false {
		t.Errorf("known entry flags wrong: %v", known)
	}
	if m, ok := known["input_modalities"].([]string); !ok || len(m) != 4 {
		t.Errorf("known entry modalities wrong: %v", known["input_modalities"])
	}

	// a model models.dev doesn't know yet falls back to 1M context — agents
	// would otherwise default to 128K and truncate long sessions
	unknown, ok := byID["glm/brand-new-model-1M"]
	if !ok {
		t.Fatalf("unknown entry missing: %v", byID)
	}
	if unknown["context_window"] != int64(1_048_576) || unknown["max_output_tokens"] != int64(131_072) {
		t.Errorf("unknown entry fallback limits wrong: %v", unknown)
	}
	if unknown["reasoning"] != true {
		t.Errorf("unknown entry should read as a reasoning model: %v", unknown)
	}
	if _, has := unknown["input_modalities"]; has {
		t.Errorf("unknown entry must not invent modalities: %v", unknown)
	}
	if _, has := unknown["description"]; has {
		t.Errorf("unknown entry must not invent a description: %v", unknown)
	}
}

// --- suffix stripping keyed on preset ---

func TestKiloPresetSuffixStrippedOnRouting(t *testing.T) {
	var mu sync.Mutex
	var kiloModels, zaiModels, genericModels []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/kilo"):
			kiloModels = append(kiloModels, body.Model)
		case strings.HasPrefix(r.URL.Path, "/zai"):
			zaiModels = append(zaiModels, body.Model)
		default:
			genericModels = append(genericModels, body.Model)
		}
		mu.Unlock()
		sseOK(w)
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	addPresetProvider(t, st, "kilo", "mykilo", up.URL+"/kilo", "kk1")
	addPresetProvider(t, st, "zai", "glm", up.URL+"/zai", "zk1")
	addGenericProvider(t, st, "generic", up.URL+"/generic", "gk1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}

	rec := httptest.NewRecorder()
	g.handleChat(rec, chatReq("mykilo", "m-262K-txt"))
	if rec.Code != 200 {
		t.Fatalf("kilo chat: got %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("glm", "glm-5.3-1M"))
	if rec.Code != 200 {
		t.Fatalf("zai chat: got %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	g.handleChat(rec, chatReq("generic", "m-262K-txt"))
	if rec.Code != 200 {
		t.Fatalf("generic chat: got %d: %s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	// kilo preset: suffixed catalog ids are stripped before the upstream call
	if len(kiloModels) != 1 || kiloModels[0] != "m" {
		t.Fatalf("kilo upstream models = %v, want [m]", kiloModels)
	}
	// zai preset: same treatment — the advertised "-1M" never reaches upstream
	if len(zaiModels) != 1 || zaiModels[0] != "glm-5.3" {
		t.Fatalf("zai upstream models = %v, want [glm-5.3]", zaiModels)
	}
	// generic provider: the suffix could be a real id — only [1m] is stripped
	if len(genericModels) != 1 || genericModels[0] != "m-262K-txt" {
		t.Fatalf("generic upstream models = %v, want [m-262K-txt]", genericModels)
	}
}

// --- presets endpoint ---

func TestProviderPresetsEndpoint(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1") // upstream never reached
	insertPlainUser(t, st, "pi@example.com", "pi-password-123", "user")
	handler := g.requireSession(g.requireSuperadmin(g.handleListPresets))

	// no cookie -> 401
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest("GET", "/api/providers/presets", nil))
	if rec.Code != 401 {
		t.Fatalf("anonymous: got %d, want 401", rec.Code)
	}

	// plain user -> 403
	userCookie := loginAs(t, g, "pi@example.com", "pi-password-123")
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/providers/presets", nil)
	req.AddCookie(userCookie)
	handler(rec, req)
	if rec.Code != 403 {
		t.Fatalf("plain user: got %d, want 403", rec.Code)
	}

	// superadmin -> the curated registry, label order preserved
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/providers/presets", nil)
	req.AddCookie(adminCookie)
	handler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("admin: got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"id":"kilo"`, "Z.ai (GLM Coding Plan)", "https://api.z.ai/api/anthropic", "https://openrouter.ai/api"} {
		if !strings.Contains(body, want) {
			t.Fatalf("presets body missing %q: %s", want, body)
		}
	}
	if got := len(jsonExtractIDs(t, body)); got != len(presetRegistry) {
		t.Fatalf("presets endpoint listed %d, registry has %d", got, len(presetRegistry))
	}
	// dropdown order == registry order
	last := -1
	for _, label := range []string{"Anthropic", "DeepSeek", "Kilo", "OpenAI", "xAI (Grok)", "Z.ai (GLM Coding Plan)"} {
		idx := strings.Index(body, label)
		if idx < 0 || idx < last {
			t.Fatalf("preset %q out of order (idx %d, prev %d)", label, idx, last)
		}
		last = idx
	}
}

func jsonExtractIDs(t *testing.T, body string) []string {
	t.Helper()
	var parsed struct {
		Presets []struct {
			ID string `json:"id"`
		} `json:"presets"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("parse presets: %v", err)
	}
	ids := make([]string, 0, len(parsed.Presets))
	for _, p := range parsed.Presets {
		ids = append(ids, p.ID)
	}
	return ids
}

// --- create-from-preset semantics ---

func TestPresetProviderCreate(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/providers", strings.NewReader(body))
		req.AddCookie(adminCookie)
		rec := httptest.NewRecorder()
		g.requireSession(g.requireSuperadmin(g.handleCreateProvider))(rec, req)
		return rec
	}

	// unknown preset -> 400
	rec := post(`{"preset":"qwen","keys":[{"key":"k1","label":"test"}]}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Unknown provider preset") {
		t.Fatalf("unknown preset: got %d: %s", rec.Code, rec.Body.String())
	}

	// zai from preset: name defaults to the id, both roots come from the registry
	rec = post(`{"preset":"zai","keys":[{"key":"k1","label":"test"}]}`)
	if rec.Code != 201 {
		t.Fatalf("zai create: got %d: %s", rec.Code, rec.Body.String())
	}
	p, err := st.Provider(mustID(t, rec))
	if err != nil || p == nil {
		t.Fatalf("zai row: %v", err)
	}
	if p.Name != "zai" || p.Preset != "zai" {
		t.Fatalf("zai name/preset = %q/%q", p.Name, p.Preset)
	}
	if p.BaseURL != "https://api.z.ai/api/coding/paas/v4" || p.AnthropicBaseURL != "https://api.z.ai/api/anthropic" {
		t.Fatalf("zai roots not defaulted from the registry: %+v", p)
	}

	// multiple providers from one preset are allowed (different names)
	for _, name := range []string{"mykilo", "mykilo2"} {
		rec = post(`{"preset":"kilo","name":"` + name + `","keys":[{"key":"kk","label":"test"}]}`)
		if rec.Code != 201 {
			t.Fatalf("kilo preset %q: got %d: %s", name, rec.Code, rec.Body.String())
		}
	}

	// zen is still reserved; kilo is no longer
	rec = post(`{"name":"zen","baseUrl":"https://x.example/v1","keys":[{"key":"k2","label":"test"}]}`)
	if rec.Code != 409 {
		t.Fatalf("zen name: got %d, want 409", rec.Code)
	}
	rec = post(`{"name":"kilo","baseUrl":"https://kilo.example/v1","keys":[{"key":"k3","label":"test"}]}`)
	if rec.Code != 201 {
		t.Fatalf("kilo-as-custom name: got %d: %s", rec.Code, rec.Body.String())
	}

	// preset create whose defaulted name collides -> the usual 409
	rec = post(`{"preset":"zai","keys":[{"key":"k4","label":"test"}]}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("zai name collision: got %d: %s", rec.Code, rec.Body.String())
	}

	// preset is immutable: the PATCH struct has no preset field
	rec = httptest.NewRecorder()
	patchReq := httptest.NewRequest("PATCH", "/api/providers/2", strings.NewReader(`{"preset":"openai"}`))
	patchReq.AddCookie(adminCookie)
	patchReq.SetPathValue("id", "2")
	g.requireSession(g.requireSuperadmin(g.handlePatchProvider))(rec, patchReq)
	if rec.Code != 200 {
		t.Fatalf("patch: got %d: %s", rec.Code, rec.Body.String())
	}
	p, _ = st.Provider(2)
	if p.Preset != "zai" {
		t.Fatalf("preset mutated by PATCH: %+v", p)
	}

	// the list carries the preset for the GUI's "already added" state
	rec = httptest.NewRecorder()
	listReq := httptest.NewRequest("GET", "/api/providers", nil)
	listReq.AddCookie(adminCookie)
	g.requireSession(g.requireSuperadmin(g.handleListProviders))(rec, listReq)
	var listed struct {
		Providers []struct {
			Name   string `json:"name"`
			Preset string `json:"preset"`
		} `json:"providers"`
	}
	json.Unmarshal(rec.Body.Bytes(), &listed)
	var zaiFound bool
	for _, lp := range listed.Providers {
		if lp.Name == "zai" && lp.Preset == "zai" {
			zaiFound = true
		}
	}
	if !zaiFound {
		t.Fatalf("list missing preset field: %s", rec.Body.String())
	}
	if !slices.ContainsFunc(listed.Providers, func(lp struct {
		Name   string `json:"name"`
		Preset string `json:"preset"`
	}) bool {
		return lp.Name == "zen" && lp.Preset == ""
	}) {
		t.Fatalf("zen should list with an empty preset: %s", rec.Body.String())
	}
}

// Upstream key labels are mandatory on every write path: provider create,
// add-key, and key PATCH (a rename may not blank the label).
func TestProviderKeyLabelRequired(t *testing.T) {
	g, _ := testStoreGateway(t, "http://127.0.0.1:1")
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")

	create := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/providers", strings.NewReader(body))
		req.AddCookie(adminCookie)
		rec := httptest.NewRecorder()
		g.requireSession(g.requireSuperadmin(g.handleCreateProvider))(rec, req)
		return rec
	}

	// create: key without a label -> 400
	rec := create(`{"name":"glm","baseUrl":"https://x.example/v1","keys":[{"key":"k1"}]}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "label is required") {
		t.Fatalf("create without label: got %d: %s", rec.Code, rec.Body.String())
	}
	// create: blank key entry -> 400
	rec = create(`{"name":"glm","baseUrl":"https://x.example/v1","keys":[{"key":"","label":"l"}]}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "API key is required") {
		t.Fatalf("create with a blank key: got %d: %s", rec.Code, rec.Body.String())
	}

	// valid create, then the later add/patch paths
	rec = create(`{"name":"glm","baseUrl":"https://x.example/v1","keys":[{"key":"k1","label":"primary"}]}`)
	if rec.Code != 201 {
		t.Fatalf("create: got %d: %s", rec.Code, rec.Body.String())
	}
	provID := mustID(t, rec)

	addKey := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/providers/%d/keys", provID), strings.NewReader(body))
		req.AddCookie(adminCookie)
		req.SetPathValue("id", fmt.Sprintf("%d", provID))
		rec := httptest.NewRecorder()
		g.requireSession(g.requireSuperadmin(g.handleAddProviderKey))(rec, req)
		return rec
	}
	rec = addKey(`{"key":"k2"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "label is required") {
		t.Fatalf("add-key without label: got %d: %s", rec.Code, rec.Body.String())
	}
	rec = addKey(`{"key":"k2","label":"backup"}`)
	if rec.Code != 201 {
		t.Fatalf("add-key: got %d: %s", rec.Code, rec.Body.String())
	}
	var added struct {
		Entry struct {
			ID int64 `json:"id"`
		} `json:"entry"`
	}
	json.Unmarshal(rec.Body.Bytes(), &added)

	// patch: renaming is fine, blanking the label is not
	patchKey := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/providers/%d/keys/%d", provID, added.Entry.ID), strings.NewReader(body))
		req.AddCookie(adminCookie)
		req.SetPathValue("id", fmt.Sprintf("%d", provID))
		req.SetPathValue("keyId", fmt.Sprintf("%d", added.Entry.ID))
		rec := httptest.NewRecorder()
		g.requireSession(g.requireSuperadmin(g.handlePatchProviderKey))(rec, req)
		return rec
	}
	rec = patchKey(`{"label":"   "}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "label is required") {
		t.Fatalf("patch to a blank label: got %d: %s", rec.Code, rec.Body.String())
	}
	rec = patchKey(`{"label":"renamed"}`)
	if rec.Code != 200 {
		t.Fatalf("patch rename: got %d: %s", rec.Code, rec.Body.String())
	}
}

func mustID(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == 0 {
		t.Fatalf("no id in create response (%v): %s", err, rec.Body.String())
	}
	return out.ID
}

// --- Z.ai per-key usage endpoint + 429 reset parsing ---

// zaiUsageMockURL points the zai registry entry's usage URL at a test server
// for the duration of the test (no real Z.ai traffic from tests).
func zaiUsageMockURL(t *testing.T, url string) {
	t.Helper()
	for i := range presetRegistry {
		if presetRegistry[i].ID == "zai" {
			old := presetRegistry[i].usageURL
			presetRegistry[i].usageURL = url
			t.Cleanup(func() { presetRegistry[i].usageURL = old })
			return
		}
	}
	t.Fatal("no zai entry in the preset registry")
}

func TestZaiRateLimitReset(t *testing.T) {
	now := time.Now()
	future := now.Add(2 * time.Hour).In(zaiCST).Format("2006-01-02 15:04:05")
	body := []byte(`{"code":"1308","message":"已达到 5 小时的使用上限。您的限额将在 ` + future + ` 重置。"}`)
	got, ok := zaiRateLimitReset(body, now)
	if !ok {
		t.Fatalf("expected a reset instant from %s", body)
	}
	if d := got.Sub(now); d < 119*time.Minute || d > 121*time.Minute {
		t.Fatalf("reset %v is %v away, want ~2h", got, d)
	}
	// implausible instants fall back to the default cooldown
	past := []byte(`{"code":"1308","message":"限额将在 2020-01-01 00:00:00 重置。"}`)
	if _, ok := zaiRateLimitReset(past, now); ok {
		t.Fatal("a past reset should not parse")
	}
	far := now.Add(9 * time.Hour).In(zaiCST).Format("2006-01-02 15:04:05")
	if _, ok := zaiRateLimitReset([]byte(`{"code":"1308","message":"限额将在 `+far+` 重置。"}`), now); ok {
		t.Fatal("a reset beyond the 5h window should not parse")
	}
	if _, ok := zaiRateLimitReset([]byte(`{"error":{"message":"limited"}}`), now); ok {
		t.Fatal("a non-Z.ai body should not parse")
	}
}

func TestZai429CoolsUntilStatedReset(t *testing.T) {
	kf := &config.KeyFile{Zen: []config.KeyFileEntry{{Label: "a", Key: "sk-a"}}}
	g := newGateway(&config.Config{}, testRuntime(), kf)
	zenRef, ok := g.provider("zen")
	if !ok {
		t.Fatal("no zen pool")
	}
	k := zenRef.pool.rawKeys()[0]

	reset := time.Now().Add(2 * time.Hour).In(zaiCST)
	body := []byte(`{"code":"1308","message":"已达到 5 小时的使用上限。您的限额将在 ` + reset.Format("2006-01-02 15:04:05") + ` 重置。"}`)
	resp := &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}
	v := g.classify(zenRef.pool, k, resp)
	if v.cooldown < 119*time.Minute || v.cooldown > 121*time.Minute {
		t.Fatalf("cooldown %v, want ~2h (until the stated reset)", v.cooldown)
	}

	// a plain 429 keeps the default cooldown
	plain := &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"limited"}}`))}
	if v := g.classify(zenRef.pool, k, plain); v.cooldown != 30*time.Second {
		t.Fatalf("plain 429 cooldown %v, want the default 30s", v.cooldown)
	}
}

func TestProviderKeyUsageEndpoint(t *testing.T) {
	var gotAuth, gotLang string
	fail := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotLang = r.Header.Get("Accept-Language")
		w.Header().Set("Content-Type", "application/json")
		if fail {
			w.Write([]byte(`{"success":false,"code":1308,"msg":"quota exceeded"}`))
			return
		}
		w.Write([]byte(`{"success":true,"code":200,"msg":"","data":{"level":"pro","limits":[{"type":"CREDIT_LIMIT","number":5,"percentage":42,"currentValue":58,"usage":100,"nextResetTime":1790000000000}]}}`))
	}))
	defer up.Close()

	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	p := addPresetProvider(t, st, "zai", "glm", "https://api.z.ai/api/coding/paas/v4", "zai-key-1")
	keys, err := st.ListProviderKeys(p.ID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys: %v (%d)", err, len(keys))
	}
	zaiUsageMockURL(t, up.URL)
	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")

	call := func(provID, keyID int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/providers/%d/keys/%d/usage", provID, keyID), nil)
		req.AddCookie(adminCookie)
		req.SetPathValue("id", fmt.Sprintf("%d", provID))
		req.SetPathValue("keyId", fmt.Sprintf("%d", keyID))
		rec := httptest.NewRecorder()
		g.requireSession(g.requireSuperadmin(g.handleProviderKeyUsage))(rec, req)
		return rec
	}

	rec := call(p.ID, keys[0].ID)
	if rec.Code != 200 {
		t.Fatalf("usage: got %d: %s", rec.Code, rec.Body.String())
	}
	if gotAuth != "zai-key-1" {
		t.Fatalf("Authorization must carry the raw token, got %q", gotAuth)
	}
	if gotLang == "" {
		t.Fatal("Accept-Language not set")
	}
	if !strings.Contains(rec.Body.String(), `"level":"pro"`) {
		t.Fatalf("data not passed through: %s", rec.Body.String())
	}

	// a non-zai provider has no usage endpoint
	gen := addGenericProvider(t, st, "generic", "https://x.example/v1", "gk1")
	gkeys, _ := st.ListProviderKeys(gen.ID)
	if rec := call(gen.ID, gkeys[0].ID); rec.Code != 400 {
		t.Fatalf("generic provider usage: got %d, want 400", rec.Code)
	}
	// unknown key -> 404
	if rec := call(p.ID, 99999); rec.Code != 404 {
		t.Fatalf("unknown key usage: got %d, want 404", rec.Code)
	}
	// upstream refusal -> 502 with the upstream message
	fail = true
	if rec := call(p.ID, keys[0].ID); rec.Code != 502 || !strings.Contains(rec.Body.String(), "quota exceeded") {
		t.Fatalf("failed usage: got %d: %s", rec.Code, rec.Body.String())
	}
}
