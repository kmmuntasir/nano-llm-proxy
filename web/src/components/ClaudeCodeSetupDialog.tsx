import { useMemo, useState } from "react"
import {
  Box,
  Button,
  Code,
  Field,
  HStack,
  Input,
  Text,
  VStack,
} from "@chakra-ui/react"
import {
  DialogBody,
  DialogCloseTrigger,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogRoot,
  DialogTitle,
} from "./ui/dialog"
import { PasswordInput } from "./ui/password-input"
import CopyButton from "./CopyButton"
import { cleanModelId } from "./ModelCard"

// ClaudeCodeSetupDialog generates the `env` block for Claude Code's
// ~/.claude/settings.json: one searchable picker per ANTHROPIC_DEFAULT_*_MODEL
// slot, an optional client-key field, and a live JSON preview with a copy
// button. Everything stays client-side — the token is only embedded in the
// copied text, never sent anywhere.

export interface ClaudeCatalogEntry {
  id: string // full catalog id: "provider/model-1M-txt"
  context_window?: number
}

const ONE_M = 1_000_000

const SLOTS = [
  { key: "opus", label: "Opus", envKey: "ANTHROPIC_DEFAULT_OPUS_MODEL" },
  { key: "sonnet", label: "Sonnet", envKey: "ANTHROPIC_DEFAULT_SONNET_MODEL" },
  { key: "haiku", label: "Haiku", envKey: "ANTHROPIC_DEFAULT_HAIKU_MODEL" },
] as const

type SlotKey = (typeof SLOTS)[number]["key"]

// SlotPicker is the searchable dropdown for one model slot. Options show
// clean ids ("glm/glm-5.3"); the picked value keeps the full catalog entry.
function SlotPicker({
  entries,
  value,
  onChange,
}: {
  entries: ClaudeCatalogEntry[]
  value: string // full catalog id, "" = none
  onChange: (id: string) => void
}) {
  const [query, setQuery] = useState("")
  const [open, setOpen] = useState(false)
  const filtered = entries.filter((e) => cleanModelId(e.id).toLowerCase().includes(query.toLowerCase()))

  return (
    <Box position="relative">
      <Input
        autoComplete="off"
        placeholder="search models…"
        fontFamily="mono"
        value={open ? query : value ? cleanModelId(value) : ""}
        onChange={(e) => {
          setQuery(e.target.value)
          setOpen(true)
        }}
        onFocus={() => {
          setQuery("")
          setOpen(true)
        }}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
      />
      {open && (
        <Box
          position="absolute"
          zIndex={20}
          top="100%"
          left={0}
          right={0}
          mt={1}
          bg="bg.panel"
          borderWidth="1px"
          rounded="md"
          boxShadow="md"
          maxH="240px"
          overflowY="auto"
        >
          <Box
            px={3}
            py={2}
            fontSize="sm"
            cursor="pointer"
            _hover={{ bg: "bg.subtle" }}
            onClick={() => {
              onChange("")
              setOpen(false)
            }}
          >
            None — leave this slot unset
          </Box>
          {filtered.length === 0 && (
            <Text px={3} py={2} fontSize="sm" color="fg.muted">
              No models match “{query}”
            </Text>
          )}
          {filtered.map((e) => (
            <Box
              key={e.id}
              px={3}
              py={2}
              fontSize="sm"
              fontFamily="mono"
              cursor="pointer"
              _hover={{ bg: "bg.subtle" }}
              onClick={() => {
                onChange(e.id)
                setOpen(false)
              }}
            >
              {cleanModelId(e.id)}
            </Box>
          ))}
        </Box>
      )}
    </Box>
  )
}

export default function ClaudeCodeSetupDialog({
  open,
  onOpenChange,
  entries,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  entries: ClaudeCatalogEntry[]
}) {
  const [picked, setPicked] = useState<Record<SlotKey, string>>({ opus: "", sonnet: "", haiku: "" })
  const [token, setToken] = useState("")
  const origin = window.location.origin

  const json = useMemo(() => {
    // [1m] opts Claude Code into 1M-context accounting; it strips the suffix
    // before sending, and the gateway strips the cosmetic suffix too
    const slotValue = (id: string) => {
      const entry = entries.find((e) => e.id === id)
      const clean = cleanModelId(id)
      return entry && (entry.context_window ?? 0) >= ONE_M ? `${clean}[1m]` : clean
    }
    const env: Record<string, string> = {
      ANTHROPIC_BASE_URL: origin,
      ANTHROPIC_AUTH_TOKEN: token.trim() || "fg-paste-your-client-key",
    }
    for (const s of SLOTS) {
      if (picked[s.key]) env[s.envKey] = slotValue(picked[s.key])
    }
    return `"env": ${JSON.stringify(env, null, 2)}`
  }, [entries, origin, token, picked])

  return (
    <DialogRoot open={open} onOpenChange={(e) => onOpenChange(e.open)} size="lg">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Claude Code setup</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <VStack align="stretch" gap={4}>
            <Text fontSize="sm" color="fg.muted">
              Generates the <Code fontFamily="mono">env</Code> block for Claude Code's{" "}
              <Code fontFamily="mono">~/.claude/settings.json</Code>. Pick a model for each
              tier, then copy the block into your settings file.
            </Text>

            <Field.Root>
              <Field.Label>Client key</Field.Label>
              <PasswordInput
                autoComplete="new-password"
                placeholder="fg-…"
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
              <Field.HelperText>
                Optional — paste a key from Profile → My API Keys to embed it. It stays in
                your browser; nothing is sent to the server.
              </Field.HelperText>
            </Field.Root>

            <VStack align="stretch" gap={3}>
              {SLOTS.map((s) => (
                <Field.Root key={s.key}>
                  <Field.Label>{s.label}</Field.Label>
                  <SlotPicker
                    entries={entries}
                    value={picked[s.key]}
                    onChange={(id) => setPicked((cur) => ({ ...cur, [s.key]: id }))}
                  />
                </Field.Root>
              ))}
            </VStack>

            <Text fontSize="xs" color="fg.muted">
              Models with ≥ 1M context get the <Code fontFamily="mono">[1m]</Code> suffix —
              Claude Code reads it for context accounting and strips it before sending.
            </Text>

            <Box borderWidth="1px" rounded="md" bg="bg.subtle">
              <HStack justify="space-between" px={3} py={2} borderBottomWidth="1px">
                <Text fontSize="xs" color="fg.muted" fontWeight="medium">
                  settings.json
                </Text>
                <CopyButton text={json} label="copy env block" size="xs" />
              </HStack>
              <Code
                as="pre"
                p={3}
                fontSize="xs"
                fontFamily="mono"
                whiteSpace="pre-wrap"
                wordBreak="break-all"
                display="block"
                bg="transparent"
                userSelect="all"
              >
                {json}
              </Code>
            </Box>
          </VStack>
        </DialogBody>
        <DialogFooter>
          <Button colorPalette="blue" onClick={() => onOpenChange(false)}>
            Done
          </Button>
        </DialogFooter>
        <DialogCloseTrigger />
      </DialogContent>
    </DialogRoot>
  )
}
