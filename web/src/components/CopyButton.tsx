import { useEffect, useRef, useState } from "react"
import { IconButton } from "@chakra-ui/react"
import { Check, Copy } from "lucide-react"

// CopyButton copies one string to the clipboard and flips to a check mark
// for a moment so the click visibly landed. Self-contained — no toasts, so
// grids of these (one per model card) don't spam notifications.
export default function CopyButton({
  text,
  label,
  size = "2xs",
}: {
  text: string
  label?: string
  size?: "2xs" | "xs" | "sm"
}) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout>>(null)

  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current)
  }, [])

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 1500)
    } catch {
      // clipboard unavailable (permissions/insecure context) — leave the icon
      // untouched; the string is selectable text anyway
    }
  }

  return (
    <IconButton
      variant="ghost"
      size={size}
      aria-label={label ?? `copy ${text}`}
      title={copied ? "Copied" : "Copy"}
      colorPalette={copied ? "green" : "gray"}
      onClick={copy}
    >
      {copied ? <Check /> : <Copy />}
    </IconButton>
  )
}
