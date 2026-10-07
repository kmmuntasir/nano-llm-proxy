package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

// Images have three separate paths through the gateway, and each one had a
// defect that made multimodal models unusable from at least one client:
//
//	chat + image_url  -> chat surface    : worked
//	chat + image_url  -> Responses surface: input_image got the nested chat
//	                                      object instead of a string, and the
//	                                      upstream rejected the message
//	/v1/messages + image block -> chat    : the block was dropped silently, so
//	                                      the model answered about a picture
//	                                      it had never seen
//
// The conversions are unit-tested here and the chat→Responses leg is also
// driven end-to-end through the proxy.

func TestConvertContentPartsImagesForResponses(t *testing.T) {
	const uri = "data:image/png;base64,AAAA"
	parts := []any{
		map[string]any{"type": "text", "text": "what is this?"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": uri, "detail": "high"}},
		map[string]any{"type": "image_url", "image_url": uri}, // already a string
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": ""}},
		map[string]any{"type": "image_url", "image_url": map[string]any{"nope": 1}},
		map[string]any{"type": "audio", "audio": "x"},
	}
	out := convertContentParts(parts, "input_text")

	if len(out) != 4 {
		t.Fatalf("got %d parts, want 4 (text, two images, passthrough): %v", len(out), out)
	}
	img := out[1].(map[string]any)
	if img["type"] != "input_image" {
		t.Errorf("type = %v, want input_image", img["type"])
	}
	// the whole point: a string, not chat's nested {"url": ...} object
	if u, ok := img["image_url"].(string); !ok || u != uri {
		t.Errorf("image_url = %#v, want the string %q", img["image_url"], uri)
	}
	if img["detail"] != "high" {
		t.Errorf("detail dropped: %v", img)
	}
	if s, _ := out[2].(map[string]any)["image_url"].(string); s != uri {
		t.Errorf("string form mangled: %v", out[2])
	}
	// unusable sources drop out rather than reaching upstream malformed
	for _, p := range out[3:] {
		if pm, _ := p.(map[string]any); pm != nil && pm["type"] == "input_image" {
			t.Errorf("unusable image part survived: %v", p)
		}
	}
}

func TestAnthropicImageBlocksBecomeChatImageURLs(t *testing.T) {
	msgs := anthropicToChatBody(map[string]any{
		"model":      "muse-spark-1.3-contributor-free",
		"max_tokens": float64(100),
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "describe"},
				map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": "image/png", "data": "AAAA"}},
				map[string]any{"type": "image", "source": map[string]any{
					"type": "url", "url": "https://example.com/a.jpg"}},
				map[string]any{"type": "image", "source": map[string]any{"type": "file"}},
				map[string]any{"type": "thinking", "thinking": "hmm"},
			}},
			// an image with no caption must survive on its own
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": "image/jpeg", "data": "BBBB"}},
			}},
		},
	})
	got := msgs["messages"].([]any)
	if len(got) != 2 {
		t.Fatalf("got %d chat messages, want 2: %v", len(got), got)
	}
	parts := got[0].(map[string]any)["content"].([]any)
	if len(parts) != 3 {
		t.Fatalf("captioned message lost parts: %v", parts)
	}
	img := parts[1].(map[string]any)
	inner, _ := img["image_url"].(map[string]any)
	url, _ := inner["url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,AAAA") {
		t.Errorf("base64 image not converted to a data URI: %v", img)
	}
	if p2 := parts[2].(map[string]any); p2["image_url"].(map[string]any)["url"] != "https://example.com/a.jpg" {
		t.Errorf("url-source image not passed through: %v", p2)
	}
	alone := got[1].(map[string]any)["content"].([]any)
	if len(alone) != 1 || alone[0].(map[string]any)["type"] != "image_url" {
		t.Errorf("image-only message dropped: %v", got[1])
	}
}

// End-to-end: a chat request carrying an image to a Responses-surface model
// must reach upstream with a string image_url. Before the fix this produced
// 400 "input[0].content did not match any supported type".
func TestChatImageReachesResponsesSurfaceIntact(t *testing.T) {
	const uri = "data:image/png;base64,QUJD"
	seen := make(chan map[string]any, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Write([]byte(`{"data":[{"id":"muse-spark-1.3-contributor-free"}]}`))
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		select {
		case seen <- body:
		default:
		}
		// reject the exact malformation the old conversion produced
		input, _ := body["input"].([]any)
		for _, it := range input {
			im, _ := it.(map[string]any)
			for _, p := range im["content"].([]any) {
				pm, _ := p.(map[string]any)
				if pm["type"] == "input_image" {
					if _, isObj := pm["image_url"].(map[string]any); isObj {
						w.WriteHeader(http.StatusBadRequest)
						w.Write([]byte(`{"error":{"message":"input[0].content did not match any supported type"}}`))
						return
					}
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"ok"}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer up.Close()

	cfg := testCfg()
	cfg.Zen.BaseURL = up.URL
	rs := testRuntime()
	rs.Zen.ModelMeta["muse-spark-1.3-contributor-free"] = settings.ModelMeta{
		ContextWindow: 262144, MaxOutputTokens: 8192, ResponsesAPI: true,
	}
	g := newGateway(cfg, rs, testKeyFile())

	body := `{"model":"zen/muse-spark-1.3-contributor-free","messages":[{"role":"user","content":[
		{"type":"text","text":"what is this?"},
		{"type":"image_url","image_url":{"url":"` + uri + `"}}]}],"max_tokens":200}`
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.handleChat(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("image request to a Responses-surface model failed: %d: %s",
			rec.Code, rec.Body.String())
	}
	select {
	case sent := <-seen:
		found := false
		for _, it := range sent["input"].([]any) {
			for _, p := range it.(map[string]any)["content"].([]any) {
				pm, _ := p.(map[string]any)
				if pm["type"] == "input_image" {
					if u, _ := pm["image_url"].(string); u == uri {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("upstream never received the image as input_image: %v", sent["input"])
		}
	default:
		t.Fatal("upstream never received a request")
	}
}

// Modalities must reach clients under every spelling they read, from one
// enriched value: pi-openai-compat reads `input`, opencode reads
// `input_modalities`, pi's opencode loader reads `architecture.input_modalities`.
// Missing the pi spelling is why a multimodal model looked text-only to pi and
// the agent dropped images without a word.
func TestCatalogMirrorsInputModalities(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"m1","context_length":4096,
			"architecture":{"input_modalities":["text","image"],"output_modalities":["text"]}},
			{"id":"text-only","context_length":2048}]}`))
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	// a custom (non-preset) provider: the generic passthrough must map
	// modalities too, or every custom provider reads as text-only
	addGenericProvider(t, st, "router", up.URL, "kk1")
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}
	ref, _ := g.provider("router")
	models, err := g.fetchUpstreamModels(ref)
	if err != nil {
		t.Fatalf("fetchUpstreamModels: %v", err)
	}
	e := models[0].(map[string]any)
	want := []string{"text", "image"}
	if fmt.Sprint(e["input"]) != fmt.Sprint(want) {
		t.Errorf("input = %v, want %v", e["input"], want)
	}
	arch, _ := e["architecture"].(map[string]any)
	if fmt.Sprint(arch["input_modalities"]) != fmt.Sprint(want) {
		t.Errorf("architecture.input_modalities = %v, want %v", arch["input_modalities"], want)
	}
	// the mirror merges into an architecture object that is already there
	// rather than replacing it
	merged := map[string]any{"input_modalities": []string{"text"}, "output_modalities": []string{"image"}}
	mirrorInputModalities(map[string]any{"input_modalities": []string{"text", "image"}, "architecture": merged})
	if fmt.Sprint(merged["output_modalities"]) != "[image]" {
		t.Errorf("existing architecture keys clobbered: %v", merged)
	}
	if fmt.Sprint(merged["input_modalities"]) != "[text image]" {
		t.Errorf("architecture.input_modalities not refreshed: %v", merged)
	}
	// a model with no modality data gets no invented field
	if len(models) > 1 {
		e2 := models[1].(map[string]any)
		if _, has := e2["input"]; has {
			t.Errorf("text-only entry must not claim modalities: %v", e2)
		}
		if _, has := e2["architecture"]; has {
			t.Errorf("text-only entry must not gain an architecture block: %v", e2)
		}
	}
}
