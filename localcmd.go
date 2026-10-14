package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"github.com/kmmuntasir/nano-llm-proxy/internal/gateway"
	"github.com/kmmuntasir/nano-llm-proxy/internal/localmode"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// The `nano-llm-proxy local …` subcommands: the curl installers call
// `local datadir` and `local install`; people run `local status` and
// `local uninstall`. `nano-llm-proxy local` with no subcommand runs the
// gateway itself in local mode (main.go).

const localUsage = `usage:
  nano-llm-proxy local                      run in local mode (foreground)
  nano-llm-proxy local install [--port N] [--no-autostart] [--no-open] [--quiet]
  nano-llm-proxy local uninstall [--purge]
  nano-llm-proxy local status
  nano-llm-proxy local datadir`

// maxLocalLog is where the local log is truncated at startup.
const maxLocalLog = 5 << 20

// localCommand runs a `local` subcommand; ok=false means args name none
// (run the gateway).
func localCommand(args []string) (ok bool) {
	if len(args) == 0 {
		return false
	}
	var err error
	switch args[0] {
	case "install":
		err = localInstall(args[1:])
	case "uninstall":
		err = localUninstall(args[1:])
	case "status":
		err = localStatus()
	case "datadir":
		var dir string
		if dir, err = localmode.DataDir(); err == nil {
			fmt.Println(dir)
		}
	case "help", "-h", "--help":
		fmt.Println(localUsage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s\n", args[0], localUsage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return true
}

// loadLocalConfig reads <dataDir>/.env, then the local-mode config.
func loadLocalConfig(dataDir string) (*config.Config, error) {
	if err := config.LoadDotEnv(filepath.Join(dataDir, ".env")); err != nil {
		return nil, fmt.Errorf("env: %w", err)
	}
	return config.LoadLocalConfig(dataDir)
}

// executable is this binary's resolved path — what the background service
// must run.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// gatewayService is the background service that runs this binary in local
// mode. LocalServiceEnv tells the process a restart will bring it back.
func gatewayService(exe, dataDir string) localmode.Service {
	return localmode.Service{
		Name: "gateway", Exec: exe, Args: []string{"local"},
		Env:     map[string]string{gateway.LocalServiceEnv: "1"},
		WorkDir: dataDir, LogFile: gateway.LocalLogFile(dataDir),
	}
}

// searxngService runs the SearXNG the installer set up under
// <dataDir>/searxng (Linux/macOS only). ok=false when it isn't installed.
func searxngService(dataDir string) (s localmode.Service, ok bool) {
	home := filepath.Join(dataDir, "searxng")
	python := filepath.Join(home, "venv", "bin", "python")
	if _, err := os.Stat(python); err != nil {
		return s, false
	}
	return localmode.Service{
		Name: "searxng", Exec: python, Args: []string{"-m", "searx.webapp"},
		Env:     map[string]string{"SEARXNG_SETTINGS_PATH": filepath.Join(home, "settings.yml")},
		WorkDir: home, LogFile: filepath.Join(home, "searxng.log"),
	}, true
}

// setupLocalLog sends the log to <dataDir>/nano-llm-proxy.log (truncated at
// startup past 5 MiB), and to stderr too when that is a terminal. The
// background instance has no terminal; on Windows it has no console at all.
func setupLocalLog(dataDir string) error {
	path := gateway.LocalLogFile(dataDir)
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLocalLog {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	// systemd's append: creates the file first, with its own umask
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	var out io.Writer = f
	if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		out = io.MultiWriter(os.Stderr, f)
	}
	log.SetOutput(out)
	return nil
}

func localInstall(args []string) error {
	fs := flag.NewFlagSet("local install", flag.ContinueOnError)
	port := fs.Int("port", 0, "port to listen on (default 8787)")
	noAutostart := fs.Bool("no-autostart", false, "don't start at sign-in (run `nano-llm-proxy local` yourself)")
	noOpen := fs.Bool("no-open", false, "don't open the browser")
	quiet := fs.Bool("quiet", false, "skip the closing summary (the installers print their own)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dataDir, err := localmode.DataDir()
	if err != nil {
		return err
	}
	if *port != 0 {
		if *port < 1024 || *port > 65535 {
			return fmt.Errorf("--port must be between 1024 and 65535")
		}
		if err := config.SetDotEnv(filepath.Join(dataDir, ".env"), "NANO_PORT", strconv.Itoa(*port)); err != nil {
			return err
		}
	}
	cfg, err := loadLocalConfig(dataDir)
	if err != nil {
		return err
	}
	st, err := store.OpenStore(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	_, err = st.BootstrapLocal(cfg)
	st.Close() //nolint:errcheck
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	exe, err := executable()
	if err != nil {
		return err
	}
	url := "http://localhost:" + strconv.Itoa(cfg.Port)

	started := false
	switch {
	case *noAutostart:
		fmt.Println("Start at sign-in: off (--no-autostart). Start it with: nano-llm-proxy local")
	case !localmode.Supported():
		fmt.Println("Start at sign-in isn't supported here. Start it with: nano-llm-proxy local")
	default:
		services := []localmode.Service{gatewayService(exe, dataDir)}
		if s, ok := searxngService(dataDir); ok {
			services = append(services, s)
		}
		for _, s := range services {
			if err := enableAndRestart(s); err != nil {
				return fmt.Errorf("background service %s: %w", s.Name, err)
			}
		}
		started = true
	}

	if started {
		if err := waitHealthy(cfg, 20*time.Second); err != nil {
			fmt.Printf("warning: %v — see %s\n", err, gateway.LocalLogFile(dataDir))
		} else if !*noOpen {
			if err := localmode.OpenBrowser(url); err != nil {
				fmt.Printf("Couldn't open a browser (%v) — open %s yourself.\n", err, url)
			}
		}
	}
	if *quiet {
		return nil
	}
	fmt.Printf(`
nano-llm-proxy is installed (local mode).
  GUI:        %[1]s
  OpenAI:     base URL %[1]s/v1
  Anthropic:  base URL %[1]s   (e.g. ANTHROPIC_BASE_URL=%[1]s)
  MCP tools:  %[1]s/mcp
  API key:    not required — use any placeholder such as "local"
  Data:       %[2]s
Add your provider keys on the Providers page. Uninstall: nano-llm-proxy local uninstall [--purge]
`, url, dataDir)
	return nil
}

// enableAndRestart registers + starts a service; one that was already
// running is restarted so an upgrade runs the new binary.
func enableAndRestart(s localmode.Service) error {
	_, wasRunning, _ := localmode.Status(s.Name)
	if err := localmode.Enable(s); err != nil {
		return err
	}
	if !wasRunning {
		return nil
	}
	err := localmode.Restart(s.Name)
	if !errors.Is(err, localmode.ErrUnsupported) {
		return err
	}
	// no restart verb (Windows, desktop-autostart fallback): stop, then
	// start again
	if err := localmode.Disable(s.Name); err != nil {
		return err
	}
	return localmode.Enable(s)
}

// waitHealthy polls /health until the background instance answers.
func waitHealthy(cfg *config.Config, within time.Duration) error {
	u := "http://" + net.JoinHostPort(cfg.Bind, strconv.Itoa(cfg.Port)) + "/health"
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if resp, err := client.Get(u); err == nil {
			resp.Body.Close() //nolint:errcheck
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("nano-llm-proxy did not answer on port %d within %s", cfg.Port, within)
}

func localUninstall(args []string) error {
	fs := flag.NewFlagSet("local uninstall", flag.ContinueOnError)
	purge := fs.Bool("purge", false, "also delete the data folder (database, provider keys, logs)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	for _, name := range []string{"searxng", "gateway"} {
		if err := localmode.Disable(name); err != nil && !errors.Is(err, localmode.ErrUnsupported) {
			return fmt.Errorf("remove background service %s: %w", name, err)
		}
	}
	fmt.Println("Background services removed.")
	dataDir, err := localmode.DataDir()
	if err != nil {
		return err
	}
	if *purge {
		// never RemoveAll anything but our own folder
		if filepath.Base(dataDir) != localmode.AppName {
			return fmt.Errorf("refusing to delete unexpected data folder %s", dataDir)
		}
		if err := os.RemoveAll(dataDir); err != nil {
			return err
		}
		fmt.Println("Deleted", dataDir)
	} else {
		fmt.Println("Kept your data in", dataDir, "(--purge deletes it)")
	}
	if exe, err := executable(); err == nil {
		fmt.Println("The program itself is still at", exe)
	}
	return nil
}

func localStatus() error {
	dataDir, err := localmode.DataDir()
	if err != nil {
		return err
	}
	cfg, err := loadLocalConfig(dataDir)
	if err != nil {
		return err
	}
	fmt.Println("Data folder: ", dataDir)
	fmt.Println("Address:     ", "http://localhost:"+strconv.Itoa(cfg.Port))
	if err := waitHealthy(cfg, time.Second); err != nil {
		fmt.Println("Answering:    no")
	} else {
		fmt.Println("Answering:    yes")
	}
	for _, name := range []string{"gateway", "searxng"} {
		installed, running, err := localmode.Status(name)
		switch {
		case errors.Is(err, localmode.ErrUnsupported):
			fmt.Printf("Service %-7s unsupported on %s\n", name+":", runtime.GOOS)
		case err != nil:
			fmt.Printf("Service %-7s error: %v\n", name+":", err)
		default:
			fmt.Printf("Service %-7s start at sign-in=%v running=%v\n", name+":", installed, running)
		}
	}
	return nil
}
