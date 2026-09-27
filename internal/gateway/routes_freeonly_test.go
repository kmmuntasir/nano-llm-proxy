package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Zen's catalog mixes the free tier with subscription models, and upstream
// marks the free ones with a "-free" suffix on the id. The GUI's "free" badge
// and the "free models only" filter must therefore agree exactly: a card
// badged free that disappears under the toggle is the bug this pins.
func TestZenFreeBadgeMatchesFreeOnlyFilter(t *testing.T) {
	// Real upstream ids, in the shape opencode.ai/zen/v1 advertises them.
	ids := []string{
		"big-pickle",                      // free tier, but not chat-protocol
		"claude-opus-5-5",                 // subscription
		"gpt-5.5",                         // subscription
		"kimi-k3",                         // subscription
		"qwen3.8-max",                     // subscription
		"jev-1.13",                        // subscription (vs jev-1.13-free below)
		"jev-1.13-free",                   // free
		"space-bunny-free",                // free
		"muse-spark-1.3",                  // subscription (vs -contributor-free below)
		"muse-spark-1.3-contributor-free", // free
	}

	catalog := func(t *testing.T, freeOnly bool) map[string]bool {
		t.Helper()
		var b strings.Builder
		b.WriteString(`{"data":[`)
		for i, id := range ids {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"id":"` + id + `"}`)
		}
		b.WriteString(`]}`)

		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/models") {
				w.Write([]byte(b.String()))
				return
			}
			w.Write([]byte(`{}`))
		}))
		defer up.Close()

		cfg := testCfg()
		cfg.Zen.BaseURL = up.URL + "/v1"
		g := newGateway(cfg, testRuntime(), testKeyFile())
		rs := g.rs()
		rs.Zen.FreeOnly = freeOnly
		g.rsPtr.Store(rs)

		ref, ok := g.provider("zen")
		if !ok {
			t.Fatal("no zen pool")
		}
		models, err := g.fetchUpstreamModels(ref)
		if err != nil {
			t.Fatalf("fetchUpstreamModels: %v", err)
		}
		out := map[string]bool{}
		for _, m := range models {
			e, _ := m.(map[string]any)
			id, _ := e["id"].(string)
			free, _ := e["free"].(bool)
			out[id] = free
		}
		return out
	}

	all := catalog(t, false)
	filtered := catalog(t, true)

	if len(all) != len(ids) {
		t.Fatalf("freeOnly=OFF should expose all %d zen models, got %d", len(ids), len(all))
	}

	// The badge must follow the id, not the provider.
	wantFree := map[string]bool{
		"zen/big-pickle-262K":                      false,
		"zen/claude-opus-5-5-262K":                 false,
		"zen/gpt-5.5-262K":                         false,
		"zen/kimi-k3-262K":                         false,
		"zen/qwen3.8-max-262K":                     false,
		"zen/jev-1.13-262K":                        false,
		"zen/jev-1.13-free-262K":                   true,
		"zen/space-bunny-free-262K":                true,
		"zen/muse-spark-1.3-262K":                  false,
		"zen/muse-spark-1.3-contributor-free-262K": true,
	}
	for id, want := range wantFree {
		got, ok := all[id]
		if !ok {
			t.Errorf("missing catalog entry %s", id)
			continue
		}
		if got != want {
			t.Errorf("%s: free badge = %v, want %v", id, got, want)
		}
	}

	// The invariant: whatever survives the filter is badged free, and
	// whatever is badged free survives the filter. No card may be badged
	// free and then hidden by the toggle, or reachable-but-unbadged.
	for id, free := range all {
		_, visible := filtered[id]
		if visible != free {
			t.Errorf("%s: badged free=%v but freeOnly visibility=%v", id, free, visible)
		}
	}
}

// hasFreeSuffix is the single free predicate, and it runs on the raw upstream
// id — before the cosmetic context/modality suffix is appended, which would
// otherwise hide the "-free" marker behind e.g. "-1M-txt".
func TestHasFreeSuffix(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"space-bunny-free", true},
		{"jev-1.13-free", true},
		{"muse-spark-1.3-contributor-free", true},
		{"nemotron-3.5-lightning-free", true},
		{"big-pickle", false},
		{"claude-opus-5-5", false},
		{"free", false},             // too short to carry the marker
		{"gpt-free-preview", false}, // marker is not the suffix
		{"", false},
	} {
		if got := hasFreeSuffix(tc.id); got != tc.want {
			t.Errorf("hasFreeSuffix(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}
