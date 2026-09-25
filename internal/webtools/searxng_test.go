package webtools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

func testCfg(searxURL string) settings.WebToolsSettings {
	cfg := settings.DefaultRuntimeSettings().WebTools
	cfg.SearXNGURL = searxURL
	return cfg
}

func TestSearchQueryFormatsResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"title": "Go Blog", "url": "https://go.dev/blog/1", "content": "First result", "engines": []string{"ddg", "brave"}},
				{"title": "Dup", "url": "https://go.dev/blog/1", "content": "dup should be skipped"},
				{"title": "Second", "url": "https://go.dev/blog/2", "content": "Second result"},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()

	out, err := searchQuery(context.Background(), testCfg(srv.URL), srv.Client(), "golang", 5)
	if err != nil {
		t.Fatalf("searchQuery: %v", err)
	}
	if !strings.Contains(out, "[Go Blog](https://go.dev/blog/1)") {
		t.Fatalf("missing first result: %q", out)
	}
	if strings.Count(out, "go.dev/blog/1") != 1 {
		t.Fatalf("duplicate URL not deduped: %q", out)
	}
	if !strings.Contains(out, "_engines: ddg, brave_") {
		t.Fatalf("missing engines footer: %q", out)
	}
}

func TestSearchQueryErrors(t *testing.T) {
	t.Run("empty query", func(t *testing.T) {
		if _, err := searchQuery(context.Background(), testCfg("http://127.0.0.1:9"), nil, "  ", 0); err == nil {
			t.Fatal("want error for empty query")
		}
	})

	t.Run("server down", func(t *testing.T) {
		_, err := searchQuery(context.Background(), testCfg("http://127.0.0.1:1"), nil, "q", 0)
		if err == nil || !strings.Contains(err.Error(), "unreachable") {
			t.Fatalf("want unreachable error, got %v", err)
		}
	})

	t.Run("500 with body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "engine exploded\nsecond line", 500)
		}))
		defer srv.Close()
		_, err := searchQuery(context.Background(), testCfg(srv.URL), srv.Client(), "q", 0)
		if err == nil || !strings.Contains(err.Error(), "status 500") || !strings.Contains(err.Error(), "engine exploded") {
			t.Fatalf("want status+body error, got %v", err)
		}
	})

	t.Run("html instead of json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("<html><body>formats not enabled</body></html>"))
		}))
		defer srv.Close()
		_, err := searchQuery(context.Background(), testCfg(srv.URL), srv.Client(), "q", 0)
		if err == nil || !strings.Contains(err.Error(), "not JSON") {
			t.Fatalf("want not-JSON error, got %v", err)
		}
	})

	t.Run("empty results", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"results": []}`))
		}))
		defer srv.Close()
		_, err := searchQuery(context.Background(), testCfg(srv.URL), srv.Client(), "q", 0)
		if err == nil || !strings.Contains(err.Error(), "no results") {
			t.Fatalf("want no-results error, got %v", err)
		}
	})

	t.Run("max results clamp", func(t *testing.T) {
		var gotMax int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r
			gotMax = -1
			res := make([]map[string]any, 40)
			for i := range res {
				res[i] = map[string]any{"title": "t", "url": "https://x.dev/" + json.Number(jsonInt(i)).String()}
			}
			json.NewEncoder(w).Encode(map[string]any{"results": res})
		}))
		defer srv.Close()
		out, err := searchQuery(context.Background(), testCfg(srv.URL), srv.Client(), "q", 100)
		if err != nil {
			t.Fatalf("searchQuery: %v", err)
		}
		if gotMax != -1 || strings.Count(out, "\n") > 30*2 {
			// 25 capped results at most: count links
		}
		if n := strings.Count(out, "](https://x.dev/"); n > maxResultsCap {
			t.Fatalf("got %d results, want clamp at %d", n, maxResultsCap)
		}
	})
}

func jsonInt(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
