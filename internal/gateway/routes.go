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

	mux.HandleFunc("GET /api/models", g.requireSession(g.handleAllModels))
	mux.HandleFunc("GET /api/providers", g.requireSession(g.requireSuperadmin(g.handleListProviders)))
	mux.HandleFunc("POST /api/providers", g.requireSession(g.requireSuperadmin(g.handleCreateProvider)))
	mux.HandleFunc("PATCH /api/providers/{id}", g.requireSession(g.requireSuperadmin(g.handlePatchProvider)))
	mux.HandleFunc("DELETE /api/providers/{id}", g.requireSession(g.requireSuperadmin(g.handleDeleteProvider)))
	mux.HandleFunc("GET /api/providers/{id}/models", g.requireSession(g.requireSuperadmin(g.handleProviderModels)))
	mux.HandleFunc("POST /api/providers/{id}/keys", g.requireSession(g.requireSuperadmin(g.handleAddProviderKey)))
	mux.HandleFunc("PATCH /api/providers/{id}/keys/{keyId}", g.requireSession(g.requireSuperadmin(g.handlePatchProviderKey)))
	mux.HandleFunc("DELETE /api/providers/{id}/keys/{keyId}", g.requireSession(g.requireSuperadmin(g.handleDeleteProviderKey)))

	mux.HandleFunc("GET /api/settings", g.requireSession(g.requireSuperadmin(g.handleGetSettings)))
	mux.HandleFunc("PUT /api/settings", g.requireSession(g.requireSuperadmin(g.handlePutSettings)))
	mux.HandleFunc("POST /api/settings/model-meta/sync", g.requireSession(g.requireSuperadmin(g.handleSyncModelMeta)))
	mux.HandleFunc("PUT /api/settings/responses-api", g.requireSession(g.requireSuperadmin(g.handleSetResponsesAPI)))

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
	provs := map[string]int{}
	for _, ref := range g.allProviders() {
		provs[ref.name] = ref.pool.healthyCount()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"uptime_s":     int(time.Since(startTime).Seconds()),
		"zen_healthy":  provs["zen"],
		"kilo_healthy": provs["kilo"],
		"providers":    provs,
	})
}

// fetchUpstreamModels GETs one provider's catalog with the first healthy key.
// Builtin providers get the self-describing suffix treatment; generic
// openai providers pass through raw under their prefix.
func (g *gateway) fetchUpstreamModels(ref providerRef) ([]any, error) {
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
			"free":     true,
		}
		switch {
		case ref.typ == "opencode":
			// Zen advertises ids only — enrich from settings meta + defaults.
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
			entry["reasoning"] = meta.Reasoning
			entry["responses_api"] = meta.ResponsesAPI
			if len(meta.InputModalities) > 0 {
				entry["input_modalities"] = meta.InputModalities
			}
			if meta.Description != "" {
				entry["description"] = meta.Description
			}
			entry["id"] = ref.name + "/" + buildSuffixedID(id, ctx, meta.InputModalities)
		case ref.builtin: // kilo
			// Kilo's catalog is rich — map the fields agents read.
			if g.rs().Kilo.FreeOnly {
				if free, _ := m["isFree"].(bool); !free {
					continue
				}
			}
			var mods []string
			if cw, ok := m["context_length"].(float64); ok {
				entry["context_window"] = int64(cw)
			}
			if tp, ok := m["top_provider"].(map[string]any); ok {
				if mc, ok := tp["max_completion_tokens"].(float64); ok {
					entry["max_output_tokens"] = int64(mc)
				}
			}
			if arch, ok := m["architecture"].(map[string]any); ok {
				if in, ok := arch["input_modalities"].([]any); ok {
					for _, v := range in {
						if s, ok := v.(string); ok {
							mods = append(mods, s)
						}
					}
					entry["input_modalities"] = in
				}
				if sp, ok := m["output_modalities"]; ok {
					entry["output_modalities"] = sp
				}
			}
			if sp, ok := m["supported_parameters"]; ok {
				entry["supported_parameters"] = sp
			}
			if d, ok := m["description"].(string); ok {
				entry["description"] = d
			}
			if free, ok := m["isFree"].(bool); ok {
				entry["free"] = free
			}
			cw, _ := entry["context_window"].(int64)
			entry["id"] = ref.name + "/" + buildSuffixedID(id, cw, mods)
		default:
			// generic openai provider: passthrough, no enrichment, no suffixes
			if d, ok := m["context_length"].(float64); ok {
				entry["context_window"] = int64(d)
			}
			if d, ok := m["description"].(string); ok {
				entry["description"] = d
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// mergedCatalog returns the cached merged catalog body, refreshing it when
// older than 10 minutes. ok=false means every provider fetch failed.
func (g *gateway) mergedCatalog() ([]byte, bool) {
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
		body, _ := json.Marshal(map[string]any{"object": "list", "data": merged})
		g.catalog = body
		g.catalogAt = time.Now()
	}
	return g.catalog, true
}

// handleModels serves the merged, prefixed catalog to API clients.
func (g *gateway) handleModels(w http.ResponseWriter, r *http.Request) {
	body, ok := g.mergedCatalog()
	if !ok {
		writeErr(w, http.StatusBadGateway, "Catalog fetch failed: all providers unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// handleAllModels serves the same merged catalog to the admin GUI's Models
// page (session auth instead of a client key).
func (g *gateway) handleAllModels(w http.ResponseWriter, r *http.Request) {
	body, ok := g.mergedCatalog()
	if !ok {
		apiErr(w, http.StatusBadGateway, "Catalog fetch failed: all providers unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func hasFreeSuffix(id string) bool {
	return len(id) > 5 && id[len(id)-5:] == "-free"
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
