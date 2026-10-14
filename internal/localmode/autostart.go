package localmode

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The XDG autostart fallback has no supervisor: the desktop session starts
// the entry at login and the process table is the only source of "running".
// Processes are matched by their exact command line (pgrep/pkill -f with an
// anchored pattern) for the current user, so neither the installer
// (`… local install`) nor another user's copy is ever matched.
//
// The command line is also kept in a record in the data dir, because
// Unregister deletes the .desktop file while the instance keeps running:
// Status must still report it running, and a later Disable must still be
// able to stop it.

// desktopWrite writes the entry and the command-line record.
func desktopWrite(s Service, n names) error {
	path, err := desktopPath(n)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, []byte(desktopEntry(s, n.description)), 0o644); err != nil {
		return err
	}
	return writeCmdlineRecord(n, append([]string{s.Exec}, s.Args...))
}

// desktopStart starts the service detached unless it already runs, so
// re-enabling never starts a second copy.
func desktopStart(s Service, n names) error {
	if processRunning(append([]string{s.Exec}, s.Args...)) {
		return nil
	}
	if err := startDetached(s.Exec, s.Args, envList(s.Env), s.LogFile); err != nil {
		return fmt.Errorf("start %s: %w", n.desktopFile, err)
	}
	return nil
}

// desktopDisable kills the service's process (found via the entry, else the
// record) and removes both; nothing to remove is success.
func desktopDisable(n names) error {
	path, err := desktopPath(n)
	if err != nil {
		return err
	}
	argv, err := desktopArgv(path)
	if err != nil {
		// Missing or unparsable entry: fall back to the record.
		if argv, err = readCmdlineRecord(n); err != nil {
			return err
		}
	}
	if len(argv) > 0 {
		// pkill exits 1 when nothing matched — the service just wasn't running.
		_, _ = runCmd("pkill", "-u", uidString(), "-f", cmdlinePattern(argv))
	}
	rec, err := cmdlineRecordPath(n)
	if err != nil {
		return err
	}
	return errors.Join(removeIfExists(path), removeIfExists(rec))
}

// desktopUnregister removes the entry only; the process and its record stay.
func desktopUnregister(n names) error {
	path, err := desktopPath(n)
	if err != nil {
		return err
	}
	return removeIfExists(path)
}

// desktopStatus: installed = the entry exists; running = a process with the
// entry's (else the record's) command line exists.
func desktopStatus(n names) (installed, running bool, err error) {
	path, err := desktopPath(n)
	if err != nil {
		return false, false, err
	}
	argv, err := desktopArgv(path)
	switch {
	case err == nil:
		return true, processRunning(argv), nil
	case !errors.Is(err, os.ErrNotExist):
		return true, false, err
	}
	argv, err = readCmdlineRecord(n)
	if err != nil || len(argv) == 0 {
		return false, false, err
	}
	return false, processRunning(argv), nil
}

func cmdlineRecordPath(n names) (string, error) {
	dir, err := dataDirPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "autostart-"+n.service+".cmdline"), nil
}

// writeCmdlineRecord stores argv one per line (validate forbids line breaks
// in every field). DataDir creates the directory 0700 first so the record
// never causes a looser data dir.
func writeCmdlineRecord(n names, argv []string) error {
	if _, err := DataDir(); err != nil {
		return err
	}
	path, err := cmdlineRecordPath(n)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(strings.Join(argv, "\n")+"\n"), 0o600)
}

// readCmdlineRecord returns the recorded argv, or nil when there is none.
func readCmdlineRecord(n names) ([]string, error) {
	path, err := cmdlineRecordPath(n)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

func desktopPath(n names) (string, error) {
	base, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "autostart", n.desktopFile), nil
}

// processRunning reports whether a process of this user runs exactly argv.
func processRunning(argv []string) bool {
	_, err := runCmd("pgrep", "-u", uidString(), "-f", cmdlinePattern(argv))
	return err == nil
}

// cmdlinePattern is an anchored POSIX ERE matching argv joined by spaces,
// the form pgrep -f compares against.
func cmdlinePattern(argv []string) string {
	return "^" + regexp.QuoteMeta(strings.Join(argv, " ")) + "$"
}

// desktopEntry renders the XDG autostart entry. A desktop entry cannot set
// the environment or redirect output itself, so Env is applied through
// env(1) and LogFile only takes effect for the immediate detached start.
func desktopEntry(s Service, description string) string {
	var b strings.Builder
	b.WriteString("[Desktop Entry]\nType=Application\n")
	fmt.Fprintf(&b, "Name=%s\n", desktopEscapeString(description))
	b.WriteString("Comment=Started at login by nano-llm-proxy local mode\n")
	fmt.Fprintf(&b, "Exec=%s\n", desktopExec(s))
	if s.WorkDir != "" {
		fmt.Fprintf(&b, "Path=%s\n", desktopEscapeString(s.WorkDir))
	}
	b.WriteString("Terminal=false\nNoDisplay=true\nX-GNOME-Autostart-enabled=true\n")
	return b.String()
}

// desktopReserved are the characters that force quoting in an Exec argument
// (Desktop Entry Specification, "The Exec key").
const desktopReserved = " \t\n\"'\\><~|&;$*?#()`"

// desktopExec renders the Exec value: arguments quoted per the spec, then
// the whole value escaped as a string (so a literal backslash inside a
// quoted argument ends up as four).
func desktopExec(s Service) string {
	var parts []string
	if len(s.Env) > 0 {
		parts = append(parts, "env")
		for _, kv := range envList(s.Env) {
			parts = append(parts, desktopQuoteArg(kv))
		}
	}
	parts = append(parts, desktopQuoteArg(s.Exec))
	for _, a := range s.Args {
		parts = append(parts, desktopQuoteArg(a))
	}
	return desktopEscapeString(strings.Join(parts, " "))
}

func desktopQuoteArg(a string) string {
	a = strings.ReplaceAll(a, "%", "%%") // field codes
	if a != "" && !strings.ContainsAny(a, desktopReserved) {
		return a
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	return `"` + r.Replace(a) + `"`
}

// desktopEscapeString applies the string-type escape rule (backslash).
func desktopEscapeString(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }

// desktopArgv reads the entry at path and returns the program argv its Exec
// key runs (with a leading env(1) and its assignments stripped), which is
// the command line the process will show.
func desktopArgv(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "Exec="); ok {
			words, err := desktopSplitExec(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			return stripEnvPrefix(words), nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return nil, fmt.Errorf("%s: no Exec key", path)
}

func stripEnvPrefix(words []string) []string {
	if len(words) == 0 || words[0] != "env" {
		return words
	}
	i := 1
	for i < len(words) && strings.Contains(words[i], "=") {
		i++
	}
	return words[i:]
}

// desktopSplitExec is the inverse of desktopExec: undo the string escapes,
// then split into words honouring double quotes and "%%".
func desktopSplitExec(v string) ([]string, error) {
	var un strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			switch v[i+1] {
			case '\\':
				un.WriteByte('\\')
			case 's':
				un.WriteByte(' ')
			case 't':
				un.WriteByte('\t')
			case 'n':
				un.WriteByte('\n')
			case 'r':
				un.WriteByte('\r')
			default:
				un.WriteByte(v[i])
				un.WriteByte(v[i+1])
			}
			i++
			continue
		}
		un.WriteByte(v[i])
	}
	s := un.String()

	var words []string
	var cur strings.Builder
	inWord, inQuote := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '%' && i+1 < len(s) && s[i+1] == '%':
			cur.WriteByte('%')
			inWord = true
			i++
		case inQuote && c == '\\' && i+1 < len(s) && strings.IndexByte("\"`$\\", s[i+1]) >= 0:
			cur.WriteByte(s[i+1])
			i++
		case inQuote && c == '"':
			inQuote = false
		case inQuote:
			cur.WriteByte(c)
		case c == '"':
			inQuote, inWord = true, true
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inQuote {
		return nil, errors.New("unterminated quote in Exec")
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}
