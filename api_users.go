package main

import (
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// User management endpoints (superadmin only). JSON views never include
// password hashes or key material.

type userJSON struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Disabled  bool   `json:"disabled"`
	CreatedAt int64  `json:"createdAt"`
	KeyCount  int64  `json:"keyCount"`
}

func userView(u *User) userJSON {
	return userJSON{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role, Disabled: u.Disabled, CreatedAt: u.CreatedAt}
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
	hash, ok := validateNewUser(w, &req.Name, &req.Email, &req.Password, &req.Role)
	if !ok {
		return
	}
	u := &User{Name: req.Name, Email: req.Email, PasswordHash: hash, Role: req.Role}
	if !g.applyMutation(w, actor, "user.create", func() error { return g.store.InsertUser(u) }) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": userView(u)})
}

func validateNewUser(w http.ResponseWriter, name, email, password, role *string) (string, bool) {
	*name = strings.TrimSpace(*name)
	*email = strings.ToLower(strings.TrimSpace(*email))
	*role = strings.TrimSpace(*role)
	if name == nil || *name == "" || email == nil || *email == "" || !strings.Contains(*email, "@") {
		apiErr(w, http.StatusBadRequest, "name and a valid email are required")
		return "", false
	}
	if role == nil || *role == "" {
		*role = "user"
	}
	if *role != "user" && *role != "superadmin" {
		apiErr(w, http.StatusBadRequest, `role must be "user" or "superadmin"`)
		return "", false
	}
	if len(*password) < 10 {
		apiErr(w, http.StatusBadRequest, "password must be at least 10 characters")
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
		apiErr(w, http.StatusNotFound, "no such user")
		return
	}
	if req.Role != nil && *req.Role != "user" && *req.Role != "superadmin" {
		apiErr(w, http.StatusBadRequest, `role must be "user" or "superadmin"`)
		return
	}
	if req.Role != nil && target.Role == "superadmin" && *req.Role != "superadmin" {
		if n, _ := g.store.CountSuperadmins(); n <= 1 {
			apiErr(w, http.StatusConflict, "cannot demote the last superadmin")
			return
		}
	}
	if req.Disabled != nil && *req.Disabled && target.Role == "superadmin" {
		if n, _ := g.store.CountSuperadmins(); n <= 1 {
			apiErr(w, http.StatusConflict, "cannot disable the last superadmin")
			return
		}
	}
	var name, email *string
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		name = &n
	}
	if req.Email != nil {
		e := strings.ToLower(strings.TrimSpace(*req.Email))
		if !strings.Contains(e, "@") {
			apiErr(w, http.StatusBadRequest, "invalid email")
			return
		}
		email = &e
	}
	var passHash *string
	if req.Password != nil {
		if len(*req.Password) < 10 {
			apiErr(w, http.StatusBadRequest, "password must be at least 10 characters")
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
	if !g.applyMutation(w, actor, "user.update", func() error {
		if err := g.store.UpdateUser(id, name, email, passHash, req.Role, req.Disabled); err != nil {
			return err
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
		apiErr(w, http.StatusConflict, "cannot delete yourself")
		return
	}
	target, err := g.store.User(id)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if target == nil {
		apiErr(w, http.StatusNotFound, "no such user")
		return
	}
	if target.Role == "superadmin" {
		if n, _ := g.store.CountSuperadmins(); n <= 1 {
			apiErr(w, http.StatusConflict, "cannot delete the last superadmin")
			return
		}
	}
	if !g.applyMutation(w, actor, "user.delete", func() error { return g.store.DeleteUser(id) }) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
