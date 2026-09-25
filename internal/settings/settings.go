package settings

import (
	"fmt"
	"maps"
	"net/url"
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

// WebToolsSettings configures the /mcp endpoint and its two self-hosted
// backends: SearXNG (search) and the obscura headless browser (JS rendering
// leg of web_read). SearXNG is the only search backend; the native Go fetch
// is always the first or fallback reader leg depending on ReaderMode.
type WebToolsSettings struct {
	Enabled               bool   `json:"enabled"`
	SearXNGURL            string `json:"searxngUrl"`
	ReaderMode            string `json:"readerMode"` // ReaderFast (default) | ReaderRender
	ObscuraPath           string `json:"obscuraPath"`
	ObscuraStealth        bool   `json:"obscuraStealth"`
	ObscuraConcurrency    int    `json:"obscuraConcurrency"`
	FetchTimeoutSeconds   int    `json:"fetchTimeoutSeconds"`
	ObscuraTimeoutSeconds int    `json:"obscuraTimeoutSeconds"`
	MaxChars              int    `json:"maxChars"`
}

const (
	ReaderFast        = "fast"   // native fetch first, escalate to obscura
	ReaderRender      = "render" // obscura first, fall back to native
	DefaultSearXNGURL = "http://127.0.0.1:8888"
)

type RuntimeSettings struct {
	Rotation  string            `json:"rotation"` // "priority" (default) | "lru"
	Retry     RetryConfig       `json:"retry"`
	Anthropic AnthropicSettings `json:"anthropic"`
	Zen       ZenSettings       `json:"zen"`
	Kilo      KiloSettings      `json:"kilo"`
	Zai       ZaiSettings       `json:"zai"`
	WebTools  WebToolsSettings  `json:"webTools"`
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
		WebTools: WebToolsSettings{
			// Enabled stays false: web tools are opt-in so an upgrade never
			// implies a working SearXNG/obscura install.
			SearXNGURL:            DefaultSearXNGURL,
			ReaderMode:            ReaderFast,
			ObscuraPath:           "obscura",
			ObscuraConcurrency:    2,
			FetchTimeoutSeconds:   15,
			ObscuraTimeoutSeconds: 30,
			MaxChars:              20000,
		},
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
	wt := &rs.WebTools
	if wt.SearXNGURL == "" {
		wt.SearXNGURL = DefaultSearXNGURL
	}
	if wt.ReaderMode == "" {
		wt.ReaderMode = ReaderFast
	}
	if wt.ObscuraPath == "" {
		wt.ObscuraPath = "obscura"
	}
	if wt.ObscuraConcurrency <= 0 {
		wt.ObscuraConcurrency = 2
	}
	if wt.FetchTimeoutSeconds <= 0 {
		wt.FetchTimeoutSeconds = 15
	}
	if wt.ObscuraTimeoutSeconds <= 0 {
		wt.ObscuraTimeoutSeconds = 30
	}
	if wt.MaxChars <= 0 {
		wt.MaxChars = 20000
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
	wt := rs.WebTools
	if wt.ReaderMode != ReaderFast && wt.ReaderMode != ReaderRender {
		return fmt.Sprintf("Web tools reader mode %q must be %q or %q", wt.ReaderMode, ReaderFast, ReaderRender)
	}
	if u, err := url.Parse(wt.SearXNGURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Sprintf("SearXNG URL %q must be an http(s) URL with a host", wt.SearXNGURL)
	}
	if wt.ObscuraConcurrency < 1 || wt.ObscuraConcurrency > 8 {
		return "Obscura concurrency must be between 1 and 8"
	}
	if wt.FetchTimeoutSeconds < 1 || wt.FetchTimeoutSeconds > 120 {
		return "Fetch timeout must be between 1 and 120 seconds"
	}
	if wt.ObscuraTimeoutSeconds < 5 || wt.ObscuraTimeoutSeconds > 300 {
		return "Obscura timeout must be between 5 and 300 seconds"
	}
	if wt.MaxChars < 1000 || wt.MaxChars > 200000 {
		return "Max chars must be between 1000 and 200000"
	}
	// ObscuraPath is exec'd (argv-only, never through a shell) — whitespace
	// or control characters would mean a broken path, not an injection, but
	// reject them early with a clear message.
	if wt.ObscuraPath == "" || strings.ContainsAny(wt.ObscuraPath, " \t\r\n\x00") {
		return "Obscura path must be a non-empty path without whitespace"
	}
	return ""
}

// Clone deep-copies the mutable collections so callers can freely derive
// from a snapshot without racing the next reader. WebTools needs no deep
// copy: every field is a value type.
func (rs *RuntimeSettings) Clone() *RuntimeSettings {
	out := *rs
	out.Zen.ModelMeta = make(map[string]ModelMeta, len(rs.Zen.ModelMeta))
	maps.Copy(out.Zen.ModelMeta, rs.Zen.ModelMeta)
	out.Zai.ModelMeta = make(map[string]ModelMeta, len(rs.Zai.ModelMeta))
	maps.Copy(out.Zai.ModelMeta, rs.Zai.ModelMeta)
	return &out
}
