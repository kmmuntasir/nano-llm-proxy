package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
	"golang.org/x/crypto/bcrypt"
)

// testCfg matches Bootstrap's expectations: zen/kilo base URLs to import.
func testCfg() *config.Config {
	return &config.Config{
		Port: 0, Bind: "127.0.0.1",
		Zen:  config.ZenProviderConfig{BaseURL: "https://zen.example/v1"},
		Kilo: config.KiloProviderConfig{BaseURL: "https://kilo.example/v1"},
	}
}

func testKeyFile() *config.KeyFile {
	return &config.KeyFile{
		Zen:  []config.KeyFileEntry{{Label: "key-a", Key: "sk-zen-1"}, {Label: "key-b", Key: "sk-zen-2"}},
		Kilo: []config.KeyFileEntry{{Label: "a", Key: "sk-kilo-1"}},
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMigrateV1AndBootstrap(t *testing.T) {
	st := openTestStore(t)
	if err := st.Bootstrap(testCfg(), testKeyFile(), "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// superadmin seeded with a working bcrypt hash
	admin, err := st.UserByEmail("admin@example.com")
	if err != nil || admin == nil {
		t.Fatalf("superadmin missing: %v", err)
	}
	if admin.Role != "superadmin" {
		t.Fatalf("role = %s, want superadmin", admin.Role)
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte("super-secret-pass")) != nil {
		t.Fatalf("bcrypt hash does not verify")
	}

	// builtin provider seeded (zen only — kilo is a GUI preset now), keys
	// imported in order (priority!)
	provs, keyLists, err := st.ListProvidersWithKeys()
	if err != nil {
		t.Fatalf("ListProvidersWithKeys: %v", err)
	}
	if len(provs) != 1 {
		t.Fatalf("got %d providers, want 1", len(provs))
	}
	if provs[0].Name != "zen" || provs[0].Type != "opencode" || !provs[0].Builtin || provs[0].SortOrder != 0 {
		t.Fatalf("zen row wrong: %+v", provs[0])
	}
	if provs[0].Preset != "" {
		t.Fatalf("zen Preset = %q, want empty", provs[0].Preset)
	}
	if len(keyLists[0]) != 2 || keyLists[0][0].Key != "sk-zen-1" || keyLists[0][1].Key != "sk-zen-2" {
		t.Fatalf("zen keys not imported in order: %+v", keyLists[0])
	}

	// no client keys exist until someone creates one — bootstrap no longer
	// imports legacy config keys (clean cut)
	keys, err := st.ListClientKeys(admin.ID)
	if err != nil {
		t.Fatalf("ListClientKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("bootstrap created client keys: %+v", keys)
	}

	// second bootstrap run is a no-op
	if err := st.Bootstrap(testCfg(), testKeyFile(), "admin@example.com", "other-pass"); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	users, _ := st.ListUsers()
	if len(users) != 1 {
		t.Fatalf("second bootstrap created extra users: %d", len(users))
	}
	keys2, _ := st.ListClientKeys(admin.ID)
	if len(keys2) != 0 {
		t.Fatalf("second bootstrap created client keys: %d", len(keys2))
	}
}

// TestMigrateV4ConvertsBuiltinKiloToPreset replays the v3→v4 upgrade on a
// hand-built DB: the seeded builtin kilo row becomes a normal provider tagged
// preset='kilo', keeping its endpoint and keys; zen stays builtin.
func TestMigrateV4ConvertsBuiltinKiloToPreset(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	s := &Store{db: db}

	// build the v3-era schema and seed it like an old boot would (the
	// migrations ledger itself is created by Migrate before v1 runs)
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create migrations table: %v", err)
	}
	for _, mig := range []func() error{s.migrateV1, s.migrateV2, s.migrateV3} {
		if err := mig(); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO providers (name, type, base_url, enabled, builtin, sort_order, created_at)
		VALUES ('zen', 'opencode', 'https://zen.example/v1', 1, 1, 0, ?)`, now)
	if err != nil {
		t.Fatalf("seed zen: %v", err)
	}
	zenID, _ := res.LastInsertId()
	res, err = s.db.Exec(`INSERT INTO providers (name, type, base_url, enabled, builtin, sort_order, created_at)
		VALUES ('kilo', 'openai', 'https://kilo.example/v1', 1, 1, 1, ?)`, now)
	if err != nil {
		t.Fatalf("seed kilo: %v", err)
	}
	kiloID, _ := res.LastInsertId()
	if _, err := s.db.Exec(`INSERT INTO provider_keys (provider_id, key, label, sort_order, disabled, created_at)
		VALUES (?, 'sk-kilo-keep', 'old', 0, 0, ?)`, kiloID, now); err != nil {
		t.Fatalf("seed kilo key: %v", err)
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	kilo, err := s.Provider(kiloID)
	if err != nil || kilo == nil {
		t.Fatalf("kilo row missing after v4: %v", err)
	}
	if kilo.Builtin {
		t.Errorf("kilo still builtin: %+v", kilo)
	}
	if kilo.Preset != "kilo" {
		t.Errorf("kilo Preset = %q, want kilo", kilo.Preset)
	}
	if kilo.BaseURL != "https://kilo.example/v1" || !kilo.Enabled {
		t.Errorf("kilo endpoint/enabled not preserved: %+v", kilo)
	}
	keys, err := s.ListProviderKeys(kiloID)
	if err != nil || len(keys) != 1 || keys[0].Key != "sk-kilo-keep" {
		t.Fatalf("kilo keys not preserved: %v %+v", err, keys)
	}
	zen, err := s.Provider(zenID)
	if err != nil || zen == nil || !zen.Builtin || zen.Preset != "" {
		t.Fatalf("zen row changed by v4: %v %+v", err, zen)
	}

	var version int64
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 10 {
		t.Fatalf("schema version = %d, %v; want 10", version, err)
	}
}

func TestBootstrapFailsWithoutAdminEnv(t *testing.T) {
	st := openTestStore(t)
	err := st.Bootstrap(testCfg(), testKeyFile(), "", "")
	if !errors.Is(err, ErrAdminEnvMissing) {
		t.Fatalf("want ErrAdminEnvMissing, got %v", err)
	}
	// nothing partially written
	users, _ := st.ListUsers()
	if len(users) != 0 {
		t.Fatalf("bootstrap partially wrote users: %d", len(users))
	}

	// A missing keys.json is NOT a bootstrap failure. keys.json is a legacy
	// schema, it is gitignored, and it ships no example — so a fresh install
	// has none, and the operator is expected to add keys in the GUI. Seeding
	// the built-in zen provider keyless is what makes the GUI reachable.
	st2 := openTestStore(t)
	if err := st2.Bootstrap(testCfg(), nil, "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatalf("bootstrap without keys.json: %v", err)
	}
	providers, err := st2.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) != 1 || providers[0].Name != "zen" {
		t.Fatalf("want the built-in zen provider seeded, got %+v", providers)
	}
	keys, err := st2.ListProviderKeys(providers[0].ID)
	if err != nil {
		t.Fatalf("ListProviderKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("zen should be keyless on a fresh install, got %d keys", len(keys))
	}
}

func TestBootstrapIsIdempotentWithoutKeyFile(t *testing.T) {
	// A second boot with no keys.json must not re-seed or duplicate the
	// provider, and must not resurrect the removed ErrLegacyKeysMissing.
	st := openTestStore(t)
	cfg := testCfg()
	if err := st.Bootstrap(cfg, nil, "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if err := st.Bootstrap(cfg, nil, "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	providers, err := st.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("second boot duplicated providers: %d", len(providers))
	}
}

func TestStoreCRUDConstraints(t *testing.T) {
	st := openTestStore(t)
	if err := st.Bootstrap(testCfg(), testKeyFile(), "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// duplicate email rejected
	dup := &User{Name: "x", Email: "admin@example.com", PasswordHash: "h", Role: "user"}
	if err := st.InsertUser(dup); err == nil {
		t.Fatal("duplicate email accepted")
	}

	// user + keys + cascade
	u := &User{Name: "Pi", Email: "pi@example.com", PasswordHash: "h", Role: "user"}
	if err := st.InsertUser(u); err != nil {
		t.Fatalf("InsertUser: %v", err)
	}
	plain, ck, err := st.CreateClientKey(u.ID, "pi-main")
	if err != nil || plain == "" || ck.ID == 0 {
		t.Fatalf("CreateClientKey: %v", err)
	}
	if len(plain) != 35 || plain[:3] != "fg-" {
		t.Fatalf("plaintext key shape wrong: %q", plain)
	}

	// duplicate provider name rejected
	p := &Provider{Name: "zen", Type: "openai", BaseURL: "https://x/v1", Enabled: true}
	if err := st.CreateProvider(p, []ProviderKey{{Key: "k"}}); err == nil {
		t.Fatal("duplicate provider name accepted")
	}

	// generic provider CRUD + builtin delete guard
	gen := &Provider{Name: "together", Type: "openai", BaseURL: "https://api.together.xyz/v1", Enabled: true, SortOrder: 100}
	if err := st.CreateProvider(gen, []ProviderKey{{Key: "tk1"}, {Key: "tk2"}}); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	provs, _ := st.ListProviders()
	var genID, zenID int64
	for _, p := range provs {
		switch p.Name {
		case "together":
			genID = p.ID
		case "zen":
			zenID = p.ID
		}
	}
	if err := st.DeleteProvider(zenID); !errors.Is(err, ErrBuiltinProvider) {
		t.Fatalf("builtin delete: want ErrBuiltinProvider, got %v", err)
	}
	if err := st.DeleteProvider(genID); err != nil {
		t.Fatalf("generic delete: %v", err)
	}
	if p2, _ := st.Provider(genID); p2 != nil {
		t.Fatal("generic provider not deleted")
	}

	// deleting a user cascades keys and sessions
	if _, _, err := st.CreateSession(u.ID, time.Hour); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.DeleteUser(u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	keys, _ := st.ListClientKeys(u.ID)
	if len(keys) != 0 {
		t.Fatalf("keys not cascaded: %d", len(keys))
	}

	// NotFound paths
	if err := st.DeleteClientKey(99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestFileBackedStorePerms(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gw.db")
	st, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close()
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("db perms = %o, want 600", perm)
	}
}

// --- settings persistence (moved from the settings package tests) ---

func TestSettingsDefaultsOnAbsentRow(t *testing.T) {
	st := openTestStore(t)
	rs, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := settings.DefaultRuntimeSettings()
	if rs.Rotation != want.Rotation {
		t.Errorf("rotation = %q, want %q", rs.Rotation, want.Rotation)
	}
	if rs.Retry != want.Retry {
		t.Errorf("retry = %+v, want %+v", rs.Retry, want.Retry)
	}
	if !rs.Zen.InjectTools {
		t.Error("zen.injectTools should default true")
	}
}

func TestSettingsCorruptRow(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.db.Exec(`INSERT INTO settings (key, value) VALUES (?, '{not json')`, RuntimeSettingsKey); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := st.LoadRuntimeSettings(); err == nil {
		t.Fatal("expected an error for a corrupt settings row")
	}
}

func TestMigrateV6EnrichesExistingUsageRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	s := &Store{db: db}

	// a V5 database with one pre-V6 usage row
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create migrations table: %v", err)
	}
	for _, mig := range []func() error{s.migrateV1, s.migrateV2, s.migrateV3, s.migrateV4, s.migrateV5} {
		if err := mig(); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	const ts = int64(1790000000)
	if _, err := s.db.Exec(`INSERT INTO usage_events
		(ts, user_id, client_key_id, provider, model, input_tokens, output_tokens, status, duration_ms)
		VALUES (?, 42, 7, 'zen', 'old-model', 3, 4, 200, 120)`, ts); err != nil {
		t.Fatalf("seed pre-V6 row: %v", err)
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var version int64
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 10 {
		t.Fatalf("schema version = %d, %v; want 10", version, err)
	}
	for idx, want := range map[string]int{
		"idx_usage_model_ts":      1,
		"idx_usage_user_model_ts": 1,
		"idx_usage_provider_ts":   0, // provider filters ride idx_usage_ts
	} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&n); err != nil || n != want {
			t.Errorf("index %s: n=%d want %d (err=%v)", idx, n, want, err)
		}
	}

	// the old row survives with defaults and gets placeholder labels
	f := UsageFilter{From: ts - 10, To: ts + 10}
	rows, total, err := s.UsageRequests(f, "ts", true, 1, 10)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("requests after V6: total=%d rows=%d err=%v", total, len(rows), err)
	}
	old := rows[0]
	if old.Surface != "" || old.Stream || old.UpstreamStatus != 0 || old.Attempts != 0 || old.TtfbMs != 0 || old.FailReason != "" {
		t.Errorf("pre-V6 row did not get zero defaults: %+v", old)
	}
	if old.Email != "deleted user #42" || old.Alias != "deleted key #7" || old.InputTokens != 3 {
		t.Errorf("pre-V6 row labels/data wrong: %+v", old)
	}

	// new rows round-trip every V6 column
	if err := s.InsertUsageEvents([]UsageEvent{{
		TS: ts + 1, UserID: 42, ClientKeyID: 7, Provider: "zen", Model: "new-model",
		Status: 502, DurationMs: 900, Surface: "messages", Stream: true, UpstreamStatus: 429,
		FailReason: "all keys cooling", UpstreamKey: "key-b", TtfbMs: 0, Attempts: 3,
		UserEmail: "gone@example.com", KeyAlias: "laptop",
	}}); err != nil {
		t.Fatalf("insert V6 row: %v", err)
	}
	rows, _, err = s.UsageRequests(f, "ts", true, 1, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("requests: %d rows err=%v", len(rows), err)
	}
	n := rows[0]
	if n.Surface != "messages" || !n.Stream || n.UpstreamStatus != 429 || n.FailReason != "all keys cooling" ||
		n.UpstreamKey != "key-b" || n.Attempts != 3 || n.Status != 502 {
		t.Errorf("V6 columns did not round-trip: %+v", n)
	}
	// snapshots label rows whose user/key rows are gone
	if n.Email != "gone@example.com" || n.Alias != "laptop" {
		t.Errorf("snapshot labels not used: email=%q alias=%q", n.Email, n.Alias)
	}
}

func TestUsageBreakdownRejectsUnknownSQLFragments(t *testing.T) {
	st := openTestStore(t)
	f := UsageFilter{From: 0, To: 10}
	for _, q := range []UsageBreakdownQuery{
		{Filter: f, GroupBy: "model; DROP TABLE users", Sort: "requests", Page: 1, PageSize: 10},
		{Filter: f, GroupBy: "model", Sort: "requests DESC; --", Page: 1, PageSize: 10},
		{Filter: f, GroupBy: "model", Sort: "requests", Page: 0, PageSize: 10},
	} {
		if _, _, _, err := st.UsageBreakdown(q); !errors.Is(err, ErrUsageParam) {
			t.Errorf("UsageBreakdown(%+v) err = %v, want ErrUsageParam", q, err)
		}
	}
	if _, _, err := st.UsageRequests(f, "id; --", true, 1, 10); !errors.Is(err, ErrUsageParam) {
		t.Errorf("UsageRequests bad sort err = %v", err)
	}
	if _, err := st.UsageSeriesByGroup(f, "model", "bogus", 3600, 0, 5); !errors.Is(err, ErrUsageParam) {
		t.Errorf("UsageSeriesByGroup bad metric err = %v", err)
	}
}

// usageBuckets is the last line of defence against hostile ranges: it must
// refuse oversized spans and never loop on a wrapping cursor.
func TestUsageBucketsBounded(t *testing.T) {
	const maxI, minI = int64(^uint64(0) >> 1), -int64(^uint64(0)>>1) - 1
	for _, tc := range []struct {
		name     string
		from, to int64
	}{
		{"whole int64 span", 0, maxI},
		{"negative from", minI, 0},
		{"inverted", 10, 5},
		{"just over cap", 0, usageMaxBuckets * 3600},
	} {
		if _, err := usageBuckets(tc.from, tc.to, 3600, 0); !errors.Is(err, ErrUsageParam) {
			t.Errorf("%s: err = %v, want ErrUsageParam", tc.name, err)
		}
	}
	// near MaxInt64 the cursor would wrap; it must stop instead
	got, err := usageBuckets(maxI-10, maxI, 3600, 3600)
	if err != nil || len(got) == 0 || len(got) > 2 {
		t.Fatalf("near-MaxInt64 range: %d buckets, err %v", len(got), err)
	}
	got, err = usageBuckets(0, 2*3600, 3600, 0)
	if err != nil || len(got) != 3 || got[0] != 0 || got[2] != 7200 {
		t.Fatalf("ordinary range: %v, %v", got, err)
	}
}

// The overview's totals and p95 queries must read the ts index, not scan the
// table.
func TestUsageOverviewUsesTsIndex(t *testing.T) {
	s := openTestStore(t)
	where, args := UsageFilter{From: 1, To: 2}.where()
	for _, q := range []string{
		`SELECT COUNT(*), SUM(CASE WHEN e.status < 400 THEN 1 ELSE 0 END) FROM usage_events e WHERE ` + where,
		`SELECT e.duration_ms FROM usage_events e WHERE ` + where + ` AND e.status < 400 ORDER BY e.duration_ms LIMIT 1 OFFSET 0`,
	} {
		rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+q, args...)
		if err != nil {
			t.Fatalf("explain: %v", err)
		}
		var plan string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatalf("scan: %v", err)
			}
			plan += detail + "; "
		}
		rows.Close()
		if !strings.Contains(plan, "idx_usage_ts") {
			t.Errorf("plan does not use idx_usage_ts: %s", plan)
		}
	}
}

func TestMigrateV7AddsCachedTokens(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	s := &Store{db: db}

	// a V6 database (what prod runs) with one pre-V7 usage row
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create migrations table: %v", err)
	}
	for _, mig := range []func() error{s.migrateV1, s.migrateV2, s.migrateV3, s.migrateV4, s.migrateV5, s.migrateV6} {
		if err := mig(); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	const ts = int64(1790000000)
	if _, err := s.db.Exec(`INSERT INTO usage_events
		(ts, user_id, client_key_id, provider, model, input_tokens, output_tokens, status, duration_ms, surface)
		VALUES (?, 42, 7, 'zen', 'old-model', 100, 4, 200, 120, 'chat')`, ts); err != nil {
		t.Fatalf("seed pre-V7 row: %v", err)
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var version int64
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 10 {
		t.Fatalf("schema version = %d, %v; want 10", version, err)
	}
	// idempotent: a second Migrate on V7 is a no-op
	if err := s.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	if err := s.InsertUsageEvents([]UsageEvent{{
		TS: ts + 1, UserID: 42, ClientKeyID: 7, Provider: "zen", Model: "new-model",
		InputTokens: 1000, OutputTokens: 10, CachedTokens: 800, Status: 200, Surface: "messages",
	}}); err != nil {
		t.Fatalf("insert V7 row: %v", err)
	}
	f := UsageFilter{From: ts - 10, To: ts + 10}
	rows, total, err := s.UsageRequests(f, "cachedTokens", true, 1, 10)
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("requests after V7: total=%d rows=%d err=%v", total, len(rows), err)
	}
	if rows[0].Model != "new-model" || rows[0].CachedTokens != 800 || rows[0].InputTokens != 1000 {
		t.Errorf("V7 row did not round-trip / sort first: %+v", rows[0])
	}
	if rows[1].Model != "old-model" || rows[1].CachedTokens != 0 || rows[1].InputTokens != 100 {
		t.Errorf("pre-V7 row did not default cached_tokens to 0: %+v", rows[1])
	}
}

// TestMigrateV8GoogleLinked: existing users start unlinked; LinkGoogleUser
// sets the flag and takes the Google name, keeping the old one when Google
// sent none; the flag shows up in every read path.
func TestMigrateV8GoogleLinked(t *testing.T) {
	st := openTestStore(t)
	u := &User{Name: "Old Name", Email: "p@example.com", PasswordHash: "x", Role: "user"}
	if err := st.InsertUser(u); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.User(u.ID); got.GoogleLinked {
		t.Fatal("new user should start unlinked")
	}
	got, err := st.LinkGoogleUser(u.ID, "Google Name", "https://lh3.googleusercontent.com/a/x")
	if err != nil || !got.GoogleLinked || got.Name != "Google Name" || got.AvatarURL != "https://lh3.googleusercontent.com/a/x" {
		t.Fatalf("link: %+v %v", got, err)
	}
	if got, _ = st.LinkGoogleUser(u.ID, "", ""); got.Name != "Google Name" || got.AvatarURL != "" {
		t.Fatalf("relink without name/picture: %+v", got)
	}
	if byEmail, _ := st.UserByEmail("p@example.com"); !byEmail.GoogleLinked {
		t.Fatal("UserByEmail lost the flag")
	}
	list, _ := st.ListUsers()
	if len(list) != 1 || !list[0].GoogleLinked {
		t.Fatalf("ListUsers lost the flag: %+v", list)
	}
}

// TestMigrateV9RebuildsUsersSafely re-runs the V9 rebuild over populated
// data: every column survives, dependent rows (client keys, sessions) are
// not cascade-deleted, deleted ids are never reused, the admin role is
// accepted while unknown roles are still refused, and foreign keys are back
// on afterwards.
func TestMigrateV9RebuildsUsersSafely(t *testing.T) {
	st := openTestStore(t)
	a := &User{Name: "A", Email: "a@example.com", PasswordHash: "h", Role: "superadmin"}
	b := &User{Name: "B", Email: "b@example.com", PasswordHash: "h", Role: "user"}
	c := &User{Name: "C", Email: "c@example.com", PasswordHash: "h", Role: "user"}
	for _, u := range []*User{a, b, c} {
		if err := st.InsertUser(u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.LinkGoogleUser(b.ID, "B Google", "https://lh3.googleusercontent.com/a/b"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateClientKey(a.ID, "k"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateSession(a.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM users WHERE id=?`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM schema_migrations WHERE version=9`); err != nil {
		t.Fatal(err)
	}
	if err := st.migrateV9(); err != nil {
		t.Fatalf("migrateV9: %v", err)
	}

	gotB, _ := st.User(b.ID)
	if gotB == nil || gotB.Name != "B Google" || !gotB.GoogleLinked || gotB.AvatarURL == "" || gotB.Role != "user" {
		t.Fatalf("user b after rebuild = %+v", gotB)
	}
	var keys, sessions int
	st.db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id=?`, a.ID).Scan(&keys)
	st.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, a.ID).Scan(&sessions)
	if keys != 1 || sessions != 1 {
		t.Fatalf("dependent rows lost: keys=%d sessions=%d", keys, sessions)
	}
	d := &User{Name: "D", Email: "d@example.com", PasswordHash: "h", Role: "admin"}
	if err := st.InsertUser(d); err != nil {
		t.Fatalf("admin role refused: %v", err)
	}
	if d.ID <= c.ID {
		t.Fatalf("deleted id reused: new id %d, deleted id %d", d.ID, c.ID)
	}
	if err := st.InsertUser(&User{Name: "E", Email: "e@example.com", PasswordHash: "h", Role: "owner"}); err == nil {
		t.Fatal("unknown role accepted")
	}
	var fk int
	st.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	if fk != 1 {
		t.Fatal("foreign keys left off after the migration")
	}
	// cascade still works after the rebuild
	if _, err := st.db.Exec(`DELETE FROM users WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	st.db.QueryRow(`SELECT COUNT(*) FROM api_keys WHERE user_id=?`, a.ID).Scan(&keys)
	if keys != 0 {
		t.Fatal("ON DELETE CASCADE lost in the rebuild")
	}
}

// TestInsertUserWithDeniedProviders: revocations land with the user in one
// transaction, only for role "user", skipping ids of missing providers; a
// failed insert (duplicate email) leaves no revocation behind.
func TestInsertUserWithDeniedProviders(t *testing.T) {
	st := openTestStore(t)
	p := &Provider{Name: "acme", Type: "openai", BaseURL: "https://acme.example/v1", Enabled: true}
	if err := st.CreateProvider(p, nil); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	u := &User{Name: "U", Email: "u@example.com", PasswordHash: "x", Role: "user"}
	if err := st.InsertUserWithDeniedProviders(u, []int64{p.ID, p.ID + 1000}); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	got, err := st.DisabledProviderIDs(u.ID)
	if err != nil {
		t.Fatalf("disabled ids: %v", err)
	}
	if len(got) != 1 || !got[p.ID] {
		t.Fatalf("revocations = %v, want only %d", got, p.ID)
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_provider_access WHERE provider_id=?`, p.ID+1000).Scan(&n); err != nil || n != 0 {
		t.Fatalf("dangling row for a missing provider: n=%d err=%v", n, err)
	}

	admin := &User{Name: "A", Email: "a@example.com", PasswordHash: "x", Role: "admin"}
	if err := st.InsertUserWithDeniedProviders(admin, []int64{p.ID}); err != nil {
		t.Fatalf("insert admin: %v", err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_provider_access WHERE user_id=?`, admin.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("admin got %d revocation rows (err=%v), want 0", n, err)
	}

	dup := &User{Name: "D", Email: "u@example.com", PasswordHash: "x", Role: "user"}
	if err := st.InsertUserWithDeniedProviders(dup, []int64{p.ID}); err == nil {
		t.Fatal("duplicate email must fail")
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM user_provider_access`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("revocation rows after failed insert = %d (err=%v), want 1", n, err)
	}
}

// TestMigrateV10MovesBuiltinZenToInference: the built-in zen row still on
// the legacy default moves to the inference path; an operator-edited URL
// and other providers are left alone.
func TestMigrateV10MovesBuiltinZenToInference(t *testing.T) {
	st := openTestStore(t)
	if err := st.Bootstrap(testCfg(), &config.KeyFile{}, "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatal(err)
	}
	set := func(url string) {
		if _, err := st.db.Exec(`UPDATE providers SET base_url=? WHERE builtin=1 AND type='opencode'`, url); err != nil {
			t.Fatal(err)
		}
	}
	zenURL := func() string {
		var u string
		st.db.QueryRow(`SELECT base_url FROM providers WHERE builtin=1 AND type='opencode'`).Scan(&u)
		return u
	}
	rerun := func() {
		if _, err := st.db.Exec(`DELETE FROM schema_migrations WHERE version=10`); err != nil {
			t.Fatal(err)
		}
		if err := st.migrateV10(); err != nil {
			t.Fatal(err)
		}
	}
	set(ZenBaseURLLegacy)
	rerun()
	if got := zenURL(); got != ZenBaseURL {
		t.Fatalf("legacy default not moved: %s", got)
	}
	set("https://my-zen-mirror.example/v1")
	rerun()
	if got := zenURL(); got != "https://my-zen-mirror.example/v1" {
		t.Fatalf("operator-edited URL changed: %s", got)
	}
}
