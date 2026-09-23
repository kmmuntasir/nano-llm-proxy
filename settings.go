package main

import (
	"fmt"
	"maps"
	"strings"
)

// Runtime settings live in the settings table as one JSON document (key
// "runtime_settings"); the admin GUI reads and writes them through
// /api/settings. A missing row means "all defaults" — the defaults here are
// the single source of truth, and unmarshalling happens ONTO a default value
// so absent fields keep it.

const (
	rotationPriority = "priority"
	rotationLRU      = "lru"
)

// RetryConfig bounds the per-request key rotation loop and 429 handling.
type RetryConfig struct {
	MaxKeysPerRequest    int  `json:"maxKeysPerRequest"`
	CooldownSeconds      int  `json:"cooldownSeconds"`
	RespectRetryAfter    bool `json:"respectRetryAfter"`
	MaxRequestsPerKeyDay int  `json:"maxRequestsPerKeyPerDay"` // 0 = off; an exhausted key cools until midnight
}

type AnthropicSettings struct {
	Aliases map[string]string `json:"aliases"` // "claude-sonnet-5" -> "zen/mimo-..." on /v1/messages
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
	ResponsesModels     []string             `json:"responsesModels"`
	FreeOnly            bool                 `json:"freeOnly"`
	ModelMeta           map[string]ModelMeta `json:"modelMeta,omitempty"`
	ModelMetaAutoSync   bool                 `json:"modelMetaAutoSync"`
	ModelMetaSyncStatus *ModelMetaSyncStatus `json:"modelMetaSyncStatus,omitempty"`
}

type KiloSettings struct {
	FreeOnly bool `json:"freeOnly"`
}

type RuntimeSettings struct {
	Rotation  string            `json:"rotation"` // "priority" (default) | "lru"
	Retry     RetryConfig       `json:"retry"`
	Anthropic AnthropicSettings `json:"anthropic"`
	Zen       ZenSettings       `json:"zen"`
	Kilo      KiloSettings      `json:"kilo"`
}

// DefaultRuntimeSettings is the seed for a fresh database and the fallback
// for any field a stored document leaves out.
func DefaultRuntimeSettings() *RuntimeSettings {
	return &RuntimeSettings{
		Rotation: rotationPriority,
		Retry: RetryConfig{
			MaxKeysPerRequest: 3,
			CooldownSeconds:   30,
			RespectRetryAfter: true,
		},
		Anthropic: AnthropicSettings{Aliases: map[string]string{}},
		Zen: ZenSettings{
			UserAgent:       "opencode/1.18.32",
			InjectTools:     true,
			ResponsesModels: []string{},
			ModelMeta:       map[string]ModelMeta{},
		},
		Kilo: KiloSettings{},
	}
}

// applyDefaults normalizes a document that was just unmarshalled from the
// store or a PUT body: a MISSING rotation falls back to priority (invalid
// values are left in place for Validate to reject, not silently normalized),
// a missing maxKeysPerRequest to 3, and nil collections to empty ones.
// CooldownSeconds is NOT coerced — 0 is meaningful (no default cooldown,
// Retry-After only).
func (rs *RuntimeSettings) applyDefaults() *RuntimeSettings {
	if rs.Rotation == "" {
		rs.Rotation = rotationPriority
	}
	if rs.Retry.MaxKeysPerRequest <= 0 {
		rs.Retry.MaxKeysPerRequest = 3
	}
	if rs.Anthropic.Aliases == nil {
		rs.Anthropic.Aliases = map[string]string{}
	}
	if rs.Zen.ResponsesModels == nil {
		rs.Zen.ResponsesModels = []string{}
	}
	if rs.Zen.UserAgent == "" {
		rs.Zen.UserAgent = "opencode/1.18.32"
	}
	if rs.Zen.ModelMeta == nil {
		rs.Zen.ModelMeta = map[string]ModelMeta{}
	}
	return rs
}

// Validate returns a human-readable problem with the settings, or "" if they
// are acceptable. The same rules run at PUT time (400) — the database must
// never hold a document boot would refuse.
func (rs *RuntimeSettings) Validate() string {
	if rs.Rotation != rotationPriority && rs.Rotation != rotationLRU {
		return fmt.Sprintf("rotation must be %q or %q", rotationPriority, rotationLRU)
	}
	if rs.Retry.MaxKeysPerRequest < 1 || rs.Retry.MaxKeysPerRequest > 100 {
		return "retry.maxKeysPerRequest must be between 1 and 100"
	}
	if rs.Retry.CooldownSeconds < 0 || rs.Retry.CooldownSeconds > 86400 {
		return "retry.cooldownSeconds must be between 0 and 86400"
	}
	if rs.Retry.MaxRequestsPerKeyDay < 0 || rs.Retry.MaxRequestsPerKeyDay > 1_000_000 {
		return "retry.maxRequestsPerKeyPerDay must be 0 (off) or between 1 and 1000000"
	}
	for alias, target := range rs.Anthropic.Aliases {
		if strings.TrimSpace(alias) == "" {
			return "anthropic.aliases keys must be non-empty"
		}
		if parts := strings.Split(target, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Sprintf("anthropic.aliases[%q]: %q must look like \"provider/model\"", alias, target)
		}
	}
	if !uaVersionOK(rs.Zen.UserAgent) {
		return fmt.Sprintf("zen.userAgent %q fails the 1.18.0 floor — the whole pool would 426", rs.Zen.UserAgent)
	}
	for _, m := range rs.Zen.ResponsesModels {
		if strings.TrimSpace(m) == "" {
			return "zen.responsesModels entries must be non-empty"
		}
	}
	for id, meta := range rs.Zen.ModelMeta {
		if meta.ContextWindow <= 0 {
			return fmt.Sprintf("zen.modelMeta[%q].contextWindow must be a positive integer", id)
		}
		if meta.MaxOutputTokens <= 0 {
			return fmt.Sprintf("zen.modelMeta[%q].maxOutputTokens must be a positive integer", id)
		}
	}
	return ""
}

// Clone deep-copies the mutable collections so callers can freely derive
// from a snapshot without racing the next reader.
func (rs *RuntimeSettings) Clone() *RuntimeSettings {
	out := *rs
	out.Anthropic.Aliases = make(map[string]string, len(rs.Anthropic.Aliases))
	maps.Copy(out.Anthropic.Aliases, rs.Anthropic.Aliases)
	out.Zen.ResponsesModels = append([]string(nil), rs.Zen.ResponsesModels...)
	out.Zen.ModelMeta = make(map[string]ModelMeta, len(rs.Zen.ModelMeta))
	maps.Copy(out.Zen.ModelMeta, rs.Zen.ModelMeta)
	return &out
}
