package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"github.com/kmmuntasir/nano-llm-proxy/internal/config"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "fg_session"
	sessionTTL    = 30 * 24 * time.Hour
	touchInterval = 24 * time.Hour // sliding-expiry write throttle
)

type ctxUserKeyType struct{}

var ctxUserKey ctxUserKeyType

// requireSession authenticates GUI/API requests via the session cookie and
// puts the user on the request context. Mutating methods also pass an Origin
// check (CSRF).
func (g *gateway) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.isLocal() {
			g.localSession(next, w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !g.originAllowed(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"message": "origin not allowed"}})
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"message": "login required"}})
			return
		}
		hash := store.HashSecret(c.Value)
		sess, user, err := g.store.SessionByHash(hash)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"message": "session lookup failed"}})
			return
		}
		if sess == nil || user == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"message": "login required"}})
			return
		}
		if time.Since(time.Unix(sess.LastSeen, 0)) > touchInterval {
			g.store.TouchSession(hash, sessionTTL) //nolint:errcheck
		}
		next(w, r.WithContext(withUser(r.Context(), user)))
	}
}

func withUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, ctxUserKey, u)
}

// requireAdmin admits superadmins and admins — they share every admin
// route; the one admin restriction (no changes to superadmin accounts) is
// enforced per handler (roles.go).
func (g *gateway) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := r.Context().Value(ctxUserKey).(*store.User)
		if !ok || !isAdminRole(user.Role) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"message": "admin required"}})
			return
		}
		next(w, r)
	}
}

func contextUser(r *http.Request) *store.User {
	user, _ := r.Context().Value(ctxUserKey).(*store.User)
	return user
}

// originAllowed guards against cross-site state changes. No Origin header =
// curl/healthchecks, fine. Behind a reverse proxy the backend may see Host
// 127.0.0.1:8787 while the browser sends Origin https://gateway.example.com —
// so the configured trustedOrigins list is accepted alongside r.Host.
func (g *gateway) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(u.Host), []byte(r.Host)) == 1 {
		return true
	}
	if g.isLocal() {
		return false // no session to ride on: only the page's own origin
	}
	for _, trusted := range g.conf().TrustedOrigins {
		if strings.EqualFold(u.Host, trusted) || strings.EqualFold(origin, "https://"+trusted) {
			return true
		}
	}
	return false
}

// --- login rate limiting ---

type failState struct {
	count    int
	lastFail time.Time
	lockedTo time.Time
}

// loginBackoff: 5 failures within 15 minutes locks the ip|email pair for
// 15 minutes. In-memory — restart clears it, fine for a single-node deployment.
type loginBackoff struct {
	mu    sync.Mutex
	fails map[string]*failState
}

const (
	backoffMaxFails   = 5
	backoffWindow     = 15 * time.Minute
	backoffLockoutFor = 15 * time.Minute
)

func newLoginBackoff() loginBackoff { return loginBackoff{fails: map[string]*failState{}} }

func backoffKey(r *http.Request, email string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host + "|" + strings.ToLower(email)
}

func (b *loginBackoff) allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.fails[key]
	if !ok {
		return true
	}
	if time.Now().Before(st.lockedTo) {
		return false
	}
	if time.Since(st.lastFail) > backoffWindow {
		delete(b.fails, key) // window expired, fresh start
	}
	return true
}

func (b *loginBackoff) fail(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.fails[key]
	if !ok || time.Since(st.lastFail) > backoffWindow {
		st = &failState{}
		b.fails[key] = st
	}
	st.count++
	st.lastFail = time.Now()
	if st.count >= backoffMaxFails {
		st.lockedTo = time.Now().Add(backoffLockoutFor)
	}
}

func (b *loginBackoff) reset(key string) {
	b.mu.Lock()
	delete(b.fails, key)
	b.mu.Unlock()
}

// --- auth handlers ---

func (g *gateway) handleLogin(w http.ResponseWriter, r *http.Request) {
	if g.isLocal() {
		apiErr(w, http.StatusNotFound, "Local mode has no sign-in")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !g.credentialLoginEnabled() {
		apiErr(w, http.StatusForbidden, "Password sign-in is disabled — sign in with Google")
		return
	}
	key := backoffKey(r, req.Email)
	if !g.backoff.allow(key) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]string{"message": "too many failed attempts — try again later"}})
		return
	}
	user, err := g.store.UserByEmail(strings.ToLower(strings.TrimSpace(req.Email)))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"message": "login failed"}})
		return
	}
	if user == nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil || user.Disabled {
		g.backoff.fail(key)
		// identical response for unknown email / wrong password / disabled
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"message": "Invalid email or password"}})
		return
	}
	g.backoff.reset(key)
	g.startSession(w, r, user, "password")
}

// startSession creates a session for an authenticated user, sets the
// cookie, and answers with the user — shared by every sign-in method so
// they all produce the identical session.
func (g *gateway) startSession(w http.ResponseWriter, r *http.Request, user *store.User, method string) {
	raw, exp, err := g.store.CreateSession(user.ID, sessionTTL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"message": "session create failed"}})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    raw,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   g.conf().SecureCookies(),
	})
	log.Printf("gui login user=%s method=%s ip=%s", user.Email, method, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]any{"user": userView(user)})
}

// credentialLoginEnabled is the effective password sign-in switch: the
// runtime setting, forced on whenever Google sign-in isn't configured so a
// stored false can never lock every account out.
func (g *gateway) credentialLoginEnabled() bool {
	return g.rs().Auth.CredentialLogin || !g.conf().GoogleEnabled()
}

// handleAuthConfig serves GET /api/auth/config (unauthenticated): which
// sign-in methods the login page should offer. The client id is public by
// design — Google Identity Services needs it in the browser.
func (g *gateway) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"credentialLogin": g.credentialLoginEnabled(), "mode": config.ModeServer}
	if g.isLocal() {
		out["mode"] = config.ModeLocal
		writeJSON(w, http.StatusOK, out)
		return
	}
	if c := g.conf(); c.GoogleEnabled() {
		google := map[string]string{"clientId": c.GoogleClientID}
		if c.GoogleWorkspaceDomain != "" {
			google["domain"] = c.GoogleWorkspaceDomain
		}
		out["google"] = google
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGoogleLogin serves POST /api/auth/google: verify the Google ID token,
// then sign in the existing account with that email. Accounts are only ever
// created by an admin on the Users page — an unknown email is refused,
// so no Google account can mint itself access to the upstream keys. A
// configured Workspace domain is an additional restriction.
func (g *gateway) handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	if g.google == nil || g.isLocal() {
		apiErr(w, http.StatusNotFound, "Google sign-in is not configured")
		return
	}
	if !g.originAllowed(r) {
		apiErr(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var req struct {
		Credential string `json:"credential"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Credential == "" {
		apiErr(w, http.StatusBadRequest, "credential is required")
		return
	}
	key := backoffKey(r, "google")
	if !g.backoff.allow(key) {
		apiErr(w, http.StatusTooManyRequests, "too many failed attempts — try again later")
		return
	}
	claims, err := g.google.verify(r.Context(), req.Credential)
	if err != nil {
		if !errors.Is(err, errGoogleToken) {
			log.Printf("google login: %v", err)
			apiErr(w, http.StatusServiceUnavailable, "Couldn't reach Google to verify the sign-in — try again")
			return
		}
		g.backoff.fail(key)
		log.Printf("google login rejected ip=%s: %v", r.RemoteAddr, err)
		apiErr(w, http.StatusUnauthorized, "Google sign-in failed")
		return
	}
	domain := g.conf().GoogleWorkspaceDomain
	if !googleDomainAllowed(claims, domain) {
		g.backoff.fail(key)
		log.Printf("google login rejected email=%s ip=%s: outside domain %s", claims.Email, r.RemoteAddr, domain)
		apiErr(w, http.StatusForbidden, "This Google account is outside the "+domain+" domain")
		return
	}
	user, err := g.store.UserByEmail(claims.Email)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, "login failed")
		return
	}
	if user == nil {
		g.backoff.fail(key)
		log.Printf("google login rejected email=%s ip=%s: no account", claims.Email, r.RemoteAddr)
		apiErr(w, http.StatusForbidden, "No account for this email — ask an admin to add you")
		return
	}
	if user.Disabled {
		apiErr(w, http.StatusForbidden, "This account is disabled")
		return
	}
	// every Google sign-in marks the account Google-linked and refreshes its
	// name and picture from the Google profile
	if user, err = g.store.LinkGoogleUser(user.ID, strings.TrimSpace(claims.Name), googleAvatarURL(claims)); err != nil || user == nil {
		log.Printf("google login: link %s: %v", claims.Email, err)
		apiErr(w, http.StatusInternalServerError, "login failed")
		return
	}
	g.backoff.reset(key)
	g.startSession(w, r, user, "google")
}

func (g *gateway) handleLogout(w http.ResponseWriter, r *http.Request) {
	if g.isLocal() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true}) // nothing to sign out of
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		g.store.DeleteSession(store.HashSecret(c.Value)) //nolint:errcheck
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: g.conf().SecureCookies(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (g *gateway) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": userView(contextUser(r))})
}

// resetAdminPassword is the operator escape hatch (-reset-admin-password):
// sets the password of the $ADMIN_EMAIL account to $ADMIN_PASSWORD and kills
// its sessions.
// Only ever run by hand on the box.
func ResetAdminPassword(st *store.Store) {
	email, pass := os.Getenv("ADMIN_EMAIL"), os.Getenv("ADMIN_PASSWORD")
	if email == "" || pass == "" {
		log.Fatalf("reset-admin-password needs ADMIN_EMAIL and ADMIN_PASSWORD in the environment")
	}
	if len(pass) < 10 {
		log.Fatalf("ADMIN_PASSWORD too short (min 10 chars)")
	}
	u, err := st.UserByEmail(strings.ToLower(email))
	if err != nil || u == nil {
		log.Fatalf("no user %s in the database", email)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), 10)
	if err != nil {
		log.Fatalf("hash: %v", err)
	}
	hashStr := string(hash)
	if err := st.UpdateUser(u.ID, nil, nil, &hashStr, nil, nil); err != nil {
		log.Fatalf("update: %v", err)
	}
	if err := st.DeleteUserSessions(u.ID); err != nil {
		log.Fatalf("session wipe: %v", err)
	}
	log.Printf("password reset for %s; all their sessions were killed", email)
}

// handleChangeMyPassword serves POST /api/me/password: self-service reset
// gated on the current password. All sessions are wiped afterwards, so the
// user (and anyone holding a stolen session cookie) logs in again.
func (g *gateway) handleChangeMyPassword(w http.ResponseWriter, r *http.Request) {
	u := contextUser(r)
	// Google-linked accounts sign in with Google; they don't manage a password
	if u.GoogleLinked {
		apiErr(w, http.StatusForbidden, "Your account signs in with Google")
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < 10 {
		apiErr(w, http.StatusBadRequest, "New password must be at least 10 characters")
		return
	}
	fresh, err := g.store.User(u.ID)
	if err != nil || fresh == nil {
		apiErr(w, http.StatusInternalServerError, "store.User lookup failed")
		return
	}
	if fresh.Disabled {
		apiErr(w, http.StatusForbidden, "Your account is disabled")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(fresh.PasswordHash), []byte(req.CurrentPassword)) != nil {
		apiErr(w, http.StatusForbidden, "Your current password is incorrect")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 10)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	hs := string(hash)
	if err := g.store.UpdateUser(u.ID, nil, nil, &hs, nil, nil); err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := g.store.DeleteUserSessions(u.ID); err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("admin user=%s action=password.self", u.Email)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
