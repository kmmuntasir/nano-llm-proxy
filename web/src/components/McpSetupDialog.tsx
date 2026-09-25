import { useMemo, useState } from "react"
import { Button, Code, Field, Input, Text, VStack } from "@chakra-ui/react"
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
import JsonBlock from "./JsonBlock"

// McpSetupDialog generates the client-side config for the gateway's /mcp
// web tools (web_search + web_read): a one-liner for Claude Code, plus
// .mcp.json / opencode / Kilo snippets. Everything stays client-side — the
// key is only embedded in the copied text, never sent anywhere.

export default function McpSetupDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const [token, setToken] = useState("")
  const [name, setName] = useState("nano-web")
  const origin = window.location.origin
  const url = `${origin}/mcp`
  const key = token.trim() || "fg-paste-your-client-key"

  const claudeCmd = `claude mcp add -s user -t http ${name} ${url} --header "Authorization: Bearer ${key}"`

  const mcpJson = useMemo(
    () =>
      JSON.stringify(
        { mcpServers: { [name]: { type: "http", url, headers: { Authorization: `Bearer ${key}` } } } },
        null,
        2,
      ),
    [name, url, key],
  )

  const opencodeJson = useMemo(
    () =>
      JSON.stringify({ mcp: { [name]: { type: "remote", url, headers: { Authorization: `Bearer ${key}` }, enabled: true } } }, null, 2),
    [name, url, key],
  )

  const kiloJson = useMemo(
    () =>
      JSON.stringify({ mcpServers: { [name]: { type: "streamable-http", url, headers: { Authorization: `Bearer ${key}` } } } }, null, 2),
    [name, url, key],
  )

  return (
    <DialogRoot open={open} onOpenChange={(e) => onOpenChange(e.open)} size="xl">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>MCP web tools setup</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <VStack align="stretch" gap={4}>
            <Text fontSize="sm" color="fg.muted">
              Adds the gateway's <Code fontFamily="mono">web_search</Code> and{" "}
              <Code fontFamily="mono">web_read</Code> tools to your coding agent. Use any client
              key from <Code fontFamily="mono">My API Keys</Code> below. The key never leaves
              your browser — it is only embedded in the config you copy.
            </Text>

            <HStackField label="Server name">
              <Input autoComplete="off"
                value={name}
                fontFamily="mono"
                onChange={(e) => setName(e.target.value)}
                placeholder="nano-web"
              />
            </HStackField>

            <Field.Root>
              <Field.Label>Client key</Field.Label>
              <PasswordInput
                autoComplete="new-password"
                placeholder="fg-…"
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
              <Field.HelperText>
                Paste a key from Profile → My API Keys.
              </Field.HelperText>
            </Field.Root>

            <JsonBlock code={claudeCmd} title="Claude Code" copyLabel="copy claude command" />
            <JsonBlock code={mcpJson} title=".mcp.json / Claude Desktop" copyLabel="copy .mcp.json" />
            <JsonBlock code={opencodeJson} title="opencode.json" copyLabel="copy opencode config" />
            <JsonBlock code={kiloJson} title="kilo.jsonc (mcpServers)" copyLabel="copy kilo config" />
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

function HStackField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Field.Root>
      <Field.Label>{label}</Field.Label>
      {children}
    </Field.Root>
  )
}
