package settings

import (
	"fmt"
	"maps"
	"strings"
)

// ModelMeta describes a model for agents reading /v1/models. Zen's own
// /models payload is sparse (id only), so per-model facts live here; anything
// missing falls back to the defaults.
type ModelMeta struct {
	ContextWindow   int64    `json:"contextWindow"`
	MaxOutputTokens int64    `json:"maxOutputTokens"`
	Reasoning       bool     `json:"reasoning"`
	ResponsesAPI    bool     `json:"responsesApi"`
	InputModalities []string `json:"inputModalities,omitempty"`
	Description     string   `json:"description,omitempty"`
}

// UAVersionOK validates "opencode/<semver>" against the server's 1.18.0
// floor. Enforced on settings saves and at boot.
func UAVersionOK(ua string) bool {
	var maj, min, pat int
	if n, _ := fmt.Sscanf(ua, "opencode/%d.%d.%d", &maj, &min, &pat); n < 2 {
		return false
	}
	if maj != 1 {
		return maj > 1
	}
	if min != 18 {
		return min > 18
	}
	return pat >= 0
}

// Runtime settings live in the settings table as one JSON document (key
// "runtime_settings"); the admin GUI reads and writes them through
// /api/settings. A missing row means "all defaults" — the defaults here are
// the single source of truth, and unmarshalling happens ONTO a default value
// so absent fields keep it.

const (
	RotationPriority = "priority"
	RotationLRU      = "lru"
)

// RetryConfig bounds the per-request key rotation loop and 429 handling.
type RetryConfig struct {
	MaxKeysPerRequest    int  `json:"maxKeysPerRequest"`
	CooldownSeconds      int  `json:"cooldownSeconds"`
	RespectRetryAfter    bool `json:"respectRetryAfter"`
	MaxRequestsPerKeyDay int  `json:"maxRequestsPerKeyPerDay"` // 0 = off; an exhausted key cools until midnight
}

type AnthropicSettings struct {
	// FallbackModel routes literal "claude-*" requests (background tasks
	// clients self-issue) to one configured "provider/model" target; empty
	// disables the rewrite.
	FallbackModel string `json:"fallbackModel"`
}

// ModelMetaSyncStatus records the outcome of the last models.dev sync (the
// GUI shows it; it is never client-writable via PUT /api/settings).
type ModelMetaSyncStatus struct {
	At      int64  `json:"at"`
	OK      bool   `json:"ok"`
	Added   int    `json:"added"`
	Updated int    `json:"updated"`
	Pruned  int    `json:"pruned"`
	Error   string `json:"error,omitempty"`
}

type ZenSettings struct {
	UserAgent           string               `json:"userAgent"`
	InjectTools         bool                 `json:"injectTools"`
	FreeOnly            bool                 `json:"freeOnly"`
	ModelMeta           map[string]ModelMeta `json:"modelMeta,omitempty"`
	ModelMetaAutoSync   bool                 `json:"modelMetaAutoSync"`
	ModelMetaSyncStatus *ModelMetaSyncStatus `json:"modelMetaSyncStatus,omitempty"`
}

type KiloSettings struct {
	FreeOnly bool `json:"freeOnly"`
}

// ZaiSettings carries the model metadata for Z.ai preset providers. Z.ai's
// own /models payload advertises ids only, so — like Zen — the facts come
// from models.dev, filled by the same sync that refreshes Zen's catalog.
// There is no separate toggle or status: one models.dev fetch, two catalogs.
type ZaiSettings struct {
	ModelMeta map[string]ModelMeta `json:"modelMeta,omitempty"`
}

type RuntimeSettings struct {
	Rotation  string            `json:"rotation"` // "priority" (default) | "lru"
	Retry     RetryConfig       `json:"retry"`
	Anthropic AnthropicSettings `json:"anthropic"`
	Zen       ZenSettings       `json:"zen"`
	Kilo      KiloSettings      `json:"kilo"`
	Zai       ZaiSettings       `json:"zai"`
}

// DefaultRuntimeSettings is the seed for a fresh database and the fallback
// for any field a stored document leaves out.
func DefaultRuntimeSettings() *RuntimeSettings {
	return &RuntimeSettings{
		Rotation: RotationPriority,
		Retry: RetryConfig{
			MaxKeysPerRequest: 3,
			CooldownSeconds:   30,
			RespectRetryAfter: true,
		},
		Anthropic: AnthropicSettings{},
		Zen: ZenSettings{
			UserAgent:   "opencode/1.18.32",
			InjectTools: true,
			ModelMeta:   map[string]ModelMeta{},
		},
		Kilo: KiloSettings{},
		Zai:  ZaiSettings{ModelMeta: map[string]ModelMeta{}},
	}
}

// ApplyDefaults normalizes a document that was just unmarshalled from the
// store or a PUT body: a MISSING rotation falls back to priority (invalid
// values are left in place for Validate to reject, not silently normalized),
// a missing maxKeysPerRequest to 3, and nil collections to empty ones.
// CooldownSeconds is NOT coerced — 0 is meaningful (no default cooldown,
// Retry-After only).
func (rs *RuntimeSettings) ApplyDefaults() *RuntimeSettings {
	if rs.Rotation == "" {
		rs.Rotation = RotationPriority
	}
	if rs.Retry.MaxKeysPerRequest <= 0 {
		rs.Retry.MaxKeysPerRequest = 3
	}
	if rs.Zen.UserAgent == "" {
		rs.Zen.UserAgent = "opencode/1.18.32"
	}
	if rs.Zen.ModelMeta == nil {
		rs.Zen.ModelMeta = map[string]ModelMeta{}
	}
	if rs.Zai.ModelMeta == nil {
		rs.Zai.ModelMeta = map[string]ModelMeta{}
	}
	return rs
}

// Validate returns a human-readable problem with the settings, or "" if they
// are acceptable. The same rules run at PUT time (400) — the database must
// never hold a document boot would refuse.
func (rs *RuntimeSettings) Validate() string {
	if rs.Rotation != RotationPriority && rs.Rotation != RotationLRU {
		return "Rotation must be \"priority\" or \"lru\""
	}
	if rs.Retry.MaxKeysPerRequest < 1 || rs.Retry.MaxKeysPerRequest > 100 {
		return "Max keys per request must be between 1 and 100"
	}
	if rs.Retry.CooldownSeconds < 0 || rs.Retry.CooldownSeconds > 86400 {
		return "Cooldown seconds must be between 0 and 86400"
	}
	if rs.Retry.MaxRequestsPerKeyDay < 0 || rs.Retry.MaxRequestsPerKeyDay > 1_000_000 {
		return "Daily cap must be 0 (off) or between 1 and 1000000"
	}
	if f := rs.Anthropic.FallbackModel; f != "" {
		if parts := strings.Split(f, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Sprintf("Fallback model %q must look like \"provider/model\"", f)
		}
	}
	if !UAVersionOK(rs.Zen.UserAgent) {
		return fmt.Sprintf("Zen user agent %q fails the 1.18.0 floor — the whole pool would get 426s", rs.Zen.UserAgent)
	}
	for id, meta := range rs.Zen.ModelMeta {
		if meta.ContextWindow <= 0 {
			return fmt.Sprintf("Model meta for %q: context window must be a positive integer", id)
		}
		if meta.MaxOutputTokens <= 0 {
			return fmt.Sprintf("Model meta for %q: max output tokens must be a positive integer", id)
		}
	}
	for id, meta := range rs.Zai.ModelMeta {
		if meta.ContextWindow <= 0 {
			return fmt.Sprintf("Z.ai model meta for %q: context window must be a positive integer", id)
		}
		if meta.MaxOutputTokens <= 0 {
			return fmt.Sprintf("Z.ai model meta for %q: max output tokens must be a positive integer", id)
		}
	}
	return ""
}

// Clone deep-copies the mutable collections so callers can freely derive
// from a snapshot without racing the next reader.
func (rs *RuntimeSettings) Clone() *RuntimeSettings {
	out := *rs
	out.Zen.ModelMeta = make(map[string]ModelMeta, len(rs.Zen.ModelMeta))
	maps.Copy(out.Zen.ModelMeta, rs.Zen.ModelMeta)
	out.Zai.ModelMeta = make(map[string]ModelMeta, len(rs.Zai.ModelMeta))
	maps.Copy(out.Zai.ModelMeta, rs.Zai.ModelMeta)
	return &out
}
