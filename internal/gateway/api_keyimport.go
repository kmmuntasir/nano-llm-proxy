package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Bulk upstream-key import: POST /api/providers/{id}/keys/import takes the
// raw text of a file holding one JSON object of "alias": "key" pairs. File
// order becomes rotation order. Each entry gets the checks a single add
// does (keycheck.go) and its own verdict; one bad key never sinks the rest.

// maxImportKeys caps one import — each key costs a few upstream probes.
const maxImportKeys = 200

// keyImportParallel bounds verifications in flight at once.
const keyImportParallel = 4

// Per-entry import statuses (the failed-verification ones are keyCheck*).
const (
	keyImportAdded     = "added"
	keyImportDuplicate = "duplicate"
	keyImportRejected  = "rejected"
)

// keyImportEntry is one "alias": "key" member of the file, in file order.
type keyImportEntry struct {
	alias, key string
}

type keyImportResult struct {
	Label   string `json:"label"` // the alias as given in the file
	Status  string `json:"status"`
	Message string `json:"message"` // empty for "added"
}

// errImportShape is the whole-file error for anything but one object of
// string members.
var errImportShape = errors.New(`The file must be one JSON object of "alias": "key" pairs`)

// parseKeyImport reads content as one JSON object of string members via the
// token stream — a map would lose file order and hide repeated aliases. The
// error text is shown to the admin as is; it never quotes a key.
func parseKeyImport(content string) ([]keyImportEntry, error) {
	dec := json.NewDecoder(strings.NewReader(content))
	tok, err := dec.Token()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("The file is empty")
	}
	if err != nil {
		return nil, fmt.Errorf("The file isn't valid JSON: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errImportShape
	}
	var entries []keyImportEntry
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("The file isn't valid JSON: %w", err)
		}
		alias, ok := tok.(string) // object member names are always strings
		if !ok {
			return nil, errImportShape
		}
		tok, err = dec.Token()
		if err != nil {
			return nil, fmt.Errorf("The file isn't valid JSON: %w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("The value for %q must be a string (the API key)", alias)
		}
		if seen[alias] {
			return nil, fmt.Errorf("Alias %q appears twice in the file", alias)
		}
		seen[alias] = true
		if len(entries) == maxImportKeys {
			return nil, fmt.Errorf("At most %d keys per import", maxImportKeys)
		}
		entries = append(entries, keyImportEntry{alias: alias, key: key})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, fmt.Errorf("The file isn't valid JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("The file has data after the JSON object")
	}
	if len(entries) == 0 {
		return nil, errors.New("The file has no keys")
	}
	return entries, nil
}

func (g *gateway) handleImportProviderKeys(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := g.store.Provider(id)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p == nil {
		apiErr(w, http.StatusNotFound, "That provider no longer exists (it may have been deleted)")
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	entries, err := parseKeyImport(req.Content)
	if err != nil {
		apiErr(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := g.store.ListProviderKeys(id)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	onProvider := make(map[string]string, len(existing))
	for _, k := range existing {
		onProvider[k.Key] = k.Label
	}

	// cheap checks first; what survives is verified below
	results := make([]keyImportResult, len(entries))
	keys := make([]string, len(entries))
	labels := make([]string, len(entries))
	inFile := map[string]string{} // key → alias of its first valid entry
	var pending []int
	for i, e := range entries {
		results[i].Label = e.alias
		key := strings.TrimSpace(e.key) // file values often carry stray whitespace
		label, labelOK := sanitizeKeyLabel(e.alias)
		switch {
		case key == "":
			results[i].Status, results[i].Message = keyImportRejected, "The key is empty"
			continue
		case !labelOK:
			results[i].Status, results[i].Message = keyImportRejected,
				fmt.Sprintf("The alias must be 1-%d characters — it becomes the key's label", maxKeyLabelLen)
			continue
		}
		if first, dup := inFile[key]; dup {
			results[i].Status, results[i].Message = keyImportDuplicate, fmt.Sprintf("Same key as %q in this file", first)
			continue
		}
		inFile[key] = e.alias
		if have, dup := onProvider[key]; dup {
			results[i].Status, results[i].Message = keyImportDuplicate, fmt.Sprintf("Already on this provider as %q", have)
			continue
		}
		keys[i], labels[i] = key, label
		pending = append(pending, i)
	}

	// verify concurrently, a few at a time; a gone client stops new probes
	ctx := r.Context()
	sem := make(chan struct{}, keyImportParallel)
	var wg sync.WaitGroup
	for _, i := range pending {
		what := fmt.Sprintf("Key %q", labels[i])
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results[i].Status, results[i].Message = classifyKeyCheck(ctx.Err(), p.Name, what)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			status, msg := g.keyCheckVerdict(ctx, p.Name, p.BaseURL, p.AnthropicBaseURL, keys[i], what)
			if status == "" {
				status = keyImportAdded
			}
			results[i].Status, results[i].Message = status, msg
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		// nobody is left to read the report; don't save keys they never saw
		log.Printf("admin user=%s action=provider.key.import provider=%s cancelled: %v", actorEmail(actor), p.Name, ctx.Err())
		apiErr(w, http.StatusServiceUnavailable, "The import was cancelled; nothing was saved")
		return
	}

	var toAdd []store.ProviderKey
	for _, i := range pending {
		if results[i].Status == keyImportAdded {
			toAdd = append(toAdd, store.ProviderKey{Key: keys[i], Label: labels[i]})
		}
	}
	if len(toAdd) > 0 {
		// one write, one pool rebuild, one audit line (count + provider only)
		action := fmt.Sprintf("provider.key.import provider=%s added=%d", p.Name, len(toAdd))
		if !g.applyMutation(w, actor, action, "This upstream key is already on this provider", func() error {
			_, err := g.store.AddProviderKeys(id, toAdd)
			return err
		}) {
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": len(toAdd), "results": results})
}
