package webtools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/settings"
)

// obscuraRunner shells out to the obscura headless browser
// (`obscura fetch <url> --dump markdown`) for pages the native leg can't
// handle: JS-rendered shells and bot-walled sites. Each fetch is a fresh
// process — instant startup, no daemon to babysit, and a bounded number of
// browsers in flight keeps the 512 MB host honest.

const (
	obscuraV8HeapFlag = "--max-old-space-size=128" // cap the V8 heap per process
	semaphoreWait     = 5 * time.Second            // bounded wait for a slot
)

// dynSemaphore is a semaphore whose limit can change at runtime (the admin
// edits ObscuraConcurrency; runners pick it up without a rebuild).
type dynSemaphore struct {
	mu       sync.Mutex
	inFlight int
	limit    atomic.Int32
}

func newDynSemaphore(limit int) *dynSemaphore {
	s := &dynSemaphore{}
	s.limit.Store(int32(limit))
	return s
}

func (s *dynSemaphore) setLimit(n int) { s.limit.Store(int32(n)) }

// acquire waits up to semaphoreWait for a slot. The returned func releases.
func (s *dynSemaphore) acquire() (func(), error) {
	deadline := time.Now().Add(semaphoreWait)
	for {
		s.mu.Lock()
		if s.inFlight < int(s.limit.Load()) {
			s.inFlight++
			s.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					s.mu.Lock()
					s.inFlight--
					s.mu.Unlock()
				})
			}, nil
		}
		s.mu.Unlock()
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("obscura busy (%d fetches already in flight, limit %d)", s.inFlight, s.limit.Load())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type obscuraRunner struct{}

func newObscuraRunner() *obscuraRunner { return &obscuraRunner{} }

// applySettings syncs the mutable knobs (concurrency limit) on each call so
// settings changes take effect without restarting the gateway.
func (o *obscuraRunner) applySettings(s *dynSemaphore, cfg settings.WebToolsSettings) {
	s.setLimit(cfg.ObscuraConcurrency)
}

// fetch runs one obscura fetch, returning its markdown dump.
func (o *obscuraRunner) fetch(ctx context.Context, cfg settings.WebToolsSettings, sem *dynSemaphore, rawURL string) (string, error) {
	o.applySettings(sem, cfg)

	release, err := sem.acquire()
	if err != nil {
		return "", err
	}
	defer release()

	cctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.ObscuraTimeoutSeconds)*time.Second)
	defer cancel()

	// --v8-flags and --stealth are GLOBAL flags on the obscura root command:
	// they must precede the `fetch` subcommand or clap rejects them.
	args := []string{}
	if cfg.ObscuraStealth {
		args = append(args, "--stealth")
	}
	args = append(args, "--v8-flags", obscuraV8HeapFlag)
	args = append(args,
		"fetch", rawURL,
		"--dump", "markdown",
		"--quiet",
		"--timeout", fmt.Sprintf("%d", cfg.ObscuraTimeoutSeconds-5), // internal < outer so we get the error, not a SIGKILL race
	)

	cmd := exec.CommandContext(cctx, cfg.ObscuraPath, args...)
	cmd.WaitDelay = 2 * time.Second // reap the process group after ctx cancel

	var stderr limitedBuffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := stderr.String()
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("obscura timed out after %ds: %s", cfg.ObscuraTimeoutSeconds, msg)
		}
		if msg != "" {
			return "", fmt.Errorf("obscura failed: %s", msg)
		}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", fmt.Errorf("obscura exited with status %d", exitErr.ExitCode())
		}
		return "", fmt.Errorf("obscura could not be started (path %q): %w — run scripts/install-web-tools.sh --check", cfg.ObscuraPath, err)
	}
	if len(out) == 0 {
		return "", errors.New("obscura returned no content")
	}
	return string(out), nil
}

// version reports `obscura --version` for the diagnostics endpoint.
func (o *obscuraRunner) version(ctx context.Context, path string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("obscura not runnable at %q: %w — run scripts/install-web-tools.sh --check", path, err)
	}
	return firstLine(string(out)), nil
}

// limitedBuffer caps captured stderr so a chatty process can't balloon memory.
type limitedBuffer struct {
	buf []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const max = 4 << 10
	if len(b.buf) < max {
		b.buf = append(b.buf, p...)
		if len(b.buf) > max {
			b.buf = b.buf[:max]
		}
	}
	return len(p), nil // pretend full write; we only use this for diagnostics
}

func (b *limitedBuffer) String() string { return firstLine(string(b.buf)) }

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' || r == '\r' {
			return s[:i]
		}
	}
	return s
}
