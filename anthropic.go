package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Anthropic Messages API compatibility (POST /v1/messages) so Claude Code and
// other Anthropic-protocol clients can use the same pools. Requests are
// converted to the internal chat shape and routed through the exact same
// fingerprint / surface / rotation machinery as /v1/chat/completions.

// --- request conversion: anthropic -> chat ---

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string | []contentBlock
}

// anthropicToChatBody converts an Anthropic Messages request into an
// OpenAI chat-completions body. Returns (body, chatToolsPresent).
func anthropicToChatBody(req map[string]any) map[string]any {
	out := map[string]any{
		"model": req["model"],
	}
	if mt, ok := req["max_tokens"].(float64); ok {
		out["max_tokens"] = int64(mt)
	}
	if t, ok := req["temperature"].(float64); ok {
		out["temperature"] = t
	}
	if t, ok := req["top_p"].(float64); ok {
		out["top_p"] = t
	}

	var msgs []any
	switch sys := req["system"].(type) {
	case string:
		if sys != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": sys})
		}
	case []any:
		if s := concatTextBlocks(sys); s != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": s})
		}
	}

	if arr, ok := req["messages"].([]any); ok {
		for _, m := range arr {
			am, ok := m.(map[string]any)
			if !ok {
				continue
			}
			msgs = append(msgs, convertAnthropicMessage(am)...)
		}
	}
	out["messages"] = msgs

	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		var chatTools []any
		for _, t := range tools {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			name, _ := tm["name"].(string)
			desc, _ := tm["description"].(string)
			chatTools = append(chatTools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        name,
					"description": desc,
					"parameters":  tm["input_schema"],
				},
			})
		}
		if len(chatTools) > 0 {
			out["tools"] = chatTools
		}
	}
	return out
}

// convertAnthropicMessage flattens one anthropic message into 0..n chat
// messages: tool_use blocks become assistant tool_calls, tool_result blocks
// become role:"tool" messages.
func convertAnthropicMessage(am map[string]any) []any {
	role, _ := am["role"].(string)
	switch c := am["content"].(type) {
	case string:
		if c == "" {
			return nil
		}
		return []any{map[string]any{"role": role, "content": c}}
	case []any:
		var out []any
		var textParts []any
		var toolCalls []any
		for _, blk := range c {
			bm, ok := blk.(map[string]any)
			if !ok {
				continue
			}
			switch bt, _ := bm["type"].(string); bt {
			case "text":
				textParts = append(textParts, map[string]any{"type": "text", "text": bm["text"]})
			case "tool_use":
				input := bm["input"]
				args, err := json.Marshal(input)
				if err != nil {
					args = []byte("{}")
				}
				id, _ := bm["id"].(string)
				name, _ := bm["name"].(string)
				toolCalls = append(toolCalls, map[string]any{
					"id":       id,
					"type":     "function",
					"function": map[string]any{"name": name, "arguments": string(args)},
				})
			case "tool_result":
				// chat format: standalone tool message
				tid, _ := bm["tool_use_id"].(string)
				out = append(out, map[string]any{
					"role":         "tool",
					"tool_call_id": tid,
					"content":      toolResultText(bm["content"]),
				})
			case "image", "thinking", "redacted_thinking":
				// dropped: not representable on the chat path
			}
		}
		if len(toolCalls) > 0 {
			am2 := map[string]any{"role": "assistant", "tool_calls": toolCalls}
			if len(textParts) > 0 {
				am2["content"] = concatTextBlocks(textParts)
			} else {
				am2["content"] = ""
			}
			out = append([]any{am2}, out...)
		} else if len(textParts) > 0 {
			out = append(out, map[string]any{"role": role, "content": textParts})
		}
		return out
	}
	return nil
}

// concatTextBlocks joins text blocks into one string.
func concatTextBlocks(blocks []any) string {
	var sb strings.Builder
	for _, b := range blocks {
		if bm, ok := b.(map[string]any); ok {
			if t, _ := bm["type"].(string); t == "text" {
				s, _ := bm["text"].(string)
				sb.WriteString(s)
			}
		}
	}
	return sb.String()
}

// toolResultText flattens a tool_result content (string | blocks) to a string.
func toolResultText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var sb strings.Builder
		for _, b := range c {
			if bm, ok := b.(map[string]any); ok {
				if t, _ := bm["type"].(string); t == "text" {
					s, _ := bm["text"].(string)
					sb.WriteString(s)
				}
			}
		}
		return sb.String()
	case nil:
		return ""
	default:
		if b, err := json.Marshal(c); err == nil {
			return string(b)
		}
		return ""
	}
}

// --- response conversion: chat -> anthropic ---

func stopReasonFromChat(fr string) string {
	switch fr {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

// chatJSONToAnthropic converts a non-streaming chat completion into an
// anthropic message object.
func chatJSONToAnthropic(chat map[string]any, model string) map[string]any {
	var content []any
	if chs, ok := chat["choices"].([]any); ok && len(chs) > 0 {
		ch := chs[0].(map[string]any)
		if msg, ok := ch["message"].(map[string]any); ok {
			if c, _ := msg["content"].(string); c != "" {
				content = append(content, map[string]any{"type": "text", "text": c})
			}
			if tcs, ok := msg["tool_calls"].([]any); ok {
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
					content = append(content, map[string]any{
						"type": "tool_use", "id": id, "name": name, "input": input,
					})
				}
			}
		}
	}
	if content == nil {
		content = []any{map[string]any{"type": "text", "text": ""}}
	}
	stop := "end_turn"
	if fr, _ := chat["choices"].([]any); len(fr) > 0 {
		if c := fr[0].(map[string]any); c != nil {
			if s, ok := c["finish_reason"].(string); ok {
				stop = stopReasonFromChat(s)
			}
		}
	}
	inTok, outTok := int64(0), int64(0)
	if u, ok := chat["usage"].(map[string]any); ok {
		inTok = toInt64(u["prompt_tokens"])
		outTok = toInt64(u["completion_tokens"])
	}
	return map[string]any{
		"id":            chat["id"],
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   stop,
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": inTok, "output_tokens": outTok},
	}
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

// --- streaming: chat SSE -> anthropic SSE ---

// anthropicStreamWriter accumulates a streamed chat completion and emits a
// valid anthropic SSE sequence on the fly. Tool calls are buffered: upstreams
// stream tool_calls as argument fragments across chunks, so complete calls are
// only emitted at finish.
type anthropicStreamWriter struct {
	w          http.ResponseWriter
	model      string
	blockIdx   int
	textOpen   bool
	flushedHdr bool
	sawTools   bool
	inTok      int64
	outTok     int64
	chatFinish string // last finish_reason seen on the chat stream
	pending    []*pendingToolCall
	toolIdx    map[int]*pendingToolCall // upstream chunk index -> pending call
}

type pendingToolCall struct {
	id, name, args string
}

func (a *anthropicStreamWriter) start() {
	if !a.flushedHdr {
		a.w.Header().Set("Content-Type", "text/event-stream")
		a.w.Header().Set("Cache-Control", "no-cache")
		a.w.Header().Set("X-Accel-Buffering", "no")
		a.w.WriteHeader(http.StatusOK)
		a.flushedHdr = true
	}
	start, _ := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_gw", "type": "message", "role": "assistant",
			"model": a.model, "content": []any{},
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
	fmt.Fprintf(a.w, "event: message_start\ndata: %s\n\n", start)
	a.flush()
}

func (a *anthropicStreamWriter) flush() {
	if f, ok := a.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (a *anthropicStreamWriter) textDelta(s string) {
	if s == "" {
		return
	}
	if !a.textOpen {
		a.textOpen = true
		bs, _ := json.Marshal(map[string]any{
			"type": "content_block_start", "index": a.blockIdx,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		fmt.Fprintf(a.w, "event: content_block_start\ndata: %s\n\n", bs)
	}
	d, _ := json.Marshal(map[string]any{
		"type": "content_block_delta", "index": a.blockIdx,
		"delta": map[string]any{"type": "text_delta", "text": s},
	})
	fmt.Fprintf(a.w, "event: content_block_delta\ndata: %s\n\n", d)
	a.flush()
}

func (a *anthropicStreamWriter) closeTextBlock() {
	if a.textOpen {
		bs, _ := json.Marshal(map[string]any{
			"type": "content_block_stop", "index": a.blockIdx,
		})
		fmt.Fprintf(a.w, "event: content_block_stop\ndata: %s\n\n", bs)
		a.textOpen = false
		a.blockIdx++
	}
}

// toolUse buffers a tool_call delta (chat surface) or a complete call
// (responses surface). Chat fragments reuse the upstream chunk index; pass a
// unique negative index for whole calls.
func (a *anthropicStreamWriter) toolUse(idx int, id, name, argsJSON string) {
	a.sawTools = true
	if a.toolIdx == nil {
		a.toolIdx = map[int]*pendingToolCall{}
	}
	if p, ok := a.toolIdx[idx]; ok {
		p.args += argsJSON
		if id != "" && p.id == "" {
			p.id = id
		}
		if name != "" && p.name == "" {
			p.name = name
		}
		return
	}
	p := &pendingToolCall{id: id, name: name, args: argsJSON}
	a.toolIdx[idx] = p
	a.pending = append(a.pending, p)
}

func (a *anthropicStreamWriter) emitToolCall(p *pendingToolCall) {
	a.closeTextBlock()
	bs, _ := json.Marshal(map[string]any{
		"type": "content_block_start", "index": a.blockIdx,
		"content_block": map[string]any{"type": "tool_use", "id": p.id, "name": p.name, "input": map[string]any{}},
	})
	fmt.Fprintf(a.w, "event: content_block_start\ndata: %s\n\n", bs)
	d, _ := json.Marshal(map[string]any{
		"type": "content_block_delta", "index": a.blockIdx,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": p.args},
	})
	fmt.Fprintf(a.w, "event: content_block_delta\ndata: %s\n\n", d)
	es, _ := json.Marshal(map[string]any{"type": "content_block_stop", "index": a.blockIdx})
	fmt.Fprintf(a.w, "event: content_block_stop\ndata: %s\n\n", es)
	a.blockIdx++
	a.flush()
}

func (a *anthropicStreamWriter) finish(chatFinish string) {
	if chatFinish != "" {
		a.chatFinish = chatFinish
	}
	a.closeTextBlock()
	for _, p := range a.pending {
		a.emitToolCall(p)
	}
	stop := stopReasonFromChat(a.chatFinish)
	if len(a.pending) > 0 {
		stop = "tool_use"
	}
	md, _ := json.Marshal(map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": a.outTok},
	})
	fmt.Fprintf(a.w, "event: message_delta\ndata: %s\n\n", md)
	ms, _ := json.Marshal(map[string]any{"type": "message_stop"})
	fmt.Fprintf(a.w, "event: message_stop\ndata: %s\n\n", ms)
	a.flush()
}

// streamChatSSEToAnthropic consumes a chat-completions SSE body and emits the
// anthropic event sequence.
func streamChatSSEToAnthropic(a *anthropicStreamWriter, body io.Reader) {
	sseLines(body, func(payload []byte) {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
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
			if d.Content != "" {
				a.textDelta(d.Content)
			}
			for _, tc := range d.ToolCalls {
				a.toolUse(tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments)
			}
			if chunk.Choices[0].FinishReason != nil {
				a.chatFinish = *chunk.Choices[0].FinishReason
			}
		}
		if chunk.Usage != nil {
			a.inTok = chunk.Usage.PromptTokens
			a.outTok = chunk.Usage.CompletionTokens
		}
	})
	a.finish("")
}

// --- streaming: responses SSE -> anthropic SSE ---

func streamResponsesSSEToAnthropic(a *anthropicStreamWriter, body io.Reader) {
	var pendingCalls []map[string]any
	sseLines(body, func(payload []byte) {
		var ev struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
			Item  *struct {
				Type      string          `json:"type"`
				CallID    string          `json:"call_id"`
				Name      string          `json:"name"`
				Arguments string          `json:"arguments"`
				Input     json.RawMessage `json:"input"`
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
			a.textDelta(ev.Delta)
		case "response.output_item.done":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				args := ev.Item.Arguments
				if args == "" {
					args = string(ev.Item.Input)
				}
				pendingCalls = append(pendingCalls, map[string]any{
					"id": ev.Item.CallID, "name": ev.Item.Name, "args": args,
				})
			}
		case "response.completed", "response.incomplete", "response.done":
			if ev.Response != nil && ev.Response.Usage != nil {
				a.inTok = ev.Response.Usage.InputTokens
				a.outTok = ev.Response.Usage.OutputTokens
			}
		}
	})
	// anthropic wants tool_use blocks before message_delta; emit them now
	for i, c := range pendingCalls {
		a.toolUse(-1-i, c["id"].(string), c["name"].(string), c["args"].(string))
	}
	if len(pendingCalls) > 0 {
		a.finish("tool_calls")
	} else {
		a.finish("")
	}
}
