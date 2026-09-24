package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// handleResponses serves POST /v1/responses for zen models that live on the
// Responses surface (muse-spark, ling-fin, ...). Request bodies pass through
// natively — only the fingerprint is injected — so clients get full fidelity
// (reasoning events, native SSE). Chat-surface and non-zen models are
// rejected with a pointer to /v1/chat/completions.
func (g *gateway) handleResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, err := parseClientBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	model, _ := body["model"].(string)
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeErr(w, http.StatusBadRequest,
			`Model must be "zen/<id>"; only zen has a Responses API — use /v1/chat/completions`)
		return
	}
	ref, ok := g.provider(parts[0])
	if !ok || ref.typ != "opencode" {
		writeErr(w, http.StatusBadRequest,
			fmt.Sprintf("store.Provider %q has no Responses API — use /v1/chat/completions", parts[0]))
		return
	}
	upstreamModel := parts[1]
	if ref.builtin {
		upstreamModel = stripModelSuffix(upstreamModel) // cosmetic ctx/modality labels
	} else {
		upstreamModel = stripClientSuffix(upstreamModel)
	}
	body["model"] = upstreamModel
	if g.surfaceFor(upstreamModel) != "responses" {
		writeErr(w, http.StatusBadRequest,
			fmt.Sprintf("Model %s is served on the chat surface — use /v1/chat/completions", upstreamModel))
		return
	}
	clientWantsStream := bodyStreamFlag(body)

	if out := g.proxyZenResponses(ref, w, r, body, upstreamModel, clientWantsStream, start); out != "" {
		g.recordActivity(r, ref, upstreamModel, "", start, http.StatusBadGateway, out, tokenUsage{})
		writeErr(w, http.StatusBadGateway, out)
	}
}

// ensureFlatStubs guarantees the gate tools in flat Responses shape.
func ensureFlatStubs(tools []any) []any {
	for _, name := range []string{"bash", "read", "edit"} {
		if !containsFlatTool(tools, name) {
			tools = append(tools, stubToolFlat(name))
		}
	}
	return tools
}

func containsFlatTool(tools []any, name string) bool {
	for _, t := range tools {
		if tm, ok := t.(map[string]any); ok && tm["name"] == name {
			return true
		}
	}
	return false
}

// proxyZenResponses runs the rotation loop against /responses.
func (g *gateway) proxyZenResponses(ref providerRef, w http.ResponseWriter, r *http.Request, body map[string]any, model string, clientWantsStream bool, start time.Time) string {
	exclude := map[string]bool{}
	var lastHint string

	for attempt := 0; attempt < g.rs().Retry.MaxKeysPerRequest; attempt++ {
		k := ref.pool.pick(exclude)
		if k == nil {
			break
		}
		exclude[k.Hash] = true

		// fingerprint: stream always on upstream, gate stubs in flat shape
		body["stream"] = true
		tools, _ := body["tools"].([]any)
		body["tools"] = ensureFlatStubs(tools)
		raw, _ := json.Marshal(body)

		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			ref.baseURL+"/responses", bytes.NewReader(raw))
		if err != nil {
			return "internal: " + err.Error()
		}
		req.Header.Set("Authorization", "Bearer "+k.Key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", g.rs().Zen.UserAgent)
		req.Header.Set("x-opencode-session", newZenSessionID())

		resp, err := g.client.Do(req)
		if err != nil {
			ref.pool.cooldown(k, 10*time.Second, "network: "+err.Error())
			lastHint = "upstream network error"
			continue
		}
		if resp.StatusCode != http.StatusOK {
			v := g.classify(ref.pool, k, resp)
			resp.Body.Close()
			if v.cooldown > 0 {
				ref.pool.cooldown(k, v.cooldown, fmt.Sprintf("HTTP %d", v.status))
			}
			if v.action == "failfast" {
				return v.hint
			}
			if v.action == "flipsurface" {
				return fmt.Sprintf("Model %s moved off the Responses surface — use /v1/chat/completions", model)
			}
			lastHint = v.hint
			continue
		}

		var tu tokenUsage
		if clientWantsStream {
			// native SSE verbatim — full fidelity passthrough
			tu = streamKilo(w, resp)
		} else {
			tu = aggregateResponsesSSE(w, resp, model)
		}
		g.recordActivity(r, ref, model, k.Hash, start, http.StatusOK, "", tu)
		return ""
	}
	if lastHint == "" {
		lastHint = "No healthy zen keys available — every key is cooling or disabled"
	}
	return lastHint
}

// aggregateResponsesSSE buffers a streamed Responses result into a single
// non-streaming response object (for clients that sent stream:false — the
// upstream is always streamed because the gate requires it).
func aggregateResponsesSSE(w http.ResponseWriter, resp *http.Response, model string) tokenUsage {
	var (
		text       bytes.Buffer
		id         string
		inTok      int64
		outTok     int64
		incomplete bool
		haveUsage  bool
	)

	sseLines(resp.Body, func(payload []byte) {
		var ev struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response *struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Usage  *struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal(payload, &ev) != nil {
			return
		}
		switch ev.Type {
		case "response.created":
			if ev.Response != nil {
				id = ev.Response.ID
			}
		case "response.output_text.delta":
			text.WriteString(ev.Delta)
		case "response.completed", "response.incomplete":
			if ev.Type == "response.incomplete" {
				incomplete = true
			}
			if ev.Response != nil {
				if ev.Response.ID != "" {
					id = ev.Response.ID
				}
				if ev.Response.Usage != nil {
					inTok = ev.Response.Usage.InputTokens
					outTok = ev.Response.Usage.OutputTokens
					haveUsage = true
				}
			}
		}
	})

	status := "completed"
	if incomplete {
		status = "incomplete"
	}
	out := map[string]any{
		"id":         id,
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     status,
		"model":      model,
		"output": []any{map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{map[string]any{
				"type": "output_text",
				"text": text.String(),
			}},
		}},
	}
	if haveUsage {
		out["usage"] = map[string]any{"input_tokens": inTok, "output_tokens": outTok}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(out)
	return tokenUsage{in: inTok, out: outTok}
}
