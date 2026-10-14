package gateway

import (
	"net/http"
	"strings"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
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
	if err := g.pruneNewUserProviders(rs); err != nil {
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
	if !rs.Auth.CredentialLogin && g.rs().Auth.CredentialLogin {
		if msg := g.credentialOffBlocked(actor); msg != "" {
			apiErr(w, http.StatusBadRequest, msg)
			return
		}
	}
	// the sync status lives in the DB and is server-owned; take it from there
	// so a concurrent sync (or a stale in-memory snapshot) can't lose it
	cur, err := g.store.LoadRuntimeSettings()
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rs.Zen.ModelMetaSyncStatus = cur.Zen.ModelMetaSyncStatus
	// new-user defaults need no pool rebuild (they only matter at user
	// creation), but stale ids are dropped so the GUI can always re-save
	if err := g.pruneNewUserProviders(rs); err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
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

// credentialOffBlocked explains why password sign-in can't be turned off, or
// returns "". Without Google sign-in nobody could log in at all; with a
// Workspace domain, an acting admin outside it couldn't log back in.
func (g *gateway) credentialOffBlocked(actor *store.User) string {
	c := g.conf()
	if !c.GoogleEnabled() {
		return "Password sign-in can't be disabled: Google sign-in is not configured on this server"
	}
	if d := c.GoogleWorkspaceDomain; d != "" && actor != nil && !strings.HasSuffix(strings.ToLower(actor.Email), "@"+d) {
		return "Password sign-in can't be disabled: your account (" + actor.Email + ") is outside the " + d + " Google domain, so it would lock you out"
	}
	return ""
}

// pruneNewUserProviders drops ids of providers that no longer exist from the
// new-user defaults. Deleting a provider doesn't rewrite the settings
// document, so its id may linger there; provider ids are AUTOINCREMENT and
// never reused, so a stale id can never come to mean a different provider —
// it is just noise, filtered on read and on save so neither ever fails on it.
func (g *gateway) pruneNewUserProviders(rs *settings.RuntimeSettings) error {
	ids := rs.NewUsers.DisabledProviderIDs
	if len(ids) == 0 {
		rs.NewUsers.DisabledProviderIDs = []int64{}
		return nil
	}
	provs, err := g.store.ListProviders()
	if err != nil {
		return err
	}
	exists := make(map[int64]bool, len(provs))
	for _, p := range provs {
		exists[p.ID] = true
	}
	kept := make([]int64, 0, len(ids))
	for _, id := range ids {
		if exists[id] {
			kept = append(kept, id)
		}
	}
	rs.NewUsers.DisabledProviderIDs = kept
	return nil
}
