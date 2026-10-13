package gateway

import (
	"encoding/hex"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

// zenSessionFresh decodes the descending opencode identifier and reports
// whether its embedded millisecond timestamp sits within a minute of now.
// Only the low 36 bits of the millisecond clock survive the 48-bit field.
func zenSessionFresh(s string, now time.Time) bool {
	b, err := hex.DecodeString(s[4:16])
	if err != nil || len(b) != 6 {
		return false
	}
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	v = ^v & (1<<48 - 1)
	if v&0xfff == 0 { // counter starts at 1
		return false
	}
	const mask = 1<<36 - 1
	got := int64(v>>12) & mask
	want := now.UnixMilli() & mask
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < 60_000
}

func TestZenSessionIDMatchesOpencodeEncoding(t *testing.T) {
	// minted by opencode 2.0.27 itself; decodes to counter 1
	if !regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`).MatchString("ses_ecc15552affe0cq3QbEJCOKyVy") {
		t.Fatal("reference id shape")
	}
	seen := map[string]bool{}
	for range 50 {
		id := newZenSessionID()
		if !validZenSession(id) {
			t.Fatalf("not an opencode-encoded session id: %q", id)
		}
		if seen[id[:16]] {
			t.Fatalf("time prefix repeated within a millisecond: %q", id)
		}
		seen[id[:16]] = true
	}
}

func TestZenFingerprintHeaders(t *testing.T) {
	req := httptest.NewRequest("POST", "/chat/completions", nil)
	setZenFingerprint(req, "opencode/latest/2.0.27/cli", "sk-a")
	h := req.Header
	sid := h.Get("x-opencode-session")
	if !validZenSession(sid) {
		t.Fatalf("session %q", sid)
	}
	for _, k := range []string{"x-opencode-session-id", "x-session-affinity", "x-session-id"} {
		if h.Get(k) != sid {
			t.Errorf("%s = %q, want %q", k, h.Get(k), sid)
		}
	}
	if h.Get("User-Agent") != "opencode/latest/2.0.27/cli" || h.Get("x-opencode-client") != "cli" {
		t.Errorf("ua/client: %q %q", h.Get("User-Agent"), h.Get("x-opencode-client"))
	}
	if p := h.Get("x-opencode-project"); !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(p) || p != zenProjectID("sk-a") || p == zenProjectID("sk-b") {
		t.Errorf("project id %q not stable per key", p)
	}
	tp := regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-01$`).FindStringSubmatch(h.Get("traceparent"))
	if tp == nil {
		t.Fatalf("traceparent %q", h.Get("traceparent"))
	}
	if !regexp.MustCompile(`^` + tp[1] + `-` + tp[2] + `-1-[0-9a-f]{16}$`).MatchString(h.Get("b3")) {
		t.Errorf("b3 %q does not match traceparent %q", h.Get("b3"), h.Get("traceparent"))
	}
}
