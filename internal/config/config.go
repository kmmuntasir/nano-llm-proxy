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
	Port           int      // listen port (default 8787)
	Bind           string   // listen host (default "127.0.0.1")
	DBPath         string   // SQLite source of truth (default "gateway.db")
	CookieSecure   *bool    // Secure flag on session cookies (default true)
	TrustedOrigins []string // extra Origin hosts accepted on /api mutations when serving the GUI behind a reverse proxy (same-host is always allowed)
	APIKeys        []string // store==nil test allowlist only (clientOnly legacy path)

	Zen  ZenProviderConfig // first-boot provider seeding + store==nil test fallback
	Kilo KiloProviderConfig
}

type ZenProviderConfig struct {
	BaseURL string
}

type KiloProviderConfig struct {
	BaseURL string
}

// secureCookies reports whether session cookies get the Secure flag
// (default true; set NANO_COOKIE_SECURE=false for plain-http local GUI dev).
func (c *Config) SecureCookies() bool {
	return c.CookieSecure == nil || *c.CookieSecure
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
		c.Zen.BaseURL = "https://opencode.ai/zen/v1"
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
