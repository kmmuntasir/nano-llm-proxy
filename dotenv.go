package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Bootstrap configuration comes from the environment, with an optional .env
// file in the working directory as the lowest-precedence source: loadDotEnv
// runs first and only fills variables the real environment doesn't already
// set, so built-in defaults < .env < process env. Runtime settings (rotation,
// retries, adapter knobs, aliases) do NOT live here — they live in the
// database and are managed in the admin GUI.

// loadDotEnv parses a .env file into the process environment. KEY=VALUE per
// line; blank lines and # comments are skipped; an optional "export " prefix
// and one pair of surrounding quotes are trimmed. Variables already present
// in the environment are never overridden. A malformed line is an error
// naming the line number.
func loadDotEnv(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // .env is optional; plain env / systemd / docker -e all work
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

// loadEnvConfig builds the bootstrap Config from NANO_* environment
// variables (after loadDotEnv has run). Anything it cannot parse is fatal —
// a typo must not silently launch the gateway on surprise defaults.
func loadEnvConfig() (*Config, error) {
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
	return cfg.applyDefaults(), nil
}
