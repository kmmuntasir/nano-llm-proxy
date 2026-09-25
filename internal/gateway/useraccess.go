package gateway

import (
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Per-user provider access. A superadmin can disable any provider for any
// user (Users page); a disabled provider then disappears from that user's
// /v1/models and routes like an unknown prefix. Absence of a restriction
// means allowed — nobody loses access until a superadmin acts.

// userAccessCache maps userID -> set of disabled provider ids. Reloaded
// wholesale by rebuildPools (so every GUI mutation keeps it fresh); lookups
// are lock-free map reads on the hot path.
type userAccessCache struct {
	v atomic.Pointer[map[int64]map[int64]bool]
}

func (c *userAccessCache) reload(s *store.Store) error {
	pairs, err := s.AllProviderAccess()
	if err != nil {
		return err
	}
	if pairs == nil {
		pairs = map[int64]map[int64]bool{}
	}
	c.v.Store(&pairs)
	return nil
}

// disabled reports whether the provider is revoked for the user. A nil/empty
// cache allows everything (store==nil test mode never loads it).
func (c *userAccessCache) disabled(userID, providerID int64) bool {
	m := c.v.Load()
	if m == nil {
		return false
	}
	return (*m)[userID][providerID]
}

// anyDisabled reports whether the user has any restriction at all — lets the
// catalog fast-path skip filtering for unrestricted users.
func (c *userAccessCache) anyDisabled(userID int64) bool {
	m := c.v.Load()
	if m == nil {
		return false
	}
	return len((*m)[userID]) > 0
}

// providerFor resolves a routing prefix like g.provider, but also applies the
// caller's per-user provider access: a revoked provider resolves as unknown,
// so every endpoint treats it exactly like one that does not exist.
func (g *gateway) providerFor(r *http.Request, prefix string) (providerRef, bool) {
	ref, ok := g.provider(prefix)
	if !ok {
		return ref, false
	}
	if g.store != nil {
		if ck, has := contextClientKey(r); has && g.access.disabled(ck.UserID, ref.id) {
			return providerRef{}, false
		}
	}
	return ref, true
}

// callerUserID identifies whose access rules apply: the client-key owner on
// /v1/*, the session user on /api/*. ok=false means no scoping (store==nil
// tests, or an unauthenticated path).
func callerUserID(r *http.Request) (int64, bool) {
	if ck, ok := contextClientKey(r); ok {
		return ck.UserID, true
	}
	if u := contextUser(r); u != nil && u.ID > 0 {
		return u.ID, true
	}
	return 0, false
}

// catalogFor returns the merged catalog scoped to the caller: entries from
// providers revoked for this user are filtered out. The merged list itself is
// shared (cached), the per-caller view is filtered per request.
func (g *gateway) catalogFor(r *http.Request) ([]any, bool) {
	models, ok := g.mergedCatalog()
	if !ok {
		return nil, false
	}
	if g.store == nil {
		return models, true
	}
	uid, scoped := callerUserID(r)
	if !scoped || !g.access.anyDisabled(uid) {
		return models, true
	}
	g.regMu.RLock()
	defer g.regMu.RUnlock()
	out := make([]any, 0, len(models))
	for _, m := range models {
		e, isMap := m.(map[string]any)
		if !isMap {
			out = append(out, m)
			continue
		}
		id, _ := e["id"].(string)
		prefix, _, found := strings.Cut(id, "/")
		if !found {
			out = append(out, e)
			continue
		}
		blocked := false
		for _, ref := range g.registry {
			if ref.name == prefix {
				blocked = g.access.disabled(uid, ref.id)
				break
			}
		}
		if !blocked {
			out = append(out, e)
		}
	}
	return out, true
}
