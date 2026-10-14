package localmode

import (
	"encoding/xml"
	"path/filepath"
	"strings"
	"time"
)

// bootstrapAttempts / bootstrapRetryDelay: `launchctl bootout` returns before
// the old job is fully gone, and an immediate bootstrap of the same label
// then fails ("Bootstrap failed: 5: Input/output error"). A short retry
// absorbs that race on re-enable. The delay is a variable for tests.
const bootstrapAttempts = 3

var bootstrapRetryDelay = 500 * time.Millisecond

// launchdEnable writes the LaunchAgent and (re)loads it into the user's GUI
// domain, so a changed plist takes effect and RunAtLoad starts it now.
func launchdEnable(s Service, n names) error {
	if err := launchdRegister(s, n); err != nil {
		return err
	}
	path, err := launchdPlistPath(n)
	if err != nil {
		return err
	}
	domain := launchdDomain()
	// Fails when the agent is not loaded yet, which is the normal first case.
	_, _ = runCmd("launchctl", "bootout", domain, path)
	for attempt := 1; ; attempt++ {
		_, err := run("launchctl", "bootstrap", domain, path)
		if err == nil || attempt == bootstrapAttempts {
			return err
		}
		time.Sleep(bootstrapRetryDelay)
	}
}

// launchdDisable unloads by service target (works even if the plist is
// already gone) and removes the plist.
func launchdDisable(n names) error {
	path, err := launchdPlistPath(n)
	if err != nil {
		return err
	}
	// Fails when the agent is not loaded — nothing to stop.
	_, _ = runCmd("launchctl", "bootout", launchdTarget(n))
	return removeIfExists(path)
}

// launchdRegister only writes the plist: launchd reads LaunchAgents at the
// next login, and an already-loaded job keeps running on its old definition.
func launchdRegister(s Service, n names) error {
	path, err := launchdPlistPath(n)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(launchdPlist(s, n.launchdLabel)), 0o644)
}

// launchdUnregister only removes the plist; a loaded job stays loaded (and
// running) until logout or Disable.
func launchdUnregister(n names) error {
	path, err := launchdPlistPath(n)
	if err != nil {
		return err
	}
	return removeIfExists(path)
}

// launchdStatus: installed = the plist is in LaunchAgents (loaded at the
// next login); running is asked of launchd regardless, since a job stays
// loaded after Unregister removed its plist.
func launchdStatus(n names) (installed, running bool, err error) {
	path, err := launchdPlistPath(n)
	if err != nil {
		return false, false, err
	}
	installed, err = exists(path)
	if err != nil {
		return false, false, err
	}
	// print fails when the job is not loaded; that is "not running".
	out, err := runCmd("launchctl", "print", launchdTarget(n))
	return installed, err == nil && strings.Contains(string(out), "state = running"), nil
}

func launchdDomain() string { return "gui/" + uidString() }

func launchdTarget(n names) string { return launchdDomain() + "/" + n.launchdLabel }

func launchdPlistPath(n names) (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", n.launchdLabel+".plist"), nil
}

// launchdPlist renders the LaunchAgent property list. Every value is
// XML-escaped; paths under a user's home can contain '&' or '<'.
func launchdPlist(s Service, label string) string {
	var b strings.Builder
	str := func(indent, v string) {
		b.WriteString(indent + "<string>")
		xml.EscapeText(&b, []byte(v)) //nolint:errcheck // strings.Builder never fails
		b.WriteString("</string>\n")
	}
	key := func(indent, k string) {
		b.WriteString(indent + "<key>")
		xml.EscapeText(&b, []byte(k)) //nolint:errcheck // strings.Builder never fails
		b.WriteString("</key>\n")
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	key("\t", "Label")
	str("\t", label)
	key("\t", "ProgramArguments")
	b.WriteString("\t<array>\n")
	str("\t\t", s.Exec)
	for _, a := range s.Args {
		str("\t\t", a)
	}
	b.WriteString("\t</array>\n")
	if len(s.Env) > 0 {
		key("\t", "EnvironmentVariables")
		b.WriteString("\t<dict>\n")
		for _, kv := range envList(s.Env) {
			k, v, _ := strings.Cut(kv, "=")
			key("\t\t", k)
			str("\t\t", v)
		}
		b.WriteString("\t</dict>\n")
	}
	if s.WorkDir != "" {
		key("\t", "WorkingDirectory")
		str("\t", s.WorkDir)
	}
	if s.LogFile != "" {
		key("\t", "StandardOutPath")
		str("\t", s.LogFile)
		key("\t", "StandardErrorPath")
		str("\t", s.LogFile)
	}
	key("\t", "RunAtLoad")
	b.WriteString("\t<true/>\n")
	key("\t", "KeepAlive")
	b.WriteString("\t<true/>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}
