package localmode

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeOS replaces the seams: goos, runCmd (scripted by exact command line)
// and startDetached (recorded). Home and XDG/LOCALAPPDATA point at a temp dir.
type fakeOS struct {
	home     string
	calls    []string          // "name arg arg …" in call order
	fail     map[string]bool   // command lines that return an error
	out      map[string]string // command lines' output
	detached [][]string        // exe, args…, then "env=…", "log=…"
}

func newFakeOS(t *testing.T, osName string) *fakeOS {
	t.Helper()
	f := &fakeOS{home: t.TempDir(), fail: map[string]bool{}, out: map[string]string{}}
	t.Setenv("HOME", f.home)
	t.Setenv("USERPROFILE", f.home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(f.home, "AppData", "Local"))

	oldGOOS, oldRun, oldStart, oldDelay := goos, runCmd, startDetached, bootstrapRetryDelay
	t.Cleanup(func() { goos, runCmd, startDetached, bootstrapRetryDelay = oldGOOS, oldRun, oldStart, oldDelay })
	goos = osName
	bootstrapRetryDelay = 0
	runCmd = func(name string, args ...string) ([]byte, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		f.calls = append(f.calls, line)
		if f.fail[line] {
			return []byte(f.out[line]), errors.New("exit status 1")
		}
		return []byte(f.out[line]), nil
	}
	startDetached = func(exe string, args []string, env []string, logFile string) error {
		rec := append([]string{exe}, args...)
		rec = append(rec, "env="+strings.Join(env, ","), "log="+logFile)
		f.detached = append(f.detached, rec)
		return nil
	}
	return f
}

func (f *fakeOS) assertCalls(t *testing.T, want ...string) {
	t.Helper()
	if !slices.Equal(f.calls, want) {
		t.Fatalf("commands:\n got %q\nwant %q", f.calls, want)
	}
	f.calls = nil
}

func assertFile(t *testing.T, path, want string, perm os.FileMode) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(b) != want {
		t.Fatalf("%s:\n got:\n%s\nwant:\n%s", path, b, want)
	}
	if runtime.GOOS == "windows" {
		return
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != perm {
		t.Fatalf("%s mode = %o, want %o", path, st.Mode().Perm(), perm)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s should not exist (err=%v)", path, err)
	}
}

func gatewaySvc(home string) Service {
	return Service{
		Name:    "gateway",
		Exec:    filepath.Join(home, "my bin", "nano-llm-proxy"),
		Args:    []string{"local"},
		WorkDir: filepath.Join(home, "data"),
		LogFile: filepath.Join(home, "data", "nano-llm-proxy.log"),
	}
}

func uid() string { return uidString() }

func TestDataDirPerOS(t *testing.T) {
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want func(home string) string
	}{
		{"linux default", "linux", nil, func(h string) string { return filepath.Join(h, ".local", "share", AppName) }},
		{"linux XDG_DATA_HOME", "linux", map[string]string{"XDG_DATA_HOME": "XDG"}, func(h string) string { return filepath.Join(h, "xdg", AppName) }},
		{"linux relative XDG ignored", "linux", map[string]string{"XDG_DATA_HOME": "rel/dir"}, func(h string) string { return filepath.Join(h, ".local", "share", AppName) }},
		{"darwin", "darwin", nil, func(h string) string { return filepath.Join(h, "Library", "Application Support", AppName) }},
		{"windows", "windows", nil, func(h string) string { return filepath.Join(h, "AppData", "Local", AppName) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeOS(t, tc.goos)
			for k, v := range tc.env {
				if v == "XDG" {
					v = filepath.Join(f.home, "xdg")
				}
				t.Setenv(k, v)
			}
			got, err := DataDir()
			if err != nil {
				t.Fatal(err)
			}
			if want := tc.want(f.home); got != want {
				t.Fatalf("DataDir = %q, want %q", got, want)
			}
			st, err := os.Stat(got)
			if err != nil || !st.IsDir() {
				t.Fatalf("data dir not created: %v", err)
			}
			if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
				t.Fatalf("data dir mode %o is group/world accessible", st.Mode().Perm())
			}
			if len(f.calls) != 0 {
				t.Fatalf("DataDir ran commands: %q", f.calls)
			}
		})
	}
}

func TestDataDirMissingEnv(t *testing.T) {
	newFakeOS(t, "windows")
	t.Setenv("LOCALAPPDATA", "")
	if _, err := DataDir(); err == nil {
		t.Fatal("windows without LOCALAPPDATA: want error")
	}
	goos = "linux"
	t.Setenv("HOME", "")
	if _, err := DataDir(); err == nil {
		t.Fatal("linux without HOME or XDG_DATA_HOME: want error")
	}
}

func TestSupported(t *testing.T) {
	for osName, want := range map[string]bool{"linux": true, "darwin": true, "windows": true, "freebsd": false} {
		newFakeOS(t, osName)
		if got := Supported(); got != want {
			t.Errorf("Supported() on %s = %v, want %v", osName, got, want)
		}
	}
}

func TestEnableValidation(t *testing.T) {
	f := newFakeOS(t, "linux")
	good := gatewaySvc(f.home)
	cases := map[string]func(*Service){
		"unknown name":    func(s *Service) { s.Name = "redis" },
		"relative exec":   func(s *Service) { s.Exec = "bin/nano-llm-proxy" },
		"empty exec":      func(s *Service) { s.Exec = "" },
		"newline in arg":  func(s *Service) { s.Args = []string{"local\nExecStartPre=/bin/evil"} },
		"newline in env":  func(s *Service) { s.Env = map[string]string{"A": "b\nc"} },
		"bad env name":    func(s *Service) { s.Env = map[string]string{"A=B": "c"} },
		"CR in log file":  func(s *Service) { s.LogFile = "/tmp/x\r" },
		"NUL in work dir": func(s *Service) { s.WorkDir = "/tmp/\x00" },
	}
	for name, mut := range cases {
		s := good
		mut(&s)
		if err := Enable(s); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	f.assertCalls(t) // nothing ran
	for _, fn := range []func(string) error{Disable, Restart} {
		if err := fn("redis"); err == nil {
			t.Error("unknown service name: want error")
		}
	}
	if _, _, err := Status("redis"); err == nil {
		t.Error("Status unknown service: want error")
	}
}

func TestUnsupportedOS(t *testing.T) {
	f := newFakeOS(t, "plan9")
	if err := Enable(gatewaySvc(f.home)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Enable = %v, want ErrUnsupported", err)
	}
	if err := Restart("gateway"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Restart = %v, want ErrUnsupported", err)
	}
	if _, _, err := Status("gateway"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Status = %v, want ErrUnsupported", err)
	}
	if err := Disable("gateway"); err != nil {
		t.Fatalf("Disable = %v, want nil", err)
	}
}

func TestOpenBrowser(t *testing.T) {
	const u = "http://localhost:8787/"
	f := newFakeOS(t, "darwin")
	if err := OpenBrowser(u); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "open "+u)

	goos = "windows"
	if err := OpenBrowser(u); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "rundll32 url.dll,FileProtocolHandler "+u)

	goos = "linux"
	if err := OpenBrowser(u); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t)
	if want := []string{"xdg-open", u, "env=", "log="}; len(f.detached) != 1 || !slices.Equal(f.detached[0], want) {
		t.Fatalf("detached = %q, want %q", f.detached, want)
	}

	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "C:\\Windows\\calc.exe", "http://", "::"} {
		if err := OpenBrowser(bad); err == nil {
			t.Errorf("OpenBrowser(%q): want error", bad)
		}
	}
	f.assertCalls(t)
}

func TestWriteFileAtomicReplacesAndSetsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "unit.service")
	if err := writeFileAtomic(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, "two", 0o644)
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestDefaultStartDetachedWritesLogAndEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	log := filepath.Join(t.TempDir(), "out.log")
	if err := os.WriteFile(log, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := defaultStartDetached("/bin/sh", []string{"-c", `echo "v=$NANO_T"; echo err >&2`}, []string{"NANO_T=hi"}, log); err != nil {
		t.Fatal(err)
	}
	// The child is detached; poll its log (bounded) instead of waiting on it.
	want := "old\nv=hi\nerr\n"
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(log)
		if string(b) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log = %q, want %q", b, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSupervised(t *testing.T) {
	for osName, want := range map[string]bool{"linux": true, "darwin": true, "windows": false, "freebsd": false} {
		newFakeOS(t, osName)
		if got := Supervised(); got != want {
			t.Errorf("Supervised() on %s = %v, want %v", osName, got, want)
		}
	}
	// Linux without a systemd user instance falls back to desktop autostart,
	// which nothing supervises
	f := newFakeOS(t, "linux")
	f.fail["systemctl --user show-environment"] = true
	if Supervised() {
		t.Error("Supervised() without systemd = true")
	}
}
