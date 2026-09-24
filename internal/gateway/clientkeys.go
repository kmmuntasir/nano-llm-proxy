package gateway

import (
	"context"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Hot-path (per-request) state for client API keys and usage accounting.
// Both structures are served from memory: the DB is only hit when the cache
// is reloaded after a CRUD write, or by the periodic usage flush.

type ctxClientKeyType struct{}

var ctxClientKey ctxClientKeyType

type clientKeyEntry struct {
	ID        int64
	UserID    int64
	Alias     string
	UserEmail string
	Disabled  bool
}

// clientKeyCache maps sha256(key) -> entry. Reloaded wholesale after CRUD
// writes; lookups are a hash + map read, no locks.
type clientKeyCache struct {
	v atomic.Pointer[map[string]clientKeyEntry]
}

func (c *clientKeyCache) reload(s *store.Store) error {
	keys, err := s.AllClientKeys()
	if err != nil {
		return err
	}
	m := make(map[string]clientKeyEntry, len(keys))
	for _, k := range keys {
		m[k.KeyHash] = clientKeyEntry{ID: k.ID, UserID: k.UserID, Alias: k.Alias, UserEmail: k.UserEmail, Disabled: k.Disabled}
	}
	c.v.Store(&m)
	return nil
}

// lookup hashes the presented key and resolves it. Disabled keys look like
// unknown keys (401 either way).
func (c *clientKeyCache) lookup(raw string) (clientKeyEntry, bool) {
	m := c.v.Load()
	if m == nil {
		return clientKeyEntry{}, false
	}
	e, ok := (*m)[store.HashSecret(raw)]
	if !ok || e.Disabled {
		return clientKeyEntry{}, false
	}
	return e, true
}

// contextClientKey returns the authenticated client key, if the middleware
// put one on the request (store mode only).
func contextClientKey(r *http.Request) (clientKeyEntry, bool) {
	e, ok := r.Context().Value(ctxClientKey).(clientKeyEntry)
	return e, ok
}

func withClientKey(r *http.Request, e clientKeyEntry) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxClientKey, e))
}

// --- usage tracking ---

type activityEntry struct {
	TS         int64  `json:"ts"`
	KeyAlias   string `json:"keyAlias"`
	User       string `json:"user"` // email of the client-key owner
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	KeyHash    string `json:"keyHash"` // upstream key hash
	Status     int    `json:"status"`  // 200 or the client-facing error status
	DurationMs int64  `json:"durationMs"`
	FailReason string `json:"failReason,omitempty"`
}

const activityRingSize = 100

// usageTracker accumulates per-client-key request deltas in memory and keeps
// a bounded ring of recent requests for the dashboard. flush() drains the
// deltas into SQLite (30s ticker) — a crash loses at most one window of
// monitoring counters.
type usageTracker struct {
	mu      sync.Mutex
	deltas  map[int64]int64
	lastUse map[int64]int64
	ring    []activityEntry
	ringIdx int
}

func newUsageTracker() *usageTracker {
	return &usageTracker{
		deltas:  map[int64]int64{},
		lastUse: map[int64]int64{},
		ring:    make([]activityEntry, activityRingSize),
	}
}

func (u *usageTracker) record(keyID int64) {
	u.mu.Lock()
	u.deltas[keyID]++
	u.lastUse[keyID] = time.Now().Unix()
	u.mu.Unlock()
}

func (u *usageTracker) addActivity(e activityEntry) {
	u.mu.Lock()
	u.ring[u.ringIdx] = e
	u.ringIdx = (u.ringIdx + 1) % activityRingSize
	u.mu.Unlock()
}

// recent returns up to n activity entries, newest first.
func (u *usageTracker) recent(n int) []activityEntry {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]activityEntry, 0, n)
	for i := 0; i < activityRingSize && len(out) < n; i++ {
		idx := (u.ringIdx - 1 - i + activityRingSize) % activityRingSize
		e := u.ring[idx]
		if e.TS == 0 {
			break // never-filled slot; ring is ordered, so we're done
		}
		out = append(out, e)
	}
	return out
}

// flush drains accumulated deltas into the store. Returns keys touched.
func (u *usageTracker) flush(s *store.Store) {
	u.mu.Lock()
	deltas := u.deltas
	lastUse := u.lastUse
	u.deltas = map[int64]int64{}
	u.lastUse = map[int64]int64{}
	u.mu.Unlock()
	for id, delta := range deltas {
		s.BumpClientKeyUsage(id, delta, lastUse[id]) //nolint:errcheck
	}
}
