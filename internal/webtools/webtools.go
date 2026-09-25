// Package webtools implements the gateway's self-hosted web search and
// read backends: SearXNG for search, a native Go fetch with readability for
// reading, and the obscura headless browser for pages that need JS
// rendering. It knows nothing about MCP — internal/gateway/mcp.go exposes
// these as tools.
package webtools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

// Result is one successful tool output, already clamped.
type Result struct {
	Content string // markdown (or raw text/json)
	Title   string
	Method  string // "native" | "render" for reads; "searxng" for search
	URL     string
}

// Service is safe for concurrent use. Configuration is pulled live on every
// call so GUI settings changes apply without restarts.
type Service struct {
	cfg func() settings.WebToolsSettings

	guarded  *http.Transport // SSRF-guarded, user URLs only
	guardOff bool            // test seam: skip both SSRF checks (loopback httptest)
	search   *http.Client    // unguarded, admin-trusted SearXNG endpoint

	ob  *obscuraRunner
	sem *dynSemaphore
}

// NewService wires a Service reading live settings through fn.
func NewService(fn func() settings.WebToolsSettings) *Service {
	return &Service{
		cfg:     fn,
		guarded: guardedTransport(),
		search:  &http.Client{Transport: &http.Transport{}},
		ob:      newObscuraRunner(),
		sem:     newDynSemaphore(fn().ObscuraConcurrency),
	}
}

// transport picks the guarded or plain transport per the test seam.
func (s *Service) transport() http.RoundTripper {
	if s.guardOff {
		return http.DefaultTransport
	}
	return s.guarded
}

// TestSearxng runs one real query; used by the admin diagnostics endpoint.
func (s *Service) TestSearxng(ctx context.Context) (string, error) {
	return searchQuery(ctx, s.cfg(), s.search, "searxng health check", 1)
}

// TestObscura reports `obscura --version`; with deep, also fetch example.com.
func (s *Service) TestObscura(ctx context.Context, deep bool) (version string, detail string, err error) {
	cfg := s.cfg()
	version, err = s.ob.version(ctx, cfg.ObscuraPath)
	if err != nil {
		return "", "", err
	}
	if !deep {
		return version, "binary runs", nil
	}
	start := time.Now()
	md, ferr := s.ob.fetch(ctx, cfg, s.sem, "https://example.com")
	if ferr != nil {
		return version, fmt.Sprintf("fetch of example.com failed: %v", ferr), ferr
	}
	return version, fmt.Sprintf("fetched example.com in %d ms (%d chars)", time.Since(start).Milliseconds(), len(md)), nil
}

// Search runs a web search via SearXNG.
func (s *Service) Search(ctx context.Context, query string, maxResults int) (Result, error) {
	cfg := s.cfg()
	out, err := searchQuery(ctx, cfg, s.search, query, maxResults)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: out, Method: "searxng"}, nil
}

// Read fetches a page: native-first with obscura escalation in fast mode
// (the default), obscura-first with native fallback in render mode.
func (s *Service) Read(ctx context.Context, rawURL string, render bool, maxChars int) (Result, error) {
	cfg := s.cfg()
	if maxChars <= 0 {
		maxChars = cfg.MaxChars
	}

	if cfg.ReaderMode == settings.ReaderRender {
		return s.readRenderFirst(ctx, cfg, rawURL, maxChars)
	}
	return s.readFast(ctx, cfg, rawURL, render, maxChars)
}

func (s *Service) readFast(ctx context.Context, cfg settings.WebToolsSettings, rawURL string, render bool, maxChars int) (Result, error) {
	// An explicit render request is the caller saying "I know this page
	// needs a browser" — obscura first, native as fallback.
	if render {
		md, oerr := s.ob.fetch(ctx, cfg, s.sem, rawURL)
		if oerr == nil {
			return Result{
				Content: truncated(strings.TrimSpace(md), maxChars),
				Method:  "render",
				URL:     rawURL,
			}, nil
		}
		out, ok := s.fetchNative(ctx, rawURL)
		if ok {
			return Result{
				Content: truncated(out.Body, maxChars),
				Title:   out.Title,
				Method:  "native",
				URL:     rawURL,
			}, nil
		}
		return Result{}, fmt.Errorf("%w (render leg failed: %v)", readFailure(rawURL, out.nativeOutcome, nil), oerr)
	}

	out, ok := s.fetchNative(ctx, rawURL)
	if ok {
		return Result{
			Content: truncated(out.Body, maxChars),
			Title:   out.Title,
			Method:  "native",
			URL:     rawURL,
		}, nil
	}
	if !escalateToRender(cfg.ReaderMode, render, out.nativeOutcome) {
		return Result{}, readFailure(rawURL, out.nativeOutcome, nil)
	}
	md, oerr := s.ob.fetch(ctx, cfg, s.sem, rawURL)
	if oerr != nil {
		// Both legs failed: prefer the native diagnostics, mention obscura's.
		return Result{}, fmt.Errorf("%w (render leg also failed: %v)", readFailure(rawURL, out.nativeOutcome, nil), oerr)
	}
	return Result{
		Content: truncated(strings.TrimSpace(md), maxChars),
		Method:  "render",
		URL:     rawURL,
	}, nil
}

func (s *Service) readRenderFirst(ctx context.Context, cfg settings.WebToolsSettings, rawURL string, maxChars int) (Result, error) {
	// Render mode: obscura first regardless of the explicit flag.
	md, oerr := s.ob.fetch(ctx, cfg, s.sem, rawURL)
	if oerr == nil {
		return Result{
			Content: truncated(strings.TrimSpace(md), maxChars),
			Method:  "render",
			URL:     rawURL,
		}, nil
	}
	out, ok := s.fetchNative(ctx, rawURL)
	if ok {
		return Result{
			Content: truncated(out.Body, maxChars),
			Title:   out.Title,
			Method:  "native",
			URL:     rawURL,
		}, nil
	}
	return Result{}, fmt.Errorf("%w (render leg failed: %v)", readFailure(rawURL, out.nativeOutcome, nil), oerr)
}

// readFailure turns a native outcome into an agent-actionable error.
func readFailure(rawURL string, o nativeOutcome, _ error) error {
	switch {
	case o.Blocked != nil:
		return fmt.Errorf("blocked %s: %v", rawURL, o.Blocked)
	case o.HTTPStatus == http.StatusNotFound:
		return fmt.Errorf("page not found (404): %s", rawURL)
	case o.HTTPStatus == http.StatusForbidden || o.HTTPStatus == http.StatusTooManyRequests:
		return fmt.Errorf("site refused the fetch (status %d): %s", o.HTTPStatus, rawURL)
	case o.HTTPStatus >= 400:
		return fmt.Errorf("fetch failed with status %d: %s", o.HTTPStatus, rawURL)
	case o.TransportErr:
		if o.RawErr != nil {
			return fmt.Errorf("could not fetch %s: %v", rawURL, o.RawErr)
		}
		return fmt.Errorf("could not fetch %s (network or timeout)", rawURL)
	case o.Unsupported:
		return fmt.Errorf("unsupported content type %q: %s", o.ContentType, rawURL)
	case o.ExtractionFailed:
		return fmt.Errorf("fetched %s but no readable content could be extracted", rawURL)
	default:
		return fmt.Errorf("fetch of %s failed", rawURL)
	}
}
