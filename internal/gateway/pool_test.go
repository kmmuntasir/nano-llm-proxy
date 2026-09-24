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

	k1 := p.pick(nil)
	if k1 == nil || k1.Label != "a" {
		t.Fatalf("first pick = %v, want a", k1)
	}
	k2 := p.pick(nil)
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
	if k3 := p.pick(nil); k3 != nil {
		t.Fatalf("third pick = %v, want nil (cap reached, no other keys)", k3)
	}
}

func TestDailyCapPriorityFallsOverToNextKey(t *testing.T) {
	p := newPool("zen", "priority", 1, []config.KeyFileEntry{mkEntry("a"), mkEntry("b")})
	if got := p.pick(nil); got.Label != "a" {
		t.Fatalf("pick 1 = %s, want a", got.Label)
	}
	if got := p.pick(nil); got.Label != "b" {
		t.Fatalf("pick 2 = %s, want b (a capped)", got.Label)
	}
	if got := p.pick(nil); got != nil {
		t.Fatalf("pick 3 = %s, want nil (all capped)", got.Label)
	}
}

func TestDailyCapLRUAlternates(t *testing.T) {
	p := newPool("zen", "lru", 1, []config.KeyFileEntry{mkEntry("a"), mkEntry("b")})
	first := p.pick(nil)
	second := p.pick(nil)
	if first == nil || second == nil || first.Label == second.Label {
		t.Fatalf("lru picks = %v, %v; want alternating keys", first, second)
	}
	if p.pick(nil) != nil {
		t.Fatal("pick 3 should be nil: both keys served their quota")
	}
}

func TestDailyCapResetsOnDateRollover(t *testing.T) {
	p := newPool("zen", "priority", 1, []config.KeyFileEntry{mkEntry("a")})
	if got := p.pick(nil); got == nil {
		t.Fatal("pick 1 = nil, want a")
	}
	// simulate yesterday's bucket left on the key
	p.mu.Lock()
	p.keys[0].DayStamp = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	p.keys[0].DayCount = 1
	p.keys[0].CooldownUntil = time.Time{}
	p.mu.Unlock()
	if got := p.pick(nil); got == nil {
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
	if got := p.pick(nil); got == nil {
		t.Fatal("pick with cap 5 = nil, want a")
	}
	p2 := newPoolPreserving("zen", "priority", 3, []config.KeyFileEntry{mkEntry("a")}, rawKeysMap(p))
	if got := p2.pick(nil); got != nil {
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
	p.pick(nil)
	p.pick(nil)
	p2 := newPoolPreserving("zen", "priority", 2, []config.KeyFileEntry{mkEntry("a")}, rawKeysMap(p))
	if got := p2.pick(nil); got != nil {
		t.Fatalf("pick after rebuild = %s, want nil (DayCount must carry over)", got.Label)
	}
}
