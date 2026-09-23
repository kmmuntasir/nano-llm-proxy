package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"strings"
	"time"
)

// handleMessages serves POST /v1/messages (Anthropic Messages protocol).
func (g *gateway) handleMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	req, err := parseClientBody(r)
	if err != nil {
		writeAnthropicErr(w, http.StatusBadRequest, err.Error())
		return
	}
	model, _ := req["model"].(string)
	// Clients configure their main models verbatim (Claude Code env slots);
	// the fallback covers the literal "claude-*" names clients still emit on
	// their own for background tasks (titling, summarization) — everything
	// unprefixed and claude-prefixed routes to one configured target.
	if !strings.Contains(model, "/") && strings.HasPrefix(model, "claude-") {
		if fallback := g.rs().Anthropic.FallbackModel; fallback != "" {
			log.Printf("anthropic fallback %s -> %s", model, fallback)
			model = fallback
		}
	}
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeAnthropicErr(w, http.StatusBadRequest, `Model must be "<provider>/<id>" (e.g. zen/mimo-...)`)
		return
	}
	ref, ok := g.provider(parts[0])
	if !ok {
		writeAnthropicErr(w, http.StatusBadRequest, fmt.Sprintf("Unknown provider %q — see GET /v1/models", parts[0]))
		return
	}
	upstreamModel := parts[1]
	if ref.builtin {
		upstreamModel = stripModelSuffix(upstreamModel)
	} else {
		upstreamModel = stripClientSuffix(upstreamModel)
	}
	req["model"] = upstreamModel
	clientWantsStream, _ := req["stream"].(bool)
	log.Printf("req anthropic provider=%s model=%s stream=%v", ref.name, upstreamModel, clientWantsStream)

	chat := anthropicToChatBody(req)
	chat["model"] = upstreamModel

	var hint string
	var tu tokenUsage
	if ref.typ == "opencode" {
		hint, tu = g.messagesViaZen(ref, w, r, chat, upstreamModel, clientWantsStream, start)
	} else {
		hint, tu = g.messagesViaOpenAI(ref, w, r, chat, upstreamModel, clientWantsStream, start)
	}
	if hint != "" {
		g.recordActivity(r, ref, upstreamModel, "", start, http.StatusBadGateway, hint, tu)
		writeAnthropicErr(w, http.StatusBadGateway, hint)
	}
}

func writeAnthropicErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "api_error", "message": msg},
	})
}

// usageFromAnthropicMsg reads the usage block a converted message carries
// (both token-name spellings appear depending on the surface).
func usageFromAnthropicMsg(msg map[string]any) tokenUsage {
	var tu tokenUsage
	u, ok := msg["usage"].(map[string]any)
	if !ok {
		return tu
	}
	gv := func(k string) int64 {
		v, _ := u[k].(float64)
		return int64(v)
	}
	tu.add(gv("input_tokens"), gv("output_tokens"))
	tu.add(gv("prompt_tokens"), gv("completion_tokens"))
	return tu
}

// usageFromChatJSON reads the usage block of a non-streaming chat response.
func usageFromChatJSON(chat map[string]any) tokenUsage {
	var tu tokenUsage
	u, ok := chat["usage"].(map[string]any)
	if !ok {
		return tu
	}
	gv := func(k string) int64 {
		v, _ := u[k].(float64)
		return int64(v)
	}
	tu.add(gv("prompt_tokens"), gv("completion_tokens"))
	tu.add(gv("input_tokens"), gv("output_tokens"))
	return tu
}

// messagesViaOpenAI serves anthropic messages via a plain OpenAI-compatible
// provider (kilo builtin + GUI-added generics).
func (g *gateway) messagesViaOpenAI(ref providerRef, w http.ResponseWriter, r *http.Request, chat map[string]any, model string, stream bool, start time.Time) (string, tokenUsage) {
	chat["stream"] = stream
	raw, _ := json.Marshal(chat)
	exclude := map[string]bool{}
	var lastHint string

	for attempt := 0; attempt < g.rs().Retry.MaxKeysPerRequest; attempt++ {
		k := ref.pool.pick(exclude)
		if k == nil {
			break
		}
		exclude[k.Hash] = true
		resp, ok := g.postUpstream(r, ref.baseURL+"/chat/completions", k.Key, raw, false)
		if !ok {
			lastHint = "upstream network error"
			ref.pool.cooldown(k, 10*time.Second, "network")
			continue
		}
		if resp.StatusCode != http.StatusOK {
			v := g.classify(ref.pool, k, resp)
			resp.Body.Close()
			if v.cooldown > 0 {
				ref.pool.cooldown(k, v.cooldown, fmt.Sprintf("HTTP %d", resp.StatusCode))
			}
			if v.action == "failfast" {
				return v.hint, tokenUsage{}
			}
			lastHint = v.hint
			continue
		}
		a := &anthropicStreamWriter{w: w, model: model}
		var tu tokenUsage
		if stream {
			a.start()
			streamChatSSEToAnthropic(a, resp.Body)
			tu = tokenUsage{in: a.inTok, out: a.outTok}
		} else {
			var chatResp map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
				return "upstream returned invalid JSON", tu
			}
			tu = usageFromChatJSON(chatResp)
			writeAnthropicJSON(w, chatJSONToAnthropic(chatResp, model))
		}
		g.recordActivity(r, ref, model, k.Hash, start, http.StatusOK, "", tu)
		return "", tu
	}
	if lastHint == "" {
		lastHint = fmt.Sprintf("No healthy %s keys available — every key is cooling or disabled", ref.name)
	}
	return lastHint, tokenUsage{}
}

func (g *gateway) messagesViaZen(ref providerRef, w http.ResponseWriter, r *http.Request, chat map[string]any, model string, stream bool, start time.Time) (string, tokenUsage) {
	exclude := map[string]bool{}
	surface := g.surfaceFor(model)
	var lastHint string

	for attempt := 0; attempt < g.rs().Retry.MaxKeysPerRequest; attempt++ {
		k := ref.pool.pick(exclude)
		if k == nil {
			break
		}
		exclude[k.Hash] = true

		upBody := chat
		path := "/chat/completions"
		if surface == "responses" {
			upBody = toResponsesBody(chat)
			path = "/responses"
		} else {
			cp := make(map[string]any, len(chat))
			maps.Copy(cp, chat)
			upBody = cp
			injectChatFingerprint(upBody, g.rs().Zen.InjectTools)
			upBody["stream"] = true // gate requires streaming
		}
		raw, _ := json.Marshal(upBody)

		resp, ok := g.postUpstream(r, ref.baseURL+path, k.Key, raw, true)
		if !ok {
			ref.pool.cooldown(k, 10*time.Second, "network")
			lastHint = "upstream network error"
			continue
		}
		if resp.StatusCode != http.StatusOK {
			v := g.classify(ref.pool, k, resp)
			resp.Body.Close()
			if v.cooldown > 0 {
				ref.pool.cooldown(k, v.cooldown, fmt.Sprintf("HTTP %d", v.status))
			}
			switch v.action {
			case "failfast":
				return v.hint, tokenUsage{}
			case "flipsurface":
				other := "chat"
				if surface == "chat" {
					other = "responses"
				}
				g.learnSurface(model, other)
				surface = other
				exclude[k.Hash] = false
				attempt--
			}
			lastHint = v.hint
			continue
		}

		a := &anthropicStreamWriter{w: w, model: model}
		var tu tokenUsage
		if surface == "responses" {
			if stream {
				a.start()
				streamResponsesSSEToAnthropic(a, resp.Body)
				tu = tokenUsage{in: a.inTok, out: a.outTok}
			} else {
				msg := responsesSSEToAnthropicMessage(resp.Body, model)
				tu = usageFromAnthropicMsg(msg)
				writeAnthropicJSON(w, msg)
			}
		} else if stream {
			a.start()
			streamChatSSEToAnthropic(a, resp.Body)
			tu = tokenUsage{in: a.inTok, out: a.outTok}
		} else {
			msg := chatSSEToAnthropicMessage(resp.Body, model)
			tu = usageFromAnthropicMsg(msg)
			writeAnthropicJSON(w, msg)
		}
		g.recordActivity(r, ref, model, k.Hash, start, http.StatusOK, "", tu)
		return "", tu
	}
	if lastHint == "" {
		lastHint = "No healthy zen keys available — every key is cooling or disabled"
	}
	return lastHint, tokenUsage{}
}

// postUpstream POSTs one upstream attempt. zenFingerprint injects the
// opencode client fingerprint (UA + session header) — decided by provider
// type, never by URL substring.
func (g *gateway) postUpstream(r *http.Request, url, key string, body []byte, zenFingerprint bool) (*http.Response, bool) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	if zenFingerprint {
		req.Header.Set("User-Agent", g.rs().Zen.UserAgent)
		req.Header.Set("x-opencode-session", newZenSessionID())
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, false
	}
	return resp, true
}

func writeAnthropicJSON(w http.ResponseWriter, msg map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(msg)
}

// chatSSEToAnthropicMessage aggregates a streamed chat completion into one
// anthropic message (for stream:false clients on stream-forced upstreams).
func chatSSEToAnthropicMessage(body io.Reader, model string) map[string]any {
	var content strings.Builder
	var toolCalls []any
	finish := ""
	var inTok, outTok int64
	sseLines(body, func(payload []byte) {
		var chunk struct {
			ID      string `json:"id"`
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(payload, &chunk) != nil {
			return
		}
		if len(chunk.Choices) > 0 {
			d := chunk.Choices[0].Delta
			content.WriteString(d.Content)
			for _, tc := range d.ToolCalls {
				idx := -1
				for i := range toolCalls {
					if toolCalls[i].(map[string]any)["id"] == tc.ID {
						idx = i
					}
				}
				if idx < 0 {
					toolCalls = append(toolCalls, map[string]any{
						"id": tc.ID, "type": "function",
						"function": map[string]any{"name": tc.Function.Name, "arguments": tc.Function.Arguments},
					})
				} else {
					fn := toolCalls[idx].(map[string]any)["function"].(map[string]any)
					fn["arguments"] = fn["arguments"].(string) + tc.Function.Arguments
				}
			}
			if chunk.Choices[0].FinishReason != nil {
				finish = *chunk.Choices[0].FinishReason
			}
		}
		if chunk.Usage != nil {
			inTok = chunk.Usage.PromptTokens
			outTok = chunk.Usage.CompletionTokens
		}
	})
	chat := map[string]any{
		"id": "msg_gw", "choices": []any{map[string]any{
			"message": map[string]any{
				"role": "assistant", "content": content.String(), "tool_calls": toolCalls,
			},
			"finish_reason": finish,
		}},
		"usage": map[string]any{"prompt_tokens": inTok, "completion_tokens": outTok},
	}
	if len(toolCalls) == 0 {
		delete(chat["choices"].([]any)[0].(map[string]any)["message"].(map[string]any), "tool_calls")
	}
	return chatJSONToAnthropic(chat, model)
}

// responsesSSEToAnthropicMessage aggregates a Responses stream into one
// anthropic message.
func responsesSSEToAnthropicMessage(body io.Reader, model string) map[string]any {
	var content strings.Builder
	type call struct{ id, name, args string }
	var calls []call
	var inTok, outTok int64
	sseLines(body, func(payload []byte) {
		var ev struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Item  *struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"item"`
			Response *struct {
				Usage *struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal(payload, &ev) != nil {
			return
		}
		switch ev.Type {
		case "response.output_text.delta":
			content.WriteString(ev.Delta)
		case "response.output_item.done":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				calls = append(calls, call{ev.Item.CallID, ev.Item.Name, ev.Item.Arguments})
			}
		case "response.completed", "response.incomplete", "response.done":
			if ev.Response != nil && ev.Response.Usage != nil {
				inTok = ev.Response.Usage.InputTokens
				outTok = ev.Response.Usage.OutputTokens
			}
		}
	})
	contentBlocks := []any{}
	if content.Len() > 0 {
		contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": content.String()})
	}
	stop := "end_turn"
	for _, c := range calls {
		var input any = map[string]any{}
		json.Unmarshal([]byte(c.args), &input)
		contentBlocks = append(contentBlocks, map[string]any{
			"type": "tool_use", "id": c.id, "name": c.name, "input": input,
		})
		stop = "tool_use"
	}
	return map[string]any{
		"id": "msg_gw", "type": "message", "role": "assistant", "model": model,
		"content":       contentBlocks,
		"stop_reason":   stop,
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": inTok, "output_tokens": outTok},
	}
}
