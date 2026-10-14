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
//
// so each surface gets two probes: its model list, and a one-token call for
// a model id that doesn't exist. The fake model means the upstream refuses
// the call before running anything — no tokens, no quota. A key is rejected
// when any probe answers 401/403 (zen's FreeTierError excepted: that gate is
// about request shape, not the key); it is accepted only when every probe
// answered and none rejected it. A probe that can't complete (network error,
// timeout, 5xx) makes the key unverifiable, and it is not saved.

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
			(status == http.StatusForbidden && !strings.Contains(string(body), "FreeTierError")):
			// some upstreams echo the key back ("Incorrect API key provided:
			// sk-…") — the detail reaches the log and the GUI, so strip it
			detail := strings.ReplaceAll(firstLine(body), key, "<key>")
			return &keyRejectedError{surface: p.surface, status: status, detail: detail}
		case status >= 500:
			unverifiable = fmt.Errorf("%s endpoint answered HTTP %d (%s %s)", p.surface, status, p.method, p.url)
		}
	}
	return unverifiable
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

// checkProviderKey runs the gateway's verifier and, on failure, writes the
// API error (400 rejected / 503 unverifiable) and returns false. what names
// the key in messages; the key itself is never logged or echoed.
func (g *gateway) checkProviderKey(w http.ResponseWriter, r *http.Request, provider, baseURL, anthropicBaseURL, key, what string) bool {
	if g.keyCheck == nil {
		return true
	}
	err := g.keyCheck(r.Context(), baseURL, anthropicBaseURL, key)
	if err == nil {
		return true
	}
	log.Printf("provider key check provider=%s key=%q failed: %v", provider, what, err)
	var rejected *keyRejectedError
	if errors.As(err, &rejected) {
		apiErr(w, http.StatusBadRequest, fmt.Sprintf("%s is invalid — %s's %s", what, provider, rejected))
		return false
	}
	apiErr(w, http.StatusServiceUnavailable, fmt.Sprintf("Couldn't verify %s against %s — %v. Nothing was saved; try again.", what, provider, err))
	return false
}
