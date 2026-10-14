import { SimpleGrid, VStack } from "@chakra-ui/react"
import { useUsageBreakdown, useUsageOverview } from "../../api/usage"
import type { UsageFilters } from "../../api/usage"
import type { UsageOverviewPoint } from "../../api/types"
import { useChartTheme } from "../../components/chartTheme"
import { cacheHitRate, fmtCompact, fmtDuration, fmtInt, fmtPct, providerLabel } from "./format"
import { ChartCard, ShareDonut, TimeChart, allZero } from "./UsageChartKit"
import type { ChartRow, ChartSeries } from "./UsageChartKit"
import SeriesCard from "./SeriesCard"
import { OverviewStats, RangeNotice } from "./UsageParts"
import { useUsageFilters } from "./urlState"

const PROVIDER_SLICES = 8

export function toRows(points: UsageOverviewPoint[] | undefined): ChartRow[] {
  return (points ?? []).map((p) => ({
    ts: p.ts,
    requests: p.requests,
    errors: p.errors,
    inputTokens: p.inputTokens,
    cachedTokens: p.cachedTokens,
    outputTokens: p.outputTokens,
    avgDurationMs: p.avgDurationMs,
  }))
}

/** Per-bucket cache hit rate in percent; null (a gap) for buckets with no input. */
function hitRateRows(points: UsageOverviewPoint[] | undefined): ChartRow[] {
  return (points ?? []).map((p) => {
    const rate = cacheHitRate(p.cachedTokens, p.inputTokens)
    return { ts: p.ts, cacheHit: rate === null ? null : rate * 100 }
  })
}

const fmtPercentValue = (n: number) => fmtPct(n / 100)
const PERCENT_DOMAIN: [number, number] = [0, 100]

/** Latency-over-time card from overview points (shared with the model detail page). */
export function LatencyCard({ filters }: { filters: UsageFilters }) {
  const theme = useChartTheme()
  const overview = useUsageOverview(filters)
  const rows = toRows(overview.data?.points)
  const series: ChartSeries[] = [{ dataKey: "avgDurationMs", name: "avg latency", color: theme.series[3] }]
  return (
    <ChartCard
      title="Latency"
      subtitle="Average request duration per bucket"
      loading={overview.isPending}
      error={overview.error?.message}
      isEmpty={allZero(rows, series)}
    >
      <TimeChart rows={rows} series={series} bucket={overview.data?.bucket ?? "hour"} format={fmtDuration} />
    </ChartCard>
  )
}

export default function OverviewTab() {
  const { filters, showUsers } = useUsageFilters()
  const theme = useChartTheme()
  const overview = useUsageOverview(filters)
  const providers = useUsageBreakdown(filters, {
    groupBy: "provider",
    sort: "requests",
    order: "desc",
    page: 1,
    pageSize: PROVIDER_SLICES,
  })

  const data = overview.data
  const bucket = data?.bucket ?? "hour"
  const rows = toRows(data?.points)
  const loading = overview.isPending
  const error = overview.error?.message

  const reqSeries: ChartSeries[] = [
    { dataKey: "requests", name: "requests", color: theme.series[0] },
    { dataKey: "errors", name: "errors", color: theme.danger },
  ]
  const tokenSeries: ChartSeries[] = [
    { dataKey: "inputTokens", name: "input", color: theme.series[0] },
    // cached is a subset of input — same scale, dashed so it reads as "part of"
    { dataKey: "cachedTokens", name: "cached input", color: theme.series[2], dashed: true },
    // output is often orders of magnitude below input — its own right-hand scale
    { dataKey: "outputTokens", name: "output (right axis)", color: theme.series[1], axis: "right" },
  ]
  const hitRows = hitRateRows(data?.points)
  const hitSeries: ChartSeries[] = [{ dataKey: "cacheHit", name: "cache hit", color: theme.series[2] }]
  const shares = (providers.data?.rows ?? [])
    .filter((r) => r.requests > 0)
    .map((r) => ({ label: providerLabel(r.provider), value: r.requests, muted: !r.provider }))

  return (
    <VStack align="stretch" gap={4}>
      <RangeNotice filters={filters} range={data?.range} />
      <OverviewStats totals={data?.totals} loading={loading} showUsers={showUsers} />
      <SimpleGrid columns={{ base: 1, xl: 2 }} gap={4}>
        <ChartCard
          title="Requests & errors"
          loading={loading}
          error={error}
          isEmpty={allZero(rows, reqSeries)}
        >
          <TimeChart rows={rows} series={reqSeries} bucket={bucket} kind="area" format={fmtInt} />
        </ChartCard>
        <ChartCard
          title="Tokens"
          subtitle="Input and cached input (left axis) / output (right axis) per bucket"
          loading={loading}
          error={error}
          isEmpty={allZero(rows, tokenSeries)}
        >
          <TimeChart rows={rows} series={tokenSeries} bucket={bucket} format={fmtCompact} />
        </ChartCard>
        <ChartCard
          title="Cache hit rate"
          subtitle="Share of input tokens served from the prompt cache; gaps mark buckets with no input"
          loading={loading}
          error={error}
          isEmpty={allZero(hitRows, hitSeries)}
        >
          <TimeChart
            rows={hitRows}
            series={hitSeries}
            bucket={bucket}
            format={fmtPercentValue}
            domain={PERCENT_DOMAIN}
            dots
          />
        </ChartCard>
        <LatencyCard filters={filters} />
        <SeriesCard filters={filters} groupBy="model" />
        <ChartCard
          title="Requests by provider"
          subtitle="Share of requests in the range"
          loading={providers.isPending}
          error={providers.error?.message}
          isEmpty={shares.length === 0}
        >
          <ShareDonut items={shares} />
        </ChartCard>
      </SimpleGrid>
    </VStack>
  )
}
