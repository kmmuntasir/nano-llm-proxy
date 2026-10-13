import type { ReactNode } from "react"
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import { Box, Card, Heading, HStack, Skeleton, Text } from "@chakra-ui/react"
import type { UsageBucket, UsageSeries } from "../../api/types"
import { useChartTheme } from "../../components/chartTheme"
import type { ChartTheme } from "../../components/chartTheme"
import { REJECTED_PROVIDER, fmtBucketLabel, fmtBucketTick, fmtCompact, fmtInt } from "./format"

// Chart building blocks for the Usage section, all themed from the shared
// chart palette. Recharts reads dataKeys as property paths, so series are
// pivoted onto synthetic keys (s0, s1, ...) — model ids contain dots.

export type ChartRow = { ts: number } & Record<string, number>

export interface ChartSeries {
  dataKey: string
  name: string
  color: string
  /** plot against the secondary (right) Y axis — for a measure on a different scale */
  axis?: "right"
}

const LEFT = "left"
const RIGHT = "right"

/**
 * Series label with pre-routing rejections spelled out: those rows have an
 * empty provider, so provider series come back as "" and model series as
 * "/<model>".
 */
function seriesLabel(line: { key: string; label: string }): string {
  const label = line.label || line.key
  if (!label) return REJECTED_PROVIDER
  if (label.startsWith("/")) return `${REJECTED_PROVIDER}${label}`
  return label
}

const MARGIN = { top: 8, right: 12, left: 0, bottom: 4 }
const DEFAULT_HEIGHT = 220
const Y_AXIS_WIDTH = 64

export function ChartCard({
  title,
  subtitle,
  loading,
  error,
  isEmpty,
  height = DEFAULT_HEIGHT,
  actions,
  children,
}: {
  title: string
  subtitle?: string
  /** header controls (e.g. a metric picker) — stay visible when the chart is empty */
  actions?: ReactNode
  loading?: boolean
  error?: string
  isEmpty?: boolean
  height?: number
  children: ReactNode
}) {
  let body: ReactNode = children
  if (error) {
    body = (
      <Text color="red.fg" fontSize="sm">
        {error}
      </Text>
    )
  } else if (loading) {
    body = <Skeleton h={`${height}px`} rounded="md" />
  } else if (isEmpty) {
    body = (
      <Box h={`${height}px`} display="grid" placeItems="center">
        <Text color="fg.muted" fontSize="sm">
          No usage in this range
        </Text>
      </Box>
    )
  }
  return (
    <Card.Root minW={0}>
      <Card.Header pb={1}>
        <HStack justify="space-between" align="start" gap={2} flexWrap="wrap">
          <Box minW={0}>
            <Heading size="sm">{title}</Heading>
            {subtitle && (
              <Text fontSize="xs" color="fg.muted">
                {subtitle}
              </Text>
            )}
          </Box>
          {actions}
        </HStack>
      </Card.Header>
      <Card.Body pt={2} minW={0}>
        {body}
      </Card.Body>
    </Card.Root>
  )
}

/** Pivots a /series response onto chart rows + synthetic-key series. */
export function pivotSeries(data: UsageSeries | undefined, theme: ChartTheme) {
  const lines = data?.series ?? []
  const byTs = new Map<number, ChartRow>()
  lines.forEach((line, i) => {
    for (const p of line.points ?? []) {
      const row = byTs.get(p.ts) ?? ({ ts: p.ts } as ChartRow)
      row[`s${i}`] = p.value
      byTs.set(p.ts, row)
    }
  })
  const rows = [...byTs.values()].sort((a, b) => a.ts - b.ts)
  const series: ChartSeries[] = lines.map((line, i) => ({
    dataKey: `s${i}`,
    name: seriesLabel(line),
    color: theme.color(i, line.key),
  }))
  return { rows, series }
}

/** True when there is nothing worth drawing (no rows, or every value zero). */
export function allZero(rows: ChartRow[], series: ChartSeries[]): boolean {
  return rows.every((r) => series.every((s) => !r[s.dataKey]))
}

export function TimeChart({
  rows,
  series,
  bucket,
  kind = "line",
  stacked = false,
  format = fmtCompact,
  rightFormat = format,
  height = DEFAULT_HEIGHT,
}: {
  rows: ChartRow[]
  series: ChartSeries[]
  bucket: UsageBucket
  kind?: "line" | "area" | "bar"
  stacked?: boolean
  format?: (n: number) => string
  /** tick formatter for the right axis (only drawn when a series asks for it) */
  rightFormat?: (n: number) => string
  height?: number
}) {
  const theme = useChartTheme()
  const firstTs = rows[0]?.ts ?? 0
  const rightSeries = series.find((s) => s.axis === "right")
  const tick0 = (fmt: (n: number) => string) => (v: unknown) =>
    Number(v) === 0 ? "0" : fmt(Number(v))
  const axisOf = (s: ChartSeries) => (s.axis === "right" ? RIGHT : LEFT)
  const common = (
    <>
      <CartesianGrid stroke={theme.grid} vertical={false} />
      <XAxis
        dataKey="ts"
        tickMargin={8}
        minTickGap={24}
        tickFormatter={(v) => fmtBucketTick(Number(v), bucket, firstTs)}
        {...theme.axis}
      />
      <YAxis
        yAxisId={LEFT}
        width={Y_AXIS_WIDTH}
        allowDecimals={false}
        tickFormatter={tick0(format)}
        {...theme.axis}
      />
      {rightSeries && (
        <YAxis
          yAxisId={RIGHT}
          orientation="right"
          width={Y_AXIS_WIDTH}
          allowDecimals={false}
          tickFormatter={tick0(rightFormat)}
          {...theme.axis}
          tick={{ ...theme.axis.tick, fill: rightSeries.color }}
          axisLine={{ stroke: rightSeries.color }}
        />
      )}
      <Tooltip
        {...theme.tooltip}
        cursor={kind === "bar" ? { fill: theme.grid, fillOpacity: 0.4 } : theme.tooltip.cursor}
        labelFormatter={(l) => fmtBucketLabel(Number(l), bucket)}
        formatter={(v, _name, item) => {
          const s = series.find((x) => x.dataKey === item.dataKey)
          return (s?.axis === "right" ? rightFormat : format)(Number(v))
        }}
      />
      {series.length > 1 && <Legend {...theme.legend} />}
    </>
  )
  const activeDot = { r: 4, stroke: theme.surface, strokeWidth: 2 }
  const stackId = stacked ? "a" : undefined

  let chart: ReactNode
  if (kind === "bar") {
    chart = (
      <BarChart data={rows} margin={MARGIN}>
        {common}
        {series.map((s) => (
          <Bar
            key={s.dataKey}
            yAxisId={axisOf(s)}
            dataKey={s.dataKey}
            name={s.name}
            stackId={stackId}
            fill={s.color}
            stroke={theme.surface}
            strokeWidth={stacked ? 1 : 0}
            maxBarSize={28}
          />
        ))}
      </BarChart>
    )
  } else if (kind === "area") {
    chart = (
      <AreaChart data={rows} margin={MARGIN}>
        {common}
        {series.map((s) => (
          <Area
            key={s.dataKey}
            yAxisId={axisOf(s)}
            type="monotone"
            dataKey={s.dataKey}
            name={s.name}
            stackId={stackId}
            stroke={s.color}
            fill={s.color}
            fillOpacity={0.15}
            strokeWidth={2}
            dot={false}
            activeDot={activeDot}
          />
        ))}
      </AreaChart>
    )
  } else {
    chart = (
      <LineChart data={rows} margin={MARGIN}>
        {common}
        {series.map((s) => (
          <Line
            key={s.dataKey}
            yAxisId={axisOf(s)}
            type="monotone"
            dataKey={s.dataKey}
            name={s.name}
            stroke={s.color}
            strokeWidth={2}
            dot={false}
            activeDot={activeDot}
          />
        ))}
      </LineChart>
    )
  }

  return (
    <ResponsiveContainer width="100%" height={height}>
      {chart}
    </ResponsiveContainer>
  )
}

export interface ShareItem {
  label: string
  value: number
  /** draw in the neutral slot (e.g. rejected requests) */
  muted?: boolean
}

/** Donut of shares (e.g. requests by provider). */
export function ShareDonut({
  items,
  format = fmtInt,
  height = DEFAULT_HEIGHT,
}: {
  items: ShareItem[]
  format?: (n: number) => string
  height?: number
}) {
  const theme = useChartTheme()
  const data = items.map((it, i) => ({ ...it, fill: it.muted ? theme.other : theme.color(i, it.label) }))
  return (
    <ResponsiveContainer width="100%" height={height}>
      <PieChart>
        <Pie
          data={data}
          dataKey="value"
          nameKey="label"
          innerRadius="50%"
          outerRadius="80%"
          paddingAngle={1}
          stroke={theme.surface}
          isAnimationActive={false}
        />
        <Tooltip {...theme.tooltip} formatter={(v) => format(Number(v))} />
        <Legend {...theme.legend} />
      </PieChart>
    </ResponsiveContainer>
  )
}

/** Horizontal ranked bars (e.g. top user×model pairs). */
export function RankedBars({
  items,
  name,
  format = fmtCompact,
}: {
  items: ShareItem[]
  name: string
  format?: (n: number) => string
}) {
  const theme = useChartTheme()
  const rowHeight = 30
  const height = Math.max(120, items.length * rowHeight + 32)
  return (
    <ResponsiveContainer width="100%" height={height}>
      <BarChart data={items} layout="vertical" margin={{ top: 4, right: 16, left: 0, bottom: 4 }}>
        <CartesianGrid stroke={theme.grid} horizontal={false} />
        <XAxis type="number" allowDecimals={false} tickFormatter={(v) => format(Number(v))} {...theme.axis} />
        <YAxis
          type="category"
          dataKey="label"
          width={170}
          interval={0}
          // non-breaking spaces: Recharts wraps tick text on whitespace
          tickFormatter={(v: string) => (v.length > 24 ? `${v.slice(0, 23)}…` : v).replace(/ /g, "\u00a0")}
          {...theme.axis}
        />
        <Tooltip
          {...theme.tooltip}
          cursor={{ fill: theme.grid, fillOpacity: 0.4 }}
          formatter={(v) => format(Number(v))}
        />
        <Bar dataKey="value" name={name} fill={theme.series[0]} maxBarSize={18} radius={[0, 4, 4, 0]} />
      </BarChart>
    </ResponsiveContainer>
  )
}
