package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
	"net/http"
	"net/url"
	"regexp"
)

// store.Provider management endpoints (superadmin only). GUI-added providers are
// always type "openai"; the two builtins (zen=opencode, kilo=openai) are
// editable but can't be renamed or deleted.

var providerNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

type providerKeyJSON struct {
	ID        int64  `json:"id"`
	Label     string `json:"label"`
	SortOrder int64  `json:"sortOrder"`
	Disabled  bool   `json:"disabled"`
	CreatedAt int64  `json:"createdAt"`
}

func (g *gateway) handleListProviders(w http.ResponseWriter, r *http.Request) {
	provs, keyLists, err := g.store.ListProvidersWithKeys()
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// live pool stats by upstream key hash
	stats := map[string]KeyView{}
	for _, ref := range g.allProviders() {
		for _, v := range ref.pool.snapshot() {
			stats[v.Hash] = v
		}
	}
	type pkJSON struct {
		providerKeyJSON
		Hash        string `json:"hash"`
		Status      string `json:"status"`
		CooldownFor string `json:"cooldownRemaining,omitempty"`
		Requests    int64  `json:"requests"`
		RateLimited int64  `json:"rateLimited"`
		Errors      int64  `json:"errors"`
	}
	type provJSON struct {
		ID        int64    `json:"id"`
		Name      string   `json:"name"`
		Type      string   `json:"type"`
		BaseURL   string   `json:"baseUrl"`
		Enabled   bool     `json:"enabled"`
		Builtin   bool     `json:"builtin"`
		SortOrder int64    `json:"sortOrder"`
		Healthy   int      `json:"healthy"`
		Total     int      `json:"total"`
		Keys      []pkJSON `json:"keys"`
	}
	out := make([]provJSON, 0, len(provs))
	for i, p := range provs {
		pj := provJSON{
			ID: p.ID, Name: p.Name, Type: p.Type, BaseURL: p.BaseURL,
			Enabled: p.Enabled, Builtin: p.Builtin, SortOrder: p.SortOrder,
			Keys: []pkJSON{},
		}
		for _, k := range keyLists[i] {
			kj := pkJSON{
				providerKeyJSON: providerKeyJSON{
					ID: k.ID, Label: k.Label, SortOrder: k.SortOrder,
					Disabled: k.Disabled, CreatedAt: k.CreatedAt,
				},
			}
			// hash of the upstream key matches Pool.KeyState.Hash
			if v, ok := stats[upstreamKeyHash(k.Key)]; ok {
				kj.Hash, kj.Status, kj.CooldownFor = v.Hash, v.Status, v.CooldownFor
				kj.Requests, kj.RateLimited, kj.Errors = v.Requests, v.RateLimited, v.Errors
				if !k.Disabled && v.Status == "healthy" {
					pj.Healthy++
				}
			}
			if !k.Disabled {
				pj.Total++
			}
			pj.Keys = append(pj.Keys, kj)
		}
		out = append(out, pj)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// upstreamKeyHash mirrors Pool's short key hash so DB rows join live stats.
func upstreamKeyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}

func (g *gateway) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	var req struct {
		Name     string   `json:"name"`
		BaseURL  string   `json:"baseUrl"`
		Keys     []string `json:"keys"`
		FirstKey string   `json:"firstKey"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	name := req.Name
	baseURL := req.BaseURL
	if !providerNameRe.MatchString(name) {
		apiErr(w, http.StatusBadRequest, "Provider name must be 1-32 characters of lowercase letters, digits, or dashes (it becomes the model prefix)")
		return
	}
	if name == "zen" || name == "kilo" {
		apiErr(w, http.StatusConflict, "That name is reserved (zen and kilo are built in)")
		return
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		apiErr(w, http.StatusBadRequest, "baseUrl must be an absolute http(s) URL (OpenAI-compatible root, e.g. https://host/v1)")
		return
	}
	keys := req.Keys
	if len(keys) == 0 && req.FirstKey != "" {
		keys = []string{req.FirstKey}
	}
	if len(keys) == 0 {
		apiErr(w, http.StatusBadRequest, "At least one upstream API key is required")
		return
	}
	p := &store.Provider{Name: name, Type: "openai", BaseURL: baseURL, Enabled: true, SortOrder: 100}
	pks := make([]store.ProviderKey, 0, len(keys))
	for _, k := range keys {
		if k != "" {
			pks = append(pks, store.ProviderKey{Key: k})
		}
	}
	if len(pks) == 0 {
		apiErr(w, http.StatusBadRequest, "At least one upstream API key is required")
		return
	}
	if !g.applyMutation(w, actor, "provider.create", "A provider with this name already exists", func() error { return g.store.CreateProvider(p, pks) }) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": p.ID})
}

func (g *gateway) handlePatchProvider(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := g.store.Provider(id)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p == nil {
		apiErr(w, http.StatusNotFound, "That provider no longer exists (it may have been deleted)")
		return
	}
	var req struct {
		Name    *string `json:"name"`
		BaseURL *string `json:"baseUrl"`
		Enabled *bool   `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name != nil && p.Builtin {
		apiErr(w, http.StatusConflict, "Built-in providers cannot be renamed")
		return
	}
	if req.Name != nil && !providerNameRe.MatchString(*req.Name) {
		apiErr(w, http.StatusBadRequest, "store.Provider name must be 1-32 characters of lowercase letters, digits, or dashes")
		return
	}
	if req.BaseURL != nil {
		u, err := url.Parse(*req.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			apiErr(w, http.StatusBadRequest, "baseUrl must be an absolute http(s) URL")
			return
		}
	}
	if !g.applyMutation(w, actor, "provider.update", "A provider with this name already exists", func() error {
		return g.store.UpdateProvider(id, req.Name, req.BaseURL, req.Enabled)
	}) {
		return
	}
	updated, _ := g.store.Provider(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": updated.Enabled})
}

func (g *gateway) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if !g.applyMutation(w, actor, "provider.delete", "", func() error { return g.store.DeleteProvider(id) }) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (g *gateway) handleAddProviderKey(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := g.store.Provider(id)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p == nil {
		apiErr(w, http.StatusNotFound, "That provider no longer exists (it may have been deleted)")
		return
	}
	var req struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Key == "" {
		apiErr(w, http.StatusBadRequest, "The upstream API key is required")
		return
	}
	var pk store.ProviderKey
	if !g.applyMutation(w, actor, "provider.key.add", "This upstream key is already on this provider", func() error {
		var err error
		pk, err = g.store.AddProviderKey(id, req.Label, req.Key)
		return err
	}) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"entry": providerKeyJSON{
		ID: pk.ID, Label: pk.Label, SortOrder: pk.SortOrder, CreatedAt: pk.CreatedAt,
	}})
}

func (g *gateway) handlePatchProviderKey(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	keyID, ok := pathID(w, r, "keyId")
	if !ok {
		return
	}
	var req struct {
		Label    *string `json:"label"`
		Disabled *bool   `json:"disabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !g.applyMutation(w, actor, "provider.key.update", "", func() error {
		return g.store.UpdateProviderKey(keyID, req.Label, req.Disabled)
	}) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (g *gateway) handleDeleteProviderKey(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	keyID, ok := pathID(w, r, "keyId")
	if !ok {
		return
	}
	if !g.applyMutation(w, actor, "provider.key.delete", "", func() error {
		return g.store.DeleteProviderKey(keyID)
	}) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleProviderModels serves GET /api/providers/{id}/models — the live,
// enriched catalog of one provider, fetched on demand for the admin GUI.
// Disabled providers are rejected: their pools are not in the registry.
func (g *gateway) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := g.store.GetProvider(id)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p == nil {
		apiErr(w, http.StatusNotFound, "That provider no longer exists (it may have been deleted)")
		return
	}
	ref, inRegistry := g.provider(p.Name)
	if !inRegistry {
		apiErr(w, http.StatusConflict, "Provider is disabled — enable it to fetch its catalog")
		return
	}
	models, err := g.fetchUpstreamModels(ref)
	if err != nil {
		apiErr(w, http.StatusBadGateway, "Catalog fetch failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}
