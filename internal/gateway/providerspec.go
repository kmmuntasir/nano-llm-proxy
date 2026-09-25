package gateway

// Curated provider presets: the GUI's "Add Provider" dropdown. Each preset is
// a release-managed endpoint pair plus, where a catalog needs special
// handling, a per-preset enrichment hook. Adding a provider from a preset
// records the preset id on the provider row — the identity future
// per-provider features (usage tracking, rate-limit windows) will key on.
//
// Endpoints verified against each provider's official documentation.

// catalogHook transforms one raw upstream catalog entry into the gateway's
// enriched shape (mutating entry in place). Returning false drops the entry.
// A hook that rewrites entry["id"] applies the suffixed-ID treatment itself.
// Raw entries carry the upstream's own field spellings (context_length,
// top_provider, architecture, isFree, ...).
type catalogHook func(g *gateway, ref providerRef, id string, entry, raw map[string]any) bool

type presetSpec struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// OpenAI-compatible root ("/models" and "/chat/completions" are appended).
	// "" = the provider has no OpenAI surface.
	BaseURL string `json:"openaiBaseUrl"`
	// Anthropic-compatible root ("/v1/messages" is appended). "" = no native
	// Anthropic surface.
	AnthropicBaseURL string `json:"anthropicBaseUrl"`
	DocsURL          string `json:"-"`

	// catalog is the per-entry enrichment hook; nil = generic passthrough
	// (context_length + description only).
	catalog catalogHook
	// suffixed marks providers whose advertised ids carry the cosmetic
	// context/modality suffix that every endpoint must strip.
	suffixed bool
}

// kiloCatalog is kilo's per-entry hook: Kilo's catalog is rich — map the
// fields agents read. Relocated verbatim from the former builtin branch of
// decodeModelList.
func kiloCatalog(g *gateway, ref providerRef, id string, entry, raw map[string]any) bool {
	if g.rs().Kilo.FreeOnly {
		if free, _ := raw["isFree"].(bool); !free {
			return false
		}
	}
	var mods []string
	if cw, ok := raw["context_length"].(float64); ok {
		entry["context_window"] = int64(cw)
	}
	if tp, ok := raw["top_provider"].(map[string]any); ok {
		if mc, ok := tp["max_completion_tokens"].(float64); ok {
			entry["max_output_tokens"] = int64(mc)
		}
	}
	if arch, ok := raw["architecture"].(map[string]any); ok {
		if in, ok := arch["input_modalities"].([]any); ok {
			for _, v := range in {
				if s, ok := v.(string); ok {
					mods = append(mods, s)
				}
			}
			entry["input_modalities"] = in
		}
		if sp, ok := raw["output_modalities"]; ok {
			entry["output_modalities"] = sp
		}
	}
	if sp, ok := raw["supported_parameters"]; ok {
		entry["supported_parameters"] = sp
	}
	if d, ok := raw["description"].(string); ok {
		entry["description"] = d
	}
	if free, ok := raw["isFree"].(bool); ok {
		entry["free"] = free
	}
	cw, _ := entry["context_window"].(int64)
	entry["id"] = ref.name + "/" + buildSuffixedID(id, cw, mods)
	return true
}

// presetRegistry lists the curated presets, ordered by label for the GUI
// dropdown. Keep it sorted — a test enforces it.
//
// OpenRouter's Anthropic root deliberately has no /v1: fetchModelsAnthropic
// appends "/v1/models" and "/v1/messages", so the bare /api root is correct.
var presetRegistry = []presetSpec{
	{
		ID:               "anthropic",
		Label:            "Anthropic",
		AnthropicBaseURL: "https://api.anthropic.com",
		DocsURL:          "https://docs.anthropic.com/en/api/getting-started",
	},
	{
		ID:               "deepseek",
		Label:            "DeepSeek",
		BaseURL:          "https://api.deepseek.com/v1",
		AnthropicBaseURL: "https://api.deepseek.com/anthropic",
		DocsURL:          "https://api-docs.deepseek.com",
	},
	{
		ID:      "groq",
		Label:   "Groq",
		BaseURL: "https://api.groq.com/openai/v1",
		DocsURL: "https://console.groq.com/docs/openai",
	},
	{
		ID:       "kilo",
		Label:    "Kilo",
		BaseURL:  "https://api.kilo.ai/api/gateway/v1",
		DocsURL:  "https://kilo.ai/docs",
		catalog:  kiloCatalog,
		suffixed: true,
	},
	{
		ID:               "kimi",
		Label:            "Kimi (Moonshot)",
		BaseURL:          "https://api.moonshot.ai/v1",
		AnthropicBaseURL: "https://api.moonshot.ai/anthropic",
		DocsURL:          "https://platform.moonshot.ai/docs",
	},
	{
		ID:               "minimax",
		Label:            "MiniMax",
		BaseURL:          "https://api.minimax.io/v1",
		AnthropicBaseURL: "https://api.minimax.io/anthropic",
		DocsURL:          "https://platform.minimax.io/docs",
	},
	{
		ID:      "openai",
		Label:   "OpenAI",
		BaseURL: "https://api.openai.com/v1",
		DocsURL: "https://platform.openai.com/docs",
	},
	{
		ID:               "openrouter",
		Label:            "OpenRouter",
		BaseURL:          "https://openrouter.ai/api/v1",
		AnthropicBaseURL: "https://openrouter.ai/api", // no /v1 — see note above
		DocsURL:          "https://openrouter.ai/docs",
	},
	{
		ID:      "xai",
		Label:   "xAI (Grok)",
		BaseURL: "https://api.x.ai/v1",
		DocsURL: "https://docs.x.ai",
	},
	{
		ID:               "zai",
		Label:            "Z.ai (GLM Coding Plan)",
		BaseURL:          "https://api.z.ai/api/coding/paas/v4",
		AnthropicBaseURL: "https://api.z.ai/api/anthropic",
		DocsURL:          "https://docs.z.ai",
	},
}

func lookupPreset(id string) (presetSpec, bool) {
	for _, p := range presetRegistry {
		if p.ID == id {
			return p, true
		}
	}
	return presetSpec{}, false
}

// presetCatalogHook returns the preset's per-entry catalog hook, or nil for
// generic passthrough (custom providers, and presets without special needs).
func presetCatalogHook(preset string) catalogHook {
	p, ok := lookupPreset(preset)
	if !ok {
		return nil
	}
	return p.catalog
}

// presetSuffixed reports whether the preset advertises suffixed catalog ids.
func presetSuffixed(preset string) bool {
	p, ok := lookupPreset(preset)
	return ok && p.suffixed
}

// suffixedCatalog reports whether this provider's advertised ids carry the
// cosmetic context/modality suffix that must be stripped before proxying:
// builtins (zen) and suffixed presets (kilo) yes, everything else no.
func (r providerRef) suffixedCatalog() bool {
	return r.builtin || presetSuffixed(r.preset)
}
