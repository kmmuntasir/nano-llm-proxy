package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// proxyOpenAI is the rotation loop for plain OpenAI-compatible providers
// (a kilo preset or any GUI-added provider): pick/classify/retry against
// <base_url>/chat/completions with a Bearer upstream key.
func (g *gateway) proxyOpenAI(ref providerRef, w http.ResponseWriter, r *http.Request, body map[string]any, clientWantsStream bool, start time.Time) string {
	model, _ := body["model"].(string)
	if !keepsDeveloperRole(ref) && normalizeInstructionRoles(body) {
		log.Printf("%s model=%s: renamed developer role to system for upstream", ref.name, model)
	}
	raw, _ := json.Marshal(body)
	exclude := map[string]bool{}
	var lastHint string

	tr := traceFrom(r)
	for attempt := 0; attempt < g.rs().Retry.MaxKeysPerRequest; attempt++ {
		k := ref.pool.pick(exclude)
		if k == nil {
			break
		}
		exclude[k.Hash] = true
		tr.attempt(k.Label)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			ref.baseURL+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return "internal: " + err.Error()
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
			v := g.classify(ref.pool, k, resp)
			resp.Body.Close()
			if v.cooldown > 0 {
				ref.pool.cooldown(k, v.cooldown, fmt.Sprintf("HTTP %d", resp.StatusCode))
			}
			switch v.action {
			case "failfast":
				return v.hint
			case "flipsurface":
				// surface flips are zen machinery; for plain providers this
				// 503 just means the endpoint is gone
				lastHint = "upstream endpoint unavailable"
				continue
			}
			lastHint = v.hint
			continue
		}
		tu := streamKilo(w, resp)
		g.recordActivity(r, ref, model, k.Hash, start, http.StatusOK, "", tu)
		return ""
	}
	if lastHint == "" {
		lastHint = fmt.Sprintf("No healthy %s keys available — every key is cooling or disabled", ref.name)
	}
	return lastHint
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
