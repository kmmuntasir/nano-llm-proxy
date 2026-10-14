package localmode

import (
	"fmt"
	"os"
	"strings"
)

// Windows autostart is a value under the per-user Run key: no admin rights,
// no service install. Nothing supervises the process, so Restart is
// unsupported and "running" is a process-table lookup.
const (
	winRunKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	// winGUIExe is the GUI-subsystem build (-H windowsgui): started at login
	// it shows no console window. Disable and Status look it up by image
	// name; the console build is never killed that way because the CLI
	// running `local uninstall` is that very image.
	winGUIExe = "nano-llm-proxyw.exe"
)

func windowsEnable(s Service, n names) error {
	if err := windowsRegister(s, n); err != nil {
		return err
	}
	exe := windowsLauncher(s.Exec)
	// Re-enabling must not start a second copy.
	if windowsRunning() {
		return nil
	}
	if err := startDetached(exe, s.Args, envList(s.Env), s.LogFile); err != nil {
		return fmt.Errorf("start %s: %w", n.winRunValue, err)
	}
	return nil
}

// windowsDisable deletes the Run value and stops the GUI build. Both fail
// harmlessly when there is nothing to delete or kill. SearXNG never exists
// on Windows, so disabling it is a no-op.
func windowsDisable(n names) error {
	if n.winRunValue == "" {
		return nil
	}
	_, _ = runCmd("reg", "delete", winRunKey, "/v", n.winRunValue, "/f")
	_, _ = runCmd("taskkill", "/IM", winGUIExe, "/F")
	return nil
}

// windowsRegister only writes the Run value.
func windowsRegister(s Service, n names) error {
	if n.winRunValue == "" {
		return fmt.Errorf("%s service on windows: %w", s.Name, ErrUnsupported)
	}
	_, err := run("reg", "add", winRunKey, "/v", n.winRunValue, "/t", "REG_SZ",
		"/d", windowsRunCommand(windowsLauncher(s.Exec), s.Args), "/f")
	return err
}

// windowsUnregister only deletes the Run value; the running process stays.
// reg delete fails when the value is already gone, which is success here.
func windowsUnregister(n names) error {
	if n.winRunValue != "" {
		_, _ = runCmd("reg", "delete", winRunKey, "/v", n.winRunValue, "/f")
	}
	return nil
}

func windowsStatus(name string, n names) (installed, running bool, err error) {
	if n.winRunValue == "" {
		return false, false, fmt.Errorf("%s service on windows: %w", name, ErrUnsupported)
	}
	// reg query fails when the value does not exist.
	_, qerr := runCmd("reg", "query", winRunKey, "/v", n.winRunValue)
	return qerr == nil, windowsRunning(), nil
}

// windowsRunning reports whether the GUI build is running. tasklist exits 0
// with an "INFO: No tasks" line when nothing matches, so the output decides.
func windowsRunning() bool {
	out, err := runCmd("tasklist", "/FI", "IMAGENAME eq "+winGUIExe, "/NH")
	return err == nil && strings.Contains(strings.ToLower(string(out)), winGUIExe)
}

// windowsLauncher prefers the sibling nano-llm-proxyw.exe next to exe and
// falls back to exe itself. Both separators are accepted so the same logic
// is testable off Windows.
func windowsLauncher(exe string) string {
	dir := exe[:strings.LastIndexAny(exe, `\/`)+1]
	cand := dir + winGUIExe
	if strings.EqualFold(cand, exe) {
		return exe
	}
	if st, err := os.Stat(cand); err == nil && st.Mode().IsRegular() {
		return cand
	}
	return exe
}

// windowsRunCommand renders the Run value: the program always quoted (it
// usually lives under a path with spaces), then each argument quoted only
// when CommandLineToArgvW would otherwise split or mangle it.
func windowsRunCommand(exe string, args []string) string {
	parts := []string{`"` + exe + `"`}
	for _, a := range args {
		parts = append(parts, windowsQuoteArg(a))
	}
	return strings.Join(parts, " ")
}

// windowsQuoteArg follows the CommandLineToArgvW rules (as syscall.EscapeArg
// does on Windows): backslashes are literal except before a quote, where
// they must be doubled.
func windowsQuoteArg(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\"") {
		return a
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(a); i++ {
		switch a[i] {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
		default:
			slashes = 0
		}
		b.WriteByte(a[i])
	}
	b.WriteString(strings.Repeat(`\`, slashes))
	b.WriteByte('"')
	return b.String()
}
