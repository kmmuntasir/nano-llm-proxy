package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Reverse of anthropic.go: serves POST /v1/chat/completions for providers
// that only expose an Anthropic-compatible endpoint. The chat request is
// translated to Anthropic Messages and the response (JSON or SSE) back into
// chat shapes, so OpenAI-protocol agents can use Anthropic-only providers.

// chatViaAnthropic is the rotation loop for anthropic-only providers.
func (g *gateway) chatViaAnthropic(ref providerRef, w http.ResponseWriter, r *http.Request, chat map[string]any, model string, clientWantsStream bool, start time.Time) string {
	// translation is key-independent — do it once, outside the retry loop
	anth := chatToAnthropicBody(chat, clientWantsStream)
	raw, _ := json.Marshal(anth)
	exclude := map[string]bool{}
	var lastHint string

	for attempt := 0; attempt < g.rs().Retry.MaxKeysPerRequest; attempt++ {
		k := ref.pool.pick(exclude)
		if k == nil {
			break
		}
		exclude[k.Hash] = true
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			ref.anthropicBaseURL+"/v1/messages", bytes.NewReader(raw))
		if err != nil {
			return "internal: " + err.Error()
		}
		req.Header.Set("x-api-key", k.Key)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Content-Type", "application/json")
		resp, err := g.client.Do(req)
		if err != nil {
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
			if v.action == "failfast" {
				return v.hint
			}
			lastHint = v.hint
			continue
		}
		var tu tokenUsage
		if clientWantsStream {
			tu = streamAnthropicSSEToChat(w, resp.Body, model)
		} else {
			var msg map[string]any
			if err := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(&msg); err != nil {
				return "upstream returned invalid JSON"
			}
			tu = usageFromAnthropicMsg(msg)
			out, _ := json.Marshal(anthropicJSONToChat(msg, model))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(out)
		}
		g.recordActivity(r, ref, model, k.Hash, start, http.StatusOK, "", tu)
		return ""
	}
	if lastHint == "" {
		lastHint = fmt.Sprintf("No healthy %s keys available — every key is cooling or disabled", ref.name)
	}
	return lastHint
}

// chatToAnthropicBody converts an OpenAI chat-completions request into an
// Anthropic Messages request. System/developer messages become the top-level
// system field, tool_calls become tool_use blocks, and consecutive role:"tool"
// results are grouped into one tool_result user message (the Anthropic
// protocol requires strict user/assistant alternation).
func chatToAnthropicBody(chat map[string]any, stream bool) map[string]any {
	out := map[string]any{"model": chat["model"], "stream": stream}
	// Anthropic requires max_tokens; chat clients may omit it.
	mt := toInt64(chat["max_tokens"])
	if mt == 0 {
		mt = toInt64(chat["max_completion_tokens"])
	}
	if mt == 0 {
		mt = 8192
	}
	out["max_tokens"] = mt
	if t, ok := chat["temperature"].(float64); ok {
		out["temperature"] = t
	}
	if t, ok := chat["top_p"].(float64); ok {
		out["top_p"] = t
	}
	if ss := stopSequences(chat["stop"]); len(ss) > 0 {
		out["stop_sequences"] = ss
	}

	var system strings.Builder
	var msgs []map[string]any
	var pendingToolResults []any
	flushToolResults := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		msgs = append(msgs, map[string]any{"role": "user", "content": pendingToolResults})
		pendingToolResults = nil
	}
	addBlocks := func(role string, blocks []any) {
		// merge consecutive same-role messages to keep alternation valid
		if n := len(msgs); n > 0 && msgs[n-1]["role"] == role {
			prev := msgs[n-1]["content"].([]any)
			msgs[n-1]["content"] = append(prev, blocks...)
			return
		}
		msgs = append(msgs, map[string]any{"role": role, "content": blocks})
	}

	if arr, ok := chat["messages"].([]any); ok {
		for _, m := range arr {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			switch role, _ := mm["role"].(string); role {
			case "system", "developer":
				flushToolResults()
				if s := chatContentText(mm["content"]); s != "" {
					if system.Len() > 0 {
						system.WriteString("\n\n")
					}
					system.WriteString(s)
				}
			case "user":
				flushToolResults()
				addBlocks("user", chatUserBlocks(mm["content"]))
			case "assistant":
				flushToolResults()
				var blocks []any
				if s, _ := mm["content"].(string); s != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": s})
				}
				if tcs, ok := mm["tool_calls"].([]any); ok {
					blocks = append(blocks, toolCallsToUseBlocks(tcs)...)
				}
				if len(blocks) > 0 {
					addBlocks("assistant", blocks)
				}
			case "tool":
				id, _ := mm["tool_call_id"].(string)
				pendingToolResults = append(pendingToolResults, map[string]any{
					"type":        "tool_result",
					"tool_use_id": id,
					"content":     chatContentText(mm["content"]),
				})
			}
		}
	}
	flushToolResults()
	if system.Len() > 0 {
		out["system"] = system.String()
	}
	if msgs == nil {
		msgs = []map[string]any{}
	}
	out["messages"] = msgs

	if tools, ok := chat["tools"].([]any); ok && len(tools) > 0 {
		var aTools []any
		for _, t := range tools {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			fn, _ := tm["function"].(map[string]any)
			name, _ := fn["name"].(string)
			if name == "" {
				continue
			}
			desc, _ := fn["description"].(string)
			aTools = append(aTools, map[string]any{
				"name": name, "description": desc, "input_schema": fn["parameters"],
			})
		}
		if len(aTools) > 0 {
			out["tools"] = aTools
			switch tc := chat["tool_choice"].(type) {
			case string:
				switch tc {
				case "required":
					out["tool_choice"] = map[string]any{"type": "any"}
				case "auto":
					out["tool_choice"] = map[string]any{"type": "auto"}
				case "none":
					delete(out, "tools") // no Anthropic equivalent: withdraw the tools
				}
			case map[string]any:
				if fn, ok := tc["function"].(map[string]any); ok {
					if name, _ := fn["name"].(string); name != "" {
						out["tool_choice"] = map[string]any{"type": "tool", "name": name}
					}
				}
			}
		}
	}
	return out
}

// toolCallsToUseBlocks converts chat tool_calls into tool_use blocks.
func toolCallsToUseBlocks(tcs []any) []any {
	var blocks []any
	for _, tc := range tcs {
		tcm, ok := tc.(map[string]any)
		if !ok {
			continue
		}
		fn, _ := tcm["function"].(map[string]any)
		name, _ := fn["name"].(string)
		argsStr, _ := fn["arguments"].(string)
		var input any
		if json.Unmarshal([]byte(argsStr), &input) != nil || input == nil {
			input = map[string]any{}
		}
		id, _ := tcm["id"].(string)
		blocks = append(blocks, map[string]any{
			"type": "tool_use", "id": id, "name": name, "input": input,
		})
	}
	return blocks
}

// chatContentText flattens a chat message content (string | content parts) to
// plain text, ignoring non-text parts.
func chatContentText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var sb strings.Builder
		for _, p := range c {
			if pm, ok := p.(map[string]any); ok {
				if t, _ := pm["type"].(string); t == "text" {
					s, _ := pm["text"].(string)
					sb.WriteString(s)
				}
			}
		}
		return sb.String()
	default:
		return ""
	}
}

// chatUserBlocks converts a chat user message content into anthropic blocks.
// Text parts pass through; image parts map to image blocks (data URLs become
// base64 sources, remote URLs use the url source type).
func chatUserBlocks(content any) []any {
	switch c := content.(type) {
	case string:
		if c == "" {
			return []any{}
		}
		return []any{map[string]any{"type": "text", "text": c}}
	case []any:
		var blocks []any
		for _, p := range c {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			switch t, _ := pm["type"].(string); t {
			case "text":
				s, _ := pm["text"].(string)
				blocks = append(blocks, map[string]any{"type": "text", "text": s})
			case "image_url":
				iu, _ := pm["image_url"].(map[string]any)
				u, _ := iu["url"].(string)
				if data, media, ok := dataURLToBase64(u); ok {
					blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{
						"type": "base64", "media_type": media, "data": data,
					}})
				} else if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
					blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{
						"type": "url", "url": u,
					}})
				}
			}
		}
		if blocks == nil {
			blocks = []any{}
		}
		return blocks
	default:
		return []any{}
	}
}

// dataURLToBase64 splits a data: URL into media type and base64 payload.
func dataURLToBase64(u string) (data, media string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(u, prefix) {
		return "", "", false
	}
	rest := u[len(prefix):]
	comma := strings.Index(rest, ",")
	if comma < 0 || !strings.Contains(rest[:comma], "base64") {
		return "", "", false
	}
	media = strings.TrimSuffix(rest[:comma], ";base64")
	if media == "" {
		media = "application/octet-stream"
	}
	return rest[comma+1:], media, true
}

// stopSequences normalizes the chat stop field (string | array) for Anthropic
// (which caps stop_sequences at 4).
func stopSequences(v any) []any {
	switch s := v.(type) {
	case string:
		if s == "" {
			return nil
		}
		return []any{s}
	case []any:
		if len(s) > 4 {
			s = s[:4]
		}
		return s
	default:
		return nil
	}
}

// stopFromAnthropic maps a stop_reason to the chat finish_reason spelling.
func stopFromAnthropic(sr string) string {
	switch sr {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default: // end_turn, stop_sequence, refusal, ...
		return "stop"
	}
}

// anthropicJSONToChat converts a non-streaming anthropic message into a chat
// completion. thinking blocks surface as reasoning_content (the GLM/OpenAI
// convention for reasoning output), tool_use blocks as tool_calls.
func anthropicJSONToChat(msg map[string]any, model string) map[string]any {
	content := ""
	var reasoning strings.Builder
	var toolCalls []any
	if blocks, ok := msg["content"].([]any); ok {
		for _, b := range blocks {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch bt, _ := bm["type"].(string); bt {
			case "text":
				s, _ := bm["text"].(string)
				content += s
			case "thinking":
				s, _ := bm["thinking"].(string)
				reasoning.WriteString(s)
			case "tool_use":
				input := bm["input"]
				args, err := json.Marshal(input)
				if err != nil {
					args = []byte("{}")
				}
				id, _ := bm["id"].(string)
				name, _ := bm["name"].(string)
				toolCalls = append(toolCalls, map[string]any{
					"id": id, "type": "function",
					"function": map[string]any{"name": name, "arguments": string(args)},
				})
			}
		}
	}
	message := map[string]any{"role": "assistant", "content": content}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	stop := "stop"
	if sr, _ := msg["stop_reason"].(string); sr != "" {
		stop = stopFromAnthropic(sr)
	}
	inTok, outTok := int64(0), int64(0)
	if u, ok := msg["usage"].(map[string]any); ok {
		inTok = toInt64(u["input_tokens"])
		outTok = toInt64(u["output_tokens"])
	}
	return map[string]any{
		"id":     msg["id"],
		"object": "chat.completion",
		"model":  model,
		"choices": []any{map[string]any{
			"index": 0, "message": message, "finish_reason": stop,
		}},
		"usage": map[string]any{
			"prompt_tokens": inTok, "completion_tokens": outTok, "total_tokens": inTok + outTok,
		},
	}
}

// chatChunkJSON builds one chat.completion.chunk payload; usage is attached
// only on the final chunk (the convention Z.ai itself uses).
func chatChunkJSON(id, model string, delta map[string]any, finishReason string, usage map[string]any) []byte {
	chunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "model": model,
		"choices": []any{map[string]any{
			"index": 0, "delta": delta,
			"finish_reason": finishReasonOrNil(finishReason),
		}},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	b, _ := json.Marshal(chunk)
	return b
}

func finishReasonOrNil(fr string) any {
	if fr == "" {
		return nil
	}
	return fr
}

// streamAnthropicSSEToChat consumes an anthropic SSE stream and emits chat
// completion chunks: thinking as reasoning_content deltas, tool_use blocks as
// streamed tool_calls fragments.
func streamAnthropicSSEToChat(w http.ResponseWriter, body io.Reader, model string) tokenUsage {
	startSSE(w)
	id := fmt.Sprintf("chatgw-%d", time.Now().UnixNano())
	writeChunk(w, chatChunkJSON(id, model, map[string]any{"role": "assistant", "content": ""}, "", nil))

	tu := tokenUsage{}
	finish := "stop"
	toolChatIdx := map[int]int{} // anthropic block index -> chat tool_calls index
	nextTool := 0

	sseLines(body, func(payload []byte) {
		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock *struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				// message_delta reuses the "delta" key for the stop reason
				StopReason *string `json:"stop_reason"`
			} `json:"delta"`
			Message *struct {
				Usage *struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage *struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(payload, &ev) != nil {
			return
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil && ev.Message.Usage != nil {
				tu.add(ev.Message.Usage.InputTokens, ev.Message.Usage.OutputTokens)
			}
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				idx := nextTool
				nextTool++
				toolChatIdx[ev.Index] = idx
				writeChunk(w, chatChunkJSON(id, model, map[string]any{
					"tool_calls": []any{map[string]any{
						"index": idx, "id": ev.ContentBlock.ID, "type": "function",
						"function": map[string]any{"name": ev.ContentBlock.Name, "arguments": ""},
					}},
				}, "", nil))
			}
		case "content_block_delta":
			if ev.Delta == nil {
				return
			}
			switch ev.Delta.Type {
			case "text_delta":
				writeChunk(w, chatChunkJSON(id, model, map[string]any{"content": ev.Delta.Text}, "", nil))
			case "thinking_delta":
				writeChunk(w, chatChunkJSON(id, model, map[string]any{"reasoning_content": ev.Delta.Thinking}, "", nil))
			case "input_json_delta":
				if idx, ok := toolChatIdx[ev.Index]; ok {
					writeChunk(w, chatChunkJSON(id, model, map[string]any{
						"tool_calls": []any{map[string]any{
							"index": idx, "type": "function",
							"function": map[string]any{"arguments": ev.Delta.PartialJSON},
						}},
					}, "", nil))
				}
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != nil {
				finish = stopFromAnthropic(*ev.Delta.StopReason)
			}
			if ev.Usage != nil {
				tu.add(ev.Usage.InputTokens, ev.Usage.OutputTokens)
			}
		}
	})
	writeChunk(w, chatChunkJSON(id, model, map[string]any{}, finish, map[string]any{
		"prompt_tokens": tu.in, "completion_tokens": tu.out, "total_tokens": tu.in + tu.out,
	}))
	w.Write([]byte("data: [DONE]\n\n"))
	flush(w)
	return tu
}
