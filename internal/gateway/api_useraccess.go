package gateway

import (
	"net/http"
)

// Per-user provider access (superadmin only): which providers a user's
// client keys may route to. Everything is allowed by default — a user only
// loses access to a provider when a superadmin revokes it here, and the
// revoked provider then behaves as if it does not exist for them (absent
// from their /v1/models, unknown prefix when routed).

// userProviderJSON is one provider row together with the user's access state.
type userProviderJSON struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Preset  string `json:"preset"`
	Enabled bool   `json:"enabled"` // globally enabled (routable at all)
	// revoked for this user; only meaningful when Enabled is true
	DisabledForUser bool `json:"disabledForUser"`
}

// handleListUserProviders serves GET /api/users/{id}/providers — every
// provider annotated with whether it is revoked for this user (the Users
// page access dialog).
func (g *gateway) handleListUserProviders(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if u, err := g.store.User(id); err != nil || u == nil {
		apiErr(w, http.StatusNotFound, "No such user")
		return
	}
	provs, err := g.store.ListProviders()
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	disabled, err := g.store.DisabledProviderIDs(id)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]userProviderJSON, 0, len(provs))
	for _, p := range provs {
		out = append(out, userProviderJSON{
			ID: p.ID, Name: p.Name, Preset: p.Preset,
			Enabled:         p.Enabled,
			DisabledForUser: disabled[p.ID],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// handleSetUserProviderAccess serves PUT /api/users/{id}/providers/{providerId}/access
// with {"disabled": bool} — grant or revoke one provider for one user.
func (g *gateway) handleSetUserProviderAccess(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	provID, ok := pathID(w, r, "providerId")
	if !ok {
		return
	}
	var req struct {
		Disabled bool `json:"disabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if u, err := g.store.User(id); err != nil || u == nil {
		apiErr(w, http.StatusNotFound, "No such user")
		return
	}
	if p, err := g.store.Provider(provID); err != nil || p == nil {
		apiErr(w, http.StatusNotFound, "No such provider")
		return
	}
	if !g.applyMutation(w, actor, "user.provider_access", "", func() error {
		return g.store.SetUserProviderAccess(id, provID, req.Disabled)
	}) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
