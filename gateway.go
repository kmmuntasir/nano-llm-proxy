package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const freeTierHint = "upstream FreeTierError: the request failed the provider's client-shape check; " +
	"check the provider's client emulation settings (userAgent, headers, body shape)"

const upgradeHint = "upstream UpgradeRequired: the configured zen.userAgent is below the " +
	"1.18.0 floor; raise it in the admin GUI (Settings → Zen)"

// providerRef is the runtime handle for one enabled provider: its routing
// prefix, proxy behavior type, key pool, and upstream base URL. Built by
// rebuildPools from the store (or from config in store==nil test mode).
type providerRef struct {
	name    string // routing prefix ("zen/...")
	typ     string // "openai" | "opencode"
	pool    *Pool
	baseURL string
	builtin bool
}

type gateway struct {
	// cfgPtr holds the bootstrap config (set once, never swapped); rsPtr
	// holds the runtime settings and is atomically swapped by the settings
	// API so the lock-free hot path always sees a coherent snapshot.
	cfgPtr atomic.Pointer[Config]
	rsPtr  atomic.Pointer[RuntimeSettings]

	store *Store
	zen   *Pool // builtin pools — swapped wholesale by rebuildPools
	kilo  *Pool

	regMu    sync.RWMutex
	registry []providerRef // every enabled provider, sort_order first

	ck      clientKeyCache // client API keys, hot path
	usage   *usageTracker  // per-client-key counters + activity ring
	backoff loginBackoff   // login rate limiting

	client *http.Client

	mu          sync.Mutex
	surfaceOver map[string]string // learned model -> surface flips ("chat"/"responses")

	catMu     sync.Mutex
	catalog   []byte // cached merged /v1/models body
	catalogAt time.Time

	usageBuf usageBuffer // per-request usage events, drained to the DB by the maintenance loop
}

// conf returns the bootstrap config; never nil after either constructor.
func (g *gateway) conf() *Config { return g.cfgPtr.Load() }

// rs returns the current runtime settings snapshot; never nil after either
// constructor, and freshly swapped values are visible to subsequent reads.
func (g *gateway) rs() *RuntimeSettings { return g.rsPtr.Load() }

// newGateway is the store-less constructor used by tests: pools come from the
// keyFile, provider resolution falls back to config builtins, and auth uses
// cfg.APIKeys. Signature and behavior are load-bearing for existing tests.
func newGateway(cfg *Config, rs *RuntimeSettings, kf *keyFile) *gateway {
	g := &gateway{
		zen:         newPool("zen", rs.Rotation, rs.Retry.MaxRequestsPerKeyDay, kf.Zen),
		kilo:        newPool("kilo", rs.Rotation, rs.Retry.MaxRequestsPerKeyDay, kf.Kilo),
		usage:       newUsageTracker(),
		backoff:     newLoginBackoff(),
		surfaceOver: map[string]string{},
	}
	g.cfgPtr.Store(cfg)
	g.rsPtr.Store(rs)
	g.initHTTPClient()
	return g
}

// newGatewayFromStore is the production constructor: the DB is source of
// truth. Migrate+Bootstrap must already have run.
func newGatewayFromStore(cfg *Config, rs *RuntimeSettings, st *Store) (*gateway, error) {
	g := newGateway(cfg, rs, &keyFile{})
	g.store = st
	if err := g.rebuildPools(); err != nil {
		return nil, err
	}
	return g, nil
}

func (g *gateway) initHTTPClient() {
	g.client = &http.Client{
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

// rebuildPools rebuilds every pool + the registry + the client-key cache from
// the store. Called after every CRUD write so GUI changes hit the hot path
// within the same request. Existing per-key runtime state (cooldowns, usage
// counters) survives via key-hash matching.
func (g *gateway) rebuildPools() error {
	if g.store == nil {
		return nil
	}
	provs, keyLists, err := g.store.ListProvidersWithKeys()
	if err != nil {
		return err
	}

	// preserve runtime KeyState across rebuilds by upstream key hash
	prev := map[string]*KeyState{}
	g.regMu.RLock()
	for _, ref := range g.registry {
		for _, ks := range ref.pool.rawKeys() {
			prev[ks.Hash] = ks
		}
	}
	for _, p := range []*Pool{g.zen, g.kilo} {
		if p != nil {
			for _, ks := range p.rawKeys() {
				prev[ks.Hash] = ks
			}
		}
	}
	g.regMu.RUnlock()

	var zenP, kiloP *Pool
	var registry []providerRef
	for i, row := range provs {
		var entries []keyFileEntry
		for _, k := range keyLists[i] {
			if k.Disabled {
				continue // DB-disabled upstream keys never enter the pool
			}
			entries = append(entries, keyFileEntry{Label: k.Label, Key: k.Key})
		}
		pool := newPoolPreserving(row.Name, g.rs().Rotation, g.rs().Retry.MaxRequestsPerKeyDay, entries, prev)
		switch {
		case row.Builtin && row.Name == "zen":
			zenP = pool
		case row.Builtin && row.Name == "kilo":
			kiloP = pool
		}
		if !row.Enabled {
			continue // disabled providers leave the registry: routing 400s
		}
		registry = append(registry, providerRef{
			name: row.Name, typ: row.Type, pool: pool,
			baseURL: row.BaseURL, builtin: row.Builtin,
		})
	}

	g.regMu.Lock()
	g.registry = registry
	if zenP != nil {
		g.zen = zenP
	}
	if kiloP != nil {
		g.kilo = kiloP
	}
	g.regMu.Unlock()

	if err := g.ck.reload(g.store); err != nil {
		return err
	}
	g.invalidateCatalog()
	return nil
}

// invalidateCatalog drops the cached merged /v1/models body so the next
// catalog request re-fetches with current settings (provider CRUD, modelMeta
// sync, and settings writes all call this).
func (g *gateway) invalidateCatalog() {
	g.catMu.Lock()
	g.catalog = nil
	g.catMu.Unlock()
}

// provider resolves a routing prefix against the registry. In store==nil
// (test) mode it falls back to the config builtins.
func (g *gateway) provider(prefix string) (providerRef, bool) {
	if g.store == nil {
		switch prefix {
		case "zen":
			return providerRef{name: "zen", typ: "opencode", pool: g.zen, baseURL: g.conf().Zen.BaseURL, builtin: true}, true
		case "kilo":
			return providerRef{name: "kilo", typ: "openai", pool: g.kilo, baseURL: g.conf().Kilo.BaseURL, builtin: true}, true
		}
		return providerRef{}, false
	}
	g.regMu.RLock()
	defer g.regMu.RUnlock()
	for _, ref := range g.registry {
		if ref.name == prefix {
			return ref, true
		}
	}
	return providerRef{}, false
}

func (g *gateway) allProviders() []providerRef {
	if g.store == nil {
		z, _ := g.provider("zen")
		k, _ := g.provider("kilo")
		return []providerRef{z, k}
	}
	g.regMu.RLock()
	defer g.regMu.RUnlock()
	return append([]providerRef(nil), g.registry...)
}

// classify decides what to do with a non-200 upstream response.
type verdict struct {
	action   string // "nextkey", "failfast", "flipsurface"
	cooldown time.Duration
	hint     string
	status   int // client status for failfast
	body     []byte
}

func (g *gateway) classify(pool *Pool, k *KeyState, resp *http.Response) verdict {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	v := verdict{action: "nextkey", body: body}

	switch resp.StatusCode {
	case 429:
		pool.recordRateLimit(k)
		if ra := resp.Header.Get("Retry-After"); ra != "" && g.rs().Retry.RespectRetryAfter {
			var secs int
			if _, err := fmt.Sscanf(ra, "%d", &secs); err == nil && secs > 0 && secs < 3600 {
				v.cooldown = time.Duration(secs) * time.Second
			}
		}
		if v.cooldown == 0 {
			v.cooldown = time.Duration(g.rs().Retry.CooldownSeconds) * time.Second
		}
	case 401:
		// zen occasionally wraps upstream flakes as 401 server_error — only a
		// genuine auth rejection disables the key; everything else just cools.
		if strings.Contains(string(body), "AuthError") || strings.Contains(string(body), "Invalid API key") {
			v.hint = fmt.Sprintf("key %s rejected (401 auth) — disabled", k.Label)
			pool.disable(k, "401 auth")
		} else {
			v.cooldown = 10 * time.Second
			pool.recordError(k)
		}
	case 403:
		if strings.Contains(string(body), "FreeTierError") {
			v.action = "failfast"
			v.status = http.StatusBadGateway
			v.hint = freeTierHint
		} else if strings.Contains(string(body), "AuthError") || strings.Contains(string(body), "auth") {
			v.hint = fmt.Sprintf("key %s rejected (403 auth) — disabled", k.Label)
			pool.disable(k, "403 auth")
		} else {
			v.cooldown = 10 * time.Second
			pool.recordError(k)
		}
	case 426:
		v.action = "failfast"
		v.status = http.StatusBadGateway
		v.hint = upgradeHint
	case 503:
		if strings.Contains(string(body), "Endpoint is unavailable") {
			v.action = "flipsurface"
		} else {
			v.cooldown = 10 * time.Second
			pool.recordError(k)
		}
	default:
		if resp.StatusCode >= 500 {
			v.cooldown = 10 * time.Second
			pool.recordError(k)
		} else {
			// 4xx model-level error (bad model, bad params) — same result either
			// way, so no retry: surface it to the client.
			v.action = "failfast"
			v.status = http.StatusBadGateway
			v.hint = fmt.Sprintf("upstream %d: %s", resp.StatusCode, firstLine(body))
		}
	}
	return v
}

// writeErr emits a JSON error to the client.
func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg}})
}

// firstLine condenses an upstream error body to a single short line.
func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		s = "(no body)"
	}
	return s
}

// chatRequestShape is the minimal view of the client body we need for routing.
func parseClientBody(r *http.Request) (map[string]any, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 20<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return body, nil
}

func bodyStreamFlag(body map[string]any) bool {
	b, _ := body["stream"].(bool)
	return b
}

// handleChat is POST /v1/chat/completions — registry routing + rotation loop.
func (g *gateway) handleChat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	body, err := parseClientBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	model, _ := body["model"].(string)
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeErr(w, http.StatusBadRequest, `Model must be "<provider>/<id>" (e.g. zen/mimo-...)`)
		return
	}
	ref, ok := g.provider(parts[0])
	if !ok {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Unknown provider %q — see GET /v1/models", parts[0]))
		return
	}
	provider, upstreamModel := parts[0], parts[1]
	if ref.builtin {
		upstreamModel = stripModelSuffix(upstreamModel) // cosmetic ctx/modality labels
	} else {
		upstreamModel = stripClientSuffix(upstreamModel) // Claude Code "[1m]" only
	}
	body["model"] = upstreamModel
	clientWantsStream := bodyStreamFlag(body)

	log.Printf("req provider=%s model=%s stream=%v", provider, upstreamModel, clientWantsStream)

	var out string
	if ref.typ == "opencode" {
		out = g.proxyZen(ref, w, r, body, upstreamModel, clientWantsStream, start)
	} else {
		out = g.proxyOpenAI(ref, w, r, body, clientWantsStream, start)
	}
	if out != "" {
		g.recordActivity(r, ref, upstreamModel, "", start, http.StatusBadGateway, out, tokenUsage{})
		log.Printf("res provider=%s model=%s status=failed %s ttft_ms=%d", provider, upstreamModel, out, time.Since(start).Milliseconds())
		writeErr(w, http.StatusBadGateway, out)
	}
}

// recordActivity files one completed request into the dashboard ring and the
// persisted usage log (proxies record their own successes with the serving
// upstream key; callers record failures with the client-facing reason).
func (g *gateway) recordActivity(r *http.Request, ref providerRef, model, upstreamHash string, start time.Time, status int, failReason string, tokens tokenUsage) {
	e := activityEntry{
		TS:         time.Now().Unix(),
		Provider:   ref.name,
		Model:      model,
		KeyHash:    upstreamHash,
		Status:     status,
		DurationMs: time.Since(start).Milliseconds(),
		FailReason: failReason,
	}
	if ck, ok := contextClientKey(r); ok {
		e.KeyAlias = ck.Alias
		e.User = ck.UserEmail
		if g.store != nil {
			g.usageBuf.add(usageEvent{
				TS:           e.TS,
				UserID:       ck.UserID,
				ClientKeyID:  ck.ID,
				Provider:     ref.name,
				Model:        model,
				InputTokens:  tokens.in,
				OutputTokens: tokens.out,
				Status:       status,
				DurationMs:   e.DurationMs,
			})
		}
	}
	g.usage.addActivity(e)
}
