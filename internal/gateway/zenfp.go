package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// The zen free tier checks two things the gateway must keep satisfying:
//
//  1. The request body (chat surface): stream:true and a tools array
//     containing a tool named "read" (schema contents are not inspected —
//     bisected 2026-11-25; see injectChatFingerprint and zenGateTools).
//  2. The x-opencode-session header: a structurally valid opencode
//     identifier ("ses_" + 12 hex + 14 base62 in the descending-timestamp
//     encoding below). Garbage is rejected with the same 429
//     FreeUsageLimitError the wire-gate answers (verified 2026-11-25).
//
// Everything else about the opencode client is now enforced at the wire
// level: zen fingerprints the TLS ClientHello and the HTTP/1.1 head, so
// zenwire.go speaks raw HTTP/1.1 over a uTLS connection replaying bun's
// ClientHello. x-opencode-request / x-opencode-project are sent in bun's
// shape but are not currently validated.

// zenGateTools are the tools the zen free-tier gate expects to see in a
// request: a tool named "read" must be present (any schema passes today,
// but the stubs carry opencode's real parameter schemas so the request
// stays indistinguishable if schema checks return).
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

// newZenSessionID mints an opencode session identifier:
// "ses_" + 12 hex + 14 base62, where the hex is the low 48 bits of
// ^(unixMillis*0x1000 + counter), big-endian.
func newZenSessionID() string { return mintZenID("ses_") }

// newZenRequestID mints an opencode per-request identifier ("msg_" + the
// same descending encoding) for the x-opencode-request header.
func newZenRequestID() string { return mintZenID("msg_") }

var zenIDState struct {
	sync.Mutex
	lastMS  int64
	counter int64
}

func mintZenID(prefix string) string {
	zenIDState.Lock()
	ms := time.Now().UnixMilli()
	if ms != zenIDState.lastMS {
		zenIDState.lastMS = ms
		zenIDState.counter = 0
	}
	zenIDState.counter++
	now := ^(ms*0x1000 + zenIDState.counter)
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
	return prefix + hex.EncodeToString(timeBytes[:]) + string(tail)
}
