package gateway

import (
	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
	"net/http"
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
	// responsesModels edits invalidate the learned chat/responses surface
	// flips so the new list takes full effect immediately
	g.mu.Lock()
	g.surfaceOver = map[string]string{}
	g.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": rs})
}
