import { useMemo } from "react"
import { Link as RouterLink } from "react-router-dom"
import {
  Badge,
  Card,
  Code,
  Heading,
  HStack,
  Link,
  List,
  Text,
  VStack,
} from "@chakra-ui/react"
import JsonBlock from "../components/JsonBlock"
import { DataTable } from "../components/DataTable"

// DocsPage — the user guide. Everything renders from window.location.origin
// so the same binary serves correct snippets whether the gateway is reached
// over LAN or a public domain. Purely client-side: keys shown here are
// placeholders, never fetched.

const FG_PLACEHOLDER = "fg-paste-your-client-key"

interface TroubleshootingRow {
  symptom: string
  meaning: string
}

const TROUBLESHOOTING: TroubleshootingRow[] = [
  {
    symptom: "401 Invalid or missing API key",
    meaning:
      "The key is missing, revoked or disabled, or your account was deactivated. Create a key in Profile → My API Keys, or ask an admin to reactivate your account.",
  },
  {
    symptom: '400 Model must be "<provider>/<id>"',
    meaning:
      "Use the full provider/model id from the Models page (e.g. zen/…). On /v1/messages a bare claude-* name only works when an admin has set an Anthropic fallback model.",
  },
  {
    symptom: "400 Unknown provider",
    meaning:
      "The provider prefix is misspelled, or an admin hasn't given your account access to that provider. The Models page lists only the providers you can use.",
  },
  {
    symptom: "400 upstream 4xx: …",
    meaning:
      "The upstream rejected the request — usually a wrong model id under a valid provider, or a parameter that model doesn't accept.",
  },
  {
    symptom: "503 No healthy <provider> keys available",
    meaning:
      "Every upstream key tried was rate-limited, cooling down or disabled. Cooling keys recover on their own — retry in a minute; if it persists an admin checks the Providers page.",
  },
  {
    symptom: "Cloudflare-branded 502 page",
    meaning:
      "The gateway itself didn't answer (stopped, restarting or unreachable) — it never returns 502 on its own, precisely because Cloudflare would replace its explanation. Retry in a minute; if it persists an admin checks the service.",
  },
  {
    symptom: "520–526 (e.g. 523) from the public URL",
    meaning:
      "The host or network in front of the gateway is unreachable, not the gateway itself — retry shortly; if it persists, check the deployment's host.",
  },
  {
    symptom: "Reply cut off (finish reason length / max_tokens)",
    meaning:
      "The model used up the client's output budget, often on hidden reasoning. Raise the client's max output tokens (Pi: maxTokens per model, default 16384), switch models, or say continue. The gateway adds no output cap of its own.",
  },
  {
    symptom: "404 from /mcp",
    meaning:
      "Web tools are disabled in Settings → Web tools (MCP). A bad key still gets 401 first.",
  },
  {
    symptom: "web_search returns no results",
    meaning:
      "Search engines may be CAPTCHA-blocked from this deployment's IP — an admin can trim engines in /etc/searxng/settings.yml (see docs/deployment.md).",
  },
  {
    symptom: "web_read returns empty/JS-shell text",
    meaning: "Re-run with render: true — the page needs a real browser.",
  },
]

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

function PageLink({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <Link asChild colorPalette="blue" variant="underline">
      <RouterLink to={to}>{children}</RouterLink>
    </Link>
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
      piModels: JSON.stringify(
        {
          $schema: "https://pi.dev/schemas/models.schema.json",
          providers: {
            nano: {
              name: "Nano",
              baseUrl: `${origin}/v1`,
              api: "openai-completions",
              apiKey: FG_PLACEHOLDER,
              models: [
                {
                  id: "provider/model-id",
                  contextWindow: 200000,
                  maxTokens: 32000,
                  reasoning: true,
                  input: ["text", "image"],
                },
              ],
            },
          },
        },
        null,
        2,
      ),
      piMcpAdd: `pi mcp add nano-web --url ${origin}/mcp --header "Authorization=Bearer ${FG_PLACEHOLDER}" --exposure direct --description "Web search and page reading"`,
      piMcpJson: JSON.stringify(
        {
          mcpServers: {
            "nano-web": {
              url: `${origin}/mcp`,
              headers: { Authorization: `Bearer ${FG_PLACEHOLDER}` },
              exposure: "direct",
              description: "Web search and page reading",
            },
          },
        },
        null,
        2,
      ),
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
        from <PageLink to="/profile">Profile → My API Keys</PageLink> and paste
        it wherever a snippet says <Code fontFamily="mono">{FG_PLACEHOLDER}</Code>.
      </Text>

      <Section title="What this is">
        <Text fontSize="sm">
          One endpoint in front of many LLM providers. You get a single{" "}
          <Code fontFamily="mono">fg-…</Code> key, every model from the
          providers your account can use under one catalog, automatic upstream
          key rotation and failover, per-user usage tracking, and self-hosted
          MCP web tools (<Code fontFamily="mono">web_search</Code>,{" "}
          <Code fontFamily="mono">web_read</Code>) — no paid search APIs.
        </Text>
        <HStack gap={2} flexWrap="wrap">
          <Badge variant="outline">OpenAI — <Code fontFamily="mono">{origin}/v1</Code></Badge>
          <Badge variant="outline">Anthropic — <Code fontFamily="mono">{origin}</Code></Badge>
          <Badge variant="outline">MCP — <Code fontFamily="mono">{origin}/mcp</Code></Badge>
        </HStack>
        <Text fontSize="sm" color="fg.muted">
          Endpoints: <Code fontFamily="mono">/v1/chat/completions</Code> and{" "}
          <Code fontFamily="mono">/v1/models</Code> (OpenAI),{" "}
          <Code fontFamily="mono">/v1/messages</Code> (Anthropic — SDKs and
          Claude Code take the base URL without <Code fontFamily="mono">/v1</Code>{" "}
          and append the path themselves),{" "}
          <Code fontFamily="mono">/v1/responses</Code> (OpenAI Responses — only
          zen models served on the Responses API), and{" "}
          <Code fontFamily="mono">POST /mcp</Code>. The same key works
          everywhere, sent as <Code fontFamily="mono">Authorization: Bearer fg-…</Code>{" "}
          or <Code fontFamily="mono">x-api-key: fg-…</Code>.
        </Text>
        <Text fontSize="sm" color="fg.muted">
          The version label at the bottom of the sidebar opens the{" "}
          <PageLink to="/about">About page</PageLink>: the running build and
          the changelog.
        </Text>
      </Section>

      <Section title="Pick models">
        <Text fontSize="sm">
          The <PageLink to="/models">Models page</PageLink> lists every model
          your account can use, with its exact{" "}
          <Code fontFamily="mono">provider/model</Code> id (copy button next to
          it), context window, output limit, reasoning flags and effort levels.
          Use that full id as the <Code fontFamily="mono">model</Code> value in
          any client.
        </Text>
        <Text fontSize="sm">
          Benchmark scores (Artificial Analysis indexes, Design Arena) come from
          OpenRouter. They refresh daily when an admin has the catalog auto-sync
          on, or when an admin presses Sync now; free and stealth models often
          have none.
        </Text>
        <List.Root fontSize="sm" pl={4}>
          <List.Item>
            The Claude Code setup generator on the Models page adds a{" "}
            <Code fontFamily="mono">[1m]</Code> suffix to models with a context
            window of 1M tokens or more, so Claude Code accounts for the larger
            context. The gateway strips a trailing{" "}
            <Code fontFamily="mono">[1m]</Code> from any model id before
            calling upstream.
          </List.Item>
          <List.Item>
            On <Code fontFamily="mono">/v1/messages</Code> only, a bare{" "}
            <Code fontFamily="mono">claude-*</Code> model name (no{" "}
            <Code fontFamily="mono">provider/</Code> prefix) is rerouted to the
            admin-configured Anthropic fallback model — handy for Claude Code
            background tasks. With no fallback set, it fails with 400.
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

      <Section title="Pi coding agent">
        <Text fontSize="sm">
          <Text as="span" fontWeight="medium">1. Connect the gateway.</Text> Pi
          supports custom endpoints natively — add the gateway as an
          OpenAI-compatible provider in{" "}
          <Code fontFamily="mono">~/.pi/agent/models.json</Code>. Pi doesn't
          discover models from this file, so list each model you want, using
          the exact id and limits from the{" "}
          <PageLink to="/models">Models page</PageLink>. Only{" "}
          <Code fontFamily="mono">id</Code> is required; without{" "}
          <Code fontFamily="mono">maxTokens</Code> Pi caps output at 16384
          tokens. Opening <Code fontFamily="mono">/model</Code> reloads the
          file.
        </Text>
        <JsonBlock code={snippets.piModels} title="~/.pi/agent/models.json" copyLabel="copy config" />
        <Text fontSize="sm">
          <Text as="span" fontWeight="medium">2. MCP web tools.</Text> Pi has
          built-in MCP support — no package needed. Add the server with one
          command (<Code fontFamily="mono">--exposure direct</Code> offers the
          tools to the model like built-ins; the default{" "}
          <Code fontFamily="mono">codemode</Code> only makes them callable from
          codemode scripts):
        </Text>
        <JsonBlock code={snippets.piMcpAdd} title="pi mcp add" copyLabel="copy command" />
        <Text fontSize="sm">
          …or write it to <Code fontFamily="mono">~/.pi/agent/mcp.json</Code>{" "}
          yourself. Check it with <Code fontFamily="mono">pi mcp list</Code> or{" "}
          <Code fontFamily="mono">/mcp</Code> inside a session. Installing the{" "}
          <Code fontFamily="mono">pi-mcp-adapter</Code> package replaces this
          built-in support, so skip it.
        </Text>
        <JsonBlock code={snippets.piMcpJson} title="~/.pi/agent/mcp.json" copyLabel="copy config" />
      </Section>

      <Section title="Anything else (OpenAI-compatible)">
        <Text fontSize="sm">
          Any client speaking OpenAI chat completions works: base URL{" "}
          <Code fontFamily="mono">{origin}/v1</Code>, your key as a Bearer
          token, and a <Code fontFamily="mono">provider/model</Code> id.
        </Text>
        <JsonBlock code={snippets.curl} title="curl" copyLabel="copy" />
      </Section>

      <Section title="MCP web tools">
        <Text fontSize="sm">
          Two tools at <Code fontFamily="mono">{origin}/mcp</Code>:{" "}
          <Code fontFamily="mono">web_search</Code> (self-hosted SearXNG;{" "}
          <Code fontFamily="mono">maxResults</Code> 1–25) and{" "}
          <Code fontFamily="mono">web_read</Code> (fast native fetch, with a
          headless-browser fallback for JavaScript-heavy or bot-protected
          pages — set <Code fontFamily="mono">render: true</Code> if a page
          comes back empty; <Code fontFamily="mono">maxChars</Code> limits the
          text returned). Private, loopback, link-local and other internal
          addresses are refused, checked after DNS and on every redirect.
        </Text>
        <Text fontSize="sm">
          Your calls are metered on the Usage page under provider{" "}
          <Code fontFamily="mono">web-tools</Code> (models{" "}
          <Code fontFamily="mono">web_search</Code> /{" "}
          <Code fontFamily="mono">web_read</Code>, surface{" "}
          <Code fontFamily="mono">mcp</Code>). An admin must have web tools
          enabled in Settings → Web tools (MCP) — otherwise the endpoint
          answers 404. Any MCP-capable client works the same way.
        </Text>
      </Section>

      <Section title="Accounts & keys">
        <List.Root fontSize="sm" pl={4} gap={1}>
          <List.Item>
            Accounts are created by an admin. Google sign-in (when enabled)
            only works for an email that already has an account — it never
            creates one. Password sign-in may be switched off by an admin.
          </List.Item>
          <List.Item>
            Only you can create, rename or revoke your own{" "}
            <Code fontFamily="mono">fg-</Code> keys, in{" "}
            <PageLink to="/profile">Profile → My API Keys</PageLink>. Admins can
            see your keys but not change them.
          </List.Item>
          <List.Item>
            If an admin deactivates your account, your sessions end and every
            one of your keys starts returning 401.
          </List.Item>
          <List.Item>
            The gateway rotates its upstream keys for you: each provider
            sticks with one healthy key (good for prompt caching) and fails over
            to the next on errors or rate limits; by default the starting key
            moves one place each day.
          </List.Item>
        </List.Root>
      </Section>

      <Section title="Usage & caching">
        <Text fontSize="sm">
          The <PageLink to="/usage">Usage page</PageLink> shows your requests,
          errors, tokens, latency and cache hits over any time range, with
          Overview, Models and Requests tabs (admins also get Users and
          Users × Models). Every request is listed with its status, the failure
          reason when it failed, and its token counts.
        </Text>
        <Text fontSize="sm">
          Cached input tokens are reported back to clients:{" "}
          <Code fontFamily="mono">prompt_tokens_details.cached_tokens</Code> on{" "}
          <Code fontFamily="mono">/v1/chat/completions</Code>, and{" "}
          <Code fontFamily="mono">cache_read_input_tokens</Code> on{" "}
          <Code fontFamily="mono">/v1/messages</Code> (where{" "}
          <Code fontFamily="mono">input_tokens</Code> excludes the cached part).
          Whether a request hits the cache is up to the upstream provider.
        </Text>
      </Section>

      <Section title="Troubleshooting">
        <DataTable
          rows={TROUBLESHOOTING}
          rowKey={(r) => r.symptom}
          columns={[
            {
              header: "Symptom",
              render: (r) => (
                <Text fontFamily="mono" fontSize="xs">
                  {r.symptom}
                </Text>
              ),
            },
            { header: "Meaning", render: (r) => <Text fontSize="sm">{r.meaning}</Text> },
          ]}
        />
      </Section>
    </VStack>
  )
}
