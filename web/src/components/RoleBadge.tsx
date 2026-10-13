import { Badge } from "@chakra-ui/react"
import { roleLabel } from "../api/roles"
import type { Role } from "../api/roles"

// RoleBadge: Superadmin solid, Admin subtle purple, User subtle gray.
export default function RoleBadge({ role }: { role: Role }) {
  if (role === "superadmin") return <Badge variant="solid">{roleLabel(role)}</Badge>
  if (role === "admin") {
    return (
      <Badge variant="subtle" colorPalette="purple">
        {roleLabel(role)}
      </Badge>
    )
  }
  return <Badge variant="subtle">{roleLabel(role)}</Badge>
}
