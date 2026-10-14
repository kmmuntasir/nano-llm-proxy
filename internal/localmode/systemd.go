package localmode

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// appendLogMinSystemd is the first systemd release that understands
// StandardOutput=append:<path> (v240). Older ones (RHEL 8 ships 239) would
// warn and fall back to the journal, so the lines are omitted there.
const appendLogMinSystemd = 240

// linuxEnable writes the entry (see linuxWrite) and starts the service now.
func linuxEnable(s Service, n names) error {
	systemd, err := linuxWrite(s, n)
	if err != nil {
		return err
	}
	if !systemd {
		return desktopStart(s, n)
	}
	if _, err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	_, err = run("systemctl", "--user", "enable", "--now", n.systemdUnit)
	return err
}

// linuxRegister writes the entry and, for systemd, enables it for the next
// login without --now, so nothing running is touched.
func linuxRegister(s Service, n names) error {
	systemd, err := linuxWrite(s, n)
	if err != nil || !systemd {
		return err
	}
	if _, err := run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	_, err = run("systemctl", "--user", "enable", n.systemdUnit)
	return err
}

// linuxWrite writes the autostart entry: a systemd user unit (supervised,
// Restart=always) when a user instance is reachable, else an XDG autostart
// entry (e.g. WSL or a minimal distro without systemd). It reports which.
func linuxWrite(s Service, n names) (systemd bool, err error) {
	if !systemdUserAvailable() {
		return false, desktopWrite(s, n)
	}
	path, err := systemdUnitPath(n)
	if err != nil {
		return true, err
	}
	unit := systemdUnit(s, n.description, systemdVersion() >= appendLogMinSystemd)
	if err := writeFileAtomic(path, []byte(unit), 0o644); err != nil {
		return true, err
	}
	// A desktop entry left by an earlier fallback install would start a
	// second copy at the next graphical login.
	dp, err := desktopPath(n)
	if err != nil {
		return true, err
	}
	return true, removeIfExists(dp)
}

// linuxDisable stops and removes whichever mechanism is present. The unit
// file's existence (not systemd's reachability) decides, so a unit is still
// removed from a session where the user manager is gone; the systemctl error
// is then reported but the file is not left behind. With no unit file, a
// unit still active after Unregister is stopped.
func linuxDisable(n names) error {
	var errs []error
	path, err := systemdUnitPath(n)
	if err != nil {
		return err
	}
	ok, err := exists(path)
	if err != nil {
		return err
	}
	if ok {
		if _, err := run("systemctl", "--user", "disable", "--now", n.systemdUnit); err != nil {
			errs = append(errs, err)
		}
		if err := removeIfExists(path); err != nil {
			errs = append(errs, err)
		}
		if _, err := run("systemctl", "--user", "daemon-reload"); err != nil {
			errs = append(errs, err)
		}
	} else if systemdActive(n) {
		if _, err := run("systemctl", "--user", "stop", n.systemdUnit); err != nil {
			errs = append(errs, err)
		}
	}
	if err := desktopDisable(n); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// linuxUnregister removes the entry without stopping anything: systemd
// keeps a running unit loaded after its file is gone and daemon-reload.
func linuxUnregister(n names) error {
	var errs []error
	path, err := systemdUnitPath(n)
	if err != nil {
		return err
	}
	ok, err := exists(path)
	if err != nil {
		return err
	}
	if ok {
		if _, err := run("systemctl", "--user", "disable", n.systemdUnit); err != nil {
			errs = append(errs, err)
		}
		if err := removeIfExists(path); err != nil {
			errs = append(errs, err)
		}
		if _, err := run("systemctl", "--user", "daemon-reload"); err != nil {
			errs = append(errs, err)
		}
	}
	if err := desktopUnregister(n); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// linuxStatus: installed is the presence of a unit file or desktop entry;
// running is asked of systemd (a unit stays active after Unregister removed
// its file) and, failing that, of the process table via the XDG record.
func linuxStatus(n names) (installed, running bool, err error) {
	path, err := systemdUnitPath(n)
	if err != nil {
		return false, false, err
	}
	ok, err := exists(path)
	if err != nil {
		return false, false, err
	}
	if ok {
		return true, systemdActive(n), nil
	}
	installed, running, err = desktopStatus(n)
	if err != nil || installed || running {
		return installed, running, err
	}
	return false, systemdActive(n), nil
}

// systemdActive asks the user manager whether the unit is active.
// is-active exits non-zero for every state but "active" (and when there is
// no user manager); the printed state is the answer, so the error is not.
func systemdActive(n names) bool {
	out, _ := runCmd("systemctl", "--user", "is-active", n.systemdUnit)
	return strings.TrimSpace(string(out)) == "active"
}

func linuxRestart(n names) error {
	path, err := systemdUnitPath(n)
	if err != nil {
		return err
	}
	ok, err := exists(path)
	if err != nil {
		return err
	}
	if ok {
		_, err := run("systemctl", "--user", "restart", n.systemdUnit)
		return err
	}
	dp, err := desktopPath(n)
	if err != nil {
		return err
	}
	if ok, err := exists(dp); err != nil {
		return err
	} else if ok {
		// XDG autostart only runs things at login; nothing supervises them.
		return fmt.Errorf("restart %s (XDG autostart): %w", n.systemdUnit, ErrUnsupported)
	}
	return fmt.Errorf("localmode: service %s is not installed", n.systemdUnit)
}

// systemdUserAvailable is the spec's probe: a reachable user manager answers
// show-environment.
func systemdUserAvailable() bool {
	_, err := runCmd("systemctl", "--user", "show-environment")
	return err == nil
}

// systemdVersion parses "systemd 255 (255.4-1ubuntu8)"; 0 when unknown.
func systemdVersion() int {
	out, err := runCmd("systemctl", "--version")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(out))
	if len(f) < 2 || f[0] != "systemd" {
		return 0
	}
	v, err := strconv.Atoi(f[1])
	if err != nil {
		return 0
	}
	return v
}

func systemdUnitPath(n names) (string, error) {
	base, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "systemd", "user", n.systemdUnit), nil
}

// systemdUnit renders the user unit. appendLog adds
// StandardOutput/StandardError=append:<LogFile> (systemd >= 240).
func systemdUnit(s Service, description string, appendLog bool) string {
	var b strings.Builder
	b.WriteString("# Managed by nano-llm-proxy local mode; rewritten on install, removed on uninstall.\n")
	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=%s\n\n", systemdSpecifiers(description))
	b.WriteString("[Service]\nType=simple\n")
	argv := append([]string{s.Exec}, s.Args...)
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = systemdQuoteArg(a)
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(quoted, " "))
	if s.WorkDir != "" {
		fmt.Fprintf(&b, "WorkingDirectory=%s\n", systemdSpecifiers(s.WorkDir))
	}
	for _, kv := range envList(s.Env) {
		fmt.Fprintf(&b, "Environment=%s\n", systemdQuoteString(kv))
	}
	if appendLog && s.LogFile != "" {
		log := systemdSpecifiers(s.LogFile)
		fmt.Fprintf(&b, "StandardOutput=append:%s\nStandardError=append:%s\n", log, log)
	}
	b.WriteString("Restart=always\nRestartSec=2\n\n")
	b.WriteString("[Install]\nWantedBy=default.target\n")
	return b.String()
}

// systemdSpecifiers escapes '%' so systemd does not expand it as a unit
// specifier (%h, %u, …).
func systemdSpecifiers(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// systemdQuoteArg quotes one ExecStart word. '$' is doubled because
// ExecStart expands $VAR; '%' because of specifiers. Words with whitespace,
// quotes, backslashes or ';' (a command separator) are double-quoted with
// C-style escapes.
func systemdQuoteArg(a string) string {
	a = strings.ReplaceAll(a, "$", "$$")
	if a != "" && !strings.ContainsAny(a, " \t\"'\\;") {
		return systemdSpecifiers(a)
	}
	return systemdQuoteString(a)
}

// systemdQuoteString double-quotes s with '\' and '"' escaped and '%'
// doubled — the form Environment= and ExecStart both accept.
func systemdQuoteString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + systemdSpecifiers(s) + `"`
}
