package webtools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2"
)

// Native fetch leg: plain HTTP + readability + markdown conversion. Fast
// (usually 0.3–2 s) and clean for articles and docs; obscura covers what it
// can't (JS shells, bot walls).

const (
	maxFetchBytes = 5 << 20 // 5 MB body cap; over-limit truncates quietly
	fallbackMin   = 200     // readability results shorter than this fall back to whole-HTML conversion
)

const desktopUA = "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"

// fetchNative performs the guarded HTTP GET and converts the body. The
// returned outcome feeds the escalation decision; on success Body holds the
// markdown/text content (unclamped — the caller clamps).
func (s *Service) fetchNative(ctx context.Context, rawURL string) (fetchOutcome, bool) {
	var out fetchOutcome

	u, err := url.Parse(rawURL)
	if err != nil {
		out.TransportErr = true
		return out, false
	}
	if !s.guardOff {
		if err := URLAllowed(rawURL); err != nil {
			out.Blocked = err
			return out, false
		}
	}

	cctx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg().FetchTimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, rawURL, nil)
	if err != nil {
		out.TransportErr = true
		return out, false
	}
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,text/plain;q=0.8,*/*;q=0.5")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{
		Transport: s.transport(),
		Timeout:   time.Duration(s.cfg().FetchTimeoutSeconds) * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			// The guarded transport re-runs the SSRF check on every
			// redirect dial, so nothing extra needed here.
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		out.TransportErr = true
		out.RawErr = err
		return out, false
	}
	defer resp.Body.Close()
	out.HTTPStatus = resp.StatusCode

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		out.TransportErr = true
		out.RawErr = err
		return out, false
	}
	out.RawLen = len(body)
	out.ContentType = resp.Header.Get("Content-Type")

	if resp.StatusCode >= 400 {
		return out, false
	}

	media := parseMediaType(out.ContentType)
	switch {
	case strings.Contains(media, "html"):
		text, title, convErr := htmlToMarkdown(body, u)
		out.TextLen = len(text)
		if convErr != nil || text == "" {
			out.ExtractionFailed = true
			return out, false
		}
		if title != "" {
			out.Body = fmt.Sprintf("# %s\n\n%s", title, text)
		} else {
			out.Body = text
		}
		out.Title = title
		return out, true

	case strings.Contains(media, "json"):
		out.Body = fmt.Sprintf("```json\n%s\n```", strings.TrimSpace(string(body)))
		out.TextLen = len(out.Body)
		return out, true

	case media == "" || strings.HasPrefix(media, "text/"):
		// text/plain, text/markdown, and servers that lie with no type:
		// pass through raw.
		out.Body = strings.TrimSpace(string(body))
		out.TextLen = len(out.Body)
		return out, true

	default:
		out.Unsupported = true
		return out, false
	}
}

// htmlToMarkdown runs readability then converts the cleaned HTML. If
// extraction comes back tiny or fails (SPAs, odd markup), it falls back to
// converting the raw HTML whole — messy beats empty.
func htmlToMarkdown(body []byte, pageURL *url.URL) (md, title string, err error) {
	art, rerr := readability.FromReader(strings.NewReader(string(body)), pageURL)
	if rerr == nil {
		var cleaned strings.Builder
		if rerr2 := art.RenderHTML(&cleaned); rerr2 == nil {
			htmlStr := cleaned.String()
			if len(htmlStr) >= fallbackMin {
				md, merr := htmltomarkdown.ConvertString(htmlStr)
				if merr == nil && strings.TrimSpace(md) != "" {
					return md, art.Title(), nil
				}
			}
		}
		title = art.Title()
	}
	// Fallback: whole-document conversion.
	md, cerr := htmltomarkdown.ConvertString(string(body))
	if cerr != nil {
		return "", "", fmt.Errorf("html conversion failed: %w", cerr)
	}
	return md, title, nil
}

func parseMediaType(contentType string) string {
	if contentType == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.TrimSpace(strings.Split(contentType, ";")[0])
	}
	return mt
}
