package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Native Anthropic Messages passthrough for providers that expose an
// Anthropic-compatible endpoint (e.g. the Z.ai GLM Coding Plan). This is the
// only adapter with zero request/response translation: coding agents lean on
// protocol extensions (thinking blocks with signatures, anthropic-beta
// features, interleaved role:"system" messages) that would not survive a
// round-trip through the chat shape. Only the auth credential is swapped for
// the pooled upstream key; everything else the client sent goes upstream as-is.

// messagesViaAnthropic serves POST /v1/messages via a provider's
// Anthropic-compatible endpoint. The body has already had its model prefix
// stripped by handleMessages; stream is set explicitly to match the client.
func (g *gateway) messagesViaAnthropic(ref providerRef, w http.ResponseWriter, r *http.Request, req map[string]any, model string, stream bool, start time.Time) (string, tokenUsage) {
	req["stream"] = stream
	raw, _ := json.Marshal(req)
	exclude := map[string]bool{}
	var lastHint string

	for attempt := 0; attempt < g.rs().Retry.MaxKeysPerRequest; attempt++ {
		k := ref.pool.pick(exclude)
		if k == nil {
			break
		}
		exclude[k.Hash] = true
		httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			ref.anthropicBaseURL+"/v1/messages", bytes.NewReader(raw))
		if err != nil {
			return "internal: " + err.Error(), tokenUsage{}
		}
		forwardAnthropicHeaders(httpReq.Header, r.Header)
		httpReq.Header.Set("x-api-key", k.Key)
		resp, err := g.client.Do(httpReq)
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
				return v.hint, tokenUsage{}
			}
			lastHint = v.hint
			continue
		}
		tu := passthroughAnthropic(w, resp)
		g.recordActivity(r, ref, model, k.Hash, start, http.StatusOK, "", tu)
		return "", tu
	}
	if lastHint == "" {
		lastHint = fmt.Sprintf("No healthy %s keys available — every key is cooling or disabled", ref.name)
	}
	return lastHint, tokenUsage{}
}

// forwardAnthropicHeaders copies the client's anthropic-protocol headers
// (version, beta flags, client identity) so upstream sees the original
// coding-agent fingerprint — Z.ai gates the coding plan on agent shape.
// Auth and hop-by-hop headers are skipped; the pooled key is set separately.
func forwardAnthropicHeaders(dst, src http.Header) {
	for k, vs := range src {
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "X-Api-Key", "Host", "Content-Length",
			"Connection", "Accept-Encoding", "X-Opencode-Session":
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	if dst.Get("anthropic-version") == "" {
		dst.Set("anthropic-version", "2023-06-01")
	}
}

// anthropicUsagePayload is the usage block in Anthropic's own shapes: the
// non-stream message and message_delta carry it top-level, message_start
// nests it under "message".
type anthropicUsagePayload struct {
	Message *struct {
		Usage *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage *struct {
		InputTokens      int64 `json:"input_tokens"`
		OutputTokens     int64 `json:"output_tokens"`
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func (p *anthropicUsagePayload) into(t *tokenUsage) {
	if p.Message != nil && p.Message.Usage != nil {
		t.add(p.Message.Usage.InputTokens, p.Message.Usage.OutputTokens)
	}
	if p.Usage == nil {
		return
	}
	in, out := p.Usage.PromptTokens, p.Usage.CompletionTokens
	if in == 0 {
		in = p.Usage.InputTokens
	}
	if out == 0 {
		out = p.Usage.OutputTokens
	}
	t.add(in, out)
}

// passthroughAnthropic streams an upstream Anthropic response to the client
// verbatim — SSE keeps its event framing (clients and event names both stay
// authentic) — while tapping the usage block for the usage log.
func passthroughAnthropic(w http.ResponseWriter, resp *http.Response) tokenUsage {
	tu := tokenUsage{}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)

	if !strings.Contains(ct, "text/event-stream") {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		flush(w)
		if err != nil {
			return tu
		}
		w.Write(body)
		flush(w)
		var up anthropicUsagePayload
		if json.Unmarshal(body, &up) == nil {
			up.into(&tu)
		}
		return tu
	}

	// SSE: copy line-by-line (scanner preserves event/data framing), tapping
	// usage from message_start / message_delta payloads on the way past.
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if bytes.HasPrefix(line, []byte("data: ")) {
			var up anthropicUsagePayload
			if json.Unmarshal(line[6:], &up) == nil {
				up.into(&tu)
			}
		}
		w.Write(line)
		w.Write([]byte{'\n'})
	}
	flush(w)
	return tu
}
