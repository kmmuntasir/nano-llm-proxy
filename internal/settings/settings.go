package settings

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
)

// ReasoningOption is one entry of a model's reasoning_options list, mirroring
// the models.dev shape an agent (oh-my-pi) expects:
//
//	{"type": "effort", "values": ["low", "medium", "high"]}
//	{"type": "toggle"}
//	{"type": "budget_tokens", "min": 1024, "max": 32768}
//
// Only "type" is always present; the rest depend on it. The effort ladder is
// what lets an agent map its own reasoning-level control onto the values the
// model actually accepts, so it is stored rather than flattened to the
// "reasoning" boolean.
type ReasoningOption struct {
	Type   string   `json:"type"`
	Values []string `json:"values,omitempty"`
	Min    *int64   `json:"min,omitempty"`
	Max    *int64   `json:"max,omitempty"`
}

// ModelCost is models.dev's price for a model. Zero input and output is the
// catalog's statement that the model is free — the fact FreeOnly filtering
// needs, because a free model need not carry the "-free" marker in its id
// (big-pickle does not). A nil pointer means the catalog doesn't price this
// model, which is different from "priced at zero".
type ModelCost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// Free reports whether the catalog priced this model as free.
func (c *ModelCost) Free() bool {
	return c != nil && c.Input == 0 && c.Output == 0
}

// ModelMeta describes a model for agents reading /v1/models. Zen's own
// /models payload is sparse (id only), so per-model facts live here; anything
// missing falls back to the defaults.
type ModelMeta struct {
	ContextWindow    int64             `json:"contextWindow"`
	MaxOutputTokens  int64             `json:"maxOutputTokens"`
	Reasoning        bool              `json:"reasoning"`
	ResponsesAPI     bool              `json:"responsesApi"`
	InputModalities  []string          `json:"inputModalities,omitempty"`
	Description      string            `json:"description,omitempty"`
	ReasoningOptions []ReasoningOption `json:"reasoningOptions,omitempty"`
	Cost             *ModelCost        `json:"cost,omitempty"`
}

// DefaultZenUserAgent mirrors the opencode release the zen fingerprint was
// last captured from (see internal/gateway/zenfp.go).
const DefaultZenUserAgent = "opencode/latest/2.0.29/cli"

// UAVersionOK validates an opencode user agent against the server's 1.18.0
// floor. Enforced on settings saves and at boot. Accepts both the 1.x shape
// "opencode/<semver>" and the 2.x shape "opencode/<channel>/<semver>/<client>"
// (e.g. "opencode/latest/2.0.29/cli").
func UAVersionOK(ua string) bool {
	rest, ok := strings.CutPrefix(ua, "opencode/")
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 1:
		rest = parts[0]
	case 3:
		if parts[0] == "" || parts[2] == "" {
			return false
		}
		rest = parts[1]
	default:
		return false
	}
	var maj, min, pat int
	if n, _ := fmt.Sscanf(rest, "%d.%d.%d", &maj, &min, &pat); n < 2 {
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

// Rotation modes. RotationPriorityDaily is the default: priority's
// stick-with-failover, with the starting key advancing one position per day
// so a pool's keys share the load over time instead of the first key
// carrying everything.
const (
	RotationPriorityDaily = "priority-daily"
	RotationPriority      = "priority"
	RotationLRU           = "lru"
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
	// ModelMeta carries the per-model reasoning facts the models.dev sync
	// resolves for Kilo's catalog. Kilo's own /models is rich (limits,
	// modalities, supported_parameters come from upstream and win), but it
	// says nothing about reasoning levels, so those come from the catalog —
	// same sync, same shape as zen/zai.
	ModelMeta map[string]ModelMeta `json:"modelMeta,omitempty"`
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
	Rotation  string            `json:"rotation"` // "priority-daily" (default) | "priority" | "lru"
	Retry     RetryConfig       `json:"retry"`
	Anthropic AnthropicSettings `json:"anthropic"`
	Zen       ZenSettings       `json:"zen"`
	Kilo      KiloSettings      `json:"kilo"`
	Zai       ZaiSettings       `json:"zai"`
	WebTools  WebToolsSettings  `json:"webTools"`
	Auth      AuthSettings      `json:"auth"`
	NewUsers  NewUserSettings   `json:"newUsers"`
	Local     LocalSettings     `json:"local"`
}

// LocalSettings only matter in local mode (one person's machine, no login).
// RequireAPIKey off (the default) lets any local program call /v1 and /mcp
// with no key or a placeholder; on, agents need a real client key exactly as
// in server mode. Server mode ignores the section.
type LocalSettings struct {
	RequireAPIKey bool `json:"requireApiKey"`
}

// LocalSearXNGURL is where the local-mode installer runs its per-user
// SearXNG (8899 rather than SearXNG's usual 8888, which Jupyter and the
// server install already claim on many machines).
const LocalSearXNGURL = "http://127.0.0.1:8899"

// DefaultLocalRuntimeSettings seeds a fresh local-mode database: the same
// defaults, with web tools on and pointed at the installer's SearXNG.
func DefaultLocalRuntimeSettings() *RuntimeSettings {
	rs := DefaultRuntimeSettings()
	rs.WebTools.Enabled = true
	rs.WebTools.SearXNGURL = LocalSearXNGURL
	return rs
}

// NewUserSettings holds the defaults applied once, when an admin creates an
// account with the "user" role. Existing users are never touched by a change
// here — per-user access stays the Users page's job.
type NewUserSettings struct {
	// DisabledProviderIDs are the providers a new regular user starts
	// without (a user_provider_access deny row each). Empty = every
	// provider. Always serialized as [] (never null) so the GUI can treat it
	// as a list. Ids of providers deleted since are tolerated: the gateway
	// prunes them on read and save, and user creation skips them.
	DisabledProviderIDs []int64 `json:"disabledProviderIds"`
}

// AuthSettings controls GUI sign-in methods. Google sign-in is on whenever
// the operator configured it (GOOGLE_CLIENT_ID, a bootstrap value); only
// password sign-in is switchable here. The gateway treats CredentialLogin as
// true whenever Google isn't configured, so a stored false can never lock
// everyone out.
type AuthSettings struct {
	CredentialLogin bool `json:"credentialLogin"`
}

// DefaultRuntimeSettings is the seed for a fresh database and the fallback
// for any field a stored document leaves out.
func DefaultRuntimeSettings() *RuntimeSettings {
	return &RuntimeSettings{
		Rotation: RotationPriorityDaily,
		Auth:     AuthSettings{CredentialLogin: true},
		Retry: RetryConfig{
			MaxKeysPerRequest: 3,
			CooldownSeconds:   30,
			RespectRetryAfter: true,
		},
		Anthropic: AnthropicSettings{},
		Zen: ZenSettings{
			UserAgent:   DefaultZenUserAgent,
			InjectTools: true,
			ModelMeta:   map[string]ModelMeta{},
		},
		Kilo:     KiloSettings{},
		Zai:      ZaiSettings{ModelMeta: map[string]ModelMeta{}},
		NewUsers: NewUserSettings{DisabledProviderIDs: []int64{}},
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
// store or a PUT body: a MISSING rotation falls back to priority-daily (invalid
// values are left in place for Validate to reject, not silently normalized),
// a missing maxKeysPerRequest to 3, and nil collections to empty ones.
// CooldownSeconds is NOT coerced — 0 is meaningful (no default cooldown,
// Retry-After only).
func (rs *RuntimeSettings) ApplyDefaults() *RuntimeSettings {
	if rs.Rotation == "" {
		rs.Rotation = RotationPriorityDaily
	}
	if rs.Retry.MaxKeysPerRequest <= 0 {
		rs.Retry.MaxKeysPerRequest = 3
	}
	if rs.Zen.UserAgent == "" {
		rs.Zen.UserAgent = DefaultZenUserAgent
	}
	if rs.Zen.ModelMeta == nil {
		rs.Zen.ModelMeta = map[string]ModelMeta{}
	}
	if rs.Zai.ModelMeta == nil {
		rs.Zai.ModelMeta = map[string]ModelMeta{}
	}
	// sorted + de-duplicated so the stored document is canonical; a
	// non-positive id survives this for Validate to reject
	ids := slices.Clone(rs.NewUsers.DisabledProviderIDs)
	if ids == nil {
		ids = []int64{}
	}
	slices.Sort(ids)
	rs.NewUsers.DisabledProviderIDs = slices.Compact(ids)
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
	switch rs.Rotation {
	case RotationPriorityDaily, RotationPriority, RotationLRU:
	default:
		return "Rotation must be \"priority-daily\", \"priority\" or \"lru\""
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
	for _, id := range rs.NewUsers.DisabledProviderIDs {
		if id <= 0 {
			return fmt.Sprintf("New-user disabled provider id %d must be a positive integer", id)
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
	out.NewUsers.DisabledProviderIDs = slices.Clone(rs.NewUsers.DisabledProviderIDs)
	if out.NewUsers.DisabledProviderIDs == nil {
		out.NewUsers.DisabledProviderIDs = []int64{}
	}
	return &out
}
