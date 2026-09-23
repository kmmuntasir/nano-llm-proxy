package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"
	"time"
)

// toolNamesChat extracts tool function names from a chat-format tools array.
func toolNamesChat(tools []any) []string {
	var names []string
	for _, t := range tools {
		if tm, ok := t.(map[string]any); ok {
			if fn, ok := tm["function"].(map[string]any); ok {
				if name, _ := fn["name"].(string); name != "" {
					names = append(names, name)
				}
			}
		}
	}
	return names
}

func (g *gateway) surfaceFor(model string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := g.surfaceOver[model]; ok {
		return s
	}
	if slices.Contains(g.rs().Zen.ResponsesModels, model) {
		return "responses"
	}
	return "chat"
}

func (g *gateway) learnSurface(model, surface string) {
	g.mu.Lock()
	g.surfaceOver[model] = surface
	g.mu.Unlock()
	log.Printf("learned surface model=%s surface=%s", model, surface)
}

// injectChatFingerprint mutates a chat-completions body to pass the gate:
// stream:true + tools named bash/read/edit present.
func injectChatFingerprint(body map[string]any, injectTools bool) {
	body["stream"] = true
	if !injectTools {
		return
	}
	tools, _ := body["tools"].([]any)
	have := toolNamesChat(tools)
	for _, name := range []string{"bash", "read", "edit"} {
		if !slices.Contains(have, name) {
			tools = append(tools, stubToolChat(name))
		}
	}
	body["tools"] = tools
}

func stubToolChat(name string) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": "internal noop tool; never call",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

func stubToolFlat(name string) map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        name,
		"description": "internal noop tool; never call",
		"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

// convertContentParts maps chat content parts to Responses content types.
// textType is "input_text" for user/system messages, "output_text" for assistant.
func convertContentParts(parts []any, textType string) []any {
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		switch t, _ := pm["type"].(string); t {
		case "text":
			out = append(out, map[string]any{"type": textType, "text": pm["text"]})
		case "image_url":
			out = append(out, map[string]any{"type": "input_image", "image_url": pm["image_url"]})
		default:
			out = append(out, p)
		}
	}
	return out
}

// chatMessagesToResponsesInput converts a chat transcript to Responses input
// items: content parts retyped (input_text/output_text), assistant tool_calls
// become function_call items, and tool results become function_call_output.
func chatMessagesToResponsesInput(msgs []any) []any {
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		content, hasContent := mm["content"]
		switch role {
		case "assistant":
			if tcs, ok := mm["tool_calls"].([]any); ok {
				for _, tc := range tcs {
					tcm, ok := tc.(map[string]any)
					if !ok {
						continue
					}
					fn, _ := tcm["function"].(map[string]any)
					name, _ := fn["name"].(string)
					args, _ := fn["arguments"].(string)
					callID, _ := tcm["id"].(string)
					out = append(out, map[string]any{
						"type": "function_call", "call_id": callID,
						"name": name, "arguments": args,
					})
				}
			}
			if hasContent {
				if s, ok := content.(string); ok && s != "" {
					out = append(out, map[string]any{"role": "assistant", "content": s})
				} else if parts, ok := content.([]any); ok && len(parts) > 0 {
					out = append(out, map[string]any{"role": "assistant",
						"content": convertContentParts(parts, "output_text")})
				}
			}
		case "tool":
			callID, _ := mm["tool_call_id"].(string)
			var output string
			if s, ok := content.(string); ok {
				output = s
			} else if b, err := json.Marshal(content); err == nil {
				output = string(b)
			}
			out = append(out, map[string]any{
				"type": "function_call_output", "call_id": callID, "output": output,
			})
		default: // user, system, developer
			if !hasContent {
				continue
			}
			item := map[string]any{"role": role}
			if s, ok := content.(string); ok {
				item["content"] = s
			} else if parts, ok := content.([]any); ok {
				item["content"] = convertContentParts(parts, "input_text")
			} else {
				item["content"] = content
			}
			out = append(out, item)
		}
	}
	return out
}

// toResponsesBody converts an OpenAI chat body to Responses-API shape.
func toResponsesBody(chat map[string]any) map[string]any {
	out := map[string]any{
		"model":  chat["model"],
		"stream": true,
	}
	if v, ok := chat["messages"].([]any); ok {
		out["input"] = chatMessagesToResponsesInput(v)
	}
	if v, ok := chat["max_tokens"]; ok {
		out["max_output_tokens"] = v
	}
	// client tools converted nested -> flat, plus gate stubs
	var flat []any
	if tools, ok := chat["tools"].([]any); ok {
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				if fn, ok := tm["function"].(map[string]any); ok {
					flat = append(flat, map[string]any{
						"type": "function", "name": fn["name"],
						"description": fn["description"], "parameters": fn["parameters"],
					})
				}
			}
		}
	}
	out["tools"] = ensureFlatStubs(flat)
	return out
}

// proxyZen runs the rotation loop for zen models across both surfaces.
func (g *gateway) proxyZen(ref providerRef, w http.ResponseWriter, r *http.Request, body map[string]any, model string, clientWantsStream bool, start time.Time) string {
	exclude := map[string]bool{}
	surface := g.surfaceFor(model)
	var lastHint string

	for attempt := 0; attempt < g.rs().Retry.MaxKeysPerRequest; attempt++ {
		k := ref.pool.pick(exclude)
		if k == nil {
			break
		}
		exclude[k.Hash] = true

		upBody := body
		if surface == "responses" {
			// toResponsesBody forces stream and ensures the gate stubs (flat shape)
			upBody = toResponsesBody(body)
		} else {
			// copy so injections don't leak into retries on the other surface
			cp := make(map[string]any, len(body))
			maps.Copy(cp, body)
			upBody = cp
			injectChatFingerprint(upBody, g.rs().Zen.InjectTools)
		}
		raw, _ := json.Marshal(upBody)

		path := "/chat/completions"
		if surface == "responses" {
			path = "/responses"
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			ref.baseURL+path, bytes.NewReader(raw))
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
			switch v.action {
			case "failfast":
				return v.hint
			case "flipsurface":
				other := "chat"
				if surface == "chat" {
					other = "responses"
				}
				g.learnSurface(model, other)
				surface = other
				exclude[k.Hash] = false // same key is fine on the right surface
				attempt--
			}
			lastHint = v.hint
			continue
		}

		var tu tokenUsage
		if surface == "responses" {
			tu = translateResponses(w, resp, model, clientWantsStream)
		} else if clientWantsStream {
			tu = passthroughSSE(w, resp)
		} else {
			tu = aggregateChatSSE(w, resp, model)
		}
		g.recordActivity(r, ref, model, k.Hash, start, http.StatusOK, "", tu)
		return ""
	}
	if lastHint == "" {
		lastHint = "No healthy zen keys available — every key is cooling or disabled"
	}
	return lastHint
}
