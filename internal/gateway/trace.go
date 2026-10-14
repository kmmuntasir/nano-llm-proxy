package gateway

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Per-request trace for the usage log. clientOnly attaches one to every
// authenticated client request; the rotation loops update it as they try
// upstream keys, and recordActivity copies it into the persisted
// UsageEvent. Every method is nil-safe so handlers called without the
// middleware (store-less tests, internal callers) need no special casing.

type ctxTraceType struct{}

var ctxTrace ctxTraceType

// reqTrace accumulates what happened upstream for one client request. The
// mutex exists for the MCP SDK, which may run tool handlers on goroutines of
// its own; the /v1 rotation loops are single-goroutine.
type reqTrace struct {
	mu             sync.Mutex
	start          time.Time
	surface        string // chat | messages | responses | mcp
	stream         bool
	attempts       int
	upstreamStatus int
	upstreamKey    string // label of the last upstream key tried
	ttfbMs         int64
}

// traceSnapshot is an unlocked copy handed to the usage recorder.
type traceSnapshot struct {
	surface        string
	stream         bool
	attempts       int
	upstreamStatus int
	upstreamKey    string
	ttfbMs         int64
}

// surfaceForPath maps a client route onto the usage surface name.
func surfaceForPath(p string) string {
	switch {
	case strings.HasPrefix(p, "/v1/chat/completions"):
		return "chat"
	case strings.HasPrefix(p, "/v1/messages"):
		return "messages"
	case strings.HasPrefix(p, "/v1/responses"):
		return "responses"
	case strings.HasPrefix(p, "/mcp"):
		return "mcp"
	}
	return ""
}

func withTrace(r *http.Request) *http.Request {
	tr := &reqTrace{start: time.Now(), surface: surfaceForPath(r.URL.Path)}
	return r.WithContext(context.WithValue(r.Context(), ctxTrace, tr))
}

// traceFrom returns the request's trace, or nil when none is attached.
func traceFrom(r *http.Request) *reqTrace {
	tr, _ := r.Context().Value(ctxTrace).(*reqTrace)
	return tr
}

// setStream records whether the client asked for a streamed response.
func (t *reqTrace) setStream(stream bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.stream = stream
	t.mu.Unlock()
}

// attempt counts one upstream call with the given key label.
func (t *reqTrace) attempt(label string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.attempts++
	t.upstreamKey = label
	t.upstreamStatus = 0 // until this attempt answers
	t.mu.Unlock()
}

// upstream records the upstream's HTTP status for the current attempt
// (0 = network error). The first 200 fixes the time to first upstream byte.
func (t *reqTrace) upstream(status int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.upstreamStatus = status
	if status == http.StatusOK && t.ttfbMs == 0 {
		t.ttfbMs = max(time.Since(t.start).Milliseconds(), 1) // 0 means "never"
	}
	t.mu.Unlock()
}

// lastUpstreamStatus is the HTTP status of the most recent upstream attempt
// (0 = none answered, or no trace attached).
func (t *reqTrace) lastUpstreamStatus() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.upstreamStatus
}

func (t *reqTrace) snapshot() traceSnapshot {
	if t == nil {
		return traceSnapshot{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return traceSnapshot{
		surface: t.surface, stream: t.stream, attempts: t.attempts,
		upstreamStatus: t.upstreamStatus, upstreamKey: t.upstreamKey, ttfbMs: t.ttfbMs,
	}
}

// maxFailReason bounds the persisted fail_reason (upstream bodies can be
// long; the usage log only needs the hint's gist).
const maxFailReason = 300

// maxUsageLabel bounds client-controlled identifiers (model, provider name)
// before they are stored: an unknown provider or a junk model string is
// still recorded, but cannot bloat the usage log.
const maxUsageLabel = 128

// truncateReason cuts s to at most maxFailReason characters (runes, so a
// multi-byte sequence is never split).
func truncateReason(s string) string {
	return truncateRunes(s, maxFailReason)
}

// truncateRunes cuts s to at most n runes without splitting a multi-byte
// sequence.
func truncateRunes(s string, n int) string {
	c := 0
	for i := range s {
		if c == n {
			return s[:i]
		}
		c++
	}
	return s
}
