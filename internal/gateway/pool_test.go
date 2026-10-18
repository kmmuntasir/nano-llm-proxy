package gateway

import (
	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"testing"
	"time"
)

func mkEntry(label string) config.KeyFileEntry {
	return config.KeyFileEntry{Label: label, Key: "key-" + label}
}

func TestDailyCapExhaustsAndCoolsUntilMidnight(t *testing.T) {
	p := newPool("zen", "priority", 2, []config.KeyFileEntry{mkEntry("a")})
	now := time.Now()

	k1 := p.pick(nil, "")
	if k1 == nil || k1.Label != "a" {
		t.Fatalf("first pick = %v, want a", k1)
	}
	k2 := p.pick(nil, "")
	if k2 == nil {
		t.Fatal("second pick = nil, want a (priority sticks)")
	}
	if k2.DayCount != 2 {
		t.Fatalf("DayCount = %d, want 2", k2.DayCount)
	}
	if !k2.CooldownUntil.Equal(nextMidnight(now)) {
		t.Errorf("CooldownUntil = %v, want %v", k2.CooldownUntil, nextMidnight(now))
	}
	if k2.Status() != statusCooling {
		t.Errorf("status = %s, want cooling", k2.Status())
	}
	if k3 := p.pick(nil, ""); k3 != nil {
		t.Fatalf("third pick = %v, want nil (cap reached, no other keys)", k3)
	}
}

func TestDailyCapPriorityFallsOverToNextKey(t *testing.T) {
	p := newPool("zen", "priority", 1, []config.KeyFileEntry{mkEntry("a"), mkEntry("b")})
	if got := p.pick(nil, ""); got.Label != "a" {
		t.Fatalf("pick 1 = %s, want a", got.Label)
	}
	if got := p.pick(nil, ""); got.Label != "b" {
		t.Fatalf("pick 2 = %s, want b (a capped)", got.Label)
	}
	if got := p.pick(nil, ""); got != nil {
		t.Fatalf("pick 3 = %s, want nil (all capped)", got.Label)
	}
}

func TestDailyCapLRUAlternates(t *testing.T) {
	p := newPool("zen", "lru", 1, []config.KeyFileEntry{mkEntry("a"), mkEntry("b")})
	first := p.pick(nil, "")
	second := p.pick(nil, "")
	if first == nil || second == nil || first.Label == second.Label {
		t.Fatalf("lru picks = %v, %v; want alternating keys", first, second)
	}
	if p.pick(nil, "") != nil {
		t.Fatal("pick 3 should be nil: both keys served their quota")
	}
}

func TestDailyCapResetsOnDateRollover(t *testing.T) {
	p := newPool("zen", "priority", 1, []config.KeyFileEntry{mkEntry("a")})
	if got := p.pick(nil, ""); got == nil {
		t.Fatal("pick 1 = nil, want a")
	}
	// simulate yesterday's bucket left on the key
	p.mu.Lock()
	p.keys[0].DayStamp = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	p.keys[0].DayCount = 1
	p.keys[0].CooldownUntil = time.Time{}
	p.mu.Unlock()
	if got := p.pick(nil, ""); got == nil {
		t.Fatal("pick after rollover = nil, want a (stale stamp must reset)")
	}
}

func TestDailyCapPredicateCatchesMidDayCapLowering(t *testing.T) {
	// key served 3 requests today, no cooldown set; lowering the cap to 3
	// must skip it even though CooldownUntil never fired
	p := newPool("zen", "priority", 5, []config.KeyFileEntry{mkEntry("a")})
	p.mu.Lock()
	p.keys[0].DayStamp = time.Now().Format("2006-01-02")
	p.keys[0].DayCount = 3
	p.mu.Unlock()
	if got := p.pick(nil, ""); got == nil {
		t.Fatal("pick with cap 5 = nil, want a")
	}
	p2 := newPoolPreserving("zen", "priority", 3, []config.KeyFileEntry{mkEntry("a")}, rawKeysMap(p))
	if got := p2.pick(nil, ""); got != nil {
		t.Fatalf("pick with lowered cap = %s, want nil", got.Label)
	}
}

// rawKeysMap is a test helper: hash -> KeyState, the shape newPoolPreserving
// consumes.
func rawKeysMap(p *Pool) map[string]*KeyState {
	m := map[string]*KeyState{}
	for _, ks := range p.rawKeys() {
		m[ks.Hash] = ks
	}
	return m
}

func TestDailyCapCountSurvivesRebuild(t *testing.T) {
	p := newPool("zen", "priority", 2, []config.KeyFileEntry{mkEntry("a")})
	p.pick(nil, "")
	p.pick(nil, "")
	p2 := newPoolPreserving("zen", "priority", 2, []config.KeyFileEntry{mkEntry("a")}, rawKeysMap(p))
	if got := p2.pick(nil, ""); got != nil {
		t.Fatalf("pick after rebuild = %s, want nil (DayCount must carry over)", got.Label)
	}
}

func mkEntries(n int) []config.KeyFileEntry {
	out := make([]config.KeyFileEntry, n)
	for i := range n {
		out[i] = mkEntry(string(rune('a' + i)))
	}
	return out
}

// TestPriorityDailyAdvancesOneKeyPerDay walks a calendar across more than a
// full cycle: each day's first pick is the key after yesterday's, the last
// key wraps to the first, and within a day the pick stays put (cache
// affinity is per day, like plain priority).
func TestPriorityDailyAdvancesOneKeyPerDay(t *testing.T) {
	p := newPool("zen", "priority-daily", 0, mkEntries(4))
	day0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.Local)
	first := p.pickAt(nil, "", day0)
	start := int(first.Label[0] - 'a')
	if start != dailyOffset(day0, 4) {
		t.Fatalf("day0 start = %d, want offset %d", start, dailyOffset(day0, 4))
	}
	for d := range 9 {
		morning := day0.AddDate(0, 0, d).Add(-8 * time.Hour) // 01:00
		evening := day0.AddDate(0, 0, d).Add(14 * time.Hour) // 23:00
		want := string(rune('a' + (start+d)%4))
		for _, at := range []time.Time{morning, evening} {
			if k := p.pickAt(nil, "", at); k == nil || k.Label != want {
				t.Fatalf("day %d at %s: picked %v, want %s", d, at.Format("15:04"), k, want)
			}
		}
	}
}

// TestPriorityDailyFailsOverAndWraps: today's primary unhealthy → the next
// key; the walk wraps past the last key to the first; excluded keys (already
// tried this request) are skipped the same way.
func TestPriorityDailyFailsOverAndWraps(t *testing.T) {
	p := newPool("zen", "priority-daily", 0, mkEntries(3))
	// find a day whose offset lands on the last key, so failover must wrap
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)
	for dailyOffset(now, 3) != 2 {
		now = now.AddDate(0, 0, 1)
	}
	keys := p.rawKeys()
	if k := p.pickAt(nil, "", now); k != keys[2] {
		t.Fatalf("primary = %v, want c", k)
	}
	keys[2].CooldownUntil = now.Add(time.Minute)
	if k := p.pickAt(nil, "", now); k != keys[0] {
		t.Fatalf("failover = %v, want a (wrapped)", k)
	}
	if k := p.pickAt(map[string]bool{keys[0].Hash: true}, "", now); k != keys[1] {
		t.Fatalf("with a excluded = %v, want b", k)
	}
	keys[0].Disabled, keys[1].Disabled = true, true
	if k := p.pickAt(nil, "", now); k != nil {
		t.Fatalf("all unhealthy: picked %v, want nil", k)
	}
	// cooldown over → back on today's primary
	if k := p.pickAt(nil, "", now.Add(2*time.Minute)); k != keys[2] {
		t.Fatalf("after cooldown = %v, want c", k)
	}
}

// TestPriorityDailyRotatesPerProvider: each pool rotates over its own key
// count, so two providers on the same day sit at different positions and
// each covers all of its keys over its own cycle.
func TestPriorityDailyRotatesPerProvider(t *testing.T) {
	zen := newPool("zen", "priority-daily", 0, mkEntries(14))
	kilo := newPool("kilo", "priority-daily", 0, mkEntries(15))
	day := time.Date(2026, 3, 1, 12, 0, 0, 0, time.Local)
	seenZen, seenKilo := map[string]bool{}, map[string]bool{}
	for d := range 15 * 14 {
		at := day.AddDate(0, 0, d)
		seenZen[zen.pickAt(nil, "", at).Label] = true
		seenKilo[kilo.pickAt(nil, "", at).Label] = true
		if d < 14 && len(seenZen) != d+1 {
			t.Fatalf("zen repeated a key before cycling all 14 (day %d)", d)
		}
	}
	if len(seenZen) != 14 || len(seenKilo) != 15 {
		t.Fatalf("coverage zen=%d kilo=%d, want 14 and 15", len(seenZen), len(seenKilo))
	}
}

// TestDailyOffsetFollowsCalendarAcrossDST: the offset advances exactly one
// per calendar day even across a 23h/25h day.
func TestDailyOffsetFollowsCalendarAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	day := time.Date(2026, 3, 7, 23, 30, 0, 0, ny) // spring-forward is Mar 8
	for d := range 4 {
		a, b := dailyOffset(day.AddDate(0, 0, d), 1000), dailyOffset(day.AddDate(0, 0, d+1), 1000)
		if (a+1)%1000 != b {
			t.Fatalf("day %d→%d offset %d→%d, want +1", d, d+1, a, b)
		}
	}
	if dailyOffset(day, 0) != 0 {
		t.Fatal("empty pool offset must be 0")
	}
}
