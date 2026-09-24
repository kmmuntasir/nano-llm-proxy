package gateway

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Model IDs on /v1/models are self-describing:
//
//	zen/muse-spark-1.3-contributor-free-1M-txt-img-vid-pdf-aud
//	zen/mimo-v2.6-flash-free-200K-txt-img-aud-vid
//	kilo/qwen/qwen3.8-27b:free-262K-txt-img-vid
//
// The suffix (-<context><-modality>...) is cosmetic; every endpoint strips it
// before calling upstream, and accepts both bare and suffixed forms.

var modelSuffixRe = regexp.MustCompile(`-(\d+(?:\.\d+)?[KM])(?:-txt|-img|-vid|-aud|-pdf)*$`)

// claudeCodeCtxRe matches Claude Code's "[1m]" context-window opt-in suffix,
// which arrives at the backend as part of the model string.
var claudeCodeCtxRe = regexp.MustCompile(`(?i)\[1m\]$`)

var modalityTags = map[string]string{
	"text": "txt", "image": "img", "video": "vid", "audio": "aud", "pdf": "pdf",
}

// formatCtx renders a context window as a short label: 1M, 200K, 262K, 1.5M.
func formatCtx(n int64) string {
	if n >= 1_000_000 {
		m := math.Round(float64(n)/1_000_000*10) / 10 // one decimal, then trim .0
		if m == math.Trunc(m) {
			return fmt.Sprintf("%dM", int64(m))
		}
		return fmt.Sprintf("%.1fM", m)
	}
	return fmt.Sprintf("%dK", int64(math.Round(float64(n)/1000)))
}

func modalitySuffix(mods []string) string {
	var parts []string
	for _, m := range mods {
		if t, ok := modalityTags[m]; ok {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "-" + strings.Join(parts, "-")
}

// buildSuffixedID appends the context + modality labels to a bare model id.
func buildSuffixedID(id string, ctx int64, mods []string) string {
	if ctx <= 0 {
		return id
	}
	return id + "-" + formatCtx(ctx) + modalitySuffix(mods)
}

// stripModelSuffix removes the cosmetic suffix and Claude Code's "[1m]"
// context opt-in, or returns the id unchanged.
func stripModelSuffix(id string) string {
	id = claudeCodeCtxRe.ReplaceAllString(id, "")
	return modelSuffixRe.ReplaceAllString(id, "")
}

// stripClientSuffix removes only Claude Code's "[1m]" opt-in — used for
// non-builtin providers, whose real model ids may legitimately end in things
// like "-4K" that the cosmetic-suffix regex would corrupt.
func stripClientSuffix(id string) string {
	return claudeCodeCtxRe.ReplaceAllString(id, "")
}
