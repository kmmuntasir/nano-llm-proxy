import { useMemo } from "react"
import {
  Badge,
  Card,
  Code,
  Heading,
  HStack,
  Link,
  List,
  Table,
  Text,
  VStack,
} from "@chakra-ui/react"
import JsonBlock from "../components/JsonBlock"

// DocsPage — the user guide. Everything renders from window.location.origin
// so the same binary serves correct snippets whether the gateway is reached
// over LAN or a public domain. Purely client-side: keys shown here are
// placeholders, never fetched.

const FG_PLACEHOLDER = "fg-paste-your-client-key"

function Section({
  title,
  children,
}: {
  title: string
  children: React.ReactNode
}) {
  return (
    <Card.Root>
      <Card.Header>
        <Heading size="sm">{title}</Heading>
      </Card.Header>
      <Card.Body>
        <VStack align="stretch" gap={3}>
          {children}
        </VStack>
      </Card.Body>
    </Card.Root>
  )
}

export default function DocsPage() {
  const origin = window.location.origin

  const snippets = useMemo(
    () => ({
      claudeEnv: JSON.stringify(
        {
          env: {
            ANTHROPIC_BASE_URL: origin,
            ANTHROPIC_AUTH_TOKEN: FG_PLACEHOLDER,
            ANTHROPIC_DEFAULT_OPUS_MODEL: "provider/model-opus",
            ANTHROPIC_DEFAULT_SONNET_MODEL: "provider/model-sonnet",
            ANTHROPIC_DEFAULT_HAIKU_MODEL: "provider/model-haiku",
          },
        },
        null,
        2,
      ),
      claudeMcp: `claude mcp add -s user -t http nano-web ${origin}/mcp --header "Authorization: Bearer ${FG_PLACEHOLDER}"`,
      mcpJson: JSON.stringify(
        {
          mcpServers: {
            "nano-web": {
              type: "http",
              url: `${origin}/mcp`,
              headers: { Authorization: `Bearer ${FG_PLACEHOLDER}` },
            },
          },
        },
        null,
        2,
      ),
      opencodeProvider: JSON.stringify(
        {
          $schema: "https://opencode.ai/config.json",
          provider: {
            nano: {
              npm: "@ai-sdk/openai-compatible",
              name: "Nano",
              options: { baseURL: `${origin}/v1`, apiKey: FG_PLACEHOLDER },
            },
          },
        },
        null,
        2,
      ),
      opencodeMcp: JSON.stringify(
        {
          mcp: {
            "nano-web": {
              type: "remote",
              url: `${origin}/mcp`,
              headers: { Authorization: `Bearer ${FG_PLACEHOLDER}` },
              enabled: true,
            },
          },
        },
        null,
        2,
      ),
      kiloMcp: JSON.stringify(
        {
          mcpServers: {
            "nano-web": {
              type: "streamable-http",
              url: `${origin}/mcp`,
              headers: { Authorization: `Bearer ${FG_PLACEHOLDER}` },
            },
          },
        },
        null,
        2,
      ),
      piCompatInstall: "pi install npm:@billjr99/pi-openai-compat",
      piCompatJson: JSON.stringify(
        {
          providers: {
            custom: {
              displayName: "Nano",
              baseUrl: `${origin}/v1`,
              apiKey: FG_PLACEHOLDER,
            },
          },
        },
        null,
        2,
      ),
      piMcpInstall: "pi install npm:pi-mcp-adapter",
      piMcpJson: JSON.stringify(
        {
          mcpServers: {
            "nano-web": {
              type: "http",
              url: `${origin}/mcp`,
              headers: { Authorization: `Bearer ${FG_PLACEHOLDER}` },
            },
          },
        },
        null,
        2,
      ),
      codexToml: `model_provider = "nano"

[model_providers.nano]
name = "Nano"
base_url = "${origin}/v1"
env_key = "NANO_API_KEY"   # export NANO_API_KEY=fg-...
wire_api = "chat"          # the gateway also speaks "responses"`,
      curl: `curl ${origin}/v1/chat/completions \\
  -H "Authorization: Bearer ${FG_PLACEHOLDER}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "provider/model-id",
    "messages": [{ "role": "user", "content": "hello" }]
  }'`,
    }),
    [origin],
  )

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">Guide</Heading>
      <Text fontSize="sm" color="fg.muted">
        Everything on this page is generated for this deployment — every URL
        below points at <Code fontFamily="mono">{origin}</Code>. Grab a key
        from <Link asChild><Code fontFamily="mono">Profile → My API Keys</Code></Link>{" "}
        and paste it wherever a snippet says <Code fontFamily="mono">{FG_PLACEHOLDER}</Code>.
      </Text>

      <Section title="What this is">
        <Text fontSize="sm">
          One endpoint in front of many LLM providers. You get a single{" "}
          <Code fontFamily="mono">fg-…</Code> key, every model from every
          configured provider under one catalog, automatic key rotation and
          failover, per-user usage tracking, and self-hosted MCP web tools
          (<Code fontFamily="mono">web_search</Code>,{" "}
          <Code fontFamily="mono">web_read</Code>) — no paid search APIs.
        </Text>
        <HStack gap={2} flexWrap="wrap">
          <Badge variant="outline">OpenAI — <Code fontFamily="mono">{origin}/v1</Code></Badge>
          <Badge variant="outline">Anthropic — <Code fontFamily="mono">{origin}/v1</Code></Badge>
          <Badge variant="outline">MCP — <Code fontFamily="mono">{origin}/mcp</Code></Badge>
        </HStack>
        <Text fontSize="sm" color="fg.muted">
          All three surfaces live under the same base:{" "}
          <Code fontFamily="mono">/v1/chat/completions</Code>,{" "}
          <Code fontFamily="mono">/v1/responses</Code> (OpenAI),{" "}
          <Code fontFamily="mono">/v1/messages</Code> (Anthropic),{" "}
          <Code fontFamily="mono">/v1/models</Code>, and{" "}
          <Code fontFamily="mono">POST /mcp</Code>. Same key works everywhere.
        </Text>
      </Section>

      <Section title="Pick models">
        <Text fontSize="sm">
          The <Link asChild><Text as="span">Models page</Text></Link> lists every
          live model with its exact <Code fontFamily="mono">provider/model</Code>{" "}
          id (click the id to copy) plus context window, output limit, and
          reasoning flags. Use that full id as the{" "}
          <Code fontFamily="mono">model</Code> value in any client.
        </Text>
        <List.Root fontSize="sm" pl={4}>
          <List.Item>
            Models with ≥ 1M context take a <Code fontFamily="mono">[1m]</Code>{" "}
            suffix for Claude Code context accounting — the gateway strips it
            before calling upstream, and the setup generator adds it
            automatically.
          </List.Item>
          <List.Item>
            A literal <Code fontFamily="mono">claude-*</Code> model name routes
            to the admin-configured fallback target (handy for Claude Code
            background tasks).
          </List.Item>
        </List.Root>
      </Section>

      <Section title="Claude Code">
        <Text fontSize="sm">
          Paste the <Code fontFamily="mono">env</Code> block into{" "}
          <Code fontFamily="mono">~/.claude/settings.json</Code>. The Models
          page has a generator that fills the model slots for you (with the{" "}
          <Code fontFamily="mono">[1m]</Code> handling).
        </Text>
        <JsonBlock code={snippets.claudeEnv} title="~/.claude/settings.json" copyLabel="copy env block" />
        <Text fontSize="sm">
          <Text as="span" fontWeight="medium">MCP web tools</Text> — one command:
        </Text>
        <JsonBlock code={snippets.claudeMcp} title="claude mcp add" copyLabel="copy command" />
      </Section>

      <Section title="opencode">
        <Text fontSize="sm">
          Custom provider in <Code fontFamily="mono">~/.config/opencode/opencode.json</Code>{" "}
          (OpenAI-compatible; the gateway also serves{" "}
          <Code fontFamily="mono">/v1/responses</Code>):
        </Text>
        <JsonBlock code={snippets.opencodeProvider} title="opencode.json" copyLabel="copy provider config" />
        <Text fontSize="sm">MCP web tools:</Text>
        <JsonBlock code={snippets.opencodeMcp} title="opencode.json (mcp)" copyLabel="copy mcp config" />
      </Section>

      <Section title="Codex CLI">
        <Text fontSize="sm">
          <Code fontFamily="mono">~/.codex/config.toml</Code> — export{" "}
          <Code fontFamily="mono">NANO_API_KEY</Code> in your shell:
        </Text>
        <JsonBlock code={snippets.codexToml} title="config.toml" copyLabel="copy config" />
      </Section>

      <Section title="Kilo Code">
        <Text fontSize="sm">
          In Kilo Code's provider settings pick <Text as="span" fontWeight="medium">OpenAI
          Compatible</Text>, set the base URL to{" "}
          <Code fontFamily="mono">{origin}/v1</Code> and paste your{" "}
          <Code fontFamily="mono">fg-</Code> key as the API key. For MCP web
          tools, add this server to <Code fontFamily="mono">~/.config/kilo/kilo.jsonc</Code>:
        </Text>
        <JsonBlock code={snippets.kiloMcp} title="kilo.jsonc (mcpServers)" copyLabel="copy config" />
      </Section>

      <Section title="Pi coding agent">
        <Text fontSize="sm">
          Pi has no built-in custom-endpoint or MCP support — both come from
          packages. <Text as="span" fontWeight="medium">1. Connect the gateway</Text>{" "}
          with the OpenAI-compat extension:
        </Text>
        <JsonBlock code={snippets.piCompatInstall} title="install compat extension" copyLabel="copy" />
        <Text fontSize="sm">
          Then put this in <Code fontFamily="mono">~/.config/pi-openai-compat/config.json</Code>{" "}
          and restart Pi — its models appear under the custom provider:
        </Text>
        <JsonBlock code={snippets.piCompatJson} title="pi-openai-compat config.json" copyLabel="copy config" />
        <Text fontSize="sm">
          <Text as="span" fontWeight="medium">2. MCP web tools</Text> via the
          MCP adapter package (restart Pi after installing):
        </Text>
        <JsonBlock code={snippets.piMcpInstall} title="install MCP adapter" copyLabel="copy" />
        <Text fontSize="sm">
          Then add the server to <Code fontFamily="mono">~/.config/mcp/mcp.json</Code>{" "}
          (Pi's global MCP config). Servers are lazy — Pi connects the first
          time a tool is used; check status with <Code fontFamily="mono">/mcp</Code>:
        </Text>
        <JsonBlock code={snippets.piMcpJson} title="~/.config/mcp/mcp.json" copyLabel="copy config" />
      </Section>

      <Section title="Anything else (OpenAI-compatible)">
        <Text fontSize="sm">
          Any client speaking OpenAI chat completions works: base URL{" "}
          <Code fontFamily="mono">{origin}/v1</Code>, your key as a Bearer
          token.
        </Text>
        <JsonBlock code={snippets.curl} title="curl" copyLabel="copy" />
      </Section>

      <Section title="MCP web tools">
        <Text fontSize="sm">
          Two tools at <Code fontFamily="mono">{origin}/mcp</Code>:{" "}
          <Code fontFamily="mono">web_search</Code> (self-hosted SearXNG) and{" "}
          <Code fontFamily="mono">web_read</Code> (fast native fetch, with a
          headless-browser fallback for JavaScript-heavy or bot-protected
          pages — set <Code fontFamily="mono">render: true</Code> if a page
          comes back empty). Private/loopback addresses are refused. Your
          calls are metered on the Usage page under the{" "}
          <Code fontFamily="mono">web-tools</Code> provider. The admin must
          have web tools enabled (Settings → Web tools) — otherwise the
          endpoint answers 404. Per-client snippets above; any MCP-capable
          client works the same way.
        </Text>
      </Section>

      <Section title="Troubleshooting">
        <Table.Root size="sm">
          <Table.Header>
            <Table.Row>
              <Table.ColumnHeader>Symptom</Table.ColumnHeader>
              <Table.ColumnHeader>Meaning</Table.ColumnHeader>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {[
              ["401 from /v1 or /mcp", "Missing/revoked key — create a new one in Profile → My API Keys"],
              ["502 no healthy keys", "The provider's upstream keys are exhausted or cooling — an admin adds/fixes keys on the Providers page"],
              ["404 from /mcp", "Web tools are disabled in Settings → Web tools (superadmin)"],
              ["web_search returns no results", "Search engines may be CAPTCHA-blocked from this deployment's IP — an admin can trim engines in /etc/searxng/settings.yml (see docs/deployment.md)"],
              ["web_read returns empty/JS-shell text", "Re-run with render: true — the page needs a real browser"],
              ["Empty reply that stops after a long pause (Pi)", "The model burned Pi's 4096-token output cap on hidden reasoning — switch models or just say continue; the gateway relays upstream finish reasons verbatim"],
              ["5xx / 523 from the public URL", "The host or network in front of the gateway is down, not the gateway — retry shortly; if it persists, check the deployment's host"],
              ["model not found", "Use the exact provider/model id from the Models page"],
            ].map(([sym, meaning]) => (
              <Table.Row key={sym}>
                <Table.Cell fontFamily="mono" fontSize="xs" whiteSpace="nowrap">{sym}</Table.Cell>
                <Table.Cell fontSize="sm">{meaning}</Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table.Root>
      </Section>
    </VStack>
  )
}
