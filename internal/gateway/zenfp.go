package gateway

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// zenGateTools are the tool names the free-tier gate expects to see in a
// request: a body with no tools 403s FreeTierError. Matches the opencode 2.x
// tool set (1.x called the shell tool "bash"); zen accepts either today.
var zenGateTools = []string{"shell", "read", "edit"}

// The opencode client fingerprint zen's free tier checks. Captured from
// opencode 2.0.27 (`opencode run --standalone` against a capture server):
//
//	User-Agent:            opencode/latest/2.0.27/cli
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
