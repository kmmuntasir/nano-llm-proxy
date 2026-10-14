package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config is the bootstrap-only configuration: the handful of values needed
// before the database opens. It comes from the environment (optionally seeded
// by a .env file — see dotenv.go). Everything runtime-tunable lives in
// RuntimeSettings (settings.go), stored in the DB and managed via the admin
// GUI.
type Config struct {
	Mode           string   // ModeServer (default) or ModeLocal — see LoadLocalConfig
	DataDir        string   // local mode: the per-user data directory ("" in server mode)
	Port           int      // listen port (default 8787)
	Bind           string   // listen host (default "127.0.0.1")
	DBPath         string   // SQLite source of truth (default "gateway.db")
	CookieSecure   *bool    // Secure flag on session cookies (default true)
	TrustedOrigins []string // extra Origin hosts accepted on /api mutations when serving the GUI behind a reverse proxy (same-host is always allowed)
	APIKeys        []string // store==nil test allowlist only (clientOnly legacy path)

	// Google sign-in (GUI). Enabled when GoogleClientID is set; it signs in
	// existing accounts only (created on the Users page). A non-empty
	// GoogleWorkspaceDomain additionally restricts it to that domain.
	GoogleClientID        string
	GoogleWorkspaceDomain string

	Zen  ZenProviderConfig // first-boot provider seeding + store==nil test fallback
	Kilo KiloProviderConfig
}

type ZenProviderConfig struct {
	BaseURL string
}

type KiloProviderConfig struct {
	BaseURL string
}

// Run modes. Server mode is the multi-user deployment; local mode is one
// person's machine: no login, loopback only, data in a per-user directory.
const (
	ModeServer = "server"
	ModeLocal  = "local"
)

// IsLocal reports whether the gateway runs in local mode.
func (c *Config) IsLocal() bool { return c.Mode == ModeLocal }

// secureCookies reports whether session cookies get the Secure flag
// (default true; set NANO_COOKIE_SECURE=false for plain-http local GUI dev).
func (c *Config) SecureCookies() bool {
	return c.CookieSecure == nil || *c.CookieSecure
}

// GoogleEnabled reports whether Google sign-in is configured.
func (c *Config) GoogleEnabled() bool {
	return c.GoogleClientID != ""
}

type KeyFileEntry struct {
	Label string `json:"label"`
	Key   string `json:"key"`
}

type KeyFile struct {
	Zen  []KeyFileEntry
	Kilo []KeyFileEntry
}

// applyDefaults fills anything the environment left unset.
func (c *Config) applyDefaults() *Config {
	if c.Mode == "" {
		c.Mode = ModeServer
	}
	if c.Port == 0 {
		c.Port = 8787
	}
	if c.Bind == "" {
		c.Bind = "127.0.0.1"
	}
	if c.DBPath == "" {
		c.DBPath = "gateway.db"
	}
	if c.Zen.BaseURL == "" {
		c.Zen.BaseURL = "https://opencode.ai/inference/v1" // opencode 2.0.29+; zen/v1 refuses the free tier
	}
	if c.Kilo.BaseURL == "" {
		c.Kilo.BaseURL = "https://api.kilo.ai/api/gateway"
	}
	return c
}

func LoadKeys(path string) (*KeyFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	kf := &KeyFile{}
	if err := json.Unmarshal(raw, kf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return kf, nil
}
