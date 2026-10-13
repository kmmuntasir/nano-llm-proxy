import type { ComponentProps } from "react"
import { Avatar } from "@chakra-ui/react"

// Initials use these palettes so different users are told apart at a glance.
// Avatar's default "subtle" variant pairs colorPalette.muted with
// colorPalette.fg, which reads well in both color modes.
const PALETTES = ["red", "orange", "green", "teal", "blue", "cyan", "purple", "pink"] as const

function paletteFor(name: string) {
  let hash = 0
  for (const ch of name) {
    hash = (hash * 31 + (ch.codePointAt(0) ?? 0)) >>> 0
  }
  return PALETTES[hash % PALETTES.length]
}

// Chakra's built-in initials split on single spaces and don't uppercase, so
// they're computed here: first letters of the first and last word, max 2.
function initialsOf(name: string) {
  const words = name.trim().split(/\s+/).filter(Boolean)
  if (words.length === 0) return ""
  const first = Array.from(words[0])[0] ?? ""
  const last = words.length > 1 ? (Array.from(words[words.length - 1])[0] ?? "") : ""
  return (first + last).toUpperCase()
}

interface UserAvatarProps {
  name: string
  avatarUrl?: string
  size?: ComponentProps<typeof Avatar.Root>["size"]
}

// UserAvatar shows the Google profile picture when there is one and falls back
// to colored initials (also when the image fails to load). Google avatar URLs
// often 403 when a referrer is sent, hence no-referrer.
export default function UserAvatar({ name, avatarUrl, size = "sm" }: UserAvatarProps) {
  const initials = initialsOf(name)
  return (
    <Avatar.Root size={size} colorPalette={paletteFor(name)} flexShrink={0}>
      <Avatar.Fallback>{initials || undefined}</Avatar.Fallback>
      {avatarUrl && <Avatar.Image src={avatarUrl} alt="" referrerPolicy="no-referrer" />}
    </Avatar.Root>
  )
}
