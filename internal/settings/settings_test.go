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

func TestSettingsCloneIsDeep(t *testing.T) {
	rs := DefaultRuntimeSettings()
	rs.Anthropic.FallbackModel = "zen/b"
	rs.Zen.ModelMeta["m"] = ModelMeta{ContextWindow: 1, MaxOutputTokens: 2}
	c := rs.Clone()
	c.Anthropic.FallbackModel = "zen/CHANGED"
	c.Zen.ModelMeta["m"] = ModelMeta{ContextWindow: 9, MaxOutputTokens: 9}
	if rs.Zen.ModelMeta["m"].ContextWindow != 1 {
		t.Fatalf("Clone is not deep: %+v", rs)
	}
}
