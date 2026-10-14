package gateway

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// zenGateTools are the tools zen's free-tier gate expects to see in a
// request. Since 2026-10-14 the gate also inspects their parameter schemas:
// a body without "shell" and "read" carrying non-empty schemas 403s
// FreeTierError ("free tier can only be used from within OpenCode"), while
// descriptions, extra client tools and tool order don't matter (bisected
// against live zen with a captured opencode 2.0.29 request). The stubs
// therefore carry opencode 2.0.29's real parameter schemas
// (zenGateToolParams) under a "never call" description.
var zenGateTools = []string{"shell", "read", "edit"}

// zenGateToolParams are the opencode 2.0.29 parameter schemas of the gate
// tools, verbatim from a captured request.
var zenGateToolParams = map[string]string{
	"shell": `{"type":"object","properties":{"command":{"type":"string","description":"Shell command string to execute"},"workdir":{"type":"string","description":"Working directory to execute the command in. Defaults to the current working directory. When possible, avoid changing directories in the command and set the working directory here instead."},"timeout":{"type":"integer","minimum":0,"description":"Timeout in milliseconds. Set to 0 to disable the timeout. Defaults to 120000 for foreground commands. Background commands have no timeout by default."},"background":{"type":"boolean","description":"Run the command in the background and return immediately (useful for dev servers and long-running builds). You do not need to use '&' at the end of the command when using this parameter. You will be notified when it completes. DO NOT poll for completion."}},"required":["command"],"additionalProperties":false}`,
	"read":  `{"type":"object","properties":{"path":{"type":"string","description":"File or directory to read"},"offset":{"type":"integer","minimum":0,"description":"The line or directory entry to start reading from (1-based)"},"limit":{"type":"integer","minimum":0,"description":"The maximum number of lines or directory entries to read (defaults to and capped at 2000)"}},"required":["path"],"additionalProperties":false}`,
	"edit":  `{"type":"object","properties":{"path":{"type":"string","description":"File to edit"},"oldString":{"type":"string","description":"Exact text to find and replace"},"newString":{"type":"string","description":"Text to replace oldString with (must differ from oldString)"},"replaceAll":{"type":"boolean","description":"Whether to replace every occurrence of oldString. When false, oldString must match exactly once. Defaults to false."}},"required":["path","oldString","newString"],"additionalProperties":false}`,
}

// zenStubParams returns a fresh copy of a gate tool's parameter schema (each
// request gets its own map, so nothing downstream can mutate a shared one).
// Unknown names get an empty object schema.
func zenStubParams(name string) map[string]any {
	out := map[string]any{"type": "object", "properties": map[string]any{}}
	if raw, ok := zenGateToolParams[name]; ok {
		json.Unmarshal([]byte(raw), &out) //nolint:errcheck // compile-time constant, covered by tests
	}
	return out
}

// The opencode client fingerprint zen's free tier checks. Captured from
// opencode 2.0.27 (`opencode run --standalone` against a capture server) and
// re-verified against 2.0.29 (2026-10-14, which posts to
// https://opencode.ai/inference/v1 — zen/v1 now refuses the free tier):
//
//	User-Agent:            opencode/latest/2.0.29/cli
//	x-opencode-client:     cli
//	x-opencode-project:    <40 hex, stable per project>
//	x-opencode-session:    ses_ecc15552affe0cq3QbEJCOKyVy
//	x-opencode-session-id, x-session-affinity, x-session-id: same session id
//	traceparent, b3:       per-request trace context
//	tools:                 shell, read, edit, glob, grep, ... (2.x renamed bash → shell)
//
// Since 2026-10-12 zen validates the session id's structure: a random
// "ses_" + 12 hex no longer passes and every request 403s FreeTierError.
// Only a properly encoded identifier gets through (the timestamp itself is
// not checked for recency), so newZenSessionID mints exactly what opencode
// mints.
func setZenFingerprint(req *http.Request, ua, key string) {
	sid := newZenSessionID()
	req.Header.Set("User-Agent", ua)
	req.Header.Set("x-opencode-client", "cli")
	req.Header.Set("x-opencode-project", zenProjectID(key))
	req.Header.Set("x-opencode-session", sid)
	req.Header.Set("x-opencode-session-id", sid)
	req.Header.Set("x-session-affinity", sid)
	req.Header.Set("x-session-id", sid)
	trace, span, parent := randHex(16), randHex(8), randHex(8)
	req.Header.Set("traceparent", "00-"+trace+"-"+span+"-01")
	req.Header.Set("b3", trace+"-"+span+"-1-"+parent)
}

// zenProjectID stands in for opencode's project id (the sha1 of the repo's
// root commit). Derived from the key so each upstream account looks like
// one user working in one project, without exposing the key.
func zenProjectID(key string) string {
	sum := sha1.Sum([]byte("nano-llm-proxy/project/" + key))
	return hex.EncodeToString(sum[:])
}

var zenIDState struct {
	sync.Mutex
	lastMS  int64
	counter int64
}

// newZenSessionID mints an opencode descending identifier:
// "ses_" + 12 hex + 14 base62, where the hex is the low 48 bits of
// ^(unixMillis*0x1000 + counter), big-endian.
func newZenSessionID() string {
	zenIDState.Lock()
	ms := time.Now().UnixMilli()
	if ms != zenIDState.lastMS {
		zenIDState.lastMS = ms
		zenIDState.counter = 0
	}
	zenIDState.counter++
	now := ^uint64(ms*0x1000 + zenIDState.counter)
	zenIDState.Unlock()

	var timeBytes [6]byte
	for i := range timeBytes {
		timeBytes[i] = byte(now >> (40 - 8*i))
	}
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	tail := make([]byte, 14)
	if _, err := rand.Read(tail); err != nil {
		panic(err)
	}
	for i := range tail {
		tail[i] = alphabet[int(tail[i])%len(alphabet)]
	}
	return "ses_" + hex.EncodeToString(timeBytes[:]) + string(tail)
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
