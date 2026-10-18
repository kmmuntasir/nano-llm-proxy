package gateway

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Rate-limit handling across a provider's key pool.
//
// A 429 cools the (key, model) pair, not the key (pool.cooldownModel). When
// 429s for one model arrive from several distinct keys in a short window,
// the limit is likely the model's or this server's IP's: rotating through
// every key on each request only multiplies upstream calls (zen flagged a
// deployment's IP for exactly that, 2026-10-17). The breaker then puts the
// model into single-attempt mode until its backoff ends: each request tries
// at most one key that isn't cooling for the model and never rotates. Keys
// that never answered 429 keep serving; a 429 extends the backoff (doubling,
// capped); a 200 clears the trip. A request fails fast with 429 +
// Retry-After (no upstream call) only when every enabled key is cooling for
// the model.

const (
	// breakerWindow is how close together 429s from distinct keys must be
	// to count as one model-level limit; also the minimum spacing between
	// two doublings of a trip's backoff, so a burst of concurrent
	// single-attempt 429s doesn't jump straight to the cap.
	breakerWindow = 15 * time.Second
	// breakerDistinctKeys is how many distinct keys must 429 to trip.
	breakerDistinctKeys = 2
	// breakerMinBackoff floors a trip when cooldownSeconds is 0 and no
	// Retry-After was sent — the breaker exists to stop hammering upstream.
	breakerMinBackoff = 5 * time.Second
	// breakerMaxBackoff caps the backoff, including repeated doubling.
	breakerMaxBackoff = 5 * time.Minute
	// breakerIdle drops entries untouched this long so client-chosen model
	// strings can't grow the map without bound.
	breakerIdle = 10 * time.Minute
)

// modelBreaker is the rate-limit state of one (provider, model).
type modelBreaker struct {
	hits      map[string]time.Time // key hash -> last 429 inside the window
	maxCool   time.Duration        // longest 429 cooldown among the hits
	tripped   bool                 // single-attempt mode until openUntil
	openUntil time.Time
	backoff   time.Duration // current trip's backoff
	grownAt   time.Time     // when backoff last grew
	touched   time.Time
}

// rateBreaker holds every (provider, model) breaker. It lives on the
// gateway, not the Pool, so it survives the pool rebuild every GUI write
// does. Every method holds mu for its whole body.
type rateBreaker struct {
	mu sync.Mutex
	m  map[string]*modelBreaker
}

func breakerKey(provider, model string) string { return provider + "\x00" + model }

// tripped reports whether (provider, model) is in single-attempt mode.
func (b *rateBreaker) tripped(provider, model string, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.m[breakerKey(provider, model)]
	return e != nil && e.tripped && now.Before(e.openUntil)
}

// record429 files a 429 from keyHash for (provider, model) whose verdict
// cooldown was cool, and reports whether the model is now in
// single-attempt mode (the caller stops rotating). Trips and backoff growth
// are logged here.
func (b *rateBreaker) record429(provider, model, keyHash string, cool, floor time.Duration, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string]*modelBreaker{}
	}
	b.prune(now)
	k := breakerKey(provider, model)
	e := b.m[k]
	if e == nil || (e.tripped && !now.Before(e.openUntil)) {
		// new, or a trip whose backoff ran out: count afresh
		e = &modelBreaker{hits: map[string]time.Time{}}
		b.m[k] = e
	}
	e.touched = now
	if e.tripped {
		// a single attempt hit the limit again: back off longer
		if now.Sub(e.grownAt) >= breakerWindow {
			e.backoff = min(max(2*e.backoff, cool), breakerMaxBackoff)
			e.grownAt = now
			log.Printf("provider=%s model=%s still rate-limited — backing off %s", provider, model, e.backoff)
		}
		if until := now.Add(e.backoff); until.After(e.openUntil) {
			e.openUntil = until
		}
		return true
	}
	for h, at := range e.hits {
		if now.Sub(at) > breakerWindow {
			delete(e.hits, h)
		}
	}
	if len(e.hits) == 0 {
		e.maxCool = 0
	}
	e.hits[keyHash] = now
	e.maxCool = max(e.maxCool, cool)
	if len(e.hits) < breakerDistinctKeys {
		return false
	}
	e.tripped = true
	e.backoff = min(max(e.maxCool, floor, breakerMinBackoff), breakerMaxBackoff)
	e.openUntil = now.Add(e.backoff)
	e.grownAt = now
	e.hits, e.maxCool = nil, 0
	log.Printf("provider=%s model=%s rate-limited across keys — backing off %s", provider, model, e.backoff)
	return true
}

// success clears (provider, model): an upstream answered 200.
func (b *rateBreaker) success(provider, model string) {
	b.mu.Lock()
	delete(b.m, breakerKey(provider, model))
	b.mu.Unlock()
}

// prune drops idle entries. Caller holds b.mu.
func (b *rateBreaker) prune(now time.Time) {
	for k, e := range b.m {
		if now.Sub(e.touched) > breakerIdle && (!e.tripped || !now.Before(e.openUntil)) {
			delete(b.m, k)
		}
	}
}

// upstreamFailure is why a rotation loop couldn't serve a request. The
// handler turns it into the protocol's error response.
type upstreamFailure struct {
	msg string
	// status is the client-facing status; 0 = derive it from the last
	// upstream answer (upstreamFailureStatus).
	status int
	// retryAfter, set with status 429, becomes the Retry-After header.
	retryAfter time.Duration
}

// failHint wraps a hint whose status follows the last upstream answer.
func failHint(msg string) *upstreamFailure { return &upstreamFailure{msg: msg} }

// rateLimitFailure is the 429 a client gets when a model is rate-limited
// across the pool.
func rateLimitFailure(provider, model string, wait time.Duration) *upstreamFailure {
	secs := retryAfterSeconds(wait)
	return &upstreamFailure{
		msg:        fmt.Sprintf("%s is rate-limiting %s — retry after %ds", provider, model, secs),
		status:     http.StatusTooManyRequests,
		retryAfter: time.Duration(secs) * time.Second,
	}
}

// retryAfterSeconds is d rounded up to whole seconds, at least 1.
func retryAfterSeconds(d time.Duration) int {
	return max(1, int(math.Ceil(d.Seconds())))
}

// writeUpstreamFailure records and answers a failed upstream request in the
// shape of the protocol being spoken (anthropic: /v1/messages).
func (g *gateway) writeUpstreamFailure(w http.ResponseWriter, r *http.Request, ref providerRef, model string, start time.Time, f *upstreamFailure, tu tokenUsage, anthropic bool) {
	status := f.status
	if status == 0 {
		status = upstreamFailureStatus(r)
	}
	g.recordActivity(r, ref, model, "", start, status, f.msg, tu)
	log.Printf("res provider=%s model=%s status=failed(%d) %s ttft_ms=%d", ref.name, model, status, f.msg, time.Since(start).Milliseconds())
	if f.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(f.retryAfter)))
	}
	if anthropic {
		writeAnthropicErr(w, status, f.msg)
		return
	}
	errType := ""
	if status == http.StatusTooManyRequests {
		errType = "rate_limit_error"
	}
	writeTypedErr(w, status, f.msg, errType)
}

// keyRotation is one request's walk over a provider's pool for one upstream
// model: it picks keys, applies classify's verdicts with the right scope,
// feeds the rate-limit breaker, and turns an exhausted walk into an
// upstreamFailure. The rotation loops own the attempt budget and the I/O.
type keyRotation struct {
	g       *gateway
	ref     providerRef
	model   string
	exclude map[string]bool
	single  bool // breaker tripped: at most one key, no rotation
	picked  int
	keyLost bool // the last answer disabled its key (auth/expired)
	limited bool // the walk stopped on a 429 with the breaker tripped
}

// newRotation starts a walk; a tripped model gets single-attempt mode.
func (g *gateway) newRotation(ref providerRef, model string) *keyRotation {
	rot := &keyRotation{g: g, ref: ref, model: model, exclude: map[string]bool{}}
	if model != "" {
		rot.single = g.breaker.tripped(ref.name, model, time.Now())
	}
	return rot
}

// next picks the next key not cooling for the model, or nil when the pool
// is exhausted, the walk stopped on a 429, or single-attempt mode already
// used its key.
func (rot *keyRotation) next() *KeyState {
	if rot.limited || (rot.single && rot.picked > 0) {
		return nil
	}
	k := rot.ref.pool.pick(rot.exclude, rot.model)
	if k != nil {
		rot.exclude[k.Hash] = true
		rot.picked++
	}
	return k
}

// retrySame lets k be picked again (a surface flip retries the same key).
func (rot *keyRotation) retrySame(k *KeyState) {
	rot.exclude[k.Hash] = false
	rot.picked--
}

// rejected classifies a non-200 answer and applies its cooldown: a 429
// cools (key, model) and feeds the breaker; an account-wide 429 and every
// other failure cool the whole key.
func (rot *keyRotation) rejected(k *KeyState, resp *http.Response) verdict {
	v := rot.g.classify(rot.ref.pool, k, resp)
	rot.keyLost = v.keyDisabled
	switch {
	case v.rateLimited && rot.model != "":
		if v.cooldown > 0 {
			rot.ref.pool.cooldownModel(k, rot.model, v.cooldown, "HTTP 429")
		}
		floor := time.Duration(rot.g.rs().Retry.CooldownSeconds) * time.Second
		if rot.g.breaker.record429(rot.ref.name, rot.model, k.Hash, v.cooldown, floor, time.Now()) {
			rot.limited = true
		}
	case v.accountWide && v.cooldown > 0:
		rot.ref.pool.cooldownAccount(k, v.cooldown, "HTTP 429, account-wide")
	case v.cooldown > 0:
		rot.ref.pool.cooldown(k, v.cooldown, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
	return v
}

// succeeded clears the model's breaker: an upstream answered 200.
func (rot *keyRotation) succeeded() {
	if rot.model != "" {
		rot.g.breaker.success(rot.ref.name, rot.model)
	}
}

// exhausted is the failure for a walk that ended without a 200:
//   - every enabled key cooling for the model after 429s → 429, Retry-After
//     when the first of them frees up;
//   - the walk stopped on a 429 (breaker tripped) while a key is still
//     free → 429, Retry-After 1s (the next request tries that key);
//   - else lastHint (or "no healthy keys"), status left to the handler.
func (rot *keyRotation) exhausted(lastHint string) *upstreamFailure {
	if wait, ok := rot.ref.pool.rateLimitedFor(rot.model, time.Now()); ok {
		return rateLimitFailure(rot.ref.name, rot.model, wait)
	}
	if rot.limited {
		return rateLimitFailure(rot.ref.name, rot.model, time.Second)
	}
	if lastHint == "" {
		lastHint = fmt.Sprintf("No healthy %s keys available — every key is cooling or disabled", rot.ref.name)
	}
	f := failHint(lastHint)
	if rot.keyLost {
		f.status = http.StatusServiceUnavailable // not the request's fault
	}
	return f
}
