import { Badge } from "@chakra-ui/react"

const palette: Record<string, { bg: string; fg: string }> = {
  healthy: { bg: "green.subtle", fg: "green.fg" },
  cooling: { bg: "orange.subtle", fg: "orange.fg" },
  disabled: { bg: "red.subtle", fg: "red.fg" },
}

export default function StatusBadge({ status }: { status: string }) {
  const c = palette[status] ?? { bg: "gray.subtle", fg: "gray.fg" }
  return (
    <Badge variant="subtle" bg={c.bg} color={c.fg} rounded="full" px={2}>
      {status}
    </Badge>
  )
}
