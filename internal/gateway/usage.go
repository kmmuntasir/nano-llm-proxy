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
type tokenUsage struct{ in, out int64 }

func (t *tokenUsage) add(in, out int64) {
	if in > t.in {
		t.in = in
	}
	if out > t.out {
		t.out = out
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
