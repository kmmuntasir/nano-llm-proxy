package localmode

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSystemdUnitGenerator(t *testing.T) {
	s := Service{
		Name:    "searxng",
		Exec:    "/home/u/My Apps/venv/bin/python",
		Args:    []string{"-m", "searx.webapp", `a"b\c`, "100%", "$HOME", ""},
		Env:     map[string]string{"SEARXNG_SETTINGS_PATH": "/home/u/data dir/settings.yml", "Z": `q"\%`},
		WorkDir: "/home/u/data dir",
		LogFile: "/home/u/data dir/searxng.log",
	}
	want := `# Managed by nano-llm-proxy local mode; rewritten on install, removed on uninstall.
[Unit]
Description=nano-llm-proxy SearXNG web search

[Service]
Type=simple
ExecStart="/home/u/My Apps/venv/bin/python" -m searx.webapp "a\"b\\c" 100%% $$HOME ""
WorkingDirectory=/home/u/data dir
Environment="SEARXNG_SETTINGS_PATH=/home/u/data dir/settings.yml"
Environment="Z=q\"\\%%"
StandardOutput=append:/home/u/data dir/searxng.log
StandardError=append:/home/u/data dir/searxng.log
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
`
	if got := systemdUnit(s, "nano-llm-proxy SearXNG web search", true); got != want {
		t.Fatalf("unit:\n%s\nwant:\n%s", got, want)
	}
	old := systemdUnit(s, "x", false)
	if strings.Contains(old, "StandardOutput") || strings.Contains(old, "StandardError") {
		t.Fatalf("pre-240 unit must not use append:\n%s", old)
	}
}

func linuxGateway(t *testing.T) (*fakeOS, Service, string, string) {
	t.Helper()
	f := newFakeOS(t, "linux")
	cfg := filepath.Join(f.home, "cfg")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	return f, gatewaySvc(f.home),
		filepath.Join(cfg, "systemd", "user", "nano-llm-proxy.service"),
		filepath.Join(cfg, "autostart", "nano-llm-proxy.desktop")
}

func TestLinuxSystemdEnable(t *testing.T) {
	f, s, unitPath, desktop := linuxGateway(t)
	f.out["systemctl --version"] = "systemd 255 (255.4-1ubuntu8)\n+PAM +AUDIT\n"
	// A leftover fallback entry must be cleaned up when systemd takes over.
	if err := writeFileAtomic(desktop, []byte("[Desktop Entry]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t,
		"systemctl --user show-environment",
		"systemctl --version",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now nano-llm-proxy.service",
	)
	assertFile(t, unitPath, systemdUnit(s, "nano-llm-proxy LLM gateway", true), 0o644)
	assertMissing(t, desktop)
	if len(f.detached) != 0 {
		t.Fatalf("systemd path must not start detached: %q", f.detached)
	}
	unit, _ := os.ReadFile(unitPath)
	for _, line := range []string{
		`ExecStart="` + s.Exec + `" local`,
		"StandardOutput=append:" + s.LogFile,
		"Restart=always", "RestartSec=2", "WantedBy=default.target",
	} {
		if !strings.Contains(string(unit), line+"\n") {
			t.Errorf("unit missing %q", line)
		}
	}

	// Idempotent: a second Enable rewrites and re-runs the same sequence.
	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 4 {
		t.Fatalf("second Enable calls = %q", f.calls)
	}
}

func TestLinuxSystemdEnableOldSystemdOmitsAppendLog(t *testing.T) {
	f, s, unitPath, _ := linuxGateway(t)
	f.out["systemctl --version"] = "systemd 239 (239-78.el8)\n"
	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(unitPath)
	if strings.Contains(string(b), "append:") {
		t.Fatalf("systemd 239 unit uses append:\n%s", b)
	}
}

func TestLinuxSystemdEnableFailureIsReported(t *testing.T) {
	f, s, _, _ := linuxGateway(t)
	line := "systemctl --user enable --now nano-llm-proxy.service"
	f.fail[line] = true
	f.out[line] = "Failed to enable unit: Unit file is masked."
	err := Enable(s)
	if err == nil || !strings.Contains(err.Error(), "masked") {
		t.Fatalf("Enable error = %v, want the systemctl message", err)
	}
}

func TestLinuxSystemdStatusRestartDisable(t *testing.T) {
	f, s, unitPath, _ := linuxGateway(t)

	// Not installed: only the is-active probe (a unit can outlive its file),
	// nothing to restart, Disable is a no-op.
	active := "systemctl --user is-active nano-llm-proxy.service"
	if inst, run, err := Status("gateway"); inst || run || err != nil {
		t.Fatalf("Status before install = %v %v %v", inst, run, err)
	}
	if err := Restart("gateway"); err == nil || errors.Is(err, ErrUnsupported) {
		t.Fatalf("Restart before install = %v, want not-installed error", err)
	}
	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, active, active)

	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	f.calls = nil

	f.out[active] = "active\n"
	if inst, run, err := Status("gateway"); !inst || !run || err != nil {
		t.Fatalf("Status active = %v %v %v", inst, run, err)
	}
	f.out[active], f.fail[active] = "inactive\n", true
	if inst, run, err := Status("gateway"); !inst || run || err != nil {
		t.Fatalf("Status inactive = %v %v %v", inst, run, err)
	}
	f.assertCalls(t, active, active)

	if err := Restart("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "systemctl --user restart nano-llm-proxy.service")

	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t,
		"systemctl --user disable --now nano-llm-proxy.service",
		"systemctl --user daemon-reload",
	)
	assertMissing(t, unitPath)
	if err := Disable("gateway"); err != nil {
		t.Fatalf("second Disable = %v", err)
	}
	f.assertCalls(t, active) // inactive: nothing to stop
}

func TestLinuxSystemdSearXNGNames(t *testing.T) {
	f := newFakeOS(t, "linux")
	s := Service{Name: "searxng", Exec: "/venv/bin/python", Args: []string{"-m", "searx.webapp"},
		Env: map[string]string{"SEARXNG_SETTINGS_PATH": "/d/settings.yml"}}
	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t,
		"systemctl --user show-environment",
		"systemctl --version",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now nano-llm-proxy-searxng.service",
	)
	path := filepath.Join(f.home, ".config", "systemd", "user", "nano-llm-proxy-searxng.service")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `Environment="SEARXNG_SETTINGS_PATH=/d/settings.yml"`) {
		t.Fatalf("unit lacks env:\n%s", b)
	}
}

func TestLinuxSystemdDisableKeepsGoingWithoutUserManager(t *testing.T) {
	f, s, unitPath, _ := linuxGateway(t)
	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	f.fail["systemctl --user disable --now nano-llm-proxy.service"] = true
	f.fail["systemctl --user daemon-reload"] = true
	if err := Disable("gateway"); err == nil {
		t.Fatal("want the systemctl error reported")
	}
	assertMissing(t, unitPath) // but the unit is gone, so the next Disable is clean
	f.calls = nil
	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "systemctl --user is-active nano-llm-proxy.service")
}

func TestDesktopEntryGenerator(t *testing.T) {
	s := Service{
		Name:    "searxng",
		Exec:    "/home/u/My Apps/python",
		Args:    []string{"-m", "searx.webapp", "50%"},
		Env:     map[string]string{"SEARXNG_SETTINGS_PATH": "/home/u/a b/settings.yml"},
		WorkDir: `/home/u/a\b`,
	}
	want := `[Desktop Entry]
Type=Application
Name=nano-llm-proxy SearXNG web search
Comment=Started at login by nano-llm-proxy local mode
Exec=env "SEARXNG_SETTINGS_PATH=/home/u/a b/settings.yml" "/home/u/My Apps/python" -m searx.webapp 50%%
Path=/home/u/a\\b
Terminal=false
NoDisplay=true
X-GNOME-Autostart-enabled=true
`
	if got := desktopEntry(s, "nano-llm-proxy SearXNG web search"); got != want {
		t.Fatalf("desktop entry:\n%s\nwant:\n%s", got, want)
	}
}

func TestDesktopExecRoundTrip(t *testing.T) {
	cases := [][]string{
		{"/usr/bin/nano-llm-proxy", "local"},
		{"/home/u/My Apps/x", "a b", ""},
		{"/p", `back\slash`, `quo"te`, "do$llar", "back`tick", "100%", "%%", `\\`, "tab\there", "semi;colon"},
	}
	for _, argv := range cases {
		exec := desktopExec(Service{Exec: argv[0], Args: argv[1:]})
		got, err := desktopSplitExec(exec)
		if err != nil {
			t.Fatalf("split %q: %v", exec, err)
		}
		if !slices.Equal(got, argv) {
			t.Fatalf("round trip via %q:\n got %q\nwant %q", exec, got, argv)
		}
	}
	// A literal backslash inside a quoted argument is four in the file.
	if got := desktopExec(Service{Exec: `/a b\c`}); got != `"/a b\\\\c"` {
		t.Fatalf("desktopExec = %s", got)
	}
	if _, err := desktopSplitExec(`"unterminated`); err == nil {
		t.Fatal("unterminated quote: want error")
	}
}

func TestLinuxFallbackEnableStatusDisable(t *testing.T) {
	f, s, unitPath, desktop := linuxGateway(t)
	s.Env = map[string]string{"NANO_X": "1"}
	f.fail["systemctl --user show-environment"] = true
	pattern := "^" + strings.ReplaceAll(s.Exec, ".", `\.`) + " local$"
	pgrep := "pgrep -u " + uid() + " -f " + pattern
	f.fail[pgrep] = true // not running yet

	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "systemctl --user show-environment", pgrep)
	assertFile(t, desktop, desktopEntry(s, "nano-llm-proxy LLM gateway"), 0o644)
	assertMissing(t, unitPath)
	want := []string{s.Exec, "local", "env=NANO_X=1", "log=" + s.LogFile}
	if len(f.detached) != 1 || !slices.Equal(f.detached[0], want) {
		t.Fatalf("detached = %q, want %q", f.detached, want)
	}

	// Already running: Enable rewrites the entry but starts nothing.
	delete(f.fail, pgrep)
	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "systemctl --user show-environment", pgrep)
	if len(f.detached) != 1 {
		t.Fatalf("second Enable started another copy: %q", f.detached)
	}

	if inst, run, err := Status("gateway"); !inst || !run || err != nil {
		t.Fatalf("Status running = %v %v %v", inst, run, err)
	}
	f.fail[pgrep] = true
	if inst, run, err := Status("gateway"); !inst || run || err != nil {
		t.Fatalf("Status stopped = %v %v %v", inst, run, err)
	}
	f.assertCalls(t, pgrep, pgrep)

	if err := Restart("gateway"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Restart fallback = %v, want ErrUnsupported", err)
	}
	f.assertCalls(t)

	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	active := "systemctl --user is-active nano-llm-proxy.service"
	f.assertCalls(t, active, "pkill -u "+uid()+" -f "+pattern)
	assertMissing(t, desktop)
	assertMissing(t, filepath.Join(f.home, ".local", "share", AppName, "autostart-gateway.cmdline"))
	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, active) // no entry, no record: nothing to kill
	if inst, run, err := Status("gateway"); inst || run || err != nil {
		t.Fatalf("Status after Disable = %v %v %v", inst, run, err)
	}
}

func TestLinuxFallbackSearXNGNames(t *testing.T) {
	f := newFakeOS(t, "linux")
	f.fail["systemctl --user show-environment"] = true
	s := Service{Name: "searxng", Exec: "/v/bin/python", Args: []string{"-m", "searx.webapp"}}
	pgrep := "pgrep -u " + uid() + ` -f ^/v/bin/python -m searx\.webapp$`
	f.fail[pgrep] = true
	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "systemctl --user show-environment", pgrep)
	if _, err := os.Stat(filepath.Join(f.home, ".config", "autostart", "nano-llm-proxy-searxng.desktop")); err != nil {
		t.Fatal(err)
	}
}

func TestCmdlinePatternEscapes(t *testing.T) {
	got := cmdlinePattern([]string{"/a+b/(x)", "y.z"})
	if got != `^/a\+b/\(x\) y\.z$` {
		t.Fatalf("pattern = %s", got)
	}
}
