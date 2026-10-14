// Package localmode is the per-user ("local") side of nano-llm-proxy: the
// data directory and the background-service registration for the gateway
// and its optional SearXNG sidecar.
//
// Mechanisms, by OS:
//   - Linux: a systemd user unit when a user instance is reachable, else an
//     XDG autostart .desktop entry plus an immediate detached start.
//   - macOS: a launchd LaunchAgent.
//   - Windows: a value under HKCU\...\CurrentVersion\Run (no admin rights,
//     no installer); SearXNG is not supported there.
//
// The OS is chosen at run time through the goos variable rather than build
// tags, and every external program goes through runCmd or startDetached, so
// the code paths of all three systems run in tests on any host without
// touching a real service manager. Only the process-attribute details of a
// detached start live in build-tagged files.
package localmode

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// AppName names the data directory, the gateway's service entries and the
// log file.
const AppName = "nano-llm-proxy"

// ErrUnsupported reports that the requested service operation has no
// mechanism on this OS (SearXNG on Windows, Restart on Windows, Restart of
// an XDG-autostart service, or an OS other than Linux, macOS and Windows).
var ErrUnsupported = errors.New("localmode: not supported on this platform")

// Service is one per-user background service.
type Service struct {
	Name    string            // "gateway" | "searxng"
	Exec    string            // absolute path of the program
	Args    []string          // arguments after Exec
	Env     map[string]string // extra environment (searxng: SEARXNG_SETTINGS_PATH)
	WorkDir string            // working directory; empty = the manager's default
	LogFile string            // stdout+stderr destination where the OS supports it
}

// cmdTimeout bounds every service-manager call so a wedged systemctl or
// launchctl cannot hang a GUI request or the installer.
const cmdTimeout = 30 * time.Second

// runCmd runs an external program and returns its combined output. It is a
// variable so tests can script the service managers.
var runCmd = func(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	hideConsole(cmd)
	return cmd.CombinedOutput()
}

// goos is the OS whose mechanism is used; a variable so tests can drive the
// macOS and Windows paths on Linux.
var goos = runtime.GOOS

// startDetached starts exe in the background, detached from this process
// (own session / no console), with env ("K=V") appended to the current
// environment and stdout+stderr appended to logFile (discarded when empty).
// It does not wait for the program to exit.
var startDetached = defaultStartDetached

// names are the fixed per-OS identifiers of one logical service.
type names struct {
	service      string // "gateway" | "searxng"
	systemdUnit  string // systemd user unit file name
	desktopFile  string // XDG autostart entry file name
	launchdLabel string // launchd label (also the plist base name)
	winRunValue  string // HKCU Run value name; empty = unsupported on Windows
	description  string
}

const (
	nameGateway = "gateway"
	nameSearXNG = "searxng"
)

func lookup(name string) (names, error) {
	switch name {
	case nameGateway:
		return names{
			service:      nameGateway,
			systemdUnit:  AppName + ".service",
			desktopFile:  AppName + ".desktop",
			launchdLabel: "dev." + AppName,
			winRunValue:  "NanoLLMProxy",
			description:  "nano-llm-proxy LLM gateway",
		}, nil
	case nameSearXNG:
		return names{
			service:      nameSearXNG,
			systemdUnit:  AppName + "-searxng.service",
			desktopFile:  AppName + "-searxng.desktop",
			launchdLabel: "dev." + AppName + ".searxng",
			description:  "nano-llm-proxy SearXNG web search",
		}, nil
	}
	return names{}, fmt.Errorf("localmode: unknown service %q", name)
}

// Supported reports whether a background-service mechanism exists on this
// OS. On Linux the XDG autostart fallback always exists, so only the OS is
// checked.
func Supported() bool {
	switch goos {
	case "linux", "darwin", "windows":
		return true
	}
	return false
}

// Enable writes the autostart entry for s and starts it now. Calling it
// again rewrites the entry and leaves an already-running service running
// (launchd reloads the agent so a changed plist takes effect).
//
// Enable/Disable are for the installer CLI, which runs outside the service
// and wants it started or stopped now. Code running inside the gateway
// service (the GUI "start at sign-in" toggle) must use Register/Unregister
// instead: Enable can reload, and Disable stops, the very process calling it.
func Enable(s Service) error {
	n, err := lookup(s.Name)
	if err != nil {
		return err
	}
	if err := validate(s); err != nil {
		return err
	}
	switch goos {
	case "linux":
		return linuxEnable(s, n)
	case "darwin":
		return launchdEnable(s, n)
	case "windows":
		return windowsEnable(s, n)
	}
	return fmt.Errorf("enable %s on %s: %w", s.Name, goos, ErrUnsupported)
}

// Disable stops the service and removes its autostart entry. A service that
// is not installed is not an error, and neither is a service the OS cannot
// host (there is nothing to remove), so uninstall can call it blindly. It
// also stops an instance left running by Unregister. Installer CLI only —
// see Enable; inside the gateway use Unregister.
func Disable(name string) error {
	n, err := lookup(name)
	if err != nil {
		return err
	}
	switch goos {
	case "linux":
		return linuxDisable(n)
	case "darwin":
		return launchdDisable(n)
	case "windows":
		return windowsDisable(n)
	}
	return nil
}

// Register writes the autostart entry for s so it starts at the next
// sign-in, and never starts, stops or reloads anything now: an already-loaded
// job keeps running untouched. It is for the GUI toggle inside the running
// gateway; the installer CLI uses Enable. Validation, file locations and
// permissions are the same as Enable's.
func Register(s Service) error {
	n, err := lookup(s.Name)
	if err != nil {
		return err
	}
	if err := validate(s); err != nil {
		return err
	}
	switch goos {
	case "linux":
		return linuxRegister(s, n)
	case "darwin":
		return launchdRegister(s, n)
	case "windows":
		return windowsRegister(s, n)
	}
	return fmt.Errorf("register %s on %s: %w", s.Name, goos, ErrUnsupported)
}

// Unregister removes the autostart entry so the service no longer starts at
// sign-in, and leaves a running instance alone (Status then reports
// installed=false, running=true). A missing entry, or a service the OS
// cannot host, is not an error. It is for the GUI toggle inside the running
// gateway; the installer CLI uses Disable.
func Unregister(name string) error {
	n, err := lookup(name)
	if err != nil {
		return err
	}
	switch goos {
	case "linux":
		return linuxUnregister(n)
	case "darwin":
		return launchdUnregister(n)
	case "windows":
		return windowsUnregister(n)
	}
	return nil
}

// Status reports installed = "an autostart entry exists, so the service
// starts at the next sign-in" and, independently, running = "an instance is
// up" as far as its manager (or, for the XDG fallback, the process table)
// can tell. The two differ after Unregister: installed=false, running=true.
func Status(name string) (installed, running bool, err error) {
	n, err := lookup(name)
	if err != nil {
		return false, false, err
	}
	switch goos {
	case "linux":
		return linuxStatus(n)
	case "darwin":
		return launchdStatus(n)
	case "windows":
		return windowsStatus(name, n)
	}
	return false, false, fmt.Errorf("status %s on %s: %w", name, goos, ErrUnsupported)
}

// Restart restarts the service through its manager. Mechanisms without a
// supervisor (Windows Run key, XDG autostart) return ErrUnsupported; the
// caller then asks the user to restart instead.
func Restart(name string) error {
	n, err := lookup(name)
	if err != nil {
		return err
	}
	switch goos {
	case "linux":
		return linuxRestart(n)
	case "darwin":
		_, err := run("launchctl", "kickstart", "-k", launchdTarget(n))
		return err
	}
	return fmt.Errorf("restart %s on %s: %w", name, goos, ErrUnsupported)
}

// Supervised reports whether the service manager here restarts a background
// service that exits (systemd Restart=always, launchd KeepAlive). The
// Windows Run key and the Linux desktop-autostart fallback only start it at
// sign-in, so an exiting instance stays down.
func Supervised() bool {
	switch goos {
	case "linux":
		return systemdUserAvailable()
	case "darwin":
		return true
	}
	return false
}

// OpenBrowser opens an http(s) URL in the user's default browser. Other
// schemes are refused: the Windows and Linux openers would happily launch a
// local file or program.
func OpenBrowser(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("open browser: not an http(s) URL")
	}
	switch goos {
	case "darwin":
		_, err = run("open", rawURL)
	case "windows":
		_, err = run("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		// xdg-open can block until the browser exits when it launches one
		// directly, so it is started detached rather than waited on.
		err = startDetached("xdg-open", []string{rawURL}, nil, "")
	}
	if err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

// validate rejects services whose fields cannot be written safely into a
// unit, plist, desktop entry or registry value. Line breaks would let a
// value inject extra unit/desktop directives.
func validate(s Service) error {
	if s.Exec == "" || !filepath.IsAbs(s.Exec) {
		return errors.New("localmode: service Exec must be an absolute path")
	}
	fields := append([]string{s.Exec, s.WorkDir, s.LogFile}, s.Args...)
	for k, v := range s.Env {
		if k == "" || strings.ContainsAny(k, "= \t") {
			return fmt.Errorf("localmode: invalid environment name %q", k)
		}
		fields = append(fields, k, v)
	}
	for _, f := range fields {
		if strings.ContainsAny(f, "\n\r\x00") {
			return errors.New("localmode: service fields must not contain line breaks or NUL")
		}
	}
	return nil
}

// run calls runCmd and wraps a failure with the command and a short excerpt
// of its output (service-manager diagnostics, never secrets).
func run(name string, args ...string) ([]byte, error) {
	out, err := runCmd(name, args...)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if msg != "" {
			msg = ": " + msg
		}
		return out, fmt.Errorf("%s %s: %w%s", name, strings.Join(args, " "), err, msg)
	}
	return out, nil
}

// envList renders env as sorted "K=V" pairs (sorted so generated files and
// command lines are deterministic).
func envList(env map[string]string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(env)) {
		out = append(out, k+"="+env[k])
	}
	return out
}

func uidString() string { return strconv.Itoa(os.Getuid()) }

// exists reports whether path exists; errors other than "not found" are
// returned so a permission problem is not mistaken for "not installed".
func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", path, err)
}

// removeIfExists deletes path; a missing file is success.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// writeFileAtomic writes data to path with perm via a temp file + rename, so
// a service manager never reads a half-written unit or plist. Parent
// directories are created 0755 (they are the user's config tree, not ours).
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup; the write error is what matters
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup; the chmod error is what matters
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup; the rename error is what matters
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// defaultStartDetached is the real startDetached. The child is reaped in a
// goroutine so a long-running gateway that starts helpers never accumulates
// zombies.
func defaultStartDetached(exe string, args []string, env []string, logFile string) error {
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), env...)
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		// The child inherits its own handle; ours can close once it has started.
		defer f.Close()
		cmd.Stdout, cmd.Stderr = f, f
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", filepath.Base(exe), err)
	}
	go func() { _ = cmd.Wait() }() // reap only; the exit status has no consumer
	return nil
}
