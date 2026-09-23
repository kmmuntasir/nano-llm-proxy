package main

import (
	"strings"
	"testing"
)

func TestSettingsDefaultsOnAbsentRow(t *testing.T) {
	st := openTestStore(t)
	rs, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := DefaultRuntimeSettings()
	if rs.Rotation != want.Rotation {
		t.Errorf("rotation = %q, want %q", rs.Rotation, want.Rotation)
	}
	if rs.Retry != want.Retry {
		t.Errorf("retry = %+v, want %+v", rs.Retry, want.Retry)
	}
	if !rs.Zen.InjectTools {
		t.Error("zen.injectTools should default true")
	}
	if rs.Zen.UserAgent != want.Zen.UserAgent {
		t.Errorf("userAgent = %q, want %q", rs.Zen.UserAgent, want.Zen.UserAgent)
	}
	if len(rs.Zen.ResponsesModels) != 0 || rs.Zen.ResponsesModels == nil {
		t.Error("responsesModels should default to empty non-nil")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	st := openTestStore(t)
	rs := &RuntimeSettings{
		Rotation: rotationLRU,
		Retry: RetryConfig{
			MaxKeysPerRequest:    7,
			CooldownSeconds:      0, // meaningful: Retry-After only
			RespectRetryAfter:    false,
			MaxRequestsPerKeyDay: 0, // off
		},
		Anthropic: AnthropicSettings{Aliases: map[string]string{"claude-sonnet-5": "zen/glm-5"}},
		Zen: ZenSettings{
			UserAgent:       "opencode/1.19.0",
			InjectTools:     false, // explicit false must survive the onto-defaults unmarshal
			ResponsesModels: []string{"mimo-test"},
			FreeOnly:        true,
			ModelMeta: map[string]ModelMeta{
				"glm-5": {ContextWindow: 128000, MaxOutputTokens: 4096, Reasoning: true},
			},
			ModelMetaAutoSync: true,
		},
		Kilo: KiloSettings{FreeOnly: true},
	}
	if err := st.SaveRuntimeSettings(rs); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Rotation != rotationLRU {
		t.Errorf("rotation = %q", got.Rotation)
	}
	if got.Retry != rs.Retry {
		t.Errorf("retry = %+v, want %+v", got.Retry, rs.Retry)
	}
	if got.Zen.InjectTools {
		t.Error("explicit injectTools=false was lost")
	}
	if !got.Zen.FreeOnly || !got.Kilo.FreeOnly {
		t.Error("freeOnly flags were lost")
	}
	if got.Zen.UserAgent != "opencode/1.19.0" {
		t.Errorf("userAgent = %q", got.Zen.UserAgent)
	}
	if got.Anthropic.Aliases["claude-sonnet-5"] != "zen/glm-5" {
		t.Errorf("aliases = %v", got.Anthropic.Aliases)
	}
	if len(got.Zen.ModelMeta) != 1 || got.Zen.ModelMeta["glm-5"].ContextWindow != 128000 {
		t.Errorf("modelMeta = %v", got.Zen.ModelMeta)
	}
	if !got.Zen.ModelMetaAutoSync {
		t.Error("modelMetaAutoSync was lost")
	}
}

func TestUpdateSettingsPreservesUntouched(t *testing.T) {
	st := openTestStore(t)
	seed := DefaultRuntimeSettings()
	seed.Rotation = rotationLRU
	seed.Anthropic.Aliases["a"] = "zen/b"
	seed.Zen.ModelMetaSyncStatus = &ModelMetaSyncStatus{At: 123, OK: true, Added: 3}
	if err := st.SaveRuntimeSettings(seed); err != nil {
		t.Fatalf("save: %v", err)
	}
	err := st.UpdateSettings(func(rs *RuntimeSettings) *RuntimeSettings {
		rs.Retry.CooldownSeconds = 99
		return rs
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Retry.CooldownSeconds != 99 {
		t.Errorf("cooldown = %d, want 99", got.Retry.CooldownSeconds)
	}
	if got.Rotation != rotationLRU || got.Anthropic.Aliases["a"] != "zen/b" {
		t.Errorf("mutate clobbered unrelated fields: %+v", got)
	}
	if got.Zen.ModelMetaSyncStatus == nil || got.Zen.ModelMetaSyncStatus.Added != 3 {
		t.Errorf("sync status lost: %+v", got.Zen.ModelMetaSyncStatus)
	}
}

func TestLoadSettingsCorruptRow(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.db.Exec(`INSERT INTO settings (key, value) VALUES (?, '{not json')`, runtimeSettingsKey); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := st.LoadRuntimeSettings(); err == nil {
		t.Fatal("expected an error for a corrupt settings row")
	}
}

func TestRuntimeSettingsValidate(t *testing.T) {
	valid := func() *RuntimeSettings { return DefaultRuntimeSettings() }
	cases := []struct {
		name string
		mut  func(*RuntimeSettings)
		want string // empty = valid
	}{
		{"defaults", func(*RuntimeSettings) {}, ""},
		{"lru", func(rs *RuntimeSettings) { rs.Rotation = rotationLRU }, ""},
		{"bad rotation", func(rs *RuntimeSettings) { rs.Rotation = "random" }, "rotation"},
		{"zero maxKeys", func(rs *RuntimeSettings) { rs.Retry.MaxKeysPerRequest = 0 }, "maxKeysPerRequest"},
		{"huge maxKeys", func(rs *RuntimeSettings) { rs.Retry.MaxKeysPerRequest = 101 }, "maxKeysPerRequest"},
		{"negative cooldown", func(rs *RuntimeSettings) { rs.Retry.CooldownSeconds = -1 }, "cooldownSeconds"},
		{"negative cap", func(rs *RuntimeSettings) { rs.Retry.MaxRequestsPerKeyDay = -5 }, "maxRequestsPerKeyPerDay"},
		{"empty alias key", func(rs *RuntimeSettings) { rs.Anthropic.Aliases["  "] = "zen/x" }, "aliases"},
		{"alias without slash", func(rs *RuntimeSettings) { rs.Anthropic.Aliases["a"] = "noslash" }, "provider/model"},
		{"ua below floor", func(rs *RuntimeSettings) { rs.Zen.UserAgent = "opencode/1.17.9" }, "1.18.0"},
		{"empty responses model", func(rs *RuntimeSettings) { rs.Zen.ResponsesModels = []string{" "} }, "responsesModels"},
		{"meta zero ctx", func(rs *RuntimeSettings) {
			rs.Zen.ModelMeta["m"] = ModelMeta{}
		}, "contextWindow"},
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
	rs.Anthropic.Aliases["a"] = "zen/b"
	rs.Zen.ResponsesModels = []string{"m"}
	rs.Zen.ModelMeta["m"] = ModelMeta{ContextWindow: 1, MaxOutputTokens: 2}
	c := rs.Clone()
	c.Anthropic.Aliases["a"] = "zen/CHANGED"
	c.Zen.ResponsesModels[0] = "CHANGED"
	c.Zen.ModelMeta["m"] = ModelMeta{ContextWindow: 9, MaxOutputTokens: 9}
	if rs.Anthropic.Aliases["a"] != "zen/b" || rs.Zen.ResponsesModels[0] != "m" || rs.Zen.ModelMeta["m"].ContextWindow != 1 {
		t.Fatalf("Clone is not deep: %+v", rs)
	}
}
