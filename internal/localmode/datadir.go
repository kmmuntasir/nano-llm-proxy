package localmode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DataDir resolves the per-user data directory and creates it 0700 (it holds
// gateway.db with reversible upstream keys):
//
//	Linux:   $XDG_DATA_HOME/nano-llm-proxy, else ~/.local/share/nano-llm-proxy
//	macOS:   ~/Library/Application Support/nano-llm-proxy
//	Windows: %LOCALAPPDATA%\nano-llm-proxy
//
// Locations come from the environment (not os.UserHomeDir) so the OS seam
// and tests can point them anywhere.
func DataDir() (string, error) {
	dir, err := dataDirPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	return dir, nil
}

func dataDirPath() (string, error) {
	switch goos {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return "", errors.New("localmode: LOCALAPPDATA is not set")
		}
		return filepath.Join(base, AppName), nil
	case "darwin":
		home, err := homeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", AppName), nil
	}
	base, err := xdgDir("XDG_DATA_HOME", ".local", "share")
	if err != nil {
		return "", err
	}
	return filepath.Join(base, AppName), nil
}

// homeDir is $HOME (%USERPROFILE% on Windows).
func homeDir() (string, error) {
	key := "HOME"
	if goos == "windows" {
		key = "USERPROFILE"
	}
	home := os.Getenv(key)
	if home == "" {
		return "", fmt.Errorf("localmode: %s is not set", key)
	}
	return home, nil
}

// xdgDir returns $env when it is an absolute path (the XDG spec says
// relative values are invalid and must be ignored), else ~/<fallback...>.
func xdgDir(env string, fallback ...string) (string, error) {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return v, nil
	}
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, fallback...)...), nil
}

// configHome is $XDG_CONFIG_HOME, else ~/.config — where systemd looks for
// user units and desktops look for autostart entries.
func configHome() (string, error) { return xdgDir("XDG_CONFIG_HOME", ".config") }
