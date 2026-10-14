package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Bootstrap configuration comes from the environment, with an optional .env
// file in the working directory as the lowest-precedence source: LoadDotEnv
// runs first and only fills variables the real environment doesn't already
// set, so built-in defaults < .env < process env. Runtime settings (rotation,
// retries, adapter knobs, aliases) do NOT live here — they live in the
// database and are managed in the admin GUI.

// LoadDotEnv parses a .env file into the process environment. KEY=VALUE per
// line; blank lines and # comments are skipped; an optional "export " prefix
// and one pair of surrounding quotes are trimmed. Variables already present
// in the environment are never overridden. A malformed line is an error
// naming the line number.
func LoadDotEnv(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // .env is optional; plain env and systemd EnvironmentFile= both work
		}
		return err
	}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.Index(line, "=")
		if eq <= 0 {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, i+1)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if strings.HasPrefix(val, "#") {
			return fmt.Errorf("%s:%d: missing value after '='", path, i+1)
		}
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, i+1)
		}
		if _, ok := os.LookupEnv(key); ok {
			continue // the real environment wins
		}
		if err := os.Setenv(key, val); err != nil {
			return err
		}
	}
	return nil
}

// LoadEnvConfig builds the bootstrap Config from NANO_* environment
// variables (after LoadDotEnv has run). Anything it cannot parse is fatal —
// a typo must not silently launch the gateway on surprise defaults.
func LoadEnvConfig() (*Config, error) {
	cfg, err := fromEnv()
	if err != nil {
		return nil, err
	}
	return cfg.applyDefaults(), nil
}

// ParseMode validates NANO_MODE: "" and "server" mean server mode, "local"
// local mode; anything else is an error (fatal at boot like every other
// malformed bootstrap value).
func ParseMode(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", ModeServer:
		return ModeServer, nil
	case ModeLocal:
		return ModeLocal, nil
	}
	return "", fmt.Errorf("invalid NANO_MODE %q (use server or local)", v)
}

// LoadLocalConfig is LoadEnvConfig for local mode: the database defaults to
// <dataDir>/gateway.db, session cookies drop the Secure flag (the GUI is
// plain http://localhost), and the bind address must be loopback — local
// mode has no login, so it must never be reachable from another machine.
// NANO_DB_PATH and NANO_COOKIE_SECURE still override.
func LoadLocalConfig(dataDir string) (*Config, error) {
	cfg, err := fromEnv()
	if err != nil {
		return nil, err
	}
	cfg.Mode, cfg.DataDir = ModeLocal, dataDir
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(dataDir, "gateway.db")
	}
	if cfg.CookieSecure == nil {
		insecure := false
		cfg.CookieSecure = &insecure
	}
	switch cfg.Bind {
	case "", "127.0.0.1", "localhost", "::1":
	default:
		return nil, fmt.Errorf("NANO_BIND %q: local mode only listens on this computer (use 127.0.0.1, localhost or ::1)", cfg.Bind)
	}
	return cfg.applyDefaults(), nil
}

// fromEnv parses the NANO_* variables without applying defaults, so the
// callers can tell "unset" from "set to the default".
func fromEnv() (*Config, error) {
	cfg := &Config{}
	if v := os.Getenv("NANO_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid NANO_PORT %q", v)
		}
		cfg.Port = port
	}
	if v := os.Getenv("NANO_BIND"); v != "" {
		cfg.Bind = v
	}
	if v := os.Getenv("NANO_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("NANO_COOKIE_SECURE"); v != "" {
		secure, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid NANO_COOKIE_SECURE %q (use true or false)", v)
		}
		cfg.CookieSecure = &secure
	}
	if v := os.Getenv("NANO_TRUSTED_ORIGINS"); v != "" {
		for host := range strings.SplitSeq(v, ",") {
			if host = strings.TrimSpace(host); host != "" {
				cfg.TrustedOrigins = append(cfg.TrustedOrigins, host)
			}
		}
	}
	cfg.GoogleClientID = strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID"))
	if v := strings.TrimSpace(os.Getenv("GOOGLE_WORKSPACE_DOMAIN")); v != "" {
		if cfg.GoogleClientID == "" {
			return nil, fmt.Errorf("GOOGLE_WORKSPACE_DOMAIN is set but GOOGLE_CLIENT_ID is not")
		}
		if strings.ContainsAny(v, "@/: ") || !strings.Contains(v, ".") {
			return nil, fmt.Errorf("invalid GOOGLE_WORKSPACE_DOMAIN %q (want a bare domain like example.com)", v)
		}
		cfg.GoogleWorkspaceDomain = strings.ToLower(v)
	}
	return cfg, nil
}

// SetDotEnv sets KEY=value in a .env file, replacing an existing assignment
// of KEY (including an "export KEY=" one) and keeping every other line
// as-is; a missing file is created. Written 0600 — .env files hold secrets.
func SetDotEnv(path, key, value string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	line := key + "=" + value
	var out []string
	found := false
	text := strings.TrimRight(string(raw), "\n")
	if text != "" {
		for _, l := range strings.Split(text, "\n") {
			t := strings.TrimPrefix(strings.TrimSpace(l), "export ")
			if k, _, ok := strings.Cut(t, "="); ok && strings.TrimSpace(k) == key {
				if !found {
					out = append(out, line)
				}
				found = true
				continue
			}
			out = append(out, l)
		}
	}
	if !found {
		out = append(out, line)
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o600)
}
