package gateway

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// sseLines reads an SSE body, invoking fn for each `data:` payload.
// Returns the scanner error, if any (client cancels surface as nil).
func sseLines(r io.Reader, fn func(payload []byte)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) < 6 || string(line[:6]) != "data: " {
			continue
		}
		payload := line[6:]
		if len(payload) > 0 && payload[0] == '[' {
			continue // [DONE] or [DONE]-like sentinel
		}
		fn(payload)
	}
	return sc.Err()
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// startSSE promotes the client response to an SSE stream (commit point:
// after this, no key retries are possible).
func startSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// usageChunk is the usage block OpenAI-compatible streams/attachments carry
// (field names vary across providers, so both spellings are accepted).
type usageChunk struct {
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		InputTokens      int64 `json:"input_tokens"`
		OutputTokens     int64 `json:"output_tokens"`
	} `json:"usage"`
}

func (u *usageChunk) into(t *tokenUsage) {
	if u.Usage == nil {
		return
	}
	in, out := u.Usage.PromptTokens, u.Usage.CompletionTokens
	if in == 0 {
		in = u.Usage.InputTokens
	}
	if out == 0 {
		out = u.Usage.OutputTokens
	}
	t.add(in, out)
}

// streamKilo passes a kilo preset's 200-response through verbatim (both stream and
// non-stream bodies; the upstream shape already matches the client's request)
// while tapping the usage block for the usage log.
func streamKilo(w http.ResponseWriter, resp *http.Response) tokenUsage {
	ct := resp.Header.Get("Content-Type")
	tu := tokenUsage{}

	if strings.Contains(ct, "text/event-stream") {
		startSSE(w)
		flush(w)
		sseLines(resp.Body, func(payload []byte) {
			var uc usageChunk
			if json.Unmarshal(payload, &uc) == nil {
				uc.into(&tu)
			}
			w.Write([]byte("data: "))
			w.Write(payload)
			w.Write([]byte("\n\n"))
			flush(w)
		})
		w.Write([]byte("data: [DONE]\n\n"))
		flush(w)
		return tu
	}

	// non-streaming JSON: buffer (bounded), tap usage, pass through verbatim
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	flush(w)
	if err != nil {
		return tu
	}
	w.Write(body)
	flush(w)
	var uc usageChunk
	if json.Unmarshal(body, &uc) == nil {
		uc.into(&tu)
	}
	return tu
}

// passthroughSSE copies a zen chat-surface SSE stream to the client, tapping
// the usage block on the way past.
func passthroughSSE(w http.ResponseWriter, resp *http.Response) tokenUsage {
	startSSE(w)
	flush(w)
	tu := tokenUsage{}
	sseLines(resp.Body, func(payload []byte) {
		var uc usageChunk
		if json.Unmarshal(payload, &uc) == nil {
			uc.into(&tu)
		}
		w.Write([]byte("data: "))
		w.Write(payload)
		w.Write([]byte("\n\n"))
		flush(w)
	})
	w.Write([]byte("data: [DONE]\n\n"))
	flush(w)
	return tu
}

// chatChunk builds one chat.completion.chunk SSE payload.
func chatChunk(id, model, delta string) []byte {
	b, _ := json.Marshal(map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": delta}, "finish_reason": nil}},
	})
	return b
}

func writeChunk(w http.ResponseWriter, payload []byte) {
	w.Write([]byte("data: "))
	w.Write(payload)
	w.Write([]byte("\n\n"))
	flush(w)
}

// aggregateChatSSE consumes a zen chat-surface SSE stream and emits one
// non-streaming chat.completion JSON to the client.
func aggregateChatSSE(w http.ResponseWriter, resp *http.Response, model string) tokenUsage {
	var content []byte
	var usage map[string]any
	var id string
	tu := tokenUsage{}
	sseLines(resp.Body, func(payload []byte) {
		var chunk struct {
			ID      string `json:"id"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage map[string]any `json:"usage"`
		}
		if json.Unmarshal(payload, &chunk) != nil {
			return
		}
		if chunk.ID != "" {
			id = chunk.ID
		}
		if len(chunk.Choices) > 0 {
			content = append(content, chunk.Choices[0].Delta.Content...)
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	})
	if usage != nil {
		if v, ok := usage["prompt_tokens"].(float64); ok {
			tu.in = int64(v)
		}
		if v, ok := usage["completion_tokens"].(float64); ok {
			tu.out = int64(v)
		}
	}
	out := map[string]any{
		"id":     id,
		"object": "chat.completion",
		"model":  model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": string(content)},
			"finish_reason": "stop",
		}},
	}
	if usage != nil {
		out["usage"] = usage
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(out)
	return tu
}
