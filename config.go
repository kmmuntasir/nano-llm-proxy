package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
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

type ZenProviderConfig struct {
	BaseURL         string               `json:"baseUrl"`
	UserAgent       string               `json:"userAgent"`
	InjectTools     bool                 `json:"injectTools"`
	ResponsesModels []string             `json:"responsesModels"`
	FreeOnly        bool                 `json:"freeOnly"`
	ModelMeta       map[string]ModelMeta `json:"modelMeta,omitempty"`
}

type KiloProviderConfig struct {
	BaseURL  string `json:"baseUrl"`
	FreeOnly bool   `json:"freeOnly"`
}

type RetryConfig struct {
	MaxKeysPerRequest    int      `json:"maxKeysPerRequest"`
	CooldownSeconds      int      `json:"cooldownSeconds"`
	RespectRetryAfter    bool     `json:"respectRetryAfter"`
	MaxRequestsPerKeyDay int      `json:"maxRequestsPerKeyPerDay"` // 0 = off
}

type Config struct {
	Port     int    `json:"port"`
	Bind     string `json:"bind"`
	DBPath   string `json:"dbPath"` // SQLite source of truth (default "gateway.db")
	CookieSecure *bool  `json:"cookieSecure,omitempty"` // Secure flag on session cookies (default true)
	TrustedOrigins []string `json:"trustedOrigins,omitempty"` // extra Origin hosts accepted on /api mutations when serving the GUI behind a reverse proxy (same-host is always allowed)
	APIKeys  []string `json:"apiKeys"`  // Deprecated: first-boot migration source only; unused for auth once the store is active
	Rotation string   `json:"rotation"` // "priority" (default) | "lru"
	Anthropic struct {
		Aliases map[string]string `json:"aliases"` // "claude-sonnet-4-5" -> "zen/mimo-..." on /v1/messages
	} `json:"anthropic"`
	Zen   ZenProviderConfig  `json:"zen"`
	Kilo  KiloProviderConfig `json:"kilo"`
	Retry RetryConfig        `json:"retry"`
}

// secureCookies reports whether session cookies get the Secure flag
// (default true; set "cookieSecure": false for plain-http local GUI dev).
func (c *Config) secureCookies() bool {
	return c.CookieSecure == nil || *c.CookieSecure
}

type keyFileEntry struct {
	Label string `json:"label"`
	Key   string `json:"key"`
}

type keyFile struct {
	Zen  []keyFileEntry `json:"zen"`
	Kilo []keyFileEntry `json:"kilo"`
}

// loadConfig reads an optional JSON config file. A missing file is fine —
// the gateway runs entirely on defaults (see applyDefaults and
// config.example.json); a malformed file is fatal.
func loadConfig(path string) (*Config, error) {
	cfg := &Config{}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg.applyDefaults(), nil
		}
		return nil, err
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg.applyDefaults(), nil
}

// applyDefaults fills anything the config file (or its absence) left unset.
func (c *Config) applyDefaults() *Config {
	if c.Port == 0 {
		c.Port = 8787
	}
	if c.Bind == "" {
		c.Bind = "127.0.0.1"
	}
	if c.DBPath == "" {
		c.DBPath = "gateway.db"
	}
	if c.Rotation == "" {
		c.Rotation = "priority"
	}
	if len(c.TrustedOrigins) == 0 {
		// Same-host Origins are always accepted; list extra hosts only when
		// serving the GUI through a reverse proxy under a different host.
		c.TrustedOrigins = nil
	}
	if c.Zen.BaseURL == "" {
		c.Zen.BaseURL = "https://opencode.ai/zen/v1"
	}
	if c.Kilo.BaseURL == "" {
		c.Kilo.BaseURL = "https://api.kilo.ai/api/gateway/v1"
	}
	if c.Zen.UserAgent == "" {
		c.Zen.UserAgent = "opencode/1.18.32"
	}
	if c.Retry.MaxKeysPerRequest == 0 {
		c.Retry.MaxKeysPerRequest = 3
	}
	if c.Retry.CooldownSeconds == 0 {
		c.Retry.CooldownSeconds = 30
	}
	return c
}

func loadKeys(path string) (*keyFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	kf := &keyFile{}
	if err := json.Unmarshal(raw, kf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return kf, nil
}

// uaVersionOK validates "opencode/<semver>" against the server's 1.18.0 floor.
func uaVersionOK(ua string) bool {
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
