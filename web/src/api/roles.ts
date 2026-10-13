import type { UserView } from "./types"

// Role rules mirrored from internal/gateway/roles.go. The server enforces
// them; these only decide what the GUI offers.

export type Role = UserView["role"]

const ALL_ROLES: Role[] = ["superadmin", "admin", "user"]
const ADMIN_GRANTABLE_ROLES: Role[] = ["admin", "user"]

/** Superadmins and admins share every admin page and capability. */
export function isAdminRole(role: Role | undefined): boolean {
  return role === "superadmin" || role === "admin"
}

/** Admins can't change superadmin accounts; superadmins can change anyone's. */
export function canManageAccount(actor: Pick<UserView, "role">, target: Pick<UserView, "role">): boolean {
  return actor.role === "superadmin" || target.role !== "superadmin"
}

export function roleLabel(role: Role): string {
  switch (role) {
    case "superadmin":
      return "Superadmin"
    case "admin":
      return "Admin"
    case "user":
      return "User"
  }
}

/** Only a superadmin can grant the superadmin role. */
export function assignableRoles(actor: Pick<UserView, "role">): Role[] {
  return actor.role === "superadmin" ? ALL_ROLES : ADMIN_GRANTABLE_ROLES
}
