package webtools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

// newTestService builds a Service against a live settings snapshot, with
// the SSRF guard lifted so tests can fetch from loopback httptest servers.
// (The guard itself is covered by TestGuardedTransportBlocksPrivate and
// TestFetchNativeSSRFBlocked, which build their own guarded Service.)
func newTestService(cfg *settings.WebToolsSettings) *Service {
	s := NewService(func() settings.WebToolsSettings { return *cfg })
	s.guardOff = true
	return s
}

const articleHTML = `<!doctype html><html><head><title>Test Article</title></head>
<body><nav>Home About Contact Blog Archives</nav>
<article><h1>Real Heading</h1><p>This is the paragraph content of the article body.</p>
<p>Second paragraph with more words to satisfy readability extraction.</p></article>
<footer>copyright 2026</footer></body></html>`

var jsShellHTML = `<html><head><title>SPA</title></head><body><div id="root"></div><script>` +
	strings.Repeat("var x=1;\n", 2500) + `</script></body></html>`

func TestFetchNativeArticle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(articleHTML))
	}))
	defer srv.Close()

	cfg := testCfg("http://127.0.0.1:1")
	s := newTestService(&cfg)
	out, ok := s.fetchNative(context.Background(), srv.URL)
	if !ok {
		t.Fatalf("fetchNative failed: %+v", out)
	}
	if !strings.Contains(out.Body, "Real Heading") || !strings.Contains(out.Body, "paragraph content") {
		t.Fatalf("markdown missing article content: %q", out.Body)
	}
	if strings.Contains(out.Body, "Home About Contact") {
		t.Fatalf("nav chrome not stripped: %q", out.Body)
	}
	if out.TextLen < 30 {
		t.Fatalf("TextLen suspiciously small: %d", out.TextLen)
	}
}

func TestFetchNativeJSShellEscalates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(jsShellHTML))
	}))
	defer srv.Close()

	cfg := testCfg("http://127.0.0.1:1")
	s := newTestService(&cfg)
	out, ok := s.fetchNative(context.Background(), srv.URL)
	if ok {
		t.Fatalf("JS shell extracted as content: %+v", out)
	}
	if !escalateToRender(cfg.ReaderMode, false, out.nativeOutcome) {
		t.Fatalf("JS shell must escalate: %+v", out.nativeOutcome)
	}
}

func TestFetchNativePlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("plain robots.txt style content\nDisallow: /private\n"))
	}))
	defer srv.Close()

	cfg := testCfg("http://127.0.0.1:1")
	s := newTestService(&cfg)
	out, ok := s.fetchNative(context.Background(), srv.URL)
	if !ok || !strings.Contains(out.Body, "Disallow: /private") {
		t.Fatalf("plain text passthrough failed: ok=%v body=%q", ok, out.Body)
	}
}

func TestFetchNativeJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()

	cfg := testCfg("http://127.0.0.1:1")
	s := newTestService(&cfg)
	out, ok := s.fetchNative(context.Background(), srv.URL)
	if !ok || !strings.Contains(out.Body, "```json") || !strings.Contains(out.Body, `"hello":"world"`) {
		t.Fatalf("json fencing failed: ok=%v body=%q", ok, out.Body)
	}
}

func TestFetchNativeSSRFBlocked(t *testing.T) {
	cfg := testCfg("http://127.0.0.1:1")
	s := NewService(func() settings.WebToolsSettings { return cfg }) // guard ON
	out, ok := s.fetchNative(context.Background(), "http://127.0.0.1:123/x")
	if ok {
		t.Fatal("loopback fetch must not succeed")
	}
	if out.Blocked == nil {
		t.Fatalf("want Blocked set: %+v", out.nativeOutcome)
	}
	if escalateToRender("fast", false, out.nativeOutcome) {
		t.Fatal("SSRF block must not escalate to obscura")
	}
}

func TestFetchNativeStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", 403)
	}))
	defer srv.Close()

	cfg := testCfg("http://127.0.0.1:1")
	s := newTestService(&cfg)
	out, ok := s.fetchNative(context.Background(), srv.URL)
	if ok {
		t.Fatal("403 must not be ok")
	}
	if out.HTTPStatus != 403 || !escalateToRender("fast", false, out.nativeOutcome) {
		t.Fatalf("403 should escalate: %+v", out.nativeOutcome)
	}
}

func TestReadFastWithFakeObscura(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(jsShellHTML))
	}))
	defer srv.Close()

	// Fake obscura: echoes markdown regardless of args.
	script := filepath.Join(t.TempDir(), "obscura")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho '# Rendered by fake obscura'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := testCfg("http://127.0.0.1:1")
	cfg.ObscuraPath = script
	cfg.ObscuraTimeoutSeconds = 10
	s := newTestService(&cfg)

	res, err := s.Read(context.Background(), srv.URL, false, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Method != "render" || !strings.Contains(res.Content, "fake obscura") {
		t.Fatalf("want rendered result: %+v", res)
	}
}

func TestReadExplicitRender(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(articleHTML))
	}))
	defer srv.Close()

	script := filepath.Join(t.TempDir(), "obscura")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho '# rendered'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testCfg("http://127.0.0.1:1")
	cfg.ObscuraPath = script
	cfg.ObscuraTimeoutSeconds = 10
	s := newTestService(&cfg)

	res, err := s.Read(context.Background(), srv.URL, true, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Method != "render" {
		t.Fatalf("explicit render must use obscura, got %q", res.Method)
	}
}

func TestReadRenderModeObscuraFirst(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(articleHTML))
	}))
	defer srv.Close()

	script := filepath.Join(t.TempDir(), "obscura")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho '# render-first'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testCfg("http://127.0.0.1:1")
	cfg.ObscuraPath = script
	cfg.ObscuraTimeoutSeconds = 10
	cfg.ReaderMode = settings.ReaderRender
	s := newTestService(&cfg)

	res, err := s.Read(context.Background(), srv.URL, false, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.Method != "render" || !strings.Contains(res.Content, "render-first") {
		t.Fatalf("render mode must try obscura first: %+v", res)
	}
}

func TestReadClampsToMaxChars(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(strings.Repeat("x", 5000)))
	}))
	defer srv.Close()

	cfg := testCfg("http://127.0.0.1:1")
	s := newTestService(&cfg)
	res, err := s.Read(context.Background(), srv.URL, false, 1000)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(res.Content, "4000 more chars") {
		t.Fatalf("missing clamp marker: len=%d", len(res.Content))
	}
}

func TestReadSearchUnreachable(t *testing.T) {
	cfg := testCfg("http://127.0.0.1:1")
	s := newTestService(&cfg)
	if _, err := s.Search(context.Background(), "q", 5); err == nil {
		t.Fatal("search against dead searxng must fail")
	}
}

func TestObscuraRunnerFakeVersion(t *testing.T) {
	script := filepath.Join(t.TempDir(), "obscura")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n[ \"$1\" = --version ] && echo 'obscura 9.9.9'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := newObscuraRunner()
	v, err := o.version(context.Background(), script)
	if err != nil || v != "obscura 9.9.9" {
		t.Fatalf("version = %q, %v", v, err)
	}
}
