package localmode

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Register/Unregister must never start, stop or reload anything: the GUI
// toggle calls them from inside the running gateway. Each test checks the
// exact commands, that nothing was started detached, and that Status
// reports installed=false, running=true after Unregister of a running
// service.

func assertStatus(t *testing.T, name string, wantInst, wantRun bool) {
	t.Helper()
	inst, run, err := Status(name)
	if err != nil || inst != wantInst || run != wantRun {
		t.Fatalf("Status(%s) = installed %v running %v err %v; want %v %v", name, inst, run, err, wantInst, wantRun)
	}
}

func TestRegisterUnregisterSystemd(t *testing.T) {
	f, s, unitPath, _ := linuxGateway(t)
	active := "systemctl --user is-active nano-llm-proxy.service"

	if err := Register(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t,
		"systemctl --user show-environment",
		"systemctl --version",
		"systemctl --user daemon-reload",
		"systemctl --user enable nano-llm-proxy.service", // no --now
	)
	assertFile(t, unitPath, systemdUnit(s, "nano-llm-proxy LLM gateway", false), 0o644)

	f.out[active] = "active\n" // started earlier by the installer
	assertStatus(t, "gateway", true, true)
	f.assertCalls(t, active)

	if err := Unregister("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t,
		"systemctl --user disable nano-llm-proxy.service", // no --now
		"systemctl --user daemon-reload",
	)
	assertMissing(t, unitPath)
	assertStatus(t, "gateway", false, true)
	f.assertCalls(t, active)

	if err := Unregister("gateway"); err != nil {
		t.Fatalf("second Unregister = %v", err)
	}
	f.assertCalls(t)

	// The installer's Disable still stops the instance Unregister left running.
	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, active, "systemctl --user stop nano-llm-proxy.service")
	if len(f.detached) != 0 {
		t.Fatalf("started detached: %q", f.detached)
	}
}

func TestRegisterUnregisterXDGFallback(t *testing.T) {
	f, s, unitPath, desktop := linuxGateway(t)
	f.fail["systemctl --user show-environment"] = true
	record := filepath.Join(f.home, ".local", "share", AppName, "autostart-gateway.cmdline")
	pattern := "^" + strings.ReplaceAll(s.Exec, ".", `\.`) + " local$"
	pgrep := "pgrep -u " + uid() + " -f " + pattern

	if err := Register(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "systemctl --user show-environment") // no pgrep, no start
	if len(f.detached) != 0 {
		t.Fatalf("Register started detached: %q", f.detached)
	}
	assertFile(t, desktop, desktopEntry(s, "nano-llm-proxy LLM gateway"), 0o644)
	assertFile(t, record, s.Exec+"\nlocal\n", 0o600)
	assertMissing(t, unitPath)

	assertStatus(t, "gateway", true, true) // pgrep matches (default success)
	f.assertCalls(t, pgrep)

	if err := Unregister("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t) // file removal only, no pkill
	assertMissing(t, desktop)
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("record must survive Unregister: %v", err)
	}
	assertStatus(t, "gateway", false, true)
	f.assertCalls(t, pgrep)

	if err := Unregister("gateway"); err != nil {
		t.Fatalf("second Unregister = %v", err)
	}
	f.assertCalls(t)

	// Disable finds the orphan through the record, kills it, removes the record.
	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "systemctl --user is-active nano-llm-proxy.service", "pkill -u "+uid()+" -f "+pattern)
	assertMissing(t, record)
}

func TestRegisterUnregisterLaunchd(t *testing.T) {
	f := newFakeOS(t, "darwin")
	s := gatewaySvc(f.home)
	plist := filepath.Join(f.home, "Library", "LaunchAgents", "dev.nano-llm-proxy.plist")
	target := "gui/" + uid() + "/dev.nano-llm-proxy"
	f.out["launchctl print "+target] = "state = running\n"

	if err := Register(s); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t) // no bootout, no bootstrap
	assertFile(t, plist, launchdPlist(s, "dev.nano-llm-proxy"), 0o644)
	assertStatus(t, "gateway", true, true)
	f.assertCalls(t, "launchctl print "+target)

	if err := Unregister("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t) // no bootout
	assertMissing(t, plist)
	assertStatus(t, "gateway", false, true)
	f.assertCalls(t, "launchctl print "+target)

	if err := Unregister("gateway"); err != nil {
		t.Fatalf("second Unregister = %v", err)
	}
	f.assertCalls(t)

	if err := Disable("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "launchctl bootout "+target)
}

func TestRegisterUnregisterWindows(t *testing.T) {
	f := newFakeOS(t, "windows")
	s := Service{Name: "gateway", Exec: filepath.Join(f.home, "nano-llm-proxy.exe"), Args: []string{"local"}}
	const key = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	regQuery := "reg query " + key + " /v NanoLLMProxy"
	tasklist := "tasklist /FI IMAGENAME eq nano-llm-proxyw.exe /NH"
	f.out[tasklist] = "nano-llm-proxyw.exe            4242 Console    1     20,000 K\r\n"

	if err := Register(s); err != nil {
		t.Fatal(err)
	}
	// No sibling GUI build here, so the console Exec is registered.
	f.assertCalls(t, "reg add "+key+` /v NanoLLMProxy /t REG_SZ /d "`+s.Exec+`" local /f`)
	assertStatus(t, "gateway", true, true)
	f.assertCalls(t, regQuery, tasklist)

	if err := Unregister("gateway"); err != nil {
		t.Fatal(err)
	}
	f.assertCalls(t, "reg delete "+key+" /v NanoLLMProxy /f") // no taskkill
	f.fail[regQuery] = true                                   // the value is gone
	assertStatus(t, "gateway", false, true)

	f.fail["reg delete "+key+" /v NanoLLMProxy /f"] = true
	if err := Unregister("gateway"); err != nil {
		t.Fatalf("Unregister of a missing value = %v", err)
	}
	if len(f.detached) != 0 {
		t.Fatalf("started detached: %q", f.detached)
	}

	f.calls = nil
	if err := Register(Service{Name: "searxng", Exec: s.Exec}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Register searxng = %v, want ErrUnsupported", err)
	}
	if err := Unregister("searxng"); err != nil {
		t.Fatalf("Unregister searxng = %v, want nil", err)
	}
	f.assertCalls(t)
}

func TestRegisterValidationAndUnsupported(t *testing.T) {
	f := newFakeOS(t, "linux")
	bad := gatewaySvc(f.home)
	bad.Args = []string{"local\nExecStartPre=/bin/evil"}
	if err := Register(bad); err == nil {
		t.Fatal("Register with a newline: want error")
	}
	if err := Register(Service{Name: "redis", Exec: "/x"}); err == nil {
		t.Fatal("Register unknown service: want error")
	}
	if err := Unregister("redis"); err == nil {
		t.Fatal("Unregister unknown service: want error")
	}
	f.assertCalls(t)

	goos = "plan9"
	if err := Register(gatewaySvc(f.home)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Register on plan9 = %v, want ErrUnsupported", err)
	}
	if err := Unregister("gateway"); err != nil {
		t.Fatalf("Unregister on plan9 = %v, want nil", err)
	}
}
