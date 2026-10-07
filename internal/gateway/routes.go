package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

// RegisterRoutes wires every endpoint onto the mux: the three /v1 surfaces,
// /health, the GUI admin API, and (with the embedded GUI present) the SPA.
// Web handlers stay unexported — all access funnels through the mux.
func (g *gateway) RegisterRoutes(mux *http.ServeMux, webFS fs.FS) {
	mux.HandleFunc("POST /v1/chat/completions", g.clientOnly(g.handleChat))
	mux.HandleFunc("POST /v1/responses", g.clientOnly(g.handleResponses))
	mux.HandleFunc("POST /v1/messages", g.clientOnly(g.handleMessages))
	mux.HandleFunc("GET /v1/models", g.clientOnly(g.handleModels))
	mux.HandleFunc("POST /mcp", g.clientOnly(g.handleMCP)) // MCP web tools (streamable HTTP, stateless)
	mux.HandleFunc("GET /health", g.handleHealth)

	// GUI backend — session-cookie auth
	mux.HandleFunc("POST /api/auth/login", g.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", g.requireSession(g.handleLogout))
	mux.HandleFunc("GET /api/auth/me", g.requireSession(g.handleMe))
	mux.HandleFunc("POST /api/me/password", g.requireSession(g.handleChangeMyPassword))

	mux.HandleFunc("GET /api/me/keys", g.requireSession(g.handleListMyKeys))
	mux.HandleFunc("POST /api/me/keys", g.requireSession(g.handleCreateMyKey))
	mux.HandleFunc("PATCH /api/me/keys/{keyId}", g.requireSession(g.handlePatchMyKey))
	mux.HandleFunc("DELETE /api/me/keys/{keyId}", g.requireSession(g.handleDeleteMyKey))

	mux.HandleFunc("GET /api/users", g.requireSession(g.requireSuperadmin(g.handleListUsers)))
	mux.HandleFunc("POST /api/users", g.requireSession(g.requireSuperadmin(g.handleCreateUser)))
	mux.HandleFunc("PATCH /api/users/{id}", g.requireSession(g.requireSuperadmin(g.handlePatchUser)))
	mux.HandleFunc("DELETE /api/users/{id}", g.requireSession(g.requireSuperadmin(g.handleDeleteUser)))
	mux.HandleFunc("GET /api/users/{id}/keys", g.requireSession(g.requireSuperadmin(g.handleListUserKeys)))
	mux.HandleFunc("POST /api/users/{id}/keys", g.requireSession(g.requireSuperadmin(g.handleCreateUserKey)))
	mux.HandleFunc("PATCH /api/users/{id}/keys/{keyId}", g.requireSession(g.requireSuperadmin(g.handlePatchUserKey)))
	mux.HandleFunc("DELETE /api/users/{id}/keys/{keyId}", g.requireSession(g.requireSuperadmin(g.handleDeleteUserKey)))
	mux.HandleFunc("GET /api/users/{id}/providers", g.requireSession(g.requireSuperadmin(g.handleListUserProviders)))
	mux.HandleFunc("PUT /api/users/{id}/providers/{providerId}/access", g.requireSession(g.requireSuperadmin(g.handleSetUserProviderAccess)))

	mux.HandleFunc("GET /api/models", g.requireSession(g.handleAllModels))
	mux.HandleFunc("GET /api/providers", g.requireSession(g.requireSuperadmin(g.handleListProviders)))
	mux.HandleFunc("GET /api/providers/presets", g.requireSession(g.requireSuperadmin(g.handleListPresets)))
	mux.HandleFunc("POST /api/providers", g.requireSession(g.requireSuperadmin(g.handleCreateProvider)))
	mux.HandleFunc("PATCH /api/providers/{id}", g.requireSession(g.requireSuperadmin(g.handlePatchProvider)))
	mux.HandleFunc("DELETE /api/providers/{id}", g.requireSession(g.requireSuperadmin(g.handleDeleteProvider)))
	mux.HandleFunc("GET /api/providers/{id}/models", g.requireSession(g.requireSuperadmin(g.handleProviderModels)))
	mux.HandleFunc("POST /api/providers/{id}/keys", g.requireSession(g.requireSuperadmin(g.handleAddProviderKey)))
	mux.HandleFunc("PATCH /api/providers/{id}/keys/{keyId}", g.requireSession(g.requireSuperadmin(g.handlePatchProviderKey)))
	mux.HandleFunc("GET /api/providers/{id}/keys/{keyId}/usage", g.requireSession(g.requireSuperadmin(g.handleProviderKeyUsage)))
	mux.HandleFunc("DELETE /api/providers/{id}/keys/{keyId}", g.requireSession(g.requireSuperadmin(g.handleDeleteProviderKey)))

	mux.HandleFunc("GET /api/settings", g.requireSession(g.requireSuperadmin(g.handleGetSettings)))
	mux.HandleFunc("PUT /api/settings", g.requireSession(g.requireSuperadmin(g.handlePutSettings)))
	mux.HandleFunc("POST /api/settings/model-meta/sync", g.requireSession(g.requireSuperadmin(g.handleSyncModelMeta)))
	mux.HandleFunc("PUT /api/settings/responses-api", g.requireSession(g.requireSuperadmin(g.handleSetResponsesAPI)))
	mux.HandleFunc("POST /api/webtools/test", g.requireSession(g.requireSuperadmin(g.handleWebToolsTest)))

	mux.HandleFunc("GET /api/dashboard", g.requireSession(g.handleDashboard))

	mux.HandleFunc("GET /api/usage/summary", g.requireSession(g.handleUsageSummary))
	mux.HandleFunc("GET /api/usage/timeseries", g.requireSession(g.handleUsageTimeseries))
	mux.HandleFunc("GET /api/usage/users", g.requireSession(g.requireSuperadmin(g.handleUsageUsers)))
	mux.HandleFunc("GET /api/usage/keys", g.requireSession(g.handleUsageKeys))
	mux.HandleFunc("GET /api/usage/activity", g.requireSession(g.handleUsageActivity))

	mux.Handle("/", g.ServeWeb(webFS))
}

// MaintenanceLoop flushes in-memory usage counters and usage events to the
// store, prunes expired sessions, and triggers the model-catalog sync.
func (g *gateway) MaintenanceLoop() {
	flush := time.NewTicker(30 * time.Second)
	defer flush.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for range flush.C {
		g.usage.flush(g.store)
		if events := g.usageBuf.drain(); len(events) > 0 {
			if err := g.store.InsertUsageEvents(events); err != nil {
				log.Printf("usage flush: %v", err)
			}
		}
		g.maybeSyncModelMeta() // 24h-gated, async, at most one in flight
		// prune piggybacks on the flush tick, roughly hourly
		if time.Since(startTime)%time.Hour < 30*time.Second {
			g.store.DeleteExpiredSessions() //nolint:errcheck
			g.store.PruneUsageEvents(usageCutoff())
		}
	}
}

// clientOnly enforces client authentication on /v1/*. With a store it checks
// the in-memory DB-backed key cache (and attributes usage); the legacy
// config.Config APIKeys allowlist still works for store==nil (tests).
// Accepts both OpenAI-style Bearer and Anthropic-style x-api-key headers.
func (g *gateway) clientOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" {
			key = r.Header.Get("x-api-key")
		}
		if g.store != nil {
			if key == "" {
				g.rejectClient(w)
				return
			}
			ck, ok := g.ck.lookup(key)
			if !ok {
				g.rejectClient(w)
				return
			}
			g.usage.record(ck.ID)
			next(w, withClientKey(r, ck))
			return
		}
		// legacy path (store==nil tests): static allowlist
		if len(g.conf().APIKeys) > 0 && (key == "" || !slices.Contains(g.conf().APIKeys, key)) {
			g.rejectClient(w)
			return
		}
		next(w, r)
	}
}

func (g *gateway) rejectClient(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="nano-llm-proxy"`)
	writeErr(w, http.StatusUnauthorized, "Invalid or missing API key")
}

func (g *gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	type poolHealth struct {
		Total   int `json:"total"`
		Healthy int `json:"healthy"`
	}
	provs := map[string]poolHealth{}
	for _, ref := range g.allProviders() {
		provs[ref.name] = poolHealth{
			Total:   len(ref.pool.snapshot()),
			Healthy: ref.pool.healthyCount(),
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"uptime_s":  int(time.Since(startTime).Seconds()),
		"providers": provs,
		"webtools":  map[string]bool{"enabled": g.rs().WebTools.Enabled},
	})
}

// fetchUpstreamModels GETs one provider's catalog with the first healthy key.
// Dual-endpoint providers are catalogued from the OpenAI root, falling back to
// the Anthropic root's /v1/models when that fails; anthropic-only providers go
// straight there. Both shapes expose ids as data[].id.
func (g *gateway) fetchUpstreamModels(ref providerRef) ([]any, error) {
	if ref.baseURL == "" {
		return g.fetchModelsAnthropic(ref)
	}
	models, err := g.fetchModelsOpenAI(ref)
	if err == nil || ref.anthropicBaseURL == "" {
		return models, err
	}
	if alt, altErr := g.fetchModelsAnthropic(ref); altErr == nil {
		return alt, nil
	}
	return models, err
}

func (g *gateway) fetchModelsOpenAI(ref providerRef) ([]any, error) {
	key := ref.pool.pick(nil)
	if key == nil {
		return nil, fmt.Errorf("No healthy %s keys available — every key is cooling or disabled", ref.name)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ref.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key.Key)
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s /models HTTP %d", ref.name, resp.StatusCode)
	}
	return g.decodeModelList(ref, resp)
}

func (g *gateway) fetchModelsAnthropic(ref providerRef) ([]any, error) {
	key := ref.pool.pick(nil)
	if key == nil {
		return nil, fmt.Errorf("No healthy %s keys available — every key is cooling or disabled", ref.name)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ref.anthropicBaseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", key.Key)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s /v1/models HTTP %d", ref.name, resp.StatusCode)
	}
	return g.decodeModelList(ref, resp)
}

// decodeModelList parses an upstream catalog ({"data":[{"id":...}, ...]}) and
// applies the per-provider enrichment: zen from its settings meta, preset
// providers through their registry hook (providerspec.go), everything else as
// a generic passthrough.
func (g *gateway) decodeModelList(ref providerRef, resp *http.Response) ([]any, error) {
	var parsed struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]any, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		id, _ := m["id"].(string)
		if id == "" {
			continue
		}
		if ref.typ == "opencode" && g.rs().Zen.FreeOnly && !hasFreeSuffix(id) {
			continue
		}
		entry := map[string]any{
			"id":       ref.name + "/" + id,
			"object":   "model",
			"owned_by": ref.name,
		}
		switch {
		case ref.typ == "opencode":
			// Zen advertises ids only — enrich from settings meta + defaults.
			//
			// Zen's catalog is NOT purely the free tier: it also carries
			// subscription models, and upstream marks the free ones with a
			// "-free" suffix on the id (big-pickle, claude-opus-5-5 and
			// friends are all served from the same /models list). So "free"
			// comes from that suffix — the same predicate the FreeOnly filter
			// above uses. Deriving the badge from anything else would let the
			// two disagree: a card badged free that vanishes under the
			// free-models-only toggle, or a paid model advertised as free.
			const defCtx, defOut = int64(262144), int64(8192)
			meta, known := g.rs().Zen.ModelMeta[id]
			if !known {
				meta = settings.ModelMeta{ContextWindow: defCtx, MaxOutputTokens: defOut, Reasoning: true}
			}
			ctx := meta.ContextWindow
			if ctx == 0 {
				ctx = defCtx
			}
			mo := meta.MaxOutputTokens
			if mo == 0 {
				mo = defOut
			}
			entry["context_window"] = ctx
			entry["max_output_tokens"] = mo
			applyReasoningFacts(entry, meta, known)
			entry["responses_api"] = meta.ResponsesAPI
			entry["free"] = hasFreeSuffix(id)
			if len(meta.InputModalities) > 0 {
				entry["input_modalities"] = meta.InputModalities
			}
			if meta.Description != "" {
				entry["description"] = meta.Description
			}
			entry["id"] = ref.name + "/" + buildSuffixedID(id, ctx, meta.InputModalities)
		default:
			if hook := presetCatalogHook(ref.preset); hook != nil {
				if !hook(g, ref, id, entry, m) {
					continue
				}
				break
			}
			// generic provider: passthrough, no enrichment, no suffixes
			if d, ok := m["context_length"].(float64); ok {
				entry["context_window"] = int64(d)
			}
			if d, ok := m["description"].(string); ok {
				entry["description"] = d
			}
			// modalities are still mapped: a custom OpenAI-compatible
			// provider (OpenRouter and friends) usually advertises them, and
			// without them an image-capable model reads as text-only to every
			// agent. Both spellings are accepted; the mirror publishes the
			// rest.
			if mods := rawModalities(m); len(mods) > 0 {
				entry["input_modalities"] = mods
			}
		}
		mirrorContextLength(entry)
		mirrorInputModalities(entry)
		out = append(out, entry)
	}
	return out, nil
}

// mergedCatalog returns the cached merged catalog (all providers, unscoped),
// refreshing it when older than 10 minutes. ok=false means every provider
// fetch failed. Per-caller scoping happens in catalogFor.
func (g *gateway) mergedCatalog() ([]any, bool) {
	g.catMu.Lock()
	defer g.catMu.Unlock()
	if g.catalog == nil || time.Since(g.catalogAt) > 10*time.Minute {
		var merged []any
		var failed []string
		for _, ref := range g.allProviders() {
			models, err := g.fetchUpstreamModels(ref)
			if err != nil {
				failed = append(failed, fmt.Sprintf("%s=%v", ref.name, err))
				log.Printf("catalog: %s unavailable: %v", ref.name, err)
				continue
			}
			merged = append(merged, models...)
		}
		if len(merged) == 0 && len(failed) > 0 {
			return nil, false
		}
		g.catalog = merged
		g.catalogAt = time.Now()
	}
	return g.catalog, true
}

// handleModels serves the merged, prefixed catalog to API clients, scoped to
// the caller's per-user provider access.
func (g *gateway) handleModels(w http.ResponseWriter, r *http.Request) {
	models, ok := g.catalogFor(r)
	if !ok {
		writeErr(w, http.StatusBadGateway, "Catalog fetch failed: all providers unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": models})
}

// handleAllModels serves the same scoped catalog to the admin GUI's Models
// page (session auth instead of a client key).
func (g *gateway) handleAllModels(w http.ResponseWriter, r *http.Request) {
	models, ok := g.catalogFor(r)
	if !ok {
		apiErr(w, http.StatusBadGateway, "Catalog fetch failed: all providers unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": models})
}

// hasFreeSuffix reports whether a raw upstream model id carries Zen's
// "-free" marker. It is the single source of truth for a zen model's free
// status: both the "free" badge and the Zen.FreeOnly filter call it, so the
// badge can never contradict the filter. The test is on the raw id, before
// the cosmetic context/modality suffix is appended.
func hasFreeSuffix(id string) bool {
	return len(id) > 5 && id[len(id)-5:] == "-free"
}

// defaultReasoningOptions is the ladder advertised for a model the catalog
// says reasons but has no effort data for (a zen/zai id models.dev doesn't
// document yet). Agents such as oh-my-pi need the level list, not just the
// boolean — an entry with reasoning:true and no options leaves them nothing
// to map their reasoning control onto. low/medium/high is the spelling
// essentially every current reasoning endpoint accepts, so the guess is
// harmless where the levels are ignored and usable where they are not.
var defaultReasoningOptions = []settings.ReasoningOption{
	{Type: "effort", Values: []string{"low", "medium", "high"}},
}

// applyReasoningFacts writes the two reasoning fields an agent reads: the
// boolean flag every client understands, and the models.dev-shaped options
// list that carries the actual levels. known is false when the entry came
// from a fallback rather than catalog metadata, which is the only case that
// gets the default ladder.
func applyReasoningFacts(entry map[string]any, meta settings.ModelMeta, known bool) {
	entry["reasoning"] = meta.Reasoning
	if !meta.Reasoning {
		delete(entry, "reasoning_options") // never advertise levels for a non-reasoning model
		return
	}
	opts := meta.ReasoningOptions
	if len(opts) == 0 && !known {
		opts = defaultReasoningOptions
	}
	if len(opts) > 0 {
		entry["reasoning_options"] = opts
	}
}

// rawModalities reads a raw upstream catalog entry's input modalities from
// either spelling it may use: Kilo's nested architecture.input_modalities and
// the flat input_modalities / input arrays other OpenAI-compatible providers
// emit. Unknown entries in the list are kept as-is (the suffix builder maps
// what it knows and ignores the rest) so the advertised fact stays faithful.
func rawModalities(raw map[string]any) []string {
	var out []string
	add := func(v any) {
		list, ok := v.([]any)
		if !ok {
			if ss, ok := v.([]string); ok {
				out = append(out, ss...)
			}
			return
		}
		for _, e := range list {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	if arch, ok := raw["architecture"].(map[string]any); ok {
		add(arch["input_modalities"])
	}
	add(raw["input_modalities"])
	if len(out) == 0 {
		add(raw["input"])
	}
	return out
}

// mirrorContextLength republishes the context window under the second
// spelling agents read. OpenAI-shaped catalogs say context_length; opencode
// and oh-my-pi say context_window; different clients look for different ones
// and a missing field is read as "unknown, assume 128K". The two are emitted
// from the single enriched value so they can never disagree.
func mirrorContextLength(entry map[string]any) {
	if cw, ok := entry["context_window"].(int64); ok {
		entry["context_length"] = cw
	}
}

// mirrorInputModalities republishes the entry's input modalities under every
// spelling clients read them, from the one enriched value:
//
//	input                          pi-openai-compat (pi, oh-my-pi): the model
//	                               is treated as text-only unless this lists
//	                               "image", so a missing field means the agent
//	                               silently drops images instead of sending them
//	input_modalities               opencode / models.dev shape
//	architecture.input_modalities  Kilo's nested shape, which pi's built-in
//	                               opencode provider loader reads
//
// Without the mirrors an image-capable model looks text-only to pi and the
// client omits the picture without a word — the failure looks like a gateway
// that "doesn't do images" rather than a field-name mismatch.
func mirrorInputModalities(entry map[string]any) {
	var mods []string
	switch m := entry["input_modalities"].(type) {
	case []string:
		mods = m
	case []any: // a hook that copied upstream's raw JSON array
		for _, v := range m {
			if s, ok := v.(string); ok {
				mods = append(mods, s)
			}
		}
	}
	if len(mods) == 0 {
		return
	}
	entry["input"] = mods
	arch, _ := entry["architecture"].(map[string]any)
	if arch == nil {
		arch = map[string]any{}
		entry["architecture"] = arch
	}
	arch["input_modalities"] = mods
}

// ServeWeb hosts the embedded admin GUI with SPA fallback; untagged builds
// (go test, go run without -tags prod) get a stub page instead.
func (g *gateway) ServeWeb(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "." {
			p = "index.html"
		}
		if _, err := fs.Stat(fsys, p); err != nil {
			// SPA routes (e.g. /users, /providers) fall back to index.html
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// HealthyUpstreamKeys reports how many upstream keys are currently healthy,
// across every provider (for the startup log).
func (g *gateway) HealthyUpstreamKeys() int {
	healthy := 0
	for _, ref := range g.allProviders() {
		healthy += ref.pool.healthyCount()
	}
	return healthy
}

// ProviderCount reports how many providers are configured.
func (g *gateway) ProviderCount() int {
	return len(g.allProviders())
}
