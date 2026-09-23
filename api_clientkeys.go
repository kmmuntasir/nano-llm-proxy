package main

import (
	"net/http"
)

// Client API key endpoints. /api/me/keys is self-service for every user;
// /api/users/{id}/keys is the superadmin equivalent for any target user.

type clientKeyJSON struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"userId"`
	KeyHint      string `json:"keyHint"`
	Alias        string `json:"alias"`
	Disabled     bool   `json:"disabled"`
	CreatedAt    int64  `json:"createdAt"`
	LastUsedAt   int64  `json:"lastUsedAt"`
	RequestCount int64  `json:"requestCount"`
}

func clientKeyView(k ClientKey) clientKeyJSON {
	return clientKeyJSON{
		ID: k.ID, UserID: k.UserID, KeyHint: k.KeyHint, Alias: k.Alias,
		Disabled: k.Disabled, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
		RequestCount: k.RequestCount,
	}
}

func (g *gateway) handleListMyKeys(w http.ResponseWriter, r *http.Request) {
	g.listKeysFor(w, contextUser(r).ID)
}

func (g *gateway) handleCreateMyKey(w http.ResponseWriter, r *http.Request) {
	g.createKeyFor(w, r, contextUser(r), contextUser(r).ID)
}

func (g *gateway) handlePatchMyKey(w http.ResponseWriter, r *http.Request) {
	g.patchKeyFor(w, r, contextUser(r).ID)
}

func (g *gateway) handleDeleteMyKey(w http.ResponseWriter, r *http.Request) {
	g.deleteKeyFor(w, r, contextUser(r).ID)
}

// --- superadmin variants (target = path user id) ---

func (g *gateway) handleListUserKeys(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	g.listKeysFor(w, id)
}

func (g *gateway) handleCreateUserKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	g.createKeyFor(w, r, contextUser(r), id)
}

func (g *gateway) handlePatchUserKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	g.patchKeyFor(w, r, id)
}

func (g *gateway) handleDeleteUserKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	g.deleteKeyFor(w, r, id)
}

// --- shared internals ---

func (g *gateway) listKeysFor(w http.ResponseWriter, userID int64) {
	keys, err := g.store.ListClientKeys(userID)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]clientKeyJSON, 0, len(keys))
	for _, k := range keys {
		out = append(out, clientKeyView(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (g *gateway) createKeyFor(w http.ResponseWriter, r *http.Request, actor *User, targetUserID int64) {
	var req struct {
		Alias string `json:"alias"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Alias) > 100 {
		apiErr(w, http.StatusBadRequest, "alias too long (max 100)")
		return
	}
	var (
		plaintext string
		ck        ClientKey
		err       error
	)
	if !g.applyMutation(w, actor, "key.create", func() error {
		plaintext, ck, err = g.store.CreateClientKey(targetUserID, req.Alias)
		return err
	}) {
		return
	}
	// the plaintext appears exactly once, in this response
	writeJSON(w, http.StatusCreated, map[string]any{"key": plaintext, "entry": clientKeyView(ck)})
}

func (g *gateway) patchKeyFor(w http.ResponseWriter, r *http.Request, ownerID int64) {
	actor := contextUser(r)
	keyID, ok := pathID(w, r, "keyId")
	if !ok {
		return
	}
	existing, err := g.store.ClientKey(keyID)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil || existing.UserID != ownerID {
		apiErr(w, http.StatusNotFound, "no such key")
		return
	}
	var req struct {
		Alias    *string `json:"alias"`
		Disabled *bool   `json:"disabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Alias != nil && len(*req.Alias) > 100 {
		apiErr(w, http.StatusBadRequest, "alias too long (max 100)")
		return
	}
	if !g.applyMutation(w, actor, "key.update", func() error {
		if req.Alias != nil {
			if err := g.store.UpdateClientKeyAlias(keyID, *req.Alias); err != nil {
				return err
			}
		}
		if req.Disabled != nil {
			return g.store.SetClientKeyDisabled(keyID, *req.Disabled)
		}
		return nil
	}) {
		return
	}
	updated, _ := g.store.ClientKey(keyID)
	writeJSON(w, http.StatusOK, map[string]any{"entry": clientKeyView(*updated)})
}

func (g *gateway) deleteKeyFor(w http.ResponseWriter, r *http.Request, ownerID int64) {
	actor := contextUser(r)
	keyID, ok := pathID(w, r, "keyId")
	if !ok {
		return
	}
	existing, err := g.store.ClientKey(keyID)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil || existing.UserID != ownerID {
		apiErr(w, http.StatusNotFound, "no such key")
		return
	}
	if !g.applyMutation(w, actor, "key.delete", func() error { return g.store.DeleteClientKey(keyID) }) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
