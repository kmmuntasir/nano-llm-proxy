package settings

import (
	"strings"
	"testing"
)

func TestRuntimeSettingsValidate(t *testing.T) {
	valid := func() *RuntimeSettings { return DefaultRuntimeSettings() }
	cases := []struct {
		name string
		mut  func(*RuntimeSettings)
		want string // empty = valid
	}{
		{"defaults", func(*RuntimeSettings) {}, ""},
		{"lru", func(rs *RuntimeSettings) { rs.Rotation = RotationLRU }, ""},
		{"bad rotation", func(rs *RuntimeSettings) { rs.Rotation = "random" }, "Rotation must be"},
		{"zero maxKeys", func(rs *RuntimeSettings) { rs.Retry.MaxKeysPerRequest = 0 }, "Max keys per request"},
		{"huge maxKeys", func(rs *RuntimeSettings) { rs.Retry.MaxKeysPerRequest = 101 }, "Max keys per request"},
		{"negative cooldown", func(rs *RuntimeSettings) { rs.Retry.CooldownSeconds = -1 }, "Cooldown seconds"},
		{"negative cap", func(rs *RuntimeSettings) { rs.Retry.MaxRequestsPerKeyDay = -5 }, "Daily cap"},
		{"fallback without slash", func(rs *RuntimeSettings) { rs.Anthropic.FallbackModel = "noslash" }, "Fallback model"},
		{"ua below floor", func(rs *RuntimeSettings) { rs.Zen.UserAgent = "opencode/1.17.9" }, "1.18.0"},
		{"meta zero ctx", func(rs *RuntimeSettings) {
			rs.Zen.ModelMeta["m"] = ModelMeta{}
		}, "context window must be"},
		{"zai meta zero out", func(rs *RuntimeSettings) {
			rs.Zai.ModelMeta["m"] = ModelMeta{ContextWindow: 1}
		}, "Z.ai model meta"},
		{"webtools bad reader mode", func(rs *RuntimeSettings) { rs.WebTools.ReaderMode = "jina" }, "reader mode"},
		{"webtools bad url scheme", func(rs *RuntimeSettings) { rs.WebTools.SearXNGURL = "ftp://x" }, "SearXNG URL"},
		{"webtools url without host", func(rs *RuntimeSettings) { rs.WebTools.SearXNGURL = "http://" }, "SearXNG URL"},
		{"webtools conc 0", func(rs *RuntimeSettings) { rs.WebTools.ObscuraConcurrency = 0 }, "Obscura concurrency"},
		{"webtools conc 9", func(rs *RuntimeSettings) { rs.WebTools.ObscuraConcurrency = 9 }, "Obscura concurrency"},
		{"webtools fetch timeout", func(rs *RuntimeSettings) { rs.WebTools.FetchTimeoutSeconds = 0 }, "Fetch timeout"},
		{"webtools obscura timeout", func(rs *RuntimeSettings) { rs.WebTools.ObscuraTimeoutSeconds = 2 }, "Obscura timeout"},
		{"webtools maxchars low", func(rs *RuntimeSettings) { rs.WebTools.MaxChars = 500 }, "Max chars"},
		{"webtools path whitespace", func(rs *RuntimeSettings) { rs.WebTools.ObscuraPath = "my obscura" }, "Obscura path"},
		{"webtools enabled ok", func(rs *RuntimeSettings) { rs.WebTools.Enabled = true }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := valid()
			tc.mut(rs)
			got := rs.Validate()
			if tc.want == "" && got != "" {
				t.Fatalf("Validate() = %q, want valid", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("Validate() = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestApplyDefaultsWebTools(t *testing.T) {
	// A stored document from before web tools existed has an empty
	// WebTools section; unmarshalling onto defaults must restore it.
	rs := DefaultRuntimeSettings()
	stored := *rs
	stored.WebTools = WebToolsSettings{} // pre-feature doc
	stored.ApplyDefaults()
	wt := stored.WebTools
	if wt.SearXNGURL != DefaultSearXNGURL || wt.ReaderMode != ReaderFast ||
		wt.ObscuraPath != "obscura" || wt.ObscuraConcurrency != 2 ||
		wt.FetchTimeoutSeconds != 15 || wt.ObscuraTimeoutSeconds != 30 || wt.MaxChars != 20000 {
		t.Fatalf("ApplyDefaults did not restore web tools defaults: %+v", wt)
	}
	if wt.Enabled {
		t.Fatal("Enabled must default to false")
	}
}

func TestSettingsCloneIsDeep(t *testing.T) {
	rs := DefaultRuntimeSettings()
	rs.Anthropic.FallbackModel = "zen/b"
	rs.Zen.ModelMeta["m"] = ModelMeta{ContextWindow: 1, MaxOutputTokens: 2}
	rs.Zai.ModelMeta["m"] = ModelMeta{ContextWindow: 3, MaxOutputTokens: 4}
	c := rs.Clone()
	c.Anthropic.FallbackModel = "zen/CHANGED"
	c.Zen.ModelMeta["m"] = ModelMeta{ContextWindow: 9, MaxOutputTokens: 9}
	c.Zai.ModelMeta["m"] = ModelMeta{ContextWindow: 8, MaxOutputTokens: 8}
	if rs.Zen.ModelMeta["m"].ContextWindow != 1 {
		t.Fatalf("Clone is not deep: %+v", rs)
	}
	if rs.Zai.ModelMeta["m"].ContextWindow != 3 {
		t.Fatalf("Clone is not deep for zai meta: %+v", rs)
	}
}
