package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
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

// streamKilo passes a kilo 200-response through verbatim (both stream and
// non-stream bodies; the upstream shape already matches the client's request).
func streamKilo(w http.ResponseWriter, resp *http.Response, _ bool) {
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	flush(w)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flush(w)
		}
		if err != nil {
			break
		}
	}
}

// passthroughSSE copies a zen chat-surface SSE stream to the client.
func passthroughSSE(w http.ResponseWriter, resp *http.Response) {
	startSSE(w)
	flush(w)
	sseLines(resp.Body, func(payload []byte) {
		w.Write([]byte("data: "))
		w.Write(payload)
		w.Write([]byte("\n\n"))
		flush(w)
	})
	w.Write([]byte("data: [DONE]\n\n"))
	flush(w)
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
func aggregateChatSSE(w http.ResponseWriter, resp *http.Response, model string) {
	var content []byte
	var usage map[string]any
	var id string
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
}
