import { useQuery } from "@tanstack/react-query"
import {
  Badge,
  Card,
  HStack,
  Heading,
  SimpleGrid,
  Stat,
  Table,
  Text,
  VStack,
} from "@chakra-ui/react"
import { api } from "../api/client"
import type { DashboardData, UsageModelRow, UsageTotals } from "../api/types"
import StatusBadge from "../components/StatusBadge"

function fmtUptime(s: number) {
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  return d > 0 ? `${d}d ${h}h ${m}m` : h > 0 ? `${h}h ${m}m` : `${m}m`
}

function fmtTs(ts: number) {
  if (!ts) return "—"
  return new Date(ts * 1000).toLocaleString()
}

function fmtTokens(n: number) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

interface UsageSummary {
  totals: UsageTotals
  topModels: UsageModelRow[]
  providers: UsageModelRow[]
}

export default function DashboardPage() {
  // 15s polling — keeps the pool health view fresh without hammering the API
  const { data, isLoading, error } = useQuery({
    queryKey: ["dashboard"],
    queryFn: () => api<DashboardData>("/api/dashboard"),
    refetchInterval: 15_000,
  })
  // 24h usage metrics, scoped to the signed-in user (superadmin sees all)
  const usage = useQuery({
    queryKey: ["dashboard", "usage"],
    queryFn: () => {
      const to = Math.ceil(Date.now() / 1000)
      return api<UsageSummary>(`/api/usage/summary?from=${to - 24 * 3600}&to=${to}`)
    },
    refetchInterval: 60_000,
  })

  if (isLoading) return <Text>Loading…</Text>
  if (error || !data) return <Text color="red.fg">Failed to load dashboard</Text>

  const totalHealthy = data.providers.reduce((n, p) => n + p.healthy, 0)
  const totalKeys = data.providers.reduce((n, p) => n + p.total, 0)
  const t = usage.data?.totals

  return (
    <VStack align="stretch" gap={6}>
      <SimpleGrid columns={{ base: 2, lg: 4 }} gap={4}>
        <Card.Root>
          <Card.Body>
            <Stat.Root>
              <Stat.Label>Uptime</Stat.Label>
              <Stat.ValueText>{fmtUptime(data.uptimeS)}</Stat.ValueText>
            </Stat.Root>
          </Card.Body>
        </Card.Root>
        <Card.Root>
          <Card.Body>
            <Stat.Root>
              <Stat.Label>Healthy upstream keys</Stat.Label>
              <Stat.ValueText>
                {totalHealthy}
                <Stat.ValueUnit>/{totalKeys}</Stat.ValueUnit>
              </Stat.ValueText>
            </Stat.Root>
          </Card.Body>
        </Card.Root>
        <Card.Root>
          <Card.Body>
            <Stat.Root>
              <Stat.Label>Requests (24h)</Stat.Label>
              <Stat.ValueText>{t ? t.requests.toLocaleString() : "—"}</Stat.ValueText>
              <Stat.HelpText>{t ? `${t.errors} failed` : ""}</Stat.HelpText>
            </Stat.Root>
          </Card.Body>
        </Card.Root>
        <Card.Root>
          <Card.Body>
            <Stat.Root>
              <Stat.Label>Tokens (24h)</Stat.Label>
              <Stat.ValueText>
                {t ? fmtTokens(t.inputTokens) : "—"}
                <Stat.ValueUnit>in</Stat.ValueUnit>
                {t ? fmtTokens(t.outputTokens) : ""}
                <Stat.ValueUnit>out</Stat.ValueUnit>
              </Stat.ValueText>
            </Stat.Root>
          </Card.Body>
        </Card.Root>
      </SimpleGrid>

      {(t?.requests ?? 0) > 0 && (
        <Card.Root>
          <Card.Header>
            <HStack justify="space-between">
              <Heading size="sm">Busiest models (24h)</Heading>
              <Text fontSize="xs" color="fg.muted">
                {usage.data?.providers?.map((p) => `${p.key}: ${p.requests}`).join(" · ")}
              </Text>
            </HStack>
          </Card.Header>
          <Card.Body pt={0}>
            <HStack gap={6} flexWrap="wrap">
              {(usage.data?.topModels ?? []).slice(0, 5).map((m) => (
                <VStack align="start" gap={0} key={m.key}>
                  <Text fontFamily="mono" fontSize="xs" truncate maxW="260px">
                    {m.key}
                  </Text>
                  <Text fontSize="sm" color="fg.muted">
                    {m.requests} reqs · {fmtTokens(m.inputTokens + m.outputTokens)} tokens
                  </Text>
                </VStack>
              ))}
              {(usage.data?.topModels ?? []).length === 0 && (
                <Text fontSize="sm" color="fg.muted">
                  No usage yet
                </Text>
              )}
            </HStack>
          </Card.Body>
        </Card.Root>
      )}

      <VStack align="stretch" gap={3}>
        <Heading size="md">Provider pools</Heading>
        {data.providers.map((p) => (
          <Card.Root key={p.name}>
            <Card.Body pt={3} pb={2} px={4}>
              <HStack justify="space-between" mb={2}>
                <HStack gap={2}>
                  <Heading size="sm">{p.name}</Heading>
                  <Badge variant="outline">{p.type}</Badge>
                  <Badge colorPalette={p.healthy > 0 ? "green" : "red"}>
                    {p.healthy}/{p.total} healthy
                  </Badge>
                </HStack>
              </HStack>
              <Table.Root size="sm">
                <Table.Header>
                  <Table.Row>
                    <Table.ColumnHeader>Key</Table.ColumnHeader>
                    <Table.ColumnHeader>Status</Table.ColumnHeader>
                    <Table.ColumnHeader>Requests</Table.ColumnHeader>
                    <Table.ColumnHeader>429s</Table.ColumnHeader>
                    <Table.ColumnHeader>Errors</Table.ColumnHeader>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {p.keys.map((k) => (
                    <Table.Row key={k.hash}>
                      <Table.Cell fontFamily="mono" fontSize="xs">
                        {k.label}
                      </Table.Cell>
                      <Table.Cell>
                        <HStack gap={2}>
                          <StatusBadge status={k.status} />
                          {k.cooldown_remaining && (
                            <Text fontSize="xs" color="fg.muted">
                              {k.cooldown_remaining}
                            </Text>
                          )}
                        </HStack>
                      </Table.Cell>
                      <Table.Cell>{k.requests}</Table.Cell>
                      <Table.Cell>{k.rate_limited}</Table.Cell>
                      <Table.Cell>{k.errors}</Table.Cell>
                    </Table.Row>
                  ))}
                </Table.Body>
              </Table.Root>
            </Card.Body>
          </Card.Root>
        ))}
      </VStack>

      <VStack align="stretch" gap={3}>
        <Heading size="md">Recent activity</Heading>
        <Card.Root>
          <Table.Root size="sm">
            <Table.Header>
              <Table.Row>
                <Table.ColumnHeader>Time</Table.ColumnHeader>
                <Table.ColumnHeader>User</Table.ColumnHeader>
                <Table.ColumnHeader>Key</Table.ColumnHeader>
                <Table.ColumnHeader>Provider</Table.ColumnHeader>
                <Table.ColumnHeader>Model</Table.ColumnHeader>
                <Table.ColumnHeader>Status</Table.ColumnHeader>
                <Table.ColumnHeader>ms</Table.ColumnHeader>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {data.activity.length === 0 && (
                <Table.Row>
                  <Table.Cell colSpan={7} color="fg.muted" textAlign="center">
                    No requests yet
                  </Table.Cell>
                </Table.Row>
              )}
              {data.activity.map((e, i) => (
                <Table.Row key={`${e.ts}-${i}`}>
                  <Table.Cell whiteSpace="nowrap">{fmtTs(e.ts)}</Table.Cell>
                  <Table.Cell>{e.user || "—"}</Table.Cell>
                  <Table.Cell>{e.keyAlias || "—"}</Table.Cell>
                  <Table.Cell>{e.provider}</Table.Cell>
                  <Table.Cell fontFamily="mono" fontSize="xs" truncate maxW="220px">
                    {e.model}
                  </Table.Cell>
                  <Table.Cell>
                    <Badge
                      colorPalette={e.status === 200 ? "green" : "red"}
                      variant="subtle"
                    >
                      {e.status}
                      {e.failReason ? " failed" : ""}
                    </Badge>
                  </Table.Cell>
                  <Table.Cell>{e.durationMs}</Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table.Root>
        </Card.Root>
      </VStack>
    </VStack>
  )
}
