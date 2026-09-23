package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

const (
	statusHealthy  = "healthy"
	statusCooling  = "cooling"
	statusDisabled = "disabled"
)

// KeyState is the mutable health record for one upstream API key.
type KeyState struct {
	Label         string
	Key           string
	Hash          string // short sha256 prefix, safe to expose
	Disabled      bool
	CooldownUntil time.Time
	LastUsed      time.Time
	Requests      int64
	RateLimited   int64
	Errors        int64
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
// and fails over down the list; mode "lru" spreads load evenly.
type Pool struct {
	mu   sync.Mutex
	name string
	mode string // "priority" | "lru"
	keys []*KeyState
}

func newPool(name, mode string, entries []keyFileEntry) *Pool {
	if mode != "lru" {
		mode = "priority"
	}
	p := &Pool{name: name, mode: mode}
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
// by hash. DB-disabled keys are excluded by the caller; runtime auth-disables
// reset (the key gets one retry before classify re-disables it).
func newPoolPreserving(name, mode string, entries []keyFileEntry, prev map[string]*KeyState) *Pool {
	p := newPool(name, mode, entries)
	for _, k := range p.keys {
		if old, ok := prev[k.Hash]; ok {
			k.CooldownUntil = old.CooldownUntil
			k.LastUsed = old.LastUsed
			k.Requests = old.Requests
			k.RateLimited = old.RateLimited
			k.Errors = old.Errors
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

// pick returns the next healthy key not in exclude, or nil. In priority mode
// this is the first configured key that is healthy — traffic returns to the
// primary automatically once its cooldown expires.
func (p *Pool) pick(exclude map[string]bool) *KeyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	var best *KeyState
	if p.mode == "priority" {
		for _, k := range p.keys {
			if k.Disabled || exclude[k.Hash] || now.Before(k.CooldownUntil) {
				continue
			}
			best = k
			break
		}
	} else {
		for _, k := range p.keys {
			if k.Disabled || exclude[k.Hash] || now.Before(k.CooldownUntil) {
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
	}
	return best
}

func (p *Pool) cooldown(k *KeyState, d time.Duration, reason string) {
	p.mu.Lock()
	k.CooldownUntil = time.Now().Add(d)
	p.mu.Unlock()
	log.Printf("key=%s/%s cooling %s (%s)", p.name, k.Label, d.Round(time.Second), reason)
}

func (p *Pool) disable(k *KeyState, reason string) {
	p.mu.Lock()
	k.Disabled = true
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
			return true
		}
	}
	return false
}

// KeyView is the admin-facing projection of a key (never raw material).
type KeyView struct {
	Label       string `json:"label"`
	Hash        string `json:"hash"`
	Status      string `json:"status"`
	CooldownFor string `json:"cooldown_remaining,omitempty"`
	Requests    int64  `json:"requests"`
	RateLimited int64  `json:"rate_limited"`
	Errors      int64  `json:"errors"`
}

func (p *Pool) snapshot() []KeyView {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]KeyView, 0, len(p.keys))
	now := time.Now()
	for _, k := range p.keys {
		v := KeyView{
			Label:       k.Label,
			Hash:        k.Hash,
			Status:      k.Status(),
			Requests:    k.Requests,
			RateLimited: k.RateLimited,
			Errors:      k.Errors,
		}
		if now.Before(k.CooldownUntil) {
			v.CooldownFor = now.Sub(k.CooldownUntil).Round(time.Second).String()
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

// newZenSessionID fabricates "ses_" + 12 hex + 14 alnum — format-checked only.
func newZenSessionID() string {
	hexBuf := make([]byte, 6)
	if _, err := rand.Read(hexBuf); err != nil {
		panic(err)
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	tail := make([]byte, 14)
	if _, err := rand.Read(tail); err != nil {
		panic(err)
	}
	for i := range tail {
		tail[i] = alphabet[int(tail[i])%len(alphabet)]
	}
	return fmt.Sprintf("ses_%s%s", hex.EncodeToString(hexBuf), tail)
}
