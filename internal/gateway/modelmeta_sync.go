package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// zen.modelMeta exists because Zen's /models payload advertises ids only.
// models.dev — the model directory the opencode ecosystem itself publishes —
// carries the missing facts (context/output limits, reasoning, input
// modalities, descriptions) for the same bare ids. The sync below imports
// them so the catalog stays fresh without hand-maintaining a JSON map:
//
//   - live zen ids known to models.dev  -> overwritten from the catalog
//   - live zen ids unknown to models.dev -> manual entries survive untouched
//   - meta ids no longer live on zen     -> pruned (they come back on their
//     own if zen re-adds the model)
//
// The per-model responsesApi flag is never auto-touched — models.dev has no
// such concept, so sync overwrites preserve whatever the admin toggled. Any
// fetch/parse/persist failure leaves the existing meta exactly as it was.

const defaultModelsDevURL = "https://models.dev/api.json"

// metaSyncInFlight guards the background trigger so overlapping maintenance
// ticks never run two syncs at once (manual syncs bypass it by design).
var metaSyncInFlight atomic.Bool

type modelsDevModel struct {
	Limit struct {
		Context int64 `json:"context"`
		Output  int64 `json:"output"`
	} `json:"limit"`
	Reasoning  bool `json:"reasoning"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
	Description string `json:"description"`
}

// decodeOpencodeModels streams the models.dev document and materializes only
// the "opencode" provider subtree. The full payload is ~5 MB and this gateway
// targets 512 MB VPS boxes — decoding everything would spike memory for data
// we throw away.
func decodeOpencodeModels(r io.Reader) (map[string]modelsDevModel, error) {
	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected top-level object")
	}
	models := map[string]modelsDevModel{}
	found := false
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		provider, _ := keyTok.(string)
		if provider == "opencode" {
			var entry struct {
				Models map[string]modelsDevModel `json:"models"`
			}
			if err := dec.Decode(&entry); err != nil {
				return nil, fmt.Errorf("decode opencode: %w", err)
			}
			models, found = entry.Models, true
		} else {
			var skip json.RawMessage // transient; freed before the next provider
			if err := dec.Decode(&skip); err != nil {
				return nil, fmt.Errorf("skip %s: %w", provider, err)
			}
		}
	}
	if _, err := dec.Token(); err != nil { // consume closing '}'
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("no opencode provider in catalog")
	}
	return models, nil
}

// liveZenModelIDs fetches Zen's own /models with the first healthy pool key —
// the same shape fetchUpstreamModels uses, minus the enrichment.
func (g *gateway) liveZenModelIDs(ctx context.Context) ([]string, error) {
	ref, ok := g.provider("zen")
	if !ok {
		return nil, fmt.Errorf("no zen provider")
	}
	key := ref.pool.pick(nil)
	if key == nil {
		return nil, fmt.Errorf("no healthy zen keys")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key.Key)
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zen /models HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&parsed); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// syncModelMeta runs one fetch→merge→persist cycle and returns the outcome.
// It never mutates modelMeta on failure — a bad sync day must not degrade the
// catalog we already have.
func (g *gateway) syncModelMeta(devURL string) settings.ModelMetaSyncStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st := settings.ModelMetaSyncStatus{At: time.Now().Unix()}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, devURL, nil)
	if err == nil {
		var resp *http.Response
		resp, err = g.client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("models.dev HTTP %d", resp.StatusCode)
			} else {
				var models map[string]modelsDevModel
				models, err = decodeOpencodeModels(io.LimitReader(resp.Body, 20<<20))
				if err == nil {
					err = g.mergeModelMeta(ctx, models, &st)
				}
			}
		}
	}
	if err != nil {
		return g.failModelMetaSync("%v", err)
	}
	st.OK = true
	return st
}

// mergeModelMeta computes the merged catalog and persists it inside the
// store's read-modify-write transaction so a concurrent admin PUT can't be
// clobbered, then refreshes the in-memory snapshot and the /v1/models cache.
func (g *gateway) mergeModelMeta(ctx context.Context, models map[string]modelsDevModel, st *settings.ModelMetaSyncStatus) error {
	ids, err := g.liveZenModelIDs(ctx)
	if err != nil {
		return fmt.Errorf("zen catalog: %w", err)
	}
	live := make(map[string]bool, len(ids))
	for _, id := range ids {
		live[id] = true
	}

	// merge on top of the STORE state, not the in-memory snapshot: an admin
	// PUT or a previous sync may have changed the document since this gateway
	// last cached it, and the DB is the source of truth
	current, err := g.store.LoadRuntimeSettings()
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	for _, id := range ids {
		dev, ok := models[id]
		if !ok || dev.Limit.Context <= 0 || dev.Limit.Output <= 0 {
			continue // unknown or unusable — keep whatever the admin curated
		}
		responses := false
		if old, exists := current.Zen.ModelMeta[id]; exists {
			st.Updated++
			responses = old.ResponsesAPI // admin toggles survive catalog refreshes
		} else {
			st.Added++
		}
		current.Zen.ModelMeta[id] = settings.ModelMeta{
			ContextWindow:   dev.Limit.Context,
			MaxOutputTokens: dev.Limit.Output,
			Reasoning:       dev.Reasoning,
			ResponsesAPI:    responses,
			InputModalities: dev.Modalities.Input,
			Description:     dev.Description,
		}
	}
	for id := range current.Zen.ModelMeta {
		if !live[id] {
			delete(current.Zen.ModelMeta, id)
			st.Pruned++
		}
	}

	// mark success BEFORE the status snapshot is persisted — syncModelMeta
	// only flips it on the returned copy after we return, and the DB must
	// not record a successful sync as failed
	st.OK = true
	status := *st
	if err := g.store.UpdateSettings(func(rs *settings.RuntimeSettings) *settings.RuntimeSettings {
		rs.Zen.ModelMeta = current.Zen.ModelMeta
		rs.Zen.ModelMetaSyncStatus = &status
		return rs
	}); err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	if rs, err := g.store.LoadRuntimeSettings(); err == nil {
		g.rsPtr.Store(rs) // in-memory snapshot matches what we just persisted
	}
	g.invalidateCatalog()
	return nil
}

// failModelMetaSync records why a sync failed without touching modelMeta.
func (g *gateway) failModelMetaSync(format string, args ...any) settings.ModelMetaSyncStatus {
	st := settings.ModelMetaSyncStatus{At: time.Now().Unix(), Error: strings.TrimSpace(fmt.Sprintf(format, args...))}
	log.Printf("modelmeta sync FAILED: %s", st.Error)
	if err := g.store.UpdateSettings(func(rs *settings.RuntimeSettings) *settings.RuntimeSettings {
		rs.Zen.ModelMetaSyncStatus = &st
		return rs
	}); err != nil {
		log.Printf("modelmeta sync: persist status: %v", err)
		return st
	}
	if rs, err := g.store.LoadRuntimeSettings(); err == nil {
		g.rsPtr.Store(rs)
	}
	return st
}

// maybeSyncModelMeta runs from the maintenance loop: at most one in flight,
// at most once per 24h, only when enabled. Zero status timestamp means "run
// on the first tick after enabling".
func (g *gateway) maybeSyncModelMeta() {
	rs := g.rs()
	if !rs.Zen.ModelMetaAutoSync {
		return
	}
	var last int64
	if rs.Zen.ModelMetaSyncStatus != nil {
		last = rs.Zen.ModelMetaSyncStatus.At
	}
	if last != 0 && time.Since(time.Unix(last, 0)) < 24*time.Hour {
		return
	}
	if !metaSyncInFlight.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer metaSyncInFlight.Store(false)
		st := g.syncModelMeta(defaultModelsDevURL)
		log.Printf("modelmeta sync: ok=%v added=%d updated=%d pruned=%d", st.OK, st.Added, st.Updated, st.Pruned)
	}()
}

// handleSyncModelMeta is the GUI "Sync now" button: synchronous, so the
// response can carry the outcome. 200 even on ok:false — the GUI renders the
// error from the status block.
func (g *gateway) handleSyncModelMeta(w http.ResponseWriter, r *http.Request) {
	st := g.syncModelMeta(defaultModelsDevURL)
	log.Printf("admin user=%s action=settings.modelmeta.sync ok=%v added=%d updated=%d pruned=%d",
		actorEmail(contextUser(r)), st.OK, st.Added, st.Updated, st.Pruned)
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "settings": g.rs()})
}
