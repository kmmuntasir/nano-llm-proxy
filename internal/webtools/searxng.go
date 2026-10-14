package webtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

// SearXNG JSON API client. The endpoint is admin-configured (usually
// loopback), so no SSRF guard here — the guard would block the very
// addresses SearXNG lives on.

const (
	searchTimeout     = 10 * time.Second
	defaultMaxResults = 8
	maxResultsCap     = 25
)

type searxResult struct {
	Title   string   `json:"title"`
	URL     string   `json:"url"`
	Content string   `json:"content"`
	Engines []string `json:"engines"`
}

type searxResponse struct {
	Results   []searxResult `json:"results"`
	Answers   []any         `json:"answers"`
	NumberAge json.Number   `json:"number_of_results"`
	// Unresponsive lists the engines that failed this query as
	// [name, reason] pairs ("CAPTCHA", "too many requests", "timeout").
	Unresponsive [][]string `json:"unresponsive_engines"`
}

// Search failures callers tell apart: SearXNG isn't answering at all, or it
// answers but no engine returned anything (usually CAPTCHA / rate limits on
// this machine's IP). The error text says which engines failed and why; the
// next step depends on the deployment, so it is left to the caller.
var (
	ErrSearxngUnreachable = errors.New("SearXNG is not answering")
	ErrSearxngNoResults   = errors.New("SearXNG answered, but no search engine returned results")
)

// describeUnresponsive renders "duckduckgo (CAPTCHA), brave (too many
// requests)" from SearXNG's unresponsive_engines list.
func describeUnresponsive(list [][]string) string {
	parts := make([]string, 0, len(list))
	for _, e := range list {
		switch {
		case len(e) >= 2 && e[1] != "":
			parts = append(parts, e[0]+" ("+e[1]+")")
		case len(e) >= 1:
			parts = append(parts, e[0])
		}
	}
	return strings.Join(parts, ", ")
}

// searchQueries SearXNG and renders a numbered markdown result list.
func searchQuery(ctx context.Context, cfg settings.WebToolsSettings, client *http.Client, query string, maxResults int) (string, error) {
	if client == nil {
		client = &http.Client{}
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return "", errors.New("search query is empty")
	}
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}
	if maxResults > maxResultsCap {
		maxResults = maxResultsCap
	}

	base, err := url.Parse(cfg.SearXNGURL)
	if err != nil {
		return "", fmt.Errorf("configured SearXNG URL %q does not parse: %w", cfg.SearXNGURL, err)
	}
	endpoint := base.JoinPath("search")
	params := url.Values{}
	params.Set("q", q)
	params.Set("format", "json")
	params.Set("safesearch", "1")
	endpoint.RawQuery = params.Encode()

	sctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(sctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w at %s: %w", ErrSearxngUnreachable, cfg.SearXNGURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("reading SearXNG response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SearXNG returned status %d: %s", resp.StatusCode, firstLine(string(body)))
	}
	var sr searxResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return "", fmt.Errorf("SearXNG response is not JSON — is \"json\" listed in settings.yml search.formats? (%w)", err)
	}

	results := sr.Results
	// Dedupe by URL (engines frequently overlap) while preserving order.
	seen := make(map[string]struct{}, len(results))
	list := make([]searxResult, 0, len(results))
	for _, r := range results {
		if r.URL == "" {
			continue
		}
		if _, dup := seen[r.URL]; dup {
			continue
		}
		seen[r.URL] = struct{}{}
		list = append(list, r)
		if len(list) >= maxResults {
			break
		}
	}
	if len(list) == 0 {
		if failed := describeUnresponsive(sr.Unresponsive); failed != "" {
			return "", fmt.Errorf("%w for %q — engines that failed: %s", ErrSearxngNoResults, q, failed)
		}
		return "", fmt.Errorf("%w for %q", ErrSearxngNoResults, q)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Web results for %q:\n\n", q)
	for i, r := range list {
		fmt.Fprintf(&b, "%d. [%s](%s)\n", i+1, r.Title, r.URL)
		if c := strings.TrimSpace(r.Content); c != "" {
			fmt.Fprintf(&b, "   %s\n", strings.ReplaceAll(c, "\n", " "))
		}
		if len(r.Engines) > 0 {
			fmt.Fprintf(&b, "   _engines: %s_\n", strings.Join(r.Engines, ", "))
		}
	}
	fmt.Fprintf(&b, "\n(%d results; SearXNG)", len(list))
	return b.String(), nil
}
