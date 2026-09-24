package gateway

import (
	"encoding/json"
	"net/http"
)

// translateResponses consumes a Responses-API SSE stream and re-emits it as
// chat.completion chunks (streaming client) or one chat.completion JSON
// (non-streaming client). Handles text deltas, function calls, finish, usage.
func translateResponses(w http.ResponseWriter, resp *http.Response, model string, clientWantsStream bool) tokenUsage {
	type usage_t struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
	type event struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
		Item  *struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"item"`
		Response *struct {
			ID    string   `json:"id"`
			Usage *usage_t `json:"usage"`
		} `json:"response"`
	}

	const chunkID = "gw-resp"
	var (
		agg        []byte
		toolCalls  []map[string]any // accumulated for non-stream clients
		sawTools   bool
		id         string
		inTok      int64
		outTok     int64
		incomplete bool
	)

	if clientWantsStream {
		startSSE(w)
		flush(w)
	}

	writeFinish := func(finish string, usage map[string]any) {
		payload, _ := json.Marshal(map[string]any{
			"id": chunkID, "object": "chat.completion.chunk", "model": model,
			"choices": []any{map[string]any{
				"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
			"usage": usage,
		})
		writeChunk(w, payload)
		w.Write([]byte("data: [DONE]\n\n"))
		flush(w)
	}

	sseLines(resp.Body, func(payload []byte) {
		var ev event
		if json.Unmarshal(payload, &ev) != nil {
			return
		}
		switch ev.Type {
		case "response.output_text.delta":
			if clientWantsStream {
				writeChunk(w, chatChunk(chunkID, model, ev.Delta))
			} else {
				agg = append(agg, ev.Delta...)
			}
		case "response.output_item.done":
			if ev.Item == nil || ev.Item.Type != "function_call" {
				return
			}
			sawTools = true
			tc := map[string]any{
				"index": len(toolCalls), "id": ev.Item.CallID, "type": "function",
				"function": map[string]any{"name": ev.Item.Name, "arguments": ev.Item.Arguments},
			}
			if clientWantsStream {
				payload, _ := json.Marshal(map[string]any{
					"id": chunkID, "object": "chat.completion.chunk", "model": model,
					"choices": []any{map[string]any{
						"index": 0, "delta": map[string]any{"tool_calls": []any{tc}}, "finish_reason": nil}},
				})
				writeChunk(w, payload)
			} else {
				toolCalls = append(toolCalls, tc)
			}
		case "response.completed", "response.done", "response.incomplete":
			incomplete = ev.Type == "response.incomplete"
			if ev.Response != nil && ev.Response.Usage != nil {
				inTok = ev.Response.Usage.InputTokens
				outTok = ev.Response.Usage.OutputTokens
			}
			if ev.Response != nil && ev.Response.ID != "" {
				id = ev.Response.ID
			}
		case "response.failed", "error":
			if clientWantsStream {
				writeFinish("stop", nil)
			}
		}
	})

	usage := map[string]any{"prompt_tokens": inTok, "completion_tokens": outTok}
	finish := finishReason(incomplete)
	if sawTools {
		finish = "tool_calls"
	}

	if clientWantsStream {
		writeFinish(finish, usage)
		return tokenUsage{in: inTok, out: outTok}
	}

	msg := map[string]any{"role": "assistant", "content": string(agg)}
	if sawTools {
		msg["tool_calls"] = toolCalls
	}
	out := map[string]any{
		"id": id, "object": "chat.completion", "model": model,
		"choices": []any{map[string]any{
			"index": 0, "message": msg, "finish_reason": finish,
		}},
		"usage": usage,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(out)
	return tokenUsage{in: inTok, out: outTok}
}

func finishReason(incomplete bool) string {
	if incomplete {
		return "length"
	}
	return "stop"
}
