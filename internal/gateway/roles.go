package gateway

import (
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Roles. Superadmins and admins share every admin capability (the
// requireAdmin routes); the one difference is that admins can't change
// superadmin accounts — not deactivate or delete them, and also not demote,
// rename or reset the password of one, since each of those would undo the
// restriction (demote, then deactivate) or take the account over. For the
// same reason only a superadmin can grant the superadmin role. Nobody can
// deactivate or delete their own account, and nobody manages another user's
// client keys (admins can only view them).
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
