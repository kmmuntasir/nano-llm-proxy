package gateway

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// proxyOpenAI is the rotation loop for plain OpenAI-compatible providers
// (a kilo preset or any GUI-added provider): pick/classify/retry against
// <base_url>/chat/completions with a Bearer upstream key.
func (g *gateway) proxyOpenAI(ref providerRef, w http.ResponseWriter, r *http.Request, body map[string]any, clientWantsStream bool, start time.Time) *upstreamFailure {
	model, _ := body["model"].(string)
	rot := g.newRotation(ref, model)
	if !keepsDeveloperRole(ref) && normalizeInstructionRoles(body) {
		log.Printf("%s model=%s: renamed developer role to system for upstream", ref.name, model)
	}
	raw, _ := json.Marshal(body)
	var lastHint string

	tr := traceFrom(r)
	for attempt := 0; attempt < g.rs().Retry.MaxKeysPerRequest; attempt++ {
		k := rot.next()
		if k == nil {
			break
		}
		tr.attempt(k.Label)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			ref.baseURL+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return failHint("internal: " + err.Error())
		}
		req.Header.Set("Authorization", "Bearer "+k.Key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := g.client.Do(req)
		if err != nil {
			tr.upstream(0)
			ref.pool.cooldown(k, 10*time.Second, "network: "+err.Error())
			lastHint = "upstream network error"
			continue
		}
		tr.upstream(resp.StatusCode)
		if resp.StatusCode != http.StatusOK {
			v := rot.rejected(k, resp)
			resp.Body.Close()
			switch v.action {
			case "failfast":
				return failHint(v.hint)
			case "flipsurface":
				// surface flips are zen machinery; for plain providers this
				// 503 just means the endpoint is gone
				lastHint = "upstream endpoint unavailable"
				continue
			}
			lastHint = v.hint
			continue
		}
		rot.succeeded()
		tu := streamKilo(w, resp)
		g.recordActivity(r, ref, model, k.Hash, start, http.StatusOK, "", tu)
		return nil
	}
	return rot.exhausted(lastHint)
}

// keepsDeveloperRole reports whether an OpenAI-compatible upstream accepts
// OpenAI's newer "developer" instruction role. Agents such as Pi send it for
// reasoning models on any endpoint they don't recognize, and strict
// upstreams refuse it — Z.ai answers 400 code 1214 "Incorrect role
// information". Every upstream accepts "system", so the role is renamed
// unless the upstream is OpenAI itself.
func keepsDeveloperRole(ref providerRef) bool {
	if ref.preset == "openai" {
		return true
	}
	u, err := url.Parse(ref.baseURL)
	return err == nil && strings.EqualFold(u.Hostname(), "api.openai.com")
}
