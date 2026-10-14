package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvConfigGoogle(t *testing.T) {
	cases := []struct {
		name, id, domain string
		wantErr          string
		wantDomain       string
	}{
		{name: "unset"},
		{name: "client only", id: "x.apps.googleusercontent.com"},
		{name: "client and domain", id: "x.apps.googleusercontent.com", domain: " Example.COM ", wantDomain: "example.com"},
		{name: "domain without client", domain: "example.com", wantErr: "GOOGLE_CLIENT_ID is not"},
		{name: "email instead of domain", id: "x", domain: "me@example.com", wantErr: "invalid GOOGLE_WORKSPACE_DOMAIN"},
		{name: "url instead of domain", id: "x", domain: "https://example.com", wantErr: "invalid GOOGLE_WORKSPACE_DOMAIN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GOOGLE_CLIENT_ID", c.id)
			t.Setenv("GOOGLE_WORKSPACE_DOMAIN", c.domain)
			cfg, err := LoadEnvConfig()
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.GoogleEnabled() != (c.id != "") || cfg.GoogleWorkspaceDomain != c.wantDomain {
				t.Fatalf("cfg = enabled:%v domain:%q", cfg.GoogleEnabled(), cfg.GoogleWorkspaceDomain)
			}
		})
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]string{"": ModeServer, "server": ModeServer, " Local ": ModeLocal} {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseMode("desktop"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestLoadLocalConfig(t *testing.T) {
	for _, k := range []string{"NANO_PORT", "NANO_BIND", "NANO_DB_PATH", "NANO_COOKIE_SECURE", "GOOGLE_CLIENT_ID", "GOOGLE_WORKSPACE_DOMAIN"} {
		t.Setenv(k, "")
	}
	dir := t.TempDir()
	cfg, err := LoadLocalConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsLocal() || cfg.DataDir != dir || cfg.DBPath != filepath.Join(dir, "gateway.db") || cfg.SecureCookies() ||
		cfg.Bind != "127.0.0.1" || cfg.Port != 8787 {
		t.Fatalf("local defaults wrong: %+v", cfg)
	}
	// explicit values still win
	t.Setenv("NANO_DB_PATH", "/tmp/x.db")
	t.Setenv("NANO_COOKIE_SECURE", "true")
	t.Setenv("NANO_BIND", "::1")
	if cfg, err = LoadLocalConfig(dir); err != nil || cfg.DBPath != "/tmp/x.db" || !cfg.SecureCookies() || cfg.Bind != "::1" {
		t.Fatalf("overrides lost: %+v %v", cfg, err)
	}
	// local mode never listens beyond loopback
	for _, bind := range []string{"0.0.0.0", "192.168.1.5", "::"} {
		t.Setenv("NANO_BIND", bind)
		if _, err := LoadLocalConfig(dir); err == nil || !strings.Contains(err.Error(), "only listens on this computer") {
			t.Errorf("bind %q: err = %v", bind, err)
		}
	}
	// server mode is untouched
	t.Setenv("NANO_BIND", "")
	t.Setenv("NANO_DB_PATH", "")
	t.Setenv("NANO_COOKIE_SECURE", "")
	if cfg, err = LoadEnvConfig(); err != nil || cfg.IsLocal() || cfg.Mode != ModeServer || cfg.DBPath != "gateway.db" || !cfg.SecureCookies() {
		t.Fatalf("server defaults changed: %+v %v", cfg, err)
	}
}

func TestSetDotEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := SetDotEnv(p, "NANO_PORT", "9000"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "NANO_PORT=9000\n" {
		t.Fatalf("new file = %q", b)
	}
	os.WriteFile(p, []byte("# comment\nNANO_BIND=127.0.0.1\nexport NANO_PORT=1\nNANO_PORT=2\nOTHER=x"), 0o600)
	if err := SetDotEnv(p, "NANO_PORT", "9001"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "# comment\nNANO_BIND=127.0.0.1\nNANO_PORT=9001\nOTHER=x\n" {
		t.Fatalf("rewritten = %q", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode())
	}
}
