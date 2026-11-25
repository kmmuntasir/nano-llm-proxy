package gateway

import (
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
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

func TestZenRequestIDShape(t *testing.T) {
	re := regexp.MustCompile(`^msg_[0-9a-f]{12}[0-9A-Za-z]{14}$`)
	for range 10 {
		if id := newZenRequestID(); !re.MatchString(id) {
			t.Fatalf("request id %q does not match the opencode msg_ shape", id)
		}
	}
}

// TestZenDoWire checks the bun-shaped request zenDo writes: the header set
// and the opencode identifiers. (Header casing and order can't be asserted
// through net/http's test server — it canonicalizes on read — they are
// pinned by the live bisect behind internal/gateway/zenwire.go.)
func TestZenDoWire(t *testing.T) {
	var mu sync.Mutex
	var gotUA, gotSession, gotRequest, gotBody string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotUA = r.Header.Get("User-Agent")
		gotSession = r.Header.Get("x-opencode-session")
		gotRequest = r.Header.Get("x-opencode-request")
		gotBody = string(b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		io.WriteString(w, `{"ok":true}`) //nolint:errcheck
	}))
	defer up.Close()

	g, _ := testStoreGateway(t, up.URL)
	resp, err := g.zenDo(httptest.NewRequest("POST", "/v1/chat/completions", nil).Context(),
		up.URL+"/chat/completions", "sk-zen-a", []byte(`{"ping":1}`))
	if err != nil {
		t.Fatal("zenDo:", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	if gotBody != `{"ping":1}` {
		t.Fatalf("upstream body %q", gotBody)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("response body %q", string(body))
	}
	if gotUA != g.rs().Zen.UserAgent {
		t.Fatalf("UA %q, want %q", gotUA, g.rs().Zen.UserAgent)
	}
	if !validZenSession(gotSession) {
		t.Fatalf("session header %q", gotSession)
	}
	if !strings.HasPrefix(gotRequest, "msg_") || len(gotRequest) != len("msg_")+26 {
		t.Fatalf("request header %q", gotRequest)
	}
}

// TestZenDoChunked makes sure the raw HTTP/1.1 client decodes chunked
// bodies (zen streams SSE with Transfer-Encoding: chunked).
func TestZenDoChunked(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(200)
		io.WriteString(w, "data: one\n\n") //nolint:errcheck
		io.WriteString(w, "data: two\n\n") //nolint:errcheck
	}))
	defer up.Close()

	g, _ := testStoreGateway(t, up.URL)
	resp, err := g.zenDo(httptest.NewRequest("POST", "/v1/chat/completions", nil).Context(),
		up.URL+"/chat/completions", "sk-zen-a", []byte(`{}`))
	if err != nil {
		t.Fatal("zenDo:", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal("read:", err)
	}
	want := "data: one\n\ndata: two\n\n"
	if string(body) != want {
		t.Fatalf("chunked body not decoded: %q, want %q", string(body), want)
	}
}
