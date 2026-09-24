import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import {
  Badge,
  Box,
  Button,
  Card,
  Field,
  Heading,
  HStack,
  Input,
  SimpleGrid,
  Stat,
  Table,
  Text,
  VStack,
} from "@chakra-ui/react"
import { api } from "../api/client"
import type { UsageActivityRow, UsageKeyRow, UsageModelRow, UsageTotals, UsageUserRow } from "../api/types"
import { DataTable } from "../components/DataTable"
import { useSession } from "../App"

interface UsageSummary {
  from: number
  to: number
  totals: UsageTotals
  topModels: UsageModelRow[]
  providers: UsageModelRow[]
}

// Presets snap to local midnights: "from" and "to" both sit at 00:00:00 of
// their day, so the default window is the 7 full days before today. Custom
// from/to edits set exact times.
const RANGES = [
  { label: "Yesterday", days: 1, toEndOfDay: false },
  { label: "7 days", days: 7, toEndOfDay: true },
  { label: "30 days", days: 30, toEndOfDay: true },
]

function startOfToday(): number {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  return Math.floor(d.getTime() / 1000)
}

function fmtLocalInput(ts: number): string {
  const d = new Date(ts * 1000)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function fmtTokens(n: number) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

function fmtTs(ts: number) {
  return new Date(ts * 1000).toLocaleString()
}

// RangePicker: presets plus optional explicit from/to (datetime-local, read
// in the browser's timezone).
function RangePicker({
  range,
  onRange,
  from,
  to,
  onCustom,
}: {
  range: string
  onRange: (label: string) => void
  from: string
  to: string
  onCustom: (from: string, to: string) => void
}) {
  return (
    <HStack gap={2} flexWrap="wrap" align="end">
      {RANGES.map((r) => (
        <Button
          key={r.label}
          size="xs"
          variant={range === r.label ? "solid" : "outline"}
          colorPalette={range === r.label ? "blue" : "gray"}
          onClick={() => onRange(r.label)}
        >
          {r.label}
        </Button>
      ))}
      <Field.Root w="200px">
        <Field.Label fontSize="xs">From</Field.Label>
        <Input
          type="datetime-local"
          size="xs"
          value={from}
          onChange={(e) => onCustom(e.target.value, to)}
        />
      </Field.Root>
      <Field.Root w="200px">
        <Field.Label fontSize="xs">To</Field.Label>
        <Input
          type="datetime-local"
          size="xs"
          value={to}
          onChange={(e) => onCustom(from, e.target.value)}
        />
      </Field.Root>
    </HStack>
  )
}

export default function UsagePage() {
  const { data: me } = useSession()
  const isSuperadmin = me?.user.role === "superadmin"

  const [range, setRange] = useState("7 days")
  const [custom, setCustom] = useState<{ from: string; to: string } | null>(null)

  const todayStart = startOfToday()
  const preset = RANGES.find((r) => r.label === range)
  const days = preset?.days ?? 7
  // Preset bounds are day boundaries: "from" is the midnight N days before
  // today. "to" is today's midnight for Yesterday, but 23:59 today for the
  // 7/30-day presets so the window includes the current day without a
  // timestamp that goes stale a minute later.
  const to = custom?.to
    ? new Date(custom.to).getTime() / 1000
    : preset?.toEndOfDay
      ? todayStart + 86340 // 23:59 today
      : todayStart
  const from = custom?.from
    ? new Date(custom.from).getTime() / 1000
    : todayStart - days * 86400
  const qs = `from=${Math.floor(from)}&to=${Math.ceil(to)}`

  const summary = useQuery({
    queryKey: ["usage", "summary", from, to],
    queryFn: () => api<UsageSummary>(`/api/usage/summary?${qs}`),
  })
  const keys = useQuery({
    queryKey: ["usage", "keys", from, to],
    queryFn: () => api<{ keys: UsageKeyRow[] }>(`/api/usage/keys?${qs}`),
  })
  const users = useQuery({
    queryKey: ["usage", "users", from, to],
    queryFn: () => api<{ users: UsageUserRow[] }>(`/api/usage/users?${qs}`),
    enabled: isSuperadmin,
  })
  const activity = useQuery({
    queryKey: ["usage", "activity"],
    queryFn: () => api<{ activity: UsageActivityRow[] }>("/api/usage/activity?limit=50"),
  })

  const t = summary.data?.totals
  const errRate = t && t.requests > 0 ? ((t.errors / t.requests) * 100).toFixed(1) : "0.0"

  return (
    <VStack align="stretch" gap={6}>
      <HStack justify="space-between" flexWrap="wrap">
        <Heading size="lg">Usage</Heading>
        <RangePicker
          range={range}
          onRange={(label) => {
            setRange(label)
            setCustom(null)
          }}
          from={custom?.from ?? fmtLocalInput(from)}
          to={custom?.to ?? fmtLocalInput(to)}
          onCustom={(f, tt) => setCustom({ from: f, to: tt })}
        />
      </HStack>

      <SimpleGrid columns={{ base: 2, md: 4 }} gap={4}>
        <Card.Root>
          <Card.Body>
            <Stat.Root>
              <Stat.Label>Requests</Stat.Label>
              <Stat.ValueText>{t ? t.requests.toLocaleString() : "—"}</Stat.ValueText>
            </Stat.Root>
          </Card.Body>
        </Card.Root>
        <Card.Root>
          <Card.Body>
            <Stat.Root>
              <Stat.Label>Input tokens</Stat.Label>
              <Stat.ValueText>{t ? fmtTokens(t.inputTokens) : "—"}</Stat.ValueText>
            </Stat.Root>
          </Card.Body>
        </Card.Root>
        <Card.Root>
          <Card.Body>
            <Stat.Root>
              <Stat.Label>Output tokens</Stat.Label>
              <Stat.ValueText>{t ? fmtTokens(t.outputTokens) : "—"}</Stat.ValueText>
            </Stat.Root>
          </Card.Body>
        </Card.Root>
        <Card.Root>
          <Card.Body>
            <Stat.Root>
              <Stat.Label>Errors</Stat.Label>
              <Stat.ValueText>
                {t ? t.errors.toLocaleString() : "—"}
                <Stat.ValueUnit>({errRate}%)</Stat.ValueUnit>
              </Stat.ValueText>
            </Stat.Root>
          </Card.Body>
        </Card.Root>
      </SimpleGrid>

      <SimpleGrid columns={{ base: 1, lg: 2 }} gap={4}>
        <Card.Root>
          <Card.Header>
            <Heading size="sm">Most used models</Heading>
          </Card.Header>
          <Card.Body pt={3}>
            <DataTable
              rows={summary.data?.topModels ?? []}
              rowKey={(m) => m.key}
              empty="No usage in this range"
              columns={[
                {
                  header: "Model",
                  render: (m) => (
                    <Text fontFamily="mono" fontSize="xs" truncate maxW="260px">
                      {m.key}
                    </Text>
                  ),
                },
                { header: "Requests", render: (m) => m.requests },
                {
                  header: "Tokens (in / out)",
                  render: (m) => `${fmtTokens(m.inputTokens)} / ${fmtTokens(m.outputTokens)}`,
                },
              ]}
            />
          </Card.Body>
        </Card.Root>

        <Card.Root>
          <Card.Header>
            <Heading size="sm">Providers</Heading>
          </Card.Header>
          <Card.Body pt={3}>
            <DataTable
              rows={summary.data?.providers ?? []}
              rowKey={(p) => p.key}
              empty="No usage in this range"
              columns={[
                {
                  header: "Provider",
                  render: (p) => <Badge variant="outline">{p.key}</Badge>,
                },
                { header: "Requests", render: (p) => p.requests },
                {
                  header: "Tokens (in / out)",
                  render: (p) => `${fmtTokens(p.inputTokens)} / ${fmtTokens(p.outputTokens)}`,
                },
              ]}
            />
          </Card.Body>
        </Card.Root>
      </SimpleGrid>

      {isSuperadmin && (
        <Card.Root>
          <Card.Header>
            <Heading size="sm">By user</Heading>
          </Card.Header>
          <Card.Body pt={3}>
            <Box overflowX="auto">
            <Table.Root size="sm">
              <Table.Header>
                <Table.Row>
                  <Table.ColumnHeader>User</Table.ColumnHeader>
                  <Table.ColumnHeader>Requests</Table.ColumnHeader>
                  <Table.ColumnHeader>Tokens (in/out)</Table.ColumnHeader>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {(users.data?.users ?? []).length === 0 && (
                  <Table.Row>
                    <Table.Cell colSpan={3} color="fg.muted" textAlign="center">
                      No usage in this range
                    </Table.Cell>
                  </Table.Row>
                )}
                {(users.data?.users ?? []).map((u) => (
                  <Table.Row key={u.userId}>
                    <Table.Cell>{u.email}</Table.Cell>
                    <Table.Cell>{u.requests}</Table.Cell>
                    <Table.Cell>
                      {fmtTokens(u.inputTokens)} / {fmtTokens(u.outputTokens)}
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Root>
            </Box>
          </Card.Body>
        </Card.Root>
      )}

      <Card.Root>
        <Card.Header>
          <Heading size="sm">By API key</Heading>
        </Card.Header>
        <Card.Body pt={3}>
          <DataTable
            rows={keys.data?.keys ?? []}
            rowKey={(k) => String(k.keyId)}
            empty="No usage in this range"
            columns={[
              { header: "Key", render: (k) => k.alias || `#${k.keyId}` },
              ...(isSuperadmin ? [{ header: "Owner", render: (k: UsageKeyRow) => k.email }] : []),
              { header: "Requests", render: (k) => k.requests },
              {
                header: "Tokens (in / out)",
                render: (k) => `${fmtTokens(k.inputTokens)} / ${fmtTokens(k.outputTokens)}`,
              },
            ]}
          />
        </Card.Body>
      </Card.Root>

      <Card.Root>
        <Card.Header>
          <Heading size="sm">Recent activity</Heading>
          <Text fontSize="xs" color="fg.muted">
            last 50 requests{isSuperadmin ? " across all users" : ""}
          </Text>
        </Card.Header>
        <Card.Body pt={3}>
          <DataTable
            rows={activity.data?.activity ?? []}
            rowKey={(e, i) => `${e.ts}-${i}`}
            empty="No requests yet"
            columns={[
              { header: "Time", render: (e) => fmtTs(e.ts) },
              ...(isSuperadmin ? [{ header: "User", render: (e: UsageActivityRow) => e.email }] : []),
              { header: "Key", render: (e) => e.alias || "—" },
              { header: "Provider", render: (e) => e.provider },
              {
                header: "Model",
                render: (e) => (
                  <Text fontFamily="mono" fontSize="xs" truncate maxW="220px">
                    {e.model}
                  </Text>
                ),
              },
              {
                header: "Tokens (in / out)",
                render: (e) => `${fmtTokens(e.inputTokens)} / ${fmtTokens(e.outputTokens)}`,
              },
              {
                header: "Status",
                render: (e) => (
                  <Badge colorPalette={e.status === 200 ? "green" : "red"} variant="subtle">
                    {e.status}
                  </Badge>
                ),
              },
              { header: "ms", render: (e) => e.durationMs },
            ]}
          />
        </Card.Body>
      </Card.Root>
    </VStack>
  )
}
