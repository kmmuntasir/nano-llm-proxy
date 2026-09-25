import { useMemo, useState } from "react"
import { Button, Code, Field, Text, VStack } from "@chakra-ui/react"
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
import ComboSelect from "./ComboSelect"
import JsonBlock from "./JsonBlock"
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

// SlotPicker is one model slot: the tier name inline in the input (the
// options are long; a label above would waste a whole row), searchable,
// keyboard-navigable (arrows + Enter).
function SlotPicker({
  entries,
  value,
  onChange,
  label,
}: {
  entries: ClaudeCatalogEntry[]
  value: string // full catalog id, "" = none
  onChange: (id: string) => void
  label: string
}) {
  const options = entries.map((e) => ({
    value: e.id,
    label: cleanModelId(e.id),
    search: e.id, // the raw id stays findable even with the suffix hidden
  }))
  return (
    <ComboSelect
      options={options}
      value={value}
      onChange={onChange}
      noneLabel="None — leave this slot unset"
      emptyText="No models match"
      mono
      ariaLabel={`${label} model`}
      startElement={
        <Text fontSize="xs" color="fg.muted">
          {label}
        </Text>
      }
    />
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
    <DialogRoot open={open} onOpenChange={(e) => onOpenChange(e.open)} size="xl">
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
                <SlotPicker
                  key={s.key}
                  entries={entries}
                  label={s.label}
                  value={picked[s.key]}
                  onChange={(id) => setPicked((cur) => ({ ...cur, [s.key]: id }))}
                />
              ))}
            </VStack>

            <Text fontSize="xs" color="fg.muted">
              Models with ≥ 1M context get the <Code fontFamily="mono">[1m]</Code> suffix —
              Claude Code reads it for context accounting and strips it before sending.
            </Text>

            <JsonBlock code={json} title="settings.json" copyLabel="copy env block" />
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
