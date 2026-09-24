package gateway

import (
	"net/http"
	"strings"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

// handleGetSettings serves the effective runtime settings document
// (superadmin only — the same audience as the GUI Settings page). Read from
// the store, not the in-memory snapshot: the background modelMeta sync writes
// the DB directly between admin requests.
func (g *gateway) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	rs, err := g.store.LoadRuntimeSettings()
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": rs})
}

// handlePutSettings replaces the runtime settings document. The body is the
// full document; omitted fields fall back to their defaults and explicit
// false/0 are honored. Validation runs before anything is written — the
// database must never hold a document boot would refuse. The models.dev sync
// status block is server-owned and survives the PUT. applyMutation swaps the
// pool mode + reloads caches in-request, so changes hit /v1/* immediately.
func (g *gateway) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	rs := settings.DefaultRuntimeSettings()
	if !readJSON(w, r, rs) {
		return
	}
	*rs = *rs.ApplyDefaults()
	if msg := rs.Validate(); msg != "" {
		apiErr(w, http.StatusBadRequest, msg)
		return
	}
	// the sync status lives in the DB and is server-owned; take it from there
	// so a concurrent sync (or a stale in-memory snapshot) can't lose it
	cur, err := g.store.LoadRuntimeSettings()
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rs.Zen.ModelMetaSyncStatus = cur.Zen.ModelMetaSyncStatus
	if !g.applyMutation(w, actor, "settings.update", "", func() error {
		if err := g.store.SaveRuntimeSettings(rs); err != nil {
			return err
		}
		g.rsPtr.Store(rs) // hot path sees the new values before the response
		return nil
	}) {
		return
	}
	// modelMeta edits invalidate the learned chat/responses surface flips so
	// the new flags take full effect immediately
	g.mu.Lock()
	g.surfaceOver = map[string]string{}
	g.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": rs})
}

// handleSetResponsesAPI serves PUT /api/settings/responses-api — flips the
// per-model responsesApi flag on one catalog model. The sync preserves the
// flag across catalog refreshes; the learned-surface cache is reset so the
// new routing takes effect immediately.
func (g *gateway) handleSetResponsesAPI(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	var req struct {
		Model        string `json:"model"`
		ResponsesAPI bool   `json:"responsesApi"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		apiErr(w, http.StatusBadRequest, "Model is required")
		return
	}
	if !g.applyMutation(w, actor, "settings.responses.update", "", func() error {
		return g.store.UpdateSettings(func(rs *settings.RuntimeSettings) *settings.RuntimeSettings {
			meta := rs.Zen.ModelMeta
			if meta == nil {
				meta = map[string]settings.ModelMeta{}
			}
			entry, ok := meta[req.Model]
			if !ok {
				// not in the catalog yet — start from the enrichment defaults
				entry = settings.ModelMeta{ContextWindow: 262144, MaxOutputTokens: 8192, Reasoning: true}
			}
			entry.ResponsesAPI = req.ResponsesAPI
			meta[req.Model] = entry
			rs.Zen.ModelMeta = meta
			return rs
		})
	}) {
		return
	}
	g.mu.Lock()
	g.surfaceOver = map[string]string{}
	g.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
