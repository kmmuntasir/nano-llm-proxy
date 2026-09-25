package webtools

import (
	"fmt"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

// nativeOutcome summarizes one native-fetch attempt for the escalation
// decision. HTTPStatus is 0 when the request never got a response
// (transport error, timeout, dial refused — including SSRF blocks).
type nativeOutcome struct {
	HTTPStatus       int
	TransportErr     bool
	Unsupported      bool   // content type neither html nor text/markdown/json
	ExtractionFailed bool   // html fetched but readability/conversion produced nothing
	RawLen           int    // bytes fetched before extraction
	TextLen          int    // chars extracted from the body
	ContentType      string // response media type, for diagnostics
	Blocked          error  // non-nil when the SSRF guard rejected the URL
	RawErr           error  // underlying transport error, for diagnostics only
}

// fetchOutcome is a nativeOutcome plus the fetched payload on success.
type fetchOutcome struct {
	nativeOutcome
	Body        string
	ContentType string
	Title       string
}

// jsShellRawMin / jsShellTextMax: a page that ships >20 KiB of HTML but
// yields <500 chars of extractable text is almost always a JS-rendered
// shell — the native leg has nothing to say, escalate to the browser.
const (
	jsShellRawMin  = 20 << 10
	jsShellTextMax = 500
)

// escalateToRender decides whether the native fetch warrants an obscura
// attempt (or the reverse ordering case: whether a render-first miss should
// still try native — it always should, handled by the caller, not here).
//
// In fast mode: explicit render requests, any hard failure, unsupported
// content types, and the JS-shell heuristic escalate. SSRF blocks never
// escalate — the browser guard would reject the same URL.
func escalateToRender(mode string, explicitRender bool, o nativeOutcome) bool {
	if mode == settings.ReaderRender {
		// obscura already ran (or runs first); native is the fallback, not
		// the escalator. Explicit render is meaningless here.
		return false
	}
	if o.Blocked != nil {
		return false
	}
	if explicitRender {
		return true
	}
	if o.TransportErr || o.HTTPStatus >= 400 {
		return true
	}
	if o.Unsupported {
		return true
	}
	if o.RawLen > jsShellRawMin && o.TextLen < jsShellTextMax {
		return true
	}
	return false
}

// truncated clamps content to maxChars with an explicit marker so agents can
// tell truncation from a short page.
func truncated(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	cut := s[:maxChars]
	return fmt.Sprintf("%s\n\n…[truncated, %d more chars]", cut, len(s)-maxChars)
}
