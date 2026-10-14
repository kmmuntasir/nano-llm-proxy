import type { ReactNode } from "react"
import { Code, Em, Link, Strong } from "@chakra-ui/react"

// InlineMarkdown renders the small inline subset the changelog uses —
// `code`, **bold**, *italic*, [text](https://…) — as React nodes. It never
// produces HTML strings: anything outside the subset stays plain text, and
// links are only emitted for http(s) URLs (others render their label only).

const ESCAPABLE = "\\`*_[]()"

export function isHttpURL(url: string): boolean {
  return /^https?:\/\/\S+$/i.test(url)
}

function parse(text: string, keyPrefix: string): ReactNode[] {
  const nodes: ReactNode[] = []
  let plain = ""
  let i = 0

  const flush = () => {
    if (plain) nodes.push(plain)
    plain = ""
  }
  const key = () => `${keyPrefix}-${nodes.length}`

  while (i < text.length) {
    const ch = text[i]

    if (ch === "\\" && i + 1 < text.length && ESCAPABLE.includes(text[i + 1])) {
      plain += text[i + 1]
      i += 2
      continue
    }

    if (ch === "`") {
      const end = text.indexOf("`", i + 1)
      if (end > i + 1) {
        flush()
        nodes.push(
          <Code key={key()} fontFamily="mono" wordBreak="break-word">
            {text.slice(i + 1, end)}
          </Code>,
        )
        i = end + 1
        continue
      }
    }

    if (text.startsWith("**", i)) {
      const end = text.indexOf("**", i + 2)
      if (end > i + 2) {
        flush()
        const k = key()
        nodes.push(<Strong key={k}>{parse(text.slice(i + 2, end), k)}</Strong>)
        i = end + 2
        continue
      }
      plain += "**"
      i += 2
      continue
    }

    if (ch === "*") {
      const end = text.indexOf("*", i + 1)
      const inner = end > i + 1 ? text.slice(i + 1, end) : ""
      if (inner && inner.trim() === inner) {
        flush()
        const k = key()
        nodes.push(<Em key={k}>{parse(inner, k)}</Em>)
        i = end + 1
        continue
      }
    }

    if (ch === "[") {
      const mid = text.indexOf("](", i + 1)
      const end = mid > i ? text.indexOf(")", mid + 2) : -1
      if (mid > i + 1 && end > mid + 2) {
        flush()
        const label = text.slice(i + 1, mid)
        const url = text.slice(mid + 2, end).trim()
        const k = key()
        if (isHttpURL(url)) {
          nodes.push(
            <Link
              key={k}
              href={url}
              target="_blank"
              rel="noopener noreferrer"
              colorPalette="blue"
              variant="underline"
              wordBreak="break-word"
            >
              {parse(label, k)}
            </Link>,
          )
        } else {
          nodes.push(...parse(label, k))
        }
        i = end + 1
        continue
      }
    }

    plain += ch
    i++
  }

  flush()
  return nodes
}

export default function InlineMarkdown({ text }: { text: string }) {
  return <>{parse(text, "md")}</>
}
