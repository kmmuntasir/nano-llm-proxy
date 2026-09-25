package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// store.Provider management endpoints (superadmin only). GUI-added providers are
// always type "openai"; the one builtin (zen=opencode) is editable but can't
// be renamed or deleted. Preset providers carry a `preset` id from the curated
// registry (providerspec.go) — set at creation, never patched.

var providerNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Upstream key labels are mandatory — they're how a pool of otherwise
// identical secrets stays identifiable in the GUI.
const maxKeyLabelLen = 64

// sanitizeKeyLabel trims and validates an upstream key label.
func sanitizeKeyLabel(label string) (string, bool) {
	label = strings.TrimSpace(label)
	return label, label != "" && len(label) <= maxKeyLabelLen
}

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
		ID               int64    `json:"id"`
		Name             string   `json:"name"`
		Type             string   `json:"type"`
		BaseURL          string   `json:"baseUrl"`
		AnthropicBaseURL string   `json:"anthropicBaseUrl"`
		Enabled          bool     `json:"enabled"`
		Builtin          bool     `json:"builtin"`
		Preset           string   `json:"preset"`
		SortOrder        int64    `json:"sortOrder"`
		Healthy          int      `json:"healthy"`
		Total            int      `json:"total"`
		Keys             []pkJSON `json:"keys"`
	}
	out := make([]provJSON, 0, len(provs))
	for i, p := range provs {
		pj := provJSON{
			ID: p.ID, Name: p.Name, Type: p.Type, BaseURL: p.BaseURL,
			AnthropicBaseURL: p.AnthropicBaseURL,
			Enabled:          p.Enabled, Builtin: p.Builtin, Preset: p.Preset,
			SortOrder: p.SortOrder,
			Keys:      []pkJSON{},
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

// handleListPresets serves GET /api/providers/presets — the curated registry
// behind the GUI's "Add Provider" dropdown.
func (g *gateway) handleListPresets(w http.ResponseWriter, r *http.Request) {
	presets := make([]presetSpec, len(presetRegistry))
	copy(presets, presetRegistry)
	writeJSON(w, http.StatusOK, map[string]any{"presets": presets})
}

func (g *gateway) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	var req struct {
		Name             string `json:"name"`
		Preset           string `json:"preset"`
		BaseURL          string `json:"baseUrl"`
		AnthropicBaseURL string `json:"anthropicBaseUrl"`
		Keys             []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
		} `json:"keys"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	name := req.Name
	baseURL := strings.TrimSpace(req.BaseURL)
	anthropicURL := strings.TrimSpace(req.AnthropicBaseURL)
	// A preset fills in whatever the caller left blank: the name (defaults to
	// the preset id) and the endpoint roots. Explicit values win, so preset
	// endpoints can still be pointed at a proxy or a custom deployment.
	var spec presetSpec
	if preset := strings.TrimSpace(req.Preset); preset != "" {
		var ok bool
		spec, ok = lookupPreset(preset)
		if !ok {
			apiErr(w, http.StatusBadRequest, "Unknown provider preset "+strconv.Quote(preset)+" — see GET /api/providers/presets")
			return
		}
		if name == "" {
			name = spec.ID
		}
		if baseURL == "" {
			baseURL = spec.BaseURL
		}
		if anthropicURL == "" {
			anthropicURL = spec.AnthropicBaseURL
		}
	}
	if !providerNameRe.MatchString(name) {
		apiErr(w, http.StatusBadRequest, "Provider name must be 1-32 characters of lowercase letters, digits, or dashes (it becomes the model prefix)")
		return
	}
	if name == "zen" {
		apiErr(w, http.StatusConflict, "That name is reserved (zen is built in)")
		return
	}
	if baseURL == "" && anthropicURL == "" {
		apiErr(w, http.StatusBadRequest, "At least one endpoint is required: baseUrl (OpenAI-compatible root) and/or anthropicBaseUrl (Anthropic-compatible root)")
		return
	}
	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			apiErr(w, http.StatusBadRequest, "baseUrl must be an absolute http(s) URL (OpenAI-compatible root, e.g. https://host/v1)")
			return
		}
	}
	if anthropicURL != "" {
		u, err := url.Parse(anthropicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			apiErr(w, http.StatusBadRequest, "anthropicBaseUrl must be an absolute http(s) URL (Anthropic-compatible root, e.g. https://api.z.ai/api/anthropic)")
			return
		}
	}
	if len(req.Keys) == 0 {
		apiErr(w, http.StatusBadRequest, "At least one upstream API key is required")
		return
	}
	p := &store.Provider{Name: name, Type: "openai", BaseURL: baseURL, AnthropicBaseURL: anthropicURL, Enabled: true, Preset: spec.ID, SortOrder: 100}
	pks := make([]store.ProviderKey, 0, len(req.Keys))
	for i, k := range req.Keys {
		key := strings.TrimSpace(k.Key)
		if key == "" {
			apiErr(w, http.StatusBadRequest, fmt.Sprintf("keys[%d]: the upstream API key is required", i))
			return
		}
		label, ok := sanitizeKeyLabel(k.Label)
		if !ok {
			apiErr(w, http.StatusBadRequest, fmt.Sprintf("keys[%d]: a label is required for every upstream API key (1-%d characters)", i, maxKeyLabelLen))
			return
		}
		pks = append(pks, store.ProviderKey{Key: key, Label: label})
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
		Name             *string `json:"name"`
		BaseURL          *string `json:"baseUrl"`
		AnthropicBaseURL *string `json:"anthropicBaseUrl"`
		Enabled          *bool   `json:"enabled"`
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
		// empty = clear the OpenAI root (allowed while the other stays)
		*req.BaseURL = strings.TrimSpace(*req.BaseURL)
		if *req.BaseURL != "" {
			u, err := url.Parse(*req.BaseURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				apiErr(w, http.StatusBadRequest, "baseUrl must be an absolute http(s) URL")
				return
			}
		}
	}
	if req.AnthropicBaseURL != nil {
		*req.AnthropicBaseURL = strings.TrimSpace(*req.AnthropicBaseURL)
		if *req.AnthropicBaseURL != "" {
			u, err := url.Parse(*req.AnthropicBaseURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				apiErr(w, http.StatusBadRequest, "anthropicBaseUrl must be an absolute http(s) URL")
				return
			}
		}
	}
	// at least one endpoint must remain after the patch
	effectiveOpenAI := p.BaseURL
	if req.BaseURL != nil {
		effectiveOpenAI = *req.BaseURL
	}
	effectiveAnthropic := p.AnthropicBaseURL
	if req.AnthropicBaseURL != nil {
		effectiveAnthropic = *req.AnthropicBaseURL
	}
	if effectiveOpenAI == "" && effectiveAnthropic == "" {
		apiErr(w, http.StatusBadRequest, "Cannot clear both endpoints — a provider needs baseUrl and/or anthropicBaseUrl")
		return
	}
	if !g.applyMutation(w, actor, "provider.update", "A provider with this name already exists", func() error {
		return g.store.UpdateProvider(id, req.Name, req.BaseURL, req.AnthropicBaseURL, req.Enabled)
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
	label, ok := sanitizeKeyLabel(req.Label)
	if !ok {
		apiErr(w, http.StatusBadRequest, fmt.Sprintf("A label is required for every upstream API key (1-%d characters)", maxKeyLabelLen))
		return
	}
	var pk store.ProviderKey
	if !g.applyMutation(w, actor, "provider.key.add", "This upstream key is already on this provider", func() error {
		var err error
		pk, err = g.store.AddProviderKey(id, label, req.Key)
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
	// labels are mandatory: a patch may rename a key but never blank it
	if req.Label != nil {
		label, ok := sanitizeKeyLabel(*req.Label)
		if !ok {
			apiErr(w, http.StatusBadRequest, fmt.Sprintf("A label is required for every upstream API key (1-%d characters)", maxKeyLabelLen))
			return
		}
		req.Label = &label
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

// handleProviderKeyUsage serves GET /api/providers/{id}/keys/{keyId}/usage —
// fetches the key's live quota/usage from the provider's per-key usage
// endpoint (Z.ai coding plans: plan tier, 5-hour and weekly windows) and
// passes the upstream data object through verbatim. On demand only — the GUI
// calls this per key when the user opens the usage modal, never on a timer.
func (g *gateway) handleProviderKeyUsage(w http.ResponseWriter, r *http.Request) {
	provID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	keyID, ok := pathID(w, r, "keyId")
	if !ok {
		return
	}
	p, err := g.store.Provider(provID)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p == nil {
		apiErr(w, http.StatusNotFound, "That provider no longer exists (it may have been deleted)")
		return
	}
	usageURL := presetUsageURL(p.Preset)
	if usageURL == "" {
		apiErr(w, http.StatusBadRequest, "Usage lookup is not available for this provider (it has no per-key usage endpoint)")
		return
	}
	keys, err := g.store.ListProviderKeys(provID)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var key string
	for _, k := range keys {
		if k.ID == keyID {
			key = k.Key
			break
		}
	}
	if key == "" {
		apiErr(w, http.StatusNotFound, "That key is not on this provider")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Z.ai quirk: the raw token, NO "Bearer " prefix
	req.Header.Set("Authorization", key)
	req.Header.Set("Accept-Language", "en-US,en")
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		apiErr(w, http.StatusBadGateway, "Usage fetch failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var parsed struct {
		Success bool            `json:"success"`
		Msg     string          `json:"msg"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		apiErr(w, http.StatusBadGateway, fmt.Sprintf("Usage fetch: upstream HTTP %d with an unparseable body", resp.StatusCode))
		return
	}
	if !parsed.Success {
		msg := parsed.Msg
		if msg == "" {
			msg = fmt.Sprintf("upstream HTTP %d", resp.StatusCode)
		}
		apiErr(w, http.StatusBadGateway, "Usage fetch failed: "+msg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(parsed.Data)
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
