import { Alert, Badge, Card, SimpleGrid, Skeleton, Stat } from "@chakra-ui/react"
import type { UsageOverviewTotals, UsageRange } from "../../api/types"
import type { UsageFilters } from "../../api/usage"
import {
  errorRate,
  fmtCacheHit,
  fmtCompact,
  fmtDateTime,
  fmtDuration,
  fmtInt,
  fmtPct,
  providerLabel,
} from "./format"

// Small shared pieces of the Usage section: stat cards, the capped-range
// notice and the request status badge.

// tolerate the minute rounding of preset ranges before calling a range capped
const RANGE_SLACK_S = 60

/** Shown when the server answered for a different range than requested (span cap). */
export function RangeNotice({ filters, range }: { filters: UsageFilters; range?: UsageRange }) {
  if (!range) return null
  const differs =
    Math.abs(range.from - filters.from) > RANGE_SLACK_S || Math.abs(range.to - filters.to) > RANGE_SLACK_S
  if (!differs) return null
  return (
    <Alert.Root status="warning" size="sm">
      <Alert.Indicator />
      <Alert.Title fontWeight="normal">
        Range limited by the server: showing {fmtDateTime(range.from)} – {fmtDateTime(range.to)}
      </Alert.Title>
    </Alert.Root>
  )
}

interface StatItem {
  label: string
  value: string
  help?: string
}

function StatCard({ item, loading }: { item: StatItem; loading: boolean }) {
  return (
    <Card.Root size="sm">
      <Card.Body>
        <Stat.Root>
          <Stat.Label>{item.label}</Stat.Label>
          {loading ? (
            <Skeleton h="28px" w="70%" />
          ) : (
            <Stat.ValueText flexWrap="wrap" fontSize={{ base: "lg", md: "xl" }}>{item.value}</Stat.ValueText>
          )}
          {item.help && !loading && <Stat.HelpText>{item.help}</Stat.HelpText>}
        </Stat.Root>
      </Card.Body>
    </Card.Root>
  )
}

/** The overview stat row; `showUsers` adds the distinct-users count (admin views). */
export function OverviewStats({
  totals,
  loading,
  showUsers,
}: {
  totals?: UsageOverviewTotals
  loading: boolean
  showUsers: boolean
}) {
  const t = totals
  const items: StatItem[] = [
    { label: "Requests", value: t ? fmtInt(t.requests) : "—" },
    {
      label: "Error rate",
      value: t ? fmtPct(errorRate(t.errors, t.requests)) : "—",
      help: t ? `${fmtInt(t.errors)} failed` : undefined,
    },
    {
      label: "Tokens in / out",
      value: t ? `${fmtCompact(t.inputTokens)} / ${fmtCompact(t.outputTokens)}` : "—",
    },
    {
      label: "Cache hit",
      value: t ? fmtCacheHit(t.cachedTokens, t.inputTokens) : "—",
      help: t ? `${fmtCompact(t.cachedTokens)} of ${fmtCompact(t.inputTokens)} input cached` : undefined,
    },
    {
      label: "Latency avg / p95",
      value: t ? `${fmtDuration(t.avgDurationMs)} / ${fmtDuration(t.p95DurationMs)}` : "—",
    },
    { label: "Avg TTFB", value: t ? fmtDuration(t.avgTtfbMs) : "—" },
    showUsers
      ? {
          label: "Active users / models",
          value: t ? `${fmtInt(t.users)} / ${fmtInt(t.models)}` : "—",
        }
      : { label: "Models used", value: t ? fmtInt(t.models) : "—" },
  ]
  return (
    <SimpleGrid columns={{ base: 2, md: 4, "2xl": 7 }} gap={3}>
      {items.map((item) => (
        <StatCard key={item.label} item={item} loading={loading} />
      ))}
    </SimpleGrid>
  )
}

/** Provider name; an empty provider (rejected before routing) renders muted. */
export function ProviderBadge({ provider }: { provider: string }) {
  if (!provider) {
    return (
      <Badge variant="subtle" size="sm" color="fg.muted" fontStyle="italic">
        {providerLabel(provider)}
      </Badge>
    )
  }
  return (
    <Badge variant="outline" size="sm">
      {provider}
    </Badge>
  )
}

/** ok (< 400) green, error red; shows the HTTP status. */
export function RequestStatusBadge({ status }: { status: number }) {
  const ok = status > 0 && status < 400
  return (
    <Badge colorPalette={ok ? "green" : "red"} variant="subtle">
      {status || "error"}
    </Badge>
  )
}
