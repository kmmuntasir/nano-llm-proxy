package webtools

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckDialedIP(t *testing.T) {
	rc := &testRawConn{}
	cases := []struct {
		addr    string
		blocked bool
	}{
		{"93.184.216.34:443", false},                        // public v4
		{"[2606:2800:220:1:248:1893:25c8:1946]:443", false}, // public v6
		{"127.0.0.1:80", true},                              // loopback v4
		{"127.8.8.8:80", true},                              // loopback range
		{"[::1]:80", true},                                  // loopback v6
		{"10.0.0.1:80", true},                               // RFC1918
		{"172.16.0.1:80", true},                             // RFC1918
		{"172.31.255.255:80", true},                         // RFC1918
		{"192.168.0.153:8787", true},                        // RFC1918
		{"169.254.1.1:80", true},                            // link-local
		{"[fe80::1]:80", true},                              // link-local v6
		{"0.0.0.0:80", true},                                // unspecified
		{"[fd00::1]:80", true},                              // ULA
		{"[::ffff:127.0.0.1]:80", true},                     // v4-mapped loopback
		{"[::ffff:192.168.1.1]:80", true},                   // v4-mapped private
		{"224.0.0.1:80", true},                              // multicast
		{"not-an-addr", true},                               // unparseable fails closed
	}
	for _, tc := range cases {
		err := checkDialedIP("tcp", tc.addr, rc)
		if tc.blocked && err == nil {
			t.Errorf("checkDialedIP(%q) = nil, want blocked", tc.addr)
		}
		if !tc.blocked && err != nil {
			t.Errorf("checkDialedIP(%q) = %v, want allowed", tc.addr, err)
		}
	}
}

func TestURLAllowed(t *testing.T) {
	cases := []struct {
		url     string
		wantErr bool
	}{
		{"https://go.dev/blog/context", false},
		{"http://example.com", false},
		{"ftp://example.com", true},
		{"file:///etc/passwd", true},
		{"http://", true},
		{"http://user:pass@example.com", true},
		{"http://127.0.0.1/x", true},
		{"http://192.168.0.1/x", true},
		{"http://[::1]/x", true},
		{"https://docs.example.com", false},
		{"", true},
	}
	for _, tc := range cases {
		err := URLAllowed(tc.url)
		if tc.wantErr && err == nil {
			t.Errorf("URLAllowed(%q) = nil, want error", tc.url)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("URLAllowed(%q) = %v, want allowed", tc.url, err)
		}
	}
}

// TestGuardedTransportBlocksPrivate ensures the transport-level guard
// refuses to dial a loopback server even when the hostname resolves to it.
func TestGuardedTransportBlocksPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("handler must never be reached")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	transport := guardedTransport()
	client := &http.Client{Transport: transport}
	_, err := client.Get(srv.URL) // srv.URL is http://127.0.0.1:port
	if err == nil {
		t.Fatal("fetch of loopback URL through guarded transport succeeded, want block")
	}
	if !strings.Contains(err.Error(), "private") && !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTruncated(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := truncated(long, 10)
	if len(got) <= 10 {
		t.Fatalf("truncated output unexpectedly short: %q", got)
	}
	if !strings.Contains(got, "truncated") || !strings.Contains(got, "90 more chars") {
		t.Fatalf("missing truncation marker: %q", got)
	}
	if got := truncated("short", 100); got != "short" {
		t.Fatalf("truncated short string: %q", got)
	}
}

func TestEscalateToRender(t *testing.T) {
	cases := []struct {
		name           string
		mode           string
		explicitRender bool
		o              nativeOutcome
		want           bool
	}{
		{"render mode never escalates", "render", false, nativeOutcome{HTTPStatus: 403}, false},
		{"fast success no escalate", "fast", false, nativeOutcome{HTTPStatus: 200, RawLen: 5000, TextLen: 3000}, false},
		{"explicit render", "fast", true, nativeOutcome{HTTPStatus: 200, RawLen: 5000, TextLen: 3000}, true},
		{"transport error", "fast", false, nativeOutcome{TransportErr: true}, true},
		{"403", "fast", false, nativeOutcome{HTTPStatus: 403}, true},
		{"404 escalates", "fast", false, nativeOutcome{HTTPStatus: 404}, true},
		{"500", "fast", false, nativeOutcome{HTTPStatus: 500}, true},
		{"unsupported type", "fast", false, nativeOutcome{HTTPStatus: 200, Unsupported: true}, true},
		{"js shell", "fast", false, nativeOutcome{HTTPStatus: 200, RawLen: 30000, TextLen: 12}, true},
		{"big html big text", "fast", false, nativeOutcome{HTTPStatus: 200, RawLen: 30000, TextLen: 800}, false},
		{"small html small text", "fast", false, nativeOutcome{HTTPStatus: 200, RawLen: 500, TextLen: 12}, false},
		{"ssrf block never escalates", "fast", true, nativeOutcome{Blocked: errors.New("x")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := escalateToRender(tc.mode, tc.explicitRender, tc.o); got != tc.want {
				t.Fatalf("escalateToRender(%q, %v, %+v) = %v, want %v", tc.mode, tc.explicitRender, tc.o, got, tc.want)
			}
		})
	}
}

// testRawConn satisfies the syscall.RawConn interface for Control-hook tests.
type testRawConn struct{}

func (testRawConn) Control(func(fd uintptr)) error   { return nil }
func (testRawConn) Read(func(fd uintptr) bool) error { return nil }
func (testRawConn) Write(func(fd uintptr) bool) error {
	return nil
}
