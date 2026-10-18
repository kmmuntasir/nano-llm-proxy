package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
	"log"
	"maps"
	"sort"
	"sync"
	"time"
)

const (
	statusHealthy  = "healthy"
	statusCooling  = "cooling"
	statusDisabled = "disabled"
)

// Why a key was disabled at runtime (KeyState.DisabledReason). Both are
// genuine auth failures; "expired" tells the admin the key must be replaced
// rather than re-checked.
const (
	disabledAuth    = "auth"
	disabledExpired = "expired"
)

// KeyState is the mutable health record for one upstream API key.
type KeyState struct {
	Label    string
	Key      string
	Hash     string // short sha256 prefix, safe to expose
	Disabled bool
	// DisabledReason is disabledAuth or disabledExpired while Disabled was
	// set by classify; "" otherwise. The runtime disable survives pool
	// rebuilds (an expired key must not return on every GUI save); it ends
	// when the admin toggles the key (setEnabled), the key is deleted, or
	// the process restarts.
	DisabledReason string
	// CooldownUntil is the key-wide cooldown (5xx, network, flaky 401, daily
	// cap, account-wide 429): the key serves no model until then.
	CooldownUntil time.Time
	// cooldownRateLimited marks CooldownUntil as an account-wide rate limit
	// (zai's stated reset), so rateLimitedFor reports it as a 429.
	cooldownRateLimited bool
	// modelCooldown holds per-model cooldowns from 429s (upstream model id ->
	// until). A rate limit is often a model- or IP-level limit, not the
	// key's; cooling the whole key for one hot model starved every other
	// model. Guarded by the owning Pool's mutex.
	modelCooldown map[string]time.Time
	LastUsed      time.Time
	Requests      int64
	RateLimited   int64
	Errors        int64
	DayStamp      string // "2006-01-02" the DayCount bucket belongs to
	DayCount      int64  // requests served today (daily-cap accounting)
}

func (k *KeyState) Status() string {
	switch {
	case k.Disabled:
		return statusDisabled
	case time.Now().Before(k.CooldownUntil):
		return statusCooling
	default:
		return statusHealthy
	}
}

// Pool is a provider key pool. Mode "priority" sticks to the first healthy
// key in configured order (prompt-cache affinity; keys.json order = priority)
// and fails over down the list; "priority-daily" does the same from a
// starting key that advances one position per day (see dailyOffset); mode
// "lru" spreads load evenly.
type Pool struct {
	mu       sync.Mutex
	name     string
	mode     string // "priority-daily" | "priority" | "lru"
	dailyCap int    // max requests per key per day, 0 = off
	keys     []*KeyState
}

func newPool(name, mode string, dailyCap int, entries []config.KeyFileEntry) *Pool {
	switch mode {
	case settings.RotationPriorityDaily, settings.RotationLRU:
	default:
		mode = settings.RotationPriority
	}
	p := &Pool{name: name, mode: mode, dailyCap: dailyCap}
	for _, e := range entries {
		sum := sha256.Sum256([]byte(e.Key))
		p.keys = append(p.keys, &KeyState{
			Label: e.Label,
			Key:   e.Key,
			Hash:  hex.EncodeToString(sum[:])[:12],
		})
	}
	return p
}

// newPoolPreserving builds a pool from DB rows while carrying over runtime
// state (cooldowns, usage counters) for keys that already existed — matched
// by hash, including runtime auth/expiry disables and their reason. DB-
// disabled keys are excluded by the caller, so an admin toggling a key off
// and on starts it fresh. prev should come from stateCopies, never live
// states.
func newPoolPreserving(name, mode string, dailyCap int, entries []config.KeyFileEntry, prev map[string]*KeyState) *Pool {
	p := newPool(name, mode, dailyCap, entries)
	for _, k := range p.keys {
		if old, ok := prev[k.Hash]; ok {
			if old.Disabled && old.DisabledReason != "" {
				k.Disabled, k.DisabledReason = true, old.DisabledReason
			}
			k.CooldownUntil = old.CooldownUntil
			k.cooldownRateLimited = old.cooldownRateLimited
			k.modelCooldown = maps.Clone(old.modelCooldown)
			k.LastUsed = old.LastUsed
			k.Requests = old.Requests
			k.RateLimited = old.RateLimited
			k.Errors = old.Errors
			k.DayStamp = old.DayStamp
			k.DayCount = old.DayCount
		}
	}
	return p
}

// rawKeys returns a snapshot of the key states (for rebuild preservation).
func (p *Pool) rawKeys() []*KeyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*KeyState(nil), p.keys...)
}

// stateCopies returns detached copies of the key states, taken under the
// pool lock: a rebuild reads them while requests on the old pool may still
// be mutating the originals.
func (p *Pool) stateCopies() []*KeyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*KeyState, 0, len(p.keys))
	for _, k := range p.keys {
		c := *k
		c.modelCooldown = maps.Clone(k.modelCooldown)
		out = append(out, &c)
	}
	return out
}

// nextMidnight returns the coming local midnight — the cooldown an
// exhausted daily-cap key rides out.
func nextMidnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).AddDate(0, 0, 1)
}

// dailyOffset is the position "priority-daily" starts from on now's day:
// the local calendar day number modulo the pool size, so consecutive days
// start one key further down and the last key wraps back to the first. It is
// derived from the date alone — no state to persist, restarts change
// nothing, and every pool rotates over its own key count. The day turns at
// local midnight (the same clock as the daily cap); the calendar date, not
// elapsed seconds, keeps DST days from skipping or repeating a key.
func dailyOffset(now time.Time, n int) int {
	if n <= 0 {
		return 0
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400
	return int(day % int64(n))
}

// pick returns the next healthy key not in exclude and not cooling for model
// (the upstream model id; "" ignores per-model cooldowns), or nil. In priority mode
// this is the first configured key that is healthy — traffic returns to the
// primary automatically once its cooldown expires; priority-daily is the
// same walk starting at today's offset and wrapping past the last key. With
// a daily cap set, a key that has served its quota today is skipped; the
// pick that reaches the cap puts the key into cooldown until midnight.
func (p *Pool) pick(exclude map[string]bool, model string) *KeyState {
	return p.pickAt(exclude, model, time.Now())
}

// pickAt is pick with an explicit clock (tests drive the calendar).
func (p *Pool) pickAt(exclude map[string]bool, model string, now time.Time) *KeyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	today := now.Format("2006-01-02")
	capped := func(k *KeyState) bool {
		return p.dailyCap > 0 && k.DayStamp == today && k.DayCount >= int64(p.dailyCap)
	}
	blocked := func(k *KeyState) bool {
		return k.Disabled || exclude[k.Hash] || now.Before(k.CooldownUntil) || capped(k) ||
			(model != "" && now.Before(k.modelCooldown[model]))
	}
	var best *KeyState
	if p.mode != settings.RotationLRU {
		start := 0
		if p.mode == settings.RotationPriorityDaily {
			start = dailyOffset(now, len(p.keys))
		}
		for i := range len(p.keys) {
			k := p.keys[(start+i)%len(p.keys)]
			if blocked(k) {
				continue
			}
			best = k
			break
		}
	} else {
		for _, k := range p.keys {
			if blocked(k) {
				continue
			}
			if best == nil || k.LastUsed.Before(best.LastUsed) {
				best = k
			}
		}
	}
	if best != nil {
		best.LastUsed = now
		best.Requests++
		if p.dailyCap > 0 {
			if best.DayStamp != today {
				best.DayStamp = today
				best.DayCount = 0
			}
			best.DayCount++
			if int(best.DayCount) == p.dailyCap {
				best.CooldownUntil = nextMidnight(now)
				best.cooldownRateLimited = false
			}
		}
	}
	return best
}

func (p *Pool) cooldown(k *KeyState, d time.Duration, reason string) {
	p.mu.Lock()
	k.CooldownUntil = time.Now().Add(d)
	k.cooldownRateLimited = false
	p.mu.Unlock()
	log.Printf("key=%s/%s cooling %s (%s)", p.name, k.Label, d.Round(time.Second), reason)
}

// cooldownAccount cools the whole key for an account-wide rate limit (zai's
// 429 with a stated reset): every model shares the quota.
func (p *Pool) cooldownAccount(k *KeyState, d time.Duration, reason string) {
	p.mu.Lock()
	k.CooldownUntil = time.Now().Add(d)
	k.cooldownRateLimited = true
	p.mu.Unlock()
	log.Printf("key=%s/%s cooling %s, all models (%s)", p.name, k.Label, d.Round(time.Second), reason)
}

// cooldownModel cools k for one upstream model only (a 429). Expired
// entries are pruned on the way so the map stays bounded by the models
// currently rate-limited.
func (p *Pool) cooldownModel(k *KeyState, model string, d time.Duration, reason string) {
	now := time.Now()
	p.mu.Lock()
	for m, until := range k.modelCooldown {
		if !now.Before(until) {
			delete(k.modelCooldown, m)
		}
	}
	if k.modelCooldown == nil {
		k.modelCooldown = map[string]time.Time{}
	}
	k.modelCooldown[model] = now.Add(d)
	p.mu.Unlock()
	log.Printf("key=%s/%s model=%s cooling %s (%s)", p.name, k.Label, model, d.Round(time.Second), reason)
}

// rateLimitedFor reports whether model is unservable only because of 429s:
// at least one key is enabled, none of the enabled keys can serve model
// now, and every one of them carries an active 429 cooldown for model
// (model-scoped, or an account-wide one). wait is
// how long until the first of them frees up. All-disabled pools or keys
// blocked purely by 5xx cooldowns/daily caps are not rate limits.
func (p *Pool) rateLimitedFor(model string, now time.Time) (wait time.Duration, ok bool) {
	if model == "" {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	today := now.Format("2006-01-02")
	for _, k := range p.keys {
		if k.Disabled {
			continue
		}
		until := k.modelCooldown[model]
		if k.cooldownRateLimited && k.CooldownUntil.After(until) {
			until = k.CooldownUntil // account-wide 429
		}
		if !now.Before(until) {
			return 0, false // free for this model (or blocked by something else)
		}
		if now.Before(k.CooldownUntil) && k.CooldownUntil.After(until) {
			until = k.CooldownUntil
		}
		if p.dailyCap > 0 && k.DayStamp == today && k.DayCount >= int64(p.dailyCap) {
			if mid := nextMidnight(now); mid.After(until) {
				until = mid
			}
		}
		if d := until.Sub(now); !ok || d < wait {
			wait, ok = d, true
		}
	}
	return wait, ok
}

// disable takes k out of rotation until the next rebuild. kind is
// disabledAuth or disabledExpired (shown to admins); reason is log detail.
func (p *Pool) disable(k *KeyState, kind, reason string) {
	p.mu.Lock()
	k.Disabled = true
	k.DisabledReason = kind
	p.mu.Unlock()
	log.Printf("key=%s/%s DISABLED (%s)", p.name, k.Label, reason)
}

func (p *Pool) recordRateLimit(k *KeyState) {
	p.mu.Lock()
	k.RateLimited++
	p.mu.Unlock()
}

func (p *Pool) recordError(k *KeyState) {
	p.mu.Lock()
	k.Errors++
	p.mu.Unlock()
}

func (p *Pool) setEnabled(hash string, enabled bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, k := range p.keys {
		if k.Hash == hash {
			k.Disabled = !enabled
			k.DisabledReason = ""
			return true
		}
	}
	return false
}

// KeyView is the admin-facing projection of a key (never raw material).
type KeyView struct {
	Label  string `json:"label"`
	Hash   string `json:"hash"`
	Status string `json:"status"`
	// DisabledReason says why a runtime-disabled key is out ("auth" |
	// "expired"); empty unless Status is "disabled" by classify.
	DisabledReason string `json:"disabled_reason,omitempty"`
	CooldownFor    string `json:"cooldown_remaining,omitempty"`
	// CoolingModels counts models this key is rate-limited for (429s).
	// Such a key is still "healthy": it serves every other model.
	CoolingModels int   `json:"cooling_models,omitempty"`
	Requests      int64 `json:"requests"`
	RateLimited   int64 `json:"rate_limited"`
	Errors        int64 `json:"errors"`
}

func (p *Pool) snapshot() []KeyView {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]KeyView, 0, len(p.keys))
	now := time.Now()
	for _, k := range p.keys {
		v := KeyView{
			Label:          k.Label,
			Hash:           k.Hash,
			Status:         k.Status(),
			DisabledReason: k.DisabledReason,
			Requests:       k.Requests,
			RateLimited:    k.RateLimited,
			Errors:         k.Errors,
		}
		for _, until := range k.modelCooldown {
			if now.Before(until) {
				v.CoolingModels++
			}
		}
		if now.Before(k.CooldownUntil) {
			v.CooldownFor = k.CooldownUntil.Sub(now).Round(time.Second).String()
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func (p *Pool) healthyCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, k := range p.keys {
		if k.Status() == statusHealthy {
			n++
		}
	}
	return n
}
