package gateway

import (
	"fmt"
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// store.User management endpoints (admins only; roles.go limits what an
// admin may do to a superadmin account). JSON views never include
// password hashes or key material.

type userJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
	// GoogleLinked: the user has signed in with Google, so the name is
	// Google-owned (rewritten on every Google sign-in) and not editable.
	GoogleLinked bool   `json:"googleLinked"`
	AvatarURL    string `json:"avatarUrl,omitempty"` // Google profile picture
	CreatedAt    int64  `json:"createdAt"`
	KeyCount     int64  `json:"keyCount"`
}

func userView(u *store.User) userJSON {
	return userJSON{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role, Disabled: u.Disabled, GoogleLinked: u.GoogleLinked, AvatarURL: u.AvatarURL, CreatedAt: u.CreatedAt}
}

func (g *gateway) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := g.store.ListUsers()
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]userJSON, 0, len(users))
	for _, u := range users {
		v := userView(&u.User)
		v.KeyCount = u.KeyCount
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (g *gateway) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	// with password sign-in off the account signs in with Google, so a
	// password is optional (one sent anyway is still validated and stored)
	hash, ok := validateNewUser(w, &req.Name, &req.Email, &req.Password, &req.Role, g.credentialLoginEnabled() || req.Password != "")
	if !ok {
		return
	}
	if msg := roleGrantDenied(actor, req.Role); msg != "" {
		apiErr(w, http.StatusForbidden, msg)
		return
	}
	u := &store.User{Name: req.Name, Email: req.Email, PasswordHash: hash, Role: req.Role}
	// A regular user starts without the providers the "new users" setting
	// disables; admins always have every provider, so they get no rows. The
	// defaults apply once, here: a later role change keeps the existing
	// behaviour (promotion clears revocations, demotion re-applies nothing).
	// Read from the store (like GET /api/settings), the authoritative copy.
	// The store skips ids of providers deleted since the setting was saved;
	// applyMutation's rebuild reloads the hot-path access cache.
	var denied []int64
	if req.Role == roleUser {
		rs, err := g.store.LoadRuntimeSettings()
		if err != nil {
			apiErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		denied = rs.NewUsers.DisabledProviderIDs
	}
	if !g.applyMutation(w, actor, "user.create", "An account with this email already exists", func() error {
		return g.store.InsertUserWithDeniedProviders(u, denied)
	}) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": userView(u)})
}

// validateNewUser normalizes and checks a create-user request and returns
// the password hash ("" when no password is required and none was given —
// an empty hash never verifies).
func validateNewUser(w http.ResponseWriter, name, email, password, role *string, requirePassword bool) (string, bool) {
	*name = strings.TrimSpace(*name)
	*email = strings.ToLower(strings.TrimSpace(*email))
	*role = strings.TrimSpace(*role)
	if name == nil || *name == "" || email == nil || *email == "" || !strings.Contains(*email, "@") {
		apiErr(w, http.StatusBadRequest, "Name and a valid email address are required")
		return "", false
	}
	if role == nil || *role == "" {
		*role = roleUser
	}
	if !validRole(*role) {
		apiErr(w, http.StatusBadRequest, roleErr)
		return "", false
	}
	if !requirePassword {
		return "", true
	}
	if len(*password) < 10 {
		apiErr(w, http.StatusBadRequest, "Password must be at least 10 characters")
		return "", false
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(*password), 10)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return "", false
	}
	return string(hash), true
}

func (g *gateway) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Name     *string `json:"name"`
		Email    *string `json:"email"`
		Password *string `json:"password"`
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	target, err := g.store.User(id)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if target == nil {
		apiErr(w, http.StatusNotFound, "That user no longer exists (it may have been deleted)")
		return
	}
	if msg := accountChangeDenied(actor, target); msg != "" {
		apiErr(w, http.StatusForbidden, msg)
		return
	}
	if req.Role != nil && !validRole(*req.Role) {
		apiErr(w, http.StatusBadRequest, roleErr)
		return
	}
	if req.Role != nil && *req.Role != target.Role {
		if msg := roleGrantDenied(actor, *req.Role); msg != "" {
			apiErr(w, http.StatusForbidden, msg)
			return
		}
	}
	if req.Disabled != nil && *req.Disabled && actor.ID == target.ID {
		apiErr(w, http.StatusConflict, "You cannot deactivate your own account")
		return
	}
	if req.Role != nil && target.Role == "superadmin" && *req.Role != "superadmin" {
		if n, _ := g.store.CountSuperadmins(); n <= 1 {
			apiErr(w, http.StatusConflict, "Cannot demote the last remaining superadmin")
			return
		}
	}
	if req.Disabled != nil && *req.Disabled && target.Role == "superadmin" {
		if n, _ := g.store.CountSuperadmins(); n <= 1 {
			apiErr(w, http.StatusConflict, "Cannot disable the last remaining superadmin")
			return
		}
	}
	var name, email *string
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if target.GoogleLinked && n != target.Name {
			apiErr(w, http.StatusBadRequest, "This user's name comes from their Google account")
			return
		}
		name = &n
	}
	if req.Email != nil {
		e := strings.ToLower(strings.TrimSpace(*req.Email))
		if !strings.Contains(e, "@") {
			apiErr(w, http.StatusBadRequest, "Invalid email address")
			return
		}
		email = &e
	}
	var passHash *string
	if req.Password != nil {
		if len(*req.Password) < 10 {
			apiErr(w, http.StatusBadRequest, "Password must be at least 10 characters")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), 10)
		if err != nil {
			apiErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		h := string(hash)
		passHash = &h
	}
	killSessions := passHash != nil || (req.Disabled != nil && *req.Disabled)
	if !g.applyMutation(w, actor, "user.update", "An account with this email already exists", func() error {
		if err := g.store.UpdateUser(id, name, email, passHash, req.Role, req.Disabled); err != nil {
			return err
		}
		if req.Role != nil && isAdminRole(*req.Role) && !isAdminRole(target.Role) {
			if err := g.store.ClearUserProviderAccess(id); err != nil {
				return err
			}
		}
		if killSessions {
			return g.store.DeleteUserSessions(id)
		}
		return nil
	}) {
		return
	}
	updated, _ := g.store.User(id)
	writeJSON(w, http.StatusOK, map[string]any{"user": userView(updated)})
}

func (g *gateway) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor := contextUser(r)
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if actor.ID == id {
		apiErr(w, http.StatusConflict, "You cannot delete your own account")
		return
	}
	target, err := g.store.User(id)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if target == nil {
		apiErr(w, http.StatusNotFound, "That user no longer exists (it may have been deleted)")
		return
	}
	if msg := accountChangeDenied(actor, target); msg != "" {
		apiErr(w, http.StatusForbidden, msg)
		return
	}
	if target.Role == roleSuperadmin {
		if n, _ := g.store.CountSuperadmins(); n <= 1 {
			apiErr(w, http.StatusConflict, "Cannot delete the last remaining superadmin")
			return
		}
	}
	if !g.applyMutation(w, actor, "user.delete", "", func() error { return g.store.DeleteUser(id) }) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// maxUserNameLen bounds self-service display names.
const maxUserNameLen = 100

// handleUpdateMe serves PATCH /api/me: a signed-in user renames themself.
// Google-linked accounts can't — their name is taken from Google on every
// Google sign-in, so an edit would silently revert.
func (g *gateway) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	me := contextUser(r)
	var req struct {
		Name *string `json:"name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name == nil {
		apiErr(w, http.StatusBadRequest, "Name is required")
		return
	}
	name := strings.TrimSpace(*req.Name)
	switch {
	case name == "":
		apiErr(w, http.StatusBadRequest, "Name is required")
		return
	case utf8.RuneCountInString(name) > maxUserNameLen:
		apiErr(w, http.StatusBadRequest, fmt.Sprintf("Name must be at most %d characters", maxUserNameLen))
		return
	case me.GoogleLinked:
		apiErr(w, http.StatusForbidden, "Your name comes from your Google account")
		return
	}
	if err := g.store.UpdateUser(me.ID, &name, nil, nil, nil, nil); err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	u, err := g.store.User(me.ID)
	if err != nil || u == nil {
		apiErr(w, http.StatusInternalServerError, "reload user failed")
		return
	}
	log.Printf("user=%s action=profile.rename", u.Email)
	writeJSON(w, http.StatusOK, map[string]any{"user": userView(u)})
}
