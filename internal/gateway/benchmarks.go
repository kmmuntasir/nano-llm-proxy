package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Model benchmarks for the GUI's Models page. models.dev publishes no scores,
// and Artificial Analysis' own API needs a key, but OpenRouter's public,
// keyless /api/v1/models carries a "benchmarks" object per model: the three
// Artificial Analysis indexes (intelligence, coding, agentic) and Design
// Arena per-category results. That document is the source:
//
//   - fetched beside the models.dev sync (Sync now, and daily when
//     modelMetaAutoSync is on) but independently — either can fail alone;
//   - only models with at least one published score are kept;
//   - persisted as one settings-table row so a restart doesn't wait a day;
//   - joined to catalog entries by a normalized model slug (benchKey) and
//     attached only to /api/models — /v1/models stays exactly as clients
//     know it.
//
// A failed or empty fetch never replaces the last good snapshot.

const defaultBenchmarksURL = "https://openrouter.ai/api/v1/models"

// benchSyncInFlight keeps background benchmark fetches from overlapping.
var benchSyncInFlight atomic.Bool

// benchIndex is an immutable lookup built from one snapshot; swapped whole.
type benchIndex struct {
	snap *store.BenchmarkSnapshot
	full map[string]*store.ModelBenchmark // "vendor/model" keys
	tail map[string]*store.ModelBenchmark // "model" keys, vendor dropped
}

// benchmarksStatus is the GUI-facing outcome of the benchmark source.
type benchmarksStatus struct {
	FetchedAt int64  `json:"fetched_at"`
	Count     int    `json:"count"`
	Error     string `json:"error,omitempty"`
}

var benchDateSuffixRe = regexp.MustCompile(`-(?:\d{8}|\d{4}-\d{2}-\d{2})$`)

// benchKey normalizes a model id for the cross-catalog join. Catalogs spell
// the same model differently — "glm-5.3-flash" (zen), "z-ai/glm-5.3-flash"
// (OpenRouter), "zhipuai/glm-5.3-flash" (models.dev), "claude-haiku-4-5" vs
// "claude-haiku-4.5", "…:free", "…-free", dated snapshots — so the key drops
// variant tags, the zen free marker and date suffixes, and folds "." and "_"
// into "-". The vendor part is kept; callers decide whether to drop it.
func benchKey(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.TrimPrefix(id, "~")
	if i := strings.IndexByte(id, ':'); i >= 0 {
		id = id[:i]
	}
	id = strings.TrimSuffix(id, "-free")
	id = benchDateSuffixRe.ReplaceAllString(id, "")
	return strings.NewReplacer(".", "-", "_", "-").Replace(id)
}

func benchTail(key string) string {
	return key[strings.LastIndexByte(key, '/')+1:]
}

// newBenchIndex builds the lookup maps. Primary ids claim keys first; a
// variant id (":batch", ":free") never displaces the base model; canonical
// (dated) slugs only fill keys nothing else claimed.
func newBenchIndex(snap *store.BenchmarkSnapshot) *benchIndex {
	ix := &benchIndex{
		snap: snap,
		full: make(map[string]*store.ModelBenchmark, len(snap.Models)),
		tail: make(map[string]*store.ModelBenchmark, len(snap.Models)),
	}
	put := func(m map[string]*store.ModelBenchmark, key string, b *store.ModelBenchmark) {
		if old, ok := m[key]; ok && !(strings.Contains(old.ID, ":") && !strings.Contains(b.ID, ":")) {
			return
		}
		m[key] = b
	}
	for i := range snap.Models {
		b := &snap.Models[i]
		k := benchKey(b.ID)
		put(ix.full, k, b)
		put(ix.tail, benchTail(k), b)
	}
	for i := range snap.Models {
		b := &snap.Models[i]
		if b.CanonicalSlug == "" {
			continue
		}
		k := benchKey(b.CanonicalSlug)
		if _, ok := ix.full[k]; !ok {
			ix.full[k] = b
		}
		if _, ok := ix.tail[benchTail(k)]; !ok {
			ix.tail[benchTail(k)] = b
		}
	}
	return ix
}

// lookup resolves a bare upstream model id: an exact vendor/model match
// first, then the vendor-less slug.
func (ix *benchIndex) lookup(id string) *store.ModelBenchmark {
	if ix == nil || id == "" {
		return nil
	}
	k := benchKey(id)
	if b := ix.full[k]; b != nil {
		return b
	}
	return ix.tail[benchTail(k)]
}

func (ix *benchIndex) status() benchmarksStatus {
	if ix == nil {
		return benchmarksStatus{}
	}
	return benchmarksStatus{FetchedAt: ix.snap.FetchedAt, Count: len(ix.snap.Models), Error: ix.snap.Error}
}

// parseOpenRouterBenchmarks extracts the benchmarked models from an
// OpenRouter /api/v1/models document. Each model's benchmark block is decoded
// leniently: a shape change in one sub-object drops that sub-object, not the
// whole sync.
func parseOpenRouterBenchmarks(r io.Reader) ([]store.ModelBenchmark, error) {
	var doc struct {
		Data []struct {
			ID            string                     `json:"id"`
			CanonicalSlug string                     `json:"canonical_slug"`
			Name          string                     `json:"name"`
			Benchmarks    map[string]json.RawMessage `json:"benchmarks"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if len(doc.Data) == 0 {
		return nil, fmt.Errorf("no models in payload")
	}
	var out []store.ModelBenchmark
	for _, m := range doc.Data {
		if m.ID == "" || len(m.Benchmarks) == 0 {
			continue
		}
		b := store.ModelBenchmark{ID: m.ID, Name: m.Name}
		if m.CanonicalSlug != m.ID {
			b.CanonicalSlug = m.CanonicalSlug
		}
		var aa struct {
			Intelligence *float64 `json:"intelligence_index"`
			Coding       *float64 `json:"coding_index"`
			Agentic      *float64 `json:"agentic_index"`
		}
		if raw, ok := m.Benchmarks["artificial_analysis"]; ok && json.Unmarshal(raw, &aa) == nil {
			b.Intelligence, b.Coding, b.Agentic = aa.Intelligence, aa.Coding, aa.Agentic
		}
		if raw, ok := m.Benchmarks["design_arena"]; ok {
			var arena []store.DesignArenaScore
			if json.Unmarshal(raw, &arena) == nil {
				for _, s := range arena {
					if s.Category != "" {
						b.DesignArena = append(b.DesignArena, s)
					}
				}
			}
		}
		if b.Intelligence == nil && b.Coding == nil && b.Agentic == nil && len(b.DesignArena) == 0 {
			continue
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("payload has no benchmark data")
	}
	return out, nil
}

func (g *gateway) fetchBenchmarks(ctx context.Context, url string) ([]store.ModelBenchmark, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("benchmarks HTTP %d", resp.StatusCode)
	}
	return parseOpenRouterBenchmarks(io.LimitReader(resp.Body, 20<<20))
}

// loadBenchmarks restores the persisted snapshot at boot. A corrupt row is
// logged and treated as empty — benchmarks are decoration, never a reason
// to refuse to start.
func (g *gateway) loadBenchmarks() {
	snap, err := g.store.LoadModelBenchmarks()
	if err != nil {
		log.Printf("benchmarks: load: %v", err)
		snap = &store.BenchmarkSnapshot{}
	}
	g.bench.Store(newBenchIndex(snap))
}

// syncBenchmarks runs one fetch→persist cycle. On failure the last good
// models stay in place and only the error/attempt time change.
func (g *gateway) syncBenchmarks(url string) benchmarksStatus {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	now := time.Now().Unix()
	models, err := g.fetchBenchmarks(ctx, url)
	snap := &store.BenchmarkSnapshot{AttemptAt: now}
	if err != nil {
		if prev := g.bench.Load(); prev != nil {
			snap.FetchedAt, snap.Models = prev.snap.FetchedAt, prev.snap.Models
		}
		snap.Error = strings.TrimSpace(err.Error())
		log.Printf("benchmarks sync FAILED: %s", snap.Error)
	} else {
		snap.FetchedAt, snap.Models = now, models
	}
	if g.store != nil {
		if perr := g.store.SaveModelBenchmarks(snap); perr != nil {
			log.Printf("benchmarks sync: persist: %v", perr)
		}
	}
	ix := newBenchIndex(snap)
	g.bench.Store(ix)
	return ix.status()
}

// maybeSyncBenchmarks runs from the maintenance loop under the catalog
// auto-sync toggle: at most once per 24h after a success, at most hourly
// after a failure, never two at once.
func (g *gateway) maybeSyncBenchmarks() {
	if !g.rs().Zen.ModelMetaAutoSync {
		return
	}
	if ix := g.bench.Load(); ix != nil {
		if s := ix.snap; s.FetchedAt != 0 && time.Since(time.Unix(s.FetchedAt, 0)) < 24*time.Hour {
			return
		} else if s.AttemptAt != 0 && time.Since(time.Unix(s.AttemptAt, 0)) < time.Hour {
			return
		}
	}
	if !benchSyncInFlight.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer benchSyncInFlight.Store(false)
		st := g.syncBenchmarks(defaultBenchmarksURL)
		log.Printf("benchmarks sync: models=%d error=%q", st.Count, st.Error)
	}()
}

// withBenchmarks returns the catalog with a "benchmarks" object attached to
// every entry the index matches. Matched entries are shallow copies — the
// shared cached catalog (and therefore /v1/models) is never mutated.
func (g *gateway) withBenchmarks(models []any) []any {
	ix := g.bench.Load()
	if ix == nil || len(ix.snap.Models) == 0 {
		return models
	}
	out := make([]any, len(models))
	for i, m := range models {
		out[i] = m
		e, ok := m.(map[string]any)
		if !ok {
			continue
		}
		id, _ := e["id"].(string)
		_, bare, found := strings.Cut(id, "/")
		if !found {
			continue
		}
		// the real id first (a custom provider's id may legitimately end in
		// something the cosmetic-suffix regex would eat), then the id with
		// the zen/zai context+modality suffix removed
		b := ix.lookup(bare)
		if b == nil {
			b = ix.lookup(stripModelSuffix(bare))
		}
		if b == nil {
			continue
		}
		view := *b
		view.CanonicalSlug = "" // join detail, not part of the API shape
		c := maps.Clone(e)
		c["benchmarks"] = view
		out[i] = c
	}
	return out
}
