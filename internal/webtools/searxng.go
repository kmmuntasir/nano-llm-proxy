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
		return "", fmt.Errorf("SearXNG unreachable at %s — is it installed and running? (scripts/install-web-tools.sh --check): %w", cfg.SearXNGURL, err)
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
		return "", fmt.Errorf("SearXNG returned no results for %q — if every query comes back empty, the instance's engines are likely CAPTCHA-blocked; see docs/deployment.md (engine trimming)", q)
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
