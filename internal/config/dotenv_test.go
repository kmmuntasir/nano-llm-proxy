package config

import (
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
