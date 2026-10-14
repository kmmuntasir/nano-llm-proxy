package localmode

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchdPlistGenerator(t *testing.T) {
	s := Service{
		Name:    "gateway",
		Exec:    "/Users/a&b/bin/nano-llm-proxy",
		Args:    []string{"local", "<x>"},
		Env:     map[string]string{"B": `"q"`, "A": "1"},
		WorkDir: "/Users/a&b/Library/Application Support/nano-llm-proxy",
		LogFile: "/Users/a&b/log's.log",
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.nano-llm-proxy</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Users/a&amp;b/bin/nano-llm-proxy</string>
		<string>local</string>
		<string>&lt;x&gt;</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>A</key>
		<string>1</string>
		<key>B</key>
		<string>&#34;q&#34;</string>
	</dict>
	<key>WorkingDirectory</key>
	<string>/Users/a&amp;b/Library/Application Support/nano-llm-proxy</string>
	<key>StandardOutPath</key>
	<string>/Users/a&amp;b/log&#39;s.log</string>
	<key>StandardErrorPath</key>
	<string>/Users/a&amp;b/log&#39;s.log</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`
	got := launchdPlist(s, "dev.nano-llm-proxy")
	if got != want {
		t.Fatalf("plist:\n%s\nwant:\n%s", got, want)
	}
	// Well-formed, and the escaped values decode back to the originals.
	dec := xml.NewDecoder(strings.NewReader(got))
	var texts []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("plist is not well-formed XML: %v", err)
		}
		if cd, ok := tok.(xml.CharData); ok && strings.TrimSpace(string(cd)) != "" {
			texts = append(texts, string(cd))
		}
	}
	for _, v := range []string{s.Exec, "<x>", `"q"`, s.LogFile} {
		found := false
		for _, txt := range texts {
			found = found || txt == v
		}
		if !found {
			t.Errorf("value %q did not survive XML round trip", v)
		}
	}
}

func TestDarwinEnableStatusRestartDisable(t *testing.T) {
	f := newFakeOS(t, "darwin")
	s := gatewaySvc(f.home)
	plist := filepath.Join(f.home, "Library", "LaunchAgents", "dev.nano-llm-proxy.plist")
	domain := "gui/" + uid()
	target := domain + "/dev.nano-llm-proxy"
	f.fail["launchctl bootout "+domain+" "+plist] = true // not loaded yet

	if inst, run, err := Status("gateway"); inst || run || err != nil {
		t.Fatalf("Status before install = %v %v %v", inst, run, err)
	}
	f.assertCalls(t, "launchctl print "+target) // a job may outlive its plist

	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t,
		"launchctl bootout "+domain+" "+plist,
		"launchctl bootstrap "+domain+" "+plist,
	)
	assertFile(t, plist, launchdPlist(s, "dev.nano-llm-proxy"), 0o644)

	f.out["launchctl print "+target] = "gui/501/dev.nano-llm-proxy = {\n\tstate = running\n\tpid = 42\n}"
	if inst, run, err := Status("gateway"); !inst || !run || err != nil {
		t.Fatalf("Status running = %v %v %v", inst, run, err)
	}
	f.out["launchctl print "+target] = "gui/501/dev.nano-llm-proxy = {\n\tstate = not running\n}"
	if inst, run, err := Status("gateway"); !inst || run || err != nil {
		t.Fatalf("Status not running = %v %v %v", inst, run, err)
	}
	f.assertCalls(t, "launchctl print "+target, "launchctl print "+target)

	if err := Restart("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "launchctl kickstart -k "+target)

	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "launchctl bootout "+target)
	assertMissing(t, plist)

	// Idempotent: not loaded + no plist is still success.
	f.fail["launchctl bootout "+target] = true
	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	if len(f.detached) != 0 {
		t.Fatalf("launchd path must not start detached: %q", f.detached)
	}
}

func TestDarwinBootstrapRetriesThenFails(t *testing.T) {
	f := newFakeOS(t, "darwin")
	plist := filepath.Join(f.home, "Library", "LaunchAgents", "dev.nano-llm-proxy.searxng.plist")
	boot := "launchctl bootstrap gui/" + uid() + " " + plist
	f.fail[boot] = true
	f.out[boot] = "Bootstrap failed: 5: Input/output error"
	err := Enable(Service{Name: "searxng", Exec: "/v/bin/python"})
	if err == nil || !strings.Contains(err.Error(), "Input/output error") {
		t.Fatalf("Enable = %v", err)
	}
	f.assertCalls(t, "launchctl bootout gui/"+uid()+" "+plist, boot, boot, boot)
}

func TestWindowsRunCommand(t *testing.T) {
	cases := []struct {
		exe  string
		args []string
		want string
	}{
		{`C:\Users\u\AppData\Local\Programs\nano-llm-proxy\nano-llm-proxyw.exe`, []string{"local"},
			`"C:\Users\u\AppData\Local\Programs\nano-llm-proxy\nano-llm-proxyw.exe" local`},
		{`C:\Program Files\n\nano-llm-proxy.exe`, []string{"local", "a b", `q"x`, `tr\`, `sp ace\`, ""},
			`"C:\Program Files\n\nano-llm-proxy.exe" local "a b" "q\"x" tr\ "sp ace\\" ""`},
	}
	for _, tc := range cases {
		if got := windowsRunCommand(tc.exe, tc.args); got != tc.want {
			t.Errorf("windowsRunCommand:\n got %s\nwant %s", got, tc.want)
		}
	}
}

func TestWindowsLauncherPrefersGUISibling(t *testing.T) {
	dir := t.TempDir()
	console := filepath.Join(dir, "nano-llm-proxy.exe")
	gui := filepath.Join(dir, "nano-llm-proxyw.exe")
	if got := windowsLauncher(console); got != console {
		t.Fatalf("no sibling: got %s", got)
	}
	if err := os.WriteFile(gui, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := windowsLauncher(console); got != gui {
		t.Fatalf("with sibling: got %s, want %s", got, gui)
	}
	if got := windowsLauncher(gui); got != gui {
		t.Fatalf("already GUI build: got %s", got)
	}
	// Backslash paths split too (the real Windows form).
	if got := windowsLauncher(`C:\nowhere\nano-llm-proxy.exe`); got != `C:\nowhere\nano-llm-proxy.exe` {
		t.Fatalf("missing sibling fallback: got %s", got)
	}
}

func TestWindowsEnableStatusDisable(t *testing.T) {
	f := newFakeOS(t, "windows")
	dir := filepath.Join(f.home, "Programs", "nano-llm-proxy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gui := filepath.Join(dir, "nano-llm-proxyw.exe")
	if err := os.WriteFile(gui, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	s := Service{Name: "gateway", Exec: filepath.Join(dir, "nano-llm-proxy.exe"), Args: []string{"local"},
		LogFile: filepath.Join(f.home, "log.txt")}
	const key = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	regAdd := "reg add " + key + ` /v NanoLLMProxy /t REG_SZ /d "` + gui + `" local /f`
	tasklist := "tasklist /FI IMAGENAME eq nano-llm-proxyw.exe /NH"
	f.out[tasklist] = "INFO: No tasks are running which match the specified criteria.\r\n"

	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, regAdd, tasklist)
	want := []string{gui, "local", "env=", "log=" + s.LogFile}
	if len(f.detached) != 1 || strings.Join(f.detached[0], "|") != strings.Join(want, "|") {
		t.Fatalf("detached = %q, want %q", f.detached, want)
	}

	// Running already: no second copy.
	f.out[tasklist] = "nano-llm-proxyw.exe            4242 Console    1     20,000 K\r\n"
	if err := Enable(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, regAdd, tasklist)
	if len(f.detached) != 1 {
		t.Fatalf("second Enable started another copy: %q", f.detached)
	}

	regQuery := "reg query " + key + " /v NanoLLMProxy"
	if inst, run, err := Status("gateway"); !inst || !run || err != nil {
		t.Fatalf("Status = %v %v %v", inst, run, err)
	}
	f.assertCalls(t, regQuery, tasklist)

	if err := Restart("gateway"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Restart = %v, want ErrUnsupported", err)
	}

	regDelete := "reg delete " + key + " /v NanoLLMProxy /f"
	taskkill := "taskkill /IM nano-llm-proxyw.exe /F"
	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, regDelete, taskkill)

	// Idempotent: both commands fail when nothing is there; still nil.
	f.fail[regDelete], f.fail[taskkill], f.fail[regQuery] = true, true, true
	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	f.out[tasklist] = "INFO: No tasks are running which match the specified criteria.\r\n"
	if inst, run, err := Status("gateway"); inst || run || err != nil {
		t.Fatalf("Status after Disable = %v %v %v", inst, run, err)
	}
}

func TestWindowsSearXNGUnsupported(t *testing.T) {
	f := newFakeOS(t, "windows")
	s := Service{Name: "searxng", Exec: filepath.Join(f.home, "python.exe")}
	if err := Enable(s); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Enable = %v", err)
	}
	if _, _, err := Status("searxng"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Status = %v", err)
	}
	if err := Restart("searxng"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Restart = %v", err)
	}
	if err := Disable("searxng"); err != nil {
		t.Fatalf("Disable = %v, want nil (nothing to remove)", err)
	}
	f.assertCalls(t)
}
