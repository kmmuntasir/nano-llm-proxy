import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import {
  Badge,
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
            <Table.Root size="sm">
              <Table.Header>
                <Table.Row>
                  <Table.ColumnHeader>Model</Table.ColumnHeader>
                  <Table.ColumnHeader>Requests</Table.ColumnHeader>
                  <Table.ColumnHeader>Tokens (in/out)</Table.ColumnHeader>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {(summary.data?.topModels ?? []).length === 0 && (
                  <Table.Row>
                    <Table.Cell colSpan={3} color="fg.muted" textAlign="center">
                      No usage in this range
                    </Table.Cell>
                  </Table.Row>
                )}
                {(summary.data?.topModels ?? []).map((m) => (
                  <Table.Row key={m.key}>
                    <Table.Cell fontFamily="mono" fontSize="xs" truncate maxW="260px">
                      {m.key}
                    </Table.Cell>
                    <Table.Cell>{m.requests}</Table.Cell>
                    <Table.Cell>
                      {fmtTokens(m.inputTokens)} / {fmtTokens(m.outputTokens)}
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Root>
          </Card.Body>
        </Card.Root>

        <Card.Root>
          <Card.Header>
            <Heading size="sm">Providers</Heading>
          </Card.Header>
          <Card.Body pt={3}>
            <Table.Root size="sm">
              <Table.Header>
                <Table.Row>
                  <Table.ColumnHeader>Provider</Table.ColumnHeader>
                  <Table.ColumnHeader>Requests</Table.ColumnHeader>
                  <Table.ColumnHeader>Tokens (in/out)</Table.ColumnHeader>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {(summary.data?.providers ?? []).length === 0 && (
                  <Table.Row>
                    <Table.Cell colSpan={3} color="fg.muted" textAlign="center">
                      No usage in this range
                    </Table.Cell>
                  </Table.Row>
                )}
                {(summary.data?.providers ?? []).map((p) => (
                  <Table.Row key={p.key}>
                    <Table.Cell>
                      <Badge variant="outline">{p.key}</Badge>
                    </Table.Cell>
                    <Table.Cell>{p.requests}</Table.Cell>
                    <Table.Cell>
                      {fmtTokens(p.inputTokens)} / {fmtTokens(p.outputTokens)}
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Root>
          </Card.Body>
        </Card.Root>
      </SimpleGrid>

      {isSuperadmin && (
        <Card.Root>
          <Card.Header>
            <Heading size="sm">By user</Heading>
          </Card.Header>
          <Card.Body pt={3}>
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
          </Card.Body>
        </Card.Root>
      )}

      <Card.Root>
        <Card.Header>
          <Heading size="sm">By API key</Heading>
        </Card.Header>
        <Card.Body pt={3}>
          <Table.Root size="sm">
            <Table.Header>
              <Table.Row>
                <Table.ColumnHeader>Key</Table.ColumnHeader>
                {isSuperadmin && <Table.ColumnHeader>Owner</Table.ColumnHeader>}
                <Table.ColumnHeader>Requests</Table.ColumnHeader>
                <Table.ColumnHeader>Tokens (in/out)</Table.ColumnHeader>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {(keys.data?.keys ?? []).length === 0 && (
                <Table.Row>
                  <Table.Cell colSpan={4} color="fg.muted" textAlign="center">
                    No usage in this range
                  </Table.Cell>
                </Table.Row>
              )}
              {(keys.data?.keys ?? []).map((k) => (
                <Table.Row key={k.keyId}>
                  <Table.Cell>{k.alias || `#${k.keyId}`}</Table.Cell>
                  {isSuperadmin && <Table.Cell>{k.email}</Table.Cell>}
                  <Table.Cell>{k.requests}</Table.Cell>
                  <Table.Cell>
                    {fmtTokens(k.inputTokens)} / {fmtTokens(k.outputTokens)}
                  </Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table.Root>
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
          <Table.Root size="sm">
            <Table.Header>
              <Table.Row>
                <Table.ColumnHeader>Time</Table.ColumnHeader>
                {isSuperadmin && <Table.ColumnHeader>User</Table.ColumnHeader>}
                <Table.ColumnHeader>Key</Table.ColumnHeader>
                <Table.ColumnHeader>Provider</Table.ColumnHeader>
                <Table.ColumnHeader>Model</Table.ColumnHeader>
                <Table.ColumnHeader>Tokens (in/out)</Table.ColumnHeader>
                <Table.ColumnHeader>Status</Table.ColumnHeader>
                <Table.ColumnHeader>ms</Table.ColumnHeader>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {(activity.data?.activity ?? []).length === 0 && (
                <Table.Row>
                  <Table.Cell colSpan={8} color="fg.muted" textAlign="center">
                    No requests yet
                  </Table.Cell>
                </Table.Row>
              )}
              {(activity.data?.activity ?? []).map((e, i) => (
                <Table.Row key={`${e.ts}-${i}`}>
                  <Table.Cell whiteSpace="nowrap">{fmtTs(e.ts)}</Table.Cell>
                  {isSuperadmin && <Table.Cell>{e.email}</Table.Cell>}
                  <Table.Cell>{e.alias || "—"}</Table.Cell>
                  <Table.Cell>{e.provider}</Table.Cell>
                  <Table.Cell fontFamily="mono" fontSize="xs" truncate maxW="220px">
                    {e.model}
                  </Table.Cell>
                  <Table.Cell>
                    {fmtTokens(e.inputTokens)} / {fmtTokens(e.outputTokens)}
                  </Table.Cell>
                  <Table.Cell>
                    <Badge colorPalette={e.status === 200 ? "green" : "red"} variant="subtle">
                      {e.status}
                    </Badge>
                  </Table.Cell>
                  <Table.Cell>{e.durationMs}</Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table.Root>
        </Card.Body>
      </Card.Root>
    </VStack>
  )
}
