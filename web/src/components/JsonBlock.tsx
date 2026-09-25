import { Box, Code, HStack, Text } from "@chakra-ui/react"
import type { ReactNode } from "react"
import CopyButton from "./CopyButton"

// JsonBlock renders a JSON snippet with lightweight syntax highlighting on a
// fixed dark surface — the token colors need a stable background, so the
// block doesn't follow the light/dark theme. Used for the generated
// settings.json preview.

// Colors: GitHub dark palette — keys green, strings blue, numbers orange,
// keywords purple, everything else the base foreground.
const COLORS = {
  bg: "#0d1117",
  fg: "#c9d1d9",
  border: "#21262d",
  key: "#7ee787",
  str: "#a5d6ff",
  num: "#ffa657",
  kw: "#d2a8ff",
}

// one pass over the JSON text: strings (distinguishing keys by a following
// colon), numbers, and the true/false/null keywords
const TOKEN_RE = /("(?:\\.|[^"\\])*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b/g

function highlight(code: string): ReactNode[] {
  const out: ReactNode[] = []
  let last = 0
  let k = 0
  for (const m of code.matchAll(TOKEN_RE)) {
    const start = m.index ?? 0
    if (start > last) out.push(<span key={k++}>{code.slice(last, start)}</span>)
    if (m[1] !== undefined) {
      const color = m[2] !== undefined ? COLORS.key : COLORS.str
      out.push(
        <span key={k++} style={{ color }}>
          {m[1]}
        </span>,
      )
      if (m[2] !== undefined) out.push(<span key={k++}>{m[2]}</span>)
    } else if (m[3] !== undefined) {
      out.push(
        <span key={k++} style={{ color: COLORS.num }}>
          {m[3]}
        </span>,
      )
    } else if (m[4] !== undefined) {
      out.push(
        <span key={k++} style={{ color: COLORS.kw }}>
          {m[4]}
        </span>,
      )
    }
    last = start + m[0].length
  }
  if (last < code.length) out.push(<span key={k++}>{code.slice(last)}</span>)
  return out
}

export default function JsonBlock({
  code,
  title,
  copyLabel,
}: {
  code: string
  title?: string
  copyLabel?: string
}) {
  return (
    <Box borderWidth="1px" rounded="md" bg={COLORS.bg} borderColor={COLORS.border}>
      {(title || copyLabel) && (
        <HStack
          justify="space-between"
          px={3}
          py={2}
          borderBottomWidth="1px"
          borderColor={COLORS.border}
        >
          <Text fontSize="xs" color="#8b949e" fontWeight="medium">
            {title}
          </Text>
          {copyLabel && <CopyButton text={code} label={copyLabel} size="xs" />}
        </HStack>
      )}
      <Code
        as="pre"
        p={3}
        fontSize="xs"
        fontFamily="mono"
        whiteSpace="pre"
        display="block"
        bg="transparent"
        color={COLORS.fg}
        maxH="320px"
        overflow="auto"
        userSelect="all"
      >
        {highlight(code)}
      </Code>
    </Box>
  )
}
