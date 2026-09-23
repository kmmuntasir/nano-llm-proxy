package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// Shared plumbing for the /api/* JSON endpoints (GUI backend).

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// readJSON decodes a bounded JSON body; on failure it has already written the
// error response, so callers just `return`.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		apiErr(w, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func apiErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": msg}})
}

// pathID parses a numeric path value; writes the error response itself.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		apiErr(w, http.StatusBadRequest, "Invalid ID in the request path")
		return 0, false
	}
	return id, true
}

func actorEmail(u *User) string {
	if u == nil {
		return "?"
	}
	return u.Email
}

// isUniqueViolation maps SQLite UNIQUE constraint errors to 409s.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// applyMutation runs one store write and refreshes every in-memory structure
// the hot path reads — GUI changes are visible to /v1/* before the response.
// conflictMsg is the sentence shown when the write hits a uniqueness
// constraint (e.g. "An account with this email already exists") — each call
// site knows which field collided. Returns false if the error response was
// already written.
func (g *gateway) applyMutation(w http.ResponseWriter, user *User, action, conflictMsg string, write func() error) bool {
	if err := write(); err != nil {
		switch {
		case isUniqueViolation(err):
			msg := conflictMsg
			if msg == "" {
				msg = "That record already exists"
			}
			apiErr(w, http.StatusConflict, msg)
		case err == ErrBuiltinProvider:
			apiErr(w, http.StatusConflict, "Built-in providers cannot be deleted")
		case err == ErrNotFound:
			apiErr(w, http.StatusNotFound, "That record no longer exists (it may have been deleted)")
		default:
			apiErr(w, http.StatusInternalServerError, err.Error())
		}
		return false
	}
	if err := g.rebuildPools(); err != nil {
		log.Printf("admin user=%s action=%s REBUILD FAILED: %v", actorEmail(user), action, err)
		apiErr(w, http.StatusInternalServerError, "Saved to the database, but applying it live failed: "+err.Error())
		return false
	}
	log.Printf("admin user=%s action=%s", actorEmail(user), action)
	return true
}
