package gateway

import (
	"net/http"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Roles. Superadmins and admins share every admin capability (the
// requireAdmin routes); the one difference is that admins can't change
// superadmin accounts — not deactivate or delete them, and also not demote,
// rename, reset the password of, or manage the client keys of one, since
// each of those would undo the restriction (demote, then deactivate) or take
// the account over. For the same reason only a superadmin can grant the
// superadmin role. Nobody can deactivate or delete their own account.
const (
	roleSuperadmin = "superadmin"
	roleAdmin      = "admin"
	roleUser       = "user"
)

// isAdminRole reports whether a role carries admin capabilities.
func isAdminRole(role string) bool {
	return role == roleSuperadmin || role == roleAdmin
}

func validRole(role string) bool {
	return role == roleSuperadmin || role == roleAdmin || role == roleUser
}

const roleErr = `Role must be "user", "admin" or "superadmin"`

// accountChangeDenied explains why actor may not change target's account,
// or returns "".
func accountChangeDenied(actor, target *store.User) string {
	if actor != nil && actor.Role != roleSuperadmin && target.Role == roleSuperadmin {
		return "Admins can't change superadmin accounts"
	}
	return ""
}

// roleGrantDenied explains why actor may not grant role, or returns "".
func roleGrantDenied(actor *store.User, role string) string {
	if role == roleSuperadmin && (actor == nil || actor.Role != roleSuperadmin) {
		return "Only a superadmin can grant the superadmin role"
	}
	return ""
}

// managedUserID resolves the {id} path user for an admin mutation on that
// user's resources (client keys), applying accountChangeDenied. ok=false
// means the response has been written.
func (g *gateway) managedUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return 0, false
	}
	target, err := g.store.User(id)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return 0, false
	}
	if target == nil {
		apiErr(w, http.StatusNotFound, "No such user")
		return 0, false
	}
	if msg := accountChangeDenied(contextUser(r), target); msg != "" {
		apiErr(w, http.StatusForbidden, msg)
		return 0, false
	}
	return id, true
}
