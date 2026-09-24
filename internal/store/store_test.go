package store

import (
	"errors"
	"os"
	"path/filepath"
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
