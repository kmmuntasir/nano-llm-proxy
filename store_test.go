package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// testCfg matches Bootstrap's expectations: zen/kilo base URLs to import.
func testCfg() *Config {
	return &Config{
		Port: 0, Bind: "127.0.0.1",
		Zen:  ZenProviderConfig{BaseURL: "https://zen.example/v1"},
		Kilo: KiloProviderConfig{BaseURL: "https://kilo.example/v1"},
	}
}

func testKeyFile() *keyFile {
	return &keyFile{
		Zen:  []keyFileEntry{{Label: "key-a", Key: "sk-zen-1"}, {Label: "key-b", Key: "sk-zen-2"}},
		Kilo: []keyFileEntry{{Label: "a", Key: "sk-kilo-1"}},
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

	// builtin providers, keys imported in order (priority!)
	provs, keyLists, err := st.ListProvidersWithKeys()
	if err != nil {
		t.Fatalf("ListProvidersWithKeys: %v", err)
	}
	if len(provs) != 2 {
		t.Fatalf("got %d providers, want 2", len(provs))
	}
	if provs[0].Name != "zen" || provs[0].Type != "opencode" || !provs[0].Builtin || provs[0].SortOrder != 0 {
		t.Fatalf("zen row wrong: %+v", provs[0])
	}
	if provs[1].Name != "kilo" || provs[1].Type != "openai" || !provs[1].Builtin || provs[1].SortOrder != 1 {
		t.Fatalf("kilo row wrong: %+v", provs[1])
	}
	if len(keyLists[0]) != 2 || keyLists[0][0].Key != "sk-zen-1" || keyLists[0][1].Key != "sk-zen-2" {
		t.Fatalf("zen keys not imported in order: %+v", keyLists[0])
	}
	if len(keyLists[1]) != 1 || keyLists[1][0].Key != "sk-kilo-1" {
		t.Fatalf("kilo keys wrong: %+v", keyLists[1])
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

	// missing keys.json on an empty DB also fails cleanly
	st2 := openTestStore(t)
	err = st2.Bootstrap(testCfg(), nil, "admin@example.com", "super-secret-pass")
	if !errors.Is(err, ErrLegacyKeysMissing) {
		t.Fatalf("want ErrLegacyKeysMissing, got %v", err)
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

// passThrough200 is the smallest possible next handler for middleware tests.
func passThrough200(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }

func TestClientKeyLifecycleHotPath(t *testing.T) {
	st := openTestStore(t)
	if err := st.Bootstrap(testCfg(), testKeyFile(), "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	admin, _ := st.UserByEmail("admin@example.com")
	cfg := testCfg()
	g, err := newGatewayFromStore(cfg, testRuntime(), st)
	if err != nil {
		t.Fatalf("newGatewayFromStore: %v", err)
	}

	plaintext, _, err := st.CreateClientKey(admin.ID, "test")
	if err != nil {
		t.Fatalf("CreateClientKey: %v", err)
	}

	// unknown key -> 401
	rec := httptest.NewRecorder()
	g.clientOnly(passThrough200)(rec, anonReq())
	if rec.Code != 401 {
		t.Fatalf("unknown key: got %d want 401", rec.Code)
	}

	// fresh key accepted only after a cache reload — direct store writes do not
	// touch the hot path; GUI mutations go through applyMutation which does.
	// This pins the documented hot-path contract.
	if err := g.ck.reload(st); err != nil {
		t.Fatalf("reload: %v", err)
	}
	rec = httptest.NewRecorder()
	g.clientOnly(passThrough200)(rec, bearerReq(plaintext))
	if rec.Code != 200 {
		t.Fatalf("fresh key: got %d want 200 (%s)", rec.Code, rec.Body.String())
	}

	// disabled key -> 401 after reload
	keyID := ckID(st, plaintext)
	if err := st.SetClientKeyDisabled(keyID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := g.ck.reload(st); err != nil {
		t.Fatalf("reload: %v", err)
	}
	rec = httptest.NewRecorder()
	g.clientOnly(passThrough200)(rec, bearerReq(plaintext))
	if rec.Code != 401 {
		t.Fatalf("disabled key: got %d want 401", rec.Code)
	}

	// re-enabled -> 200, deleted -> 401
	if err := st.SetClientKeyDisabled(keyID, false); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if err := g.ck.reload(st); err != nil {
		t.Fatalf("reload: %v", err)
	}
	rec = httptest.NewRecorder()
	g.clientOnly(passThrough200)(rec, bearerReq(plaintext))
	if rec.Code != 200 {
		t.Fatalf("re-enabled key: got %d want 200", rec.Code)
	}
	if err := st.DeleteClientKey(keyID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := g.ck.reload(st); err != nil {
		t.Fatalf("reload: %v", err)
	}
	rec = httptest.NewRecorder()
	g.clientOnly(passThrough200)(rec, bearerReq(plaintext))
	if rec.Code != 401 {
		t.Fatalf("deleted key: got %d want 401", rec.Code)
	}
}

func ckID(st *Store, plaintext string) int64 {
	keys, _ := st.AllClientKeys()
	for _, k := range keys {
		if k.KeyHash == hashSecret(plaintext) {
			return k.ID
		}
	}
	return 0
}

func anonReq() *http.Request {
	return httptest.NewRequest("GET", "/v1/models", nil)
}

func bearerReq(key string) *http.Request {
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+key)
	return r
}

func TestUsageFlushAndActivityRing(t *testing.T) {
	st := openTestStore(t)
	if err := st.Bootstrap(testCfg(), testKeyFile(), "admin@example.com", "super-secret-pass"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	admin, _ := st.UserByEmail("admin@example.com")
	_, ck, err := st.CreateClientKey(admin.ID, "flush-test")
	if err != nil {
		t.Fatalf("CreateClientKey: %v", err)
	}
	ut := newUsageTracker()
	for i := 0; i < 3; i++ {
		ut.record(ck.ID)
	}
	ut.flush(st)
	keys, _ := st.ListClientKeys(admin.ID)
	var fk *ClientKey
	for i := range keys {
		if keys[i].Alias == "flush-test" {
			fk = &keys[i]
		}
	}
	if fk == nil || fk.RequestCount != 3 {
		t.Fatalf("flush did not persist counts: %+v", keys)
	}
	if fk.LastUsedAt == 0 {
		t.Fatal("last_used_at not persisted")
	}

	// ring: 105 entries -> newest first, capped at 100
	for i := 0; i < 105; i++ {
		ut.addActivity(activityEntry{TS: int64(1000 + i), KeyAlias: fmt.Sprintf("k%d", i)})
	}
	recent := ut.recent(activityRingSize)
	if len(recent) != 100 {
		t.Fatalf("ring len = %d, want 100", len(recent))
	}
	if recent[0].KeyAlias != "k104" || recent[99].KeyAlias != "k5" {
		t.Fatalf("ring order wrong: first=%s last=%s", recent[0].KeyAlias, recent[99].KeyAlias)
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
