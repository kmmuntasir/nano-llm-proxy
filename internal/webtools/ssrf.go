package webtools

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// SSRF protection for user-supplied URLs. The canonical guard lives in the
// dialer's Control hook: it runs with the post-DNS resolved address, after
// every redirect, so neither DNS rebinding nor a redirect to 127.0.0.1 slips
// through. The SearXNG client deliberately does NOT use this transport —
// SearXNGURL is admin-configured and usually loopback, which is exactly what
// the guard blocks.

var errBlockedPrivate = errors.New("URL resolves to a private, loopback, or link-local address — not allowed")

// checkDialedIP is the Control hook for the guarded dialer. Unparseable
// addresses are rejected: fail closed.
func checkDialedIP(_, addr string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return fmt.Errorf("dial address %q did not parse: %w", addr, errBlockedPrivate)
	}
	if ipIsBlocked(ap.Addr()) {
		return errBlockedPrivate
	}
	return nil
}

func ipIsBlocked(ip netip.Addr) bool {
	// Unwrap IPv4-mapped IPv6 first so v4 checks see real v4 addresses.
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() ||
		// ULA fc00::/7 — IsPrivate only covers fc00::/18 per Go docs; the
		// explicit prefix check is the safe form.
		isULA(ip)
}

func isULA(ip netip.Addr) bool {
	if !ip.Is6() || ip.Is4() {
		return false
	}
	ula, err := netip.ParsePrefix("fc00::/7")
	if err != nil {
		return false
	}
	return ula.Contains(ip)
}

// guardedTransport builds the transport used for user-supplied URLs only.
func guardedTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, Control: checkDialedIP}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// URLAllowed is the cheap pre-dial validation: scheme and host sanity, plus
// literal-IP and literal-host rejection. It gives fast 400s without any I/O;
// the authoritative check remains the dial-time Control hook.
func URLAllowed(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https URLs are supported, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("URL has no host")
	}
	if u.User != nil {
		return errors.New("URLs with embedded credentials are not allowed")
	}
	// A hostname that is literally an IP is checked now; named hosts are
	// caught at dial time by checkDialedIP (post-DNS).
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && ipIsBlocked(ip) {
		return errBlockedPrivate
	}
	return nil
}
