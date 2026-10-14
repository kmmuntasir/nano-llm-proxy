package main

import (
	"path/filepath"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

func TestObscuraPathOwned(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "nano-llm-proxy")
	cases := []struct {
		current string
		want    bool
	}{
		{"obscura", true}, // the default
		{"", true},
		{filepath.Join(dataDir, "bin", "obscura"), true},              // an earlier installer copy
		{filepath.Join(dataDir, "bin", "..", "bin", "obscura"), true}, // cleaned first
		{filepath.Join(dataDir, "..", "elsewhere", "obscura"), false},
		{filepath.Join(filepath.Dir(dataDir), "nano-llm-proxy-other", "obscura"), false}, // a sibling prefix
		{dataDir, false},
		{"/usr/local/bin/obscura", false},
		{"bin/obscura", false}, // relative: someone typed it
		{"/opt/obscura/obscura", false},
	}
	for _, c := range cases {
		if got := obscuraPathOwned(c.current, dataDir); got != c.want {
			t.Errorf("obscuraPathOwned(%q) = %v, want %v", c.current, got, c.want)
		}
	}
}

func openLocalStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SaveRuntimeSettings(settings.DefaultLocalRuntimeSettings()); err != nil {
		t.Fatal(err)
	}
	return st
}

func setObscura(t *testing.T, st *store.Store, path string) {
	t.Helper()
	rs, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	rs.WebTools.ObscuraPath = path
	if err := st.SaveRuntimeSettings(rs); err != nil {
		t.Fatal(err)
	}
}

func obscuraOf(t *testing.T, st *store.Store) string {
	t.Helper()
	rs, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	return rs.WebTools.ObscuraPath
}

func TestStoreObscuraPath(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "nano-llm-proxy")
	managed := filepath.Join(dataDir, "bin", "obscura")
	mine := "/home/me/.cargo/bin/obscura"

	cases := []struct {
		name, current, path, want string
		note                      bool
	}{
		{"default is replaced", "obscura", mine, mine, false},
		{"installer copy is replaced", managed, mine, mine, false},
		{"default becomes the installer copy", "obscura", managed, managed, false},
		{"user path is kept", "/opt/tools/obscura", managed, "/opt/tools/obscura", true},
		{"same path is a no-op", mine, mine, mine, false},
		// settings.Validate rejects control characters: never store what a GUI save would refuse
		{"invalid path is not stored", "obscura", "/tmp/bad\npath/obscura", "obscura", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := openLocalStore(t)
			setObscura(t, st, c.current)
			note, err := storeObscuraPath(st, dataDir, c.path)
			if err != nil {
				t.Fatal(err)
			}
			if got := obscuraOf(t, st); got != c.want {
				t.Fatalf("obscuraPath = %q, want %q", got, c.want)
			}
			if (note != "") != c.note {
				t.Fatalf("note = %q, want note=%v", note, c.note)
			}
		})
	}
}

func TestStoreObscuraPathKeepsOtherSettings(t *testing.T) {
	st := openLocalStore(t)
	rs, _ := st.LoadRuntimeSettings()
	rs.WebTools.ObscuraStealth = true
	rs.WebTools.MaxChars = 12345
	if err := st.SaveRuntimeSettings(rs); err != nil {
		t.Fatal(err)
	}
	if _, err := storeObscuraPath(st, t.TempDir(), "/usr/bin/obscura"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.LoadRuntimeSettings()
	if got.WebTools.ObscuraPath != "/usr/bin/obscura" || !got.WebTools.ObscuraStealth ||
		got.WebTools.MaxChars != 12345 || !got.WebTools.Enabled {
		t.Fatalf("web tools = %+v", got.WebTools)
	}
}
