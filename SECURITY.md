# Security Policy

## Supported versions

nano-llm-proxy is pre-1.0. Fixes land on `main` and in the latest tag; there
are no long-term support branches yet.

| Version | Supported |
| --- | --- |
| `main` / latest tag | yes |
| Anything older | no |

## Reporting a vulnerability

**Do not open a public issue for a security report.**

Use GitHub's private reporting: go to
[Security → Report a vulnerability](https://github.com/kmmuntasir/nano-llm-proxy/security/advisories/new)
on this repository. If that is unavailable to you, open a public issue that
says only "security report available privately, please get in touch" with no
technical detail, and wait for a reply.

Please include:

- what the issue is, and which component (gateway, admin API, web tools, GUI)
- the version or commit you tested (`./nano-llm-proxy -version`)
- reproduction steps or a proof of concept
- the impact you believe it has, and any deployment preconditions

You can expect an acknowledgement within a few days. Fixes for confirmed
issues are released as a tagged version, and the advisory is published with
credit to the reporter unless you ask otherwise.

## Scope

In scope:

- anything in `internal/` or `main.go`
- the admin GUI in `web/`
- `deploy.sh` and `scripts/install-web-tools.sh`
- the running service's authentication, authorization, and network surface

Out of scope:

- the upstream providers themselves; report those to them
- a deployment that exposes the admin port to the public internet without a
  reverse proxy and TLS. The gateway ships bound to `127.0.0.1` for a reason
- weaknesses in Go, SQLite, or any npm dependency with no reachable path
  through this code
- social engineering, physical access, and other non-software attacks
- self-inflicted issues, such as running as root when the systemd unit already
  provides an unprivileged service user

## Things you should know before deploying

These are design decisions, not vulnerabilities, but they are the things most
worth understanding before you expose this to a network. The README covers
them in more depth; this is the short version.

**Upstream provider keys are stored reversibly.** They have to stay usable
verbatim to be forwarded, so they live in the SQLite file without envelope
encryption. Keep `gateway.db` at mode `0600`, on a host you trust, and out of
version control. If your threat model needs provider credentials encrypted at
rest, this is the wrong tool.

**The admin GUI has no built-in TLS.** It binds to `127.0.0.1` and sets the
`Secure` flag on session cookies by default. If you put it behind a reverse
proxy, terminate TLS there and add your public host to `NANO_TRUSTED_ORIGINS`
or the GUI will reject its own mutations on Origin.

**There is no rate limit on the login endpoint by default.** There is per-IP +
email backoff, which blunts credential stuffing, but if you expose the admin
port to a wide network, put a rate limiter in front of it.

**`web_read` fetches URLs on behalf of authenticated users.** It has SSRF
protection, but treat the web tools as a privileged feature: enable them only
if you trust the client keys you have issued.
