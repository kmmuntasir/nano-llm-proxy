package gateway

import (
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
	"sync"
	"time"
)

// Per-request usage accounting. Every proxied request reports the
// upstream-reported token counts (when the upstream surfaces them) plus
// provider/model/status/duration; events land in an in-memory buffer that
// the maintenance loop drains into the usage_events table every 30s. A crash
// loses at most one window of usage events — the same trade the client-key
// request counters already make.

// tokenUsage carries the upstream-reported token counts for one request.
// Upstreams repeat usage on consecutive chunks with the final total, so the
// largest observed value wins.
//
// Semantics follow OpenAI: in is the TOTAL prompt token count including any
// cache hits, cached is the cache-read subset of in (0 = none or unknown).
// Anthropic-native usage (input_tokens excluding cache, plus
// cache_read/cache_creation) is normalized into this shape on the way in.
type tokenUsage struct{ in, out, cached int64 }

func (t *tokenUsage) add(in, out int64) {
	if in > t.in {
		t.in = in
	}
	if out > t.out {
		t.out = out
	}
}

// addCached records the cache-read subset of the prompt (largest wins, like
// in/out).
func (t *tokenUsage) addCached(cached int64) {
	if cached > t.cached {
		t.cached = cached
	}
}

// anthropicUsage is the usage block for a /v1/messages response built from
// tu: Anthropic clients sum input + cache_read + cache_creation, so the cache
// hits are split back out of the total. cache_creation is unknown on the
// translated paths and reported as 0.
func (t tokenUsage) anthropicUsage(withOutput bool) map[string]any {
	in := t.in - t.cached
	if in < 0 {
		in = 0
	}
	u := map[string]any{
		"input_tokens":                in,
		"cache_read_input_tokens":     t.cached,
		"cache_creation_input_tokens": int64(0),
	}
	if withOutput {
		u["output_tokens"] = t.out
	}
	return u
}

// chatUsage is the OpenAI chat usage block for tu (prompt_tokens is the
// total; cached hits ride in prompt_tokens_details).
func (t tokenUsage) chatUsage() map[string]any {
	return map[string]any{
		"prompt_tokens":         t.in,
		"completion_tokens":     t.out,
		"total_tokens":          t.in + t.out,
		"prompt_tokens_details": map[string]any{"cached_tokens": t.cached},
	}
}

const usageBufferMax = 4096 // bounded: a firehose must not eat the heap

// usageBuffer accumulates events between flushes. On overflow the oldest
// pending event is dropped — monitoring degrades before the gateway does.
type usageBuffer struct {
	mu     sync.Mutex
	events []store.UsageEvent
}

func (b *usageBuffer) add(e store.UsageEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) >= usageBufferMax {
		copy(b.events, b.events[1:])
		b.events[len(b.events)-1] = e
		return
	}
	b.events = append(b.events, e)
}

func (b *usageBuffer) drain() []store.UsageEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.events
	b.events = nil
	return out
}

// usageRetentionDays bounds the usage_events table; older rows are pruned
// hourly by the maintenance loop.
const usageRetentionDays = 90

func usageCutoff() int64 {
	return time.Now().AddDate(0, 0, -usageRetentionDays).Unix()
}
