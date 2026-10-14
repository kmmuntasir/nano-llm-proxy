package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Upstream key verification. A key is checked against every surface the
// provider exposes (OpenAI-compatible and/or Anthropic-compatible) before it
// is saved. No single request works everywhere — measured 2026-10-14:
//
//	zen        GET /models rejects a bad key (401); chat checks the model first
//	kilo       GET /models is public; chat rejects a bad key (401)
//	zai openai both reject a bad key
//	zai anthr. GET /v1/models is public; messages rejects a bad key (401)
//	openrouter model lists are public; chat/messages and GET /key reject (401)
//	kimi       its Anthropic surface has no /v1/models (404); messages rejects
//	xai        a wrong key is HTTP 400 "Incorrect API key provided" (401 only
//	           when no key is sent)
//	anthropic, deepseek, groq, minimax, openai: every probe rejects (401)
//
// so each surface gets two probes: its model list, and a one-token call for
// a model id that doesn't exist (OpenRouter also gets its key-info endpoint).
// The fake model means the upstream refuses the call before running
// anything — no tokens, no quota. The verdict, per probe:
//
//   - 401, or a 400 whose message says the key is wrong: the key is invalid.
//   - 403: the key is refused — it may be genuine, but this server can't use
//     it (region, IP allowlist, permissions, a blocked team or budget). Zen's
//     FreeTierError is excepted: that gate is about request shape.
//   - Network error, timeout or 5xx: the key is unverifiable.
//   - Anything else (2xx, other 4xx including 402/429): it authenticated.
//
// A key is saved only when no probe rejected it and every probe answered.

// keyCheckModel is the deliberately nonexistent model the probes ask for.
const keyCheckModel = "nano-llm-proxy-key-check"

// keyCheckTimeout bounds each probe.
const keyCheckTimeout = 15 * time.Second

// keyRejectedError is an upstream's refusal of a key; any other error from
// verifyUpstreamKey means the check couldn't complete.
type keyRejectedError struct {
	surface string // "OpenAI" | "Anthropic"
	status  int
	detail  string
	refused bool // 403: authenticated but not permitted (vs. an invalid key)
}

func (e *keyRejectedError) Error() string {
	return fmt.Sprintf("%s endpoint answered HTTP %d: %s", e.surface, e.status, e.detail)
}

// keyVerifier checks one upstream key against a provider's endpoints ("" =
// surface absent). The gateway's field defaults to verifyUpstreamKey; tests
// that don't exercise verification swap in a no-op.
type keyVerifier func(ctx context.Context, baseURL, anthropicBaseURL, key string) error

// verifyUpstreamKey runs the probes for every available surface.
func (g *gateway) verifyUpstreamKey(ctx context.Context, baseURL, anthropicBaseURL, key string) error {
	type probe struct {
		surface, method, url string
		headers              map[string]string
		body                 any
	}
	var probes []probe
	if baseURL != "" {
		h := map[string]string{"Authorization": "Bearer " + key}
		probes = append(probes,
			probe{"OpenAI", http.MethodGet, baseURL + "/models", h, nil},
			probe{"OpenAI", http.MethodPost, baseURL + "/chat/completions", h, map[string]any{
				"model": keyCheckModel, "max_tokens": 1,
				"messages": []any{map[string]any{"role": "user", "content": "ping"}},
			}})
		if path := keyInfoPath(baseURL); path != "" {
			// a key-info endpoint answers only for a valid key — a direct
			// check that needs no model at all
			probes = append(probes, probe{"OpenAI", http.MethodGet, baseURL + path, h, nil})
		}
	}
	if anthropicBaseURL != "" {
		h := map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}
		probes = append(probes,
			probe{"Anthropic", http.MethodGet, anthropicBaseURL + "/v1/models", h, nil},
			probe{"Anthropic", http.MethodPost, anthropicBaseURL + "/v1/messages", h, map[string]any{
				"model": keyCheckModel, "max_tokens": 1,
				"messages": []any{map[string]any{"role": "user", "content": "ping"}},
			}})
	}
	var unverifiable error
	for _, p := range probes {
		status, body, err := g.keyProbe(ctx, p.method, p.url, p.headers, p.body)
		switch {
		case err != nil:
			unverifiable = fmt.Errorf("%s endpoint unreachable (%s %s): %v", p.surface, p.method, p.url, err)
		case status == http.StatusUnauthorized ||
			(status == http.StatusBadRequest && saysKeyIsWrong(body)) ||
			(status == http.StatusForbidden && !strings.Contains(string(body), "FreeTierError")):
			// some upstreams echo the key back ("Incorrect API key provided:
			// sk-…") — the detail reaches the log and the GUI, so strip it
			detail := strings.ReplaceAll(oneLine(body), key, "<key>")
			return &keyRejectedError{surface: p.surface, status: status, detail: detail, refused: status == http.StatusForbidden}
		case status >= 500:
			unverifiable = fmt.Errorf("%s endpoint answered HTTP %d (%s %s)", p.surface, status, p.method, p.url)
		}
	}
	return unverifiable
}

// oneLine flattens an error body for a message: some upstreams (OpenAI)
// pretty-print JSON, so the first line alone is just "{". Whitespace runs
// collapse to one space; the result is capped at 200 characters.
func oneLine(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// keyErrorPhrases are how upstreams that answer 400 (not 401) for a wrong
// key say so — xAI: "Incorrect API key provided". Matched case-insensitively
// and kept specific, so an ordinary 400 (unknown model, bad parameter) still
// counts as "the key authenticated".
var keyErrorPhrases = []string{
	"incorrect api key", "invalid api key", "invalid_api_key", "api key is invalid",
	"api key not valid", "invalid x-api-key", "invalid authentication",
}

// saysKeyIsWrong reports whether a 400 body blames the key.
func saysKeyIsWrong(body []byte) bool {
	b := strings.ToLower(string(body))
	for _, p := range keyErrorPhrases {
		if strings.Contains(b, p) {
			return true
		}
	}
	return false
}

// keyInfoEndpoints maps an upstream host to its key-info endpoint (relative
// to the OpenAI-compatible root): a GET that succeeds only for a valid key.
// OpenRouter: GET /api/v1/key (verified 401 for a bad or missing key).
var keyInfoEndpoints = map[string]string{"openrouter.ai": "/key"}

// keyInfoPath returns the key-info endpoint for an OpenAI-compatible root,
// or "" when its host has none.
func keyInfoPath(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return keyInfoEndpoints[strings.ToLower(u.Hostname())]
}

// keyProbe performs one bounded request and returns the status and the
// start of the body.
func (g *gateway) keyProbe(ctx context.Context, method, url string, headers map[string]string, body any) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, keyCheckTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, b, nil
}

// Key-check verdicts for a key that failed verification — the statuses the
// bulk import reports per key (api_keyimport.go).
const (
	keyCheckInvalid      = "invalid"      // the upstream says the key is wrong
	keyCheckRefused      = "refused"      // authenticated, but this server may not use it
	keyCheckUnverifiable = "unverifiable" // a probe couldn't complete
)

// classifyKeyCheck turns a verification failure into its verdict and the
// message the admin sees. what names the key ("Key \"work-1\""); provider
// is the provider's name. Writes nothing, so a single add and a bulk import
// word every failure identically.
func classifyKeyCheck(err error, provider, what string) (status, message string) {
	var rejected *keyRejectedError
	if errors.As(err, &rejected) {
		if rejected.refused {
			return keyCheckRefused, fmt.Sprintf("%s was refused — %s's %s. The key may be genuine, but this server can't use it (region, IP allowlist, permissions, or a blocked account or budget).", what, provider, rejected)
		}
		return keyCheckInvalid, fmt.Sprintf("%s is invalid — %s's %s", what, provider, rejected)
	}
	return keyCheckUnverifiable, fmt.Sprintf("Couldn't verify %s against %s — %v. Nothing was saved; try again.", what, provider, err)
}

// keyCheckVerdict runs the gateway's verifier (none configured = pass) and
// returns "" when the key passed, else classifyKeyCheck's verdict and
// message. Failures are logged by label; the key itself never is.
func (g *gateway) keyCheckVerdict(ctx context.Context, provider, baseURL, anthropicBaseURL, key, what string) (status, message string) {
	if g.keyCheck == nil {
		return "", ""
	}
	err := g.keyCheck(ctx, baseURL, anthropicBaseURL, key)
	if err == nil {
		return "", ""
	}
	log.Printf("provider key check provider=%s key=%q failed: %v", provider, what, err)
	return classifyKeyCheck(err, provider, what)
}

// checkProviderKey runs the gateway's verifier and, on failure, writes the
// API error (400 rejected / 503 unverifiable) and returns false. what names
// the key in messages; the key itself is never logged or echoed.
func (g *gateway) checkProviderKey(w http.ResponseWriter, r *http.Request, provider, baseURL, anthropicBaseURL, key, what string) bool {
	status, msg := g.keyCheckVerdict(r.Context(), provider, baseURL, anthropicBaseURL, key, what)
	switch status {
	case "":
		return true
	case keyCheckUnverifiable:
		apiErr(w, http.StatusServiceUnavailable, msg)
	default:
		apiErr(w, http.StatusBadRequest, msg)
	}
	return false
}
