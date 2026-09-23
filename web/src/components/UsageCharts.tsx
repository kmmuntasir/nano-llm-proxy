import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import { Button, Card, HStack, SimpleGrid, Text, VStack } from "@chakra-ui/react"
import { api } from "../api/client"
import type { UsageModelRow } from "../api/types"
import { useColorMode } from "./ui/color-mode"

// Dashboard charts over the persisted usage log. Palette slots are the
// validated dataviz defaults (blue/orange), stepped per color mode; chrome
// (grid, ticks, surfaces) follows the same system. One measure per chart —
// no dual axes.

interface UsagePoint {
  ts: number
  requests: number
  inputTokens: number
  outputTokens: number
}
interface UsageProviderPoint {
  ts: number
  provider: string
  requests: number
}
interface TimeseriesData {
  bucket: number
  points: UsagePoint[]
  providers: UsageProviderPoint[]
}
interface UsageSummary {
  totals: { requests: number; errors: number; inputTokens: number; outputTokens: number }
  topModels: UsageModelRow[]
  providers: UsageModelRow[]
}

// validated categorical slots, light + dark steps
const PALETTE = {
  light: { s1: "#2a78d6", s2: "#eb6834", grid: "#e1e0d9", tick: "#898781", axis: "#c3c2b7", surface: "#ffffff", ink: "#0b0b0b", ink2: "#52514e" },
  dark: { s1: "#3987e5", s2: "#d95926", grid: "#2c2c2a", tick: "#898781", axis: "#383835", surface: "#1a1a19", ink: "#ffffff", ink2: "#c3c2b7" },
}

const RANGES = [
  { label: "Today", days: 1 },
  { label: "7d", days: 7 },
  { label: "30d", days: 30 },
]

function fmtCompact(n: number) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(0)}k`
  return String(n)
}

export default function UsageCharts() {
  const { colorMode } = useColorMode()
  const c = PALETTE[colorMode === "dark" ? "dark" : "light"]
  const [range, setRange] = useState("Today")

  const days = RANGES.find((r) => r.label === range)?.days ?? 1
  const now = Date.now() / 1000
  const from = range === "Today" ? Math.floor(now) - 86400 : Math.floor(now) - days * 86400
  const to = Math.ceil(now)
  const tz = -new Date().getTimezoneOffset() * 60
  const qs = `from=${from}&to=${to}&tz=${tz}`

  const series = useQuery({
    queryKey: ["usage", "timeseries", from, to],
    queryFn: () => api<TimeseriesData>(`/api/usage/timeseries?${qs}`),
  })
  const summary = useQuery({
    queryKey: ["usage", "summary", from, to],
    queryFn: () => api<UsageSummary>(`/api/usage/summary?${qs}`),
  })

  const hourBuckets = range === "Today"
  const fmtX = (ts: number) => {
    const d = new Date(ts * 1000)
    return hourBuckets
      ? d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
      : d.toLocaleDateString([], { month: "short", day: "numeric" })
  }

  const points = (series.data?.points ?? []).map((p) => ({ ...p, x: fmtX(p.ts) }))

  // pivot [{ts, provider, requests}] into rows {x, zen: n, kilo: m, ...}
  const names = [...new Set((series.data?.providers ?? []).map((p) => p.provider))]
  const byTs = new Map<number, Record<string, number>>()
  for (const p of series.data?.providers ?? []) {
    const row = byTs.get(p.ts) ?? {}
    row[p.provider] = p.requests
    byTs.set(p.ts, row)
  }
  const providerRows = [...byTs.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([ts, row]) => ({ x: fmtX(ts), ...row }))
  const providerColors = names.map((_, i) => (i === 0 ? c.s1 : i === 1 ? c.s2 : c.tick))

  const topModels = (summary.data?.topModels ?? []).slice(0, 5)
  const tooltipProps = {
    cursor: { stroke: c.grid, strokeWidth: 1 },
    contentStyle: { background: c.surface, border: `1px solid ${c.grid}`, borderRadius: 8, fontSize: 12 },
    labelStyle: { color: c.ink2, fontSize: 12 },
    itemStyle: { color: c.ink, fontSize: 12 },
  }
  const axis = {
    tick: { fill: c.tick, fontSize: 11 },
    tickLine: false,
    axisLine: { stroke: c.axis },
  }

  return (
    <Card.Root>
      <Card.Header pb={2}>
        <HStack justify="space-between" flexWrap="wrap" gap={2}>
          <VStack align="start" gap={0}>
            <Text fontWeight="medium">Requests &amp; tokens</Text>
            <Text fontSize="xs" color="fg.muted">
              Usage over time — buckets are local hours (Today) or days
            </Text>
          </VStack>
          <HStack gap={2}>
            {RANGES.map((r) => (
              <Button
                key={r.label}
                size="2xs"
                variant={range === r.label ? "solid" : "outline"}
                colorPalette={range === r.label ? "blue" : "gray"}
                onClick={() => setRange(r.label)}
              >
                {r.label}
              </Button>
            ))}
          </HStack>
        </HStack>
      </Card.Header>
      <Card.Body pt={3} gap={6}>
        <SimpleGrid columns={{ base: 1, xl: 2 }} gap={6}>
          <VStack align="stretch" gap={1}>
            <Text fontSize="sm" color="fg.muted">
              Requests
            </Text>
            <ResponsiveContainer width="100%" height={200}>
              <AreaChart data={points} margin={{ top: 8, right: 8, left: -16, bottom: 0 }}>
                <defs>
                  <linearGradient id="reqFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={c.s1} stopOpacity={0.18} />
                    <stop offset="100%" stopColor={c.s1} stopOpacity={0.02} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke={c.grid} vertical={false} />
                <XAxis dataKey="x" {...axis} />
                <YAxis allowDecimals={false} {...axis} />
                <Tooltip {...tooltipProps} />
                <Area
                  type="monotone"
                  dataKey="requests"
                  name="requests"
                  stroke={c.s1}
                  strokeWidth={2}
                  fill="url(#reqFill)"
                  dot={false}
                  activeDot={{ r: 4, stroke: c.surface, strokeWidth: 2 }}
                />
              </AreaChart>
            </ResponsiveContainer>
          </VStack>

          <VStack align="stretch" gap={1}>
            <Text fontSize="sm" color="fg.muted">
              Tokens (input / output)
            </Text>
            <ResponsiveContainer width="100%" height={200}>
              <LineChart data={points} margin={{ top: 8, right: 8, left: -16, bottom: 0 }}>
                <CartesianGrid stroke={c.grid} vertical={false} />
                <XAxis dataKey="x" {...axis} />
                <YAxis allowDecimals={false} {...axis} tickFormatter={fmtCompact} />
                <Tooltip {...tooltipProps} />
                <Legend
                  iconType="plainline"
                  wrapperStyle={{ fontSize: 12, color: c.ink2 }}
                />
                <Line type="monotone" dataKey="inputTokens" name="input" stroke={c.s1} strokeWidth={2} dot={false} activeDot={{ r: 4, stroke: c.surface, strokeWidth: 2 }} />
                <Line type="monotone" dataKey="outputTokens" name="output" stroke={c.s2} strokeWidth={2} dot={false} activeDot={{ r: 4, stroke: c.surface, strokeWidth: 2 }} />
              </LineChart>
            </ResponsiveContainer>
          </VStack>

          <VStack align="stretch" gap={1}>
            <Text fontSize="sm" color="fg.muted">
              Requests by provider
            </Text>
            <ResponsiveContainer width="100%" height={200}>
              <BarChart data={providerRows} margin={{ top: 8, right: 8, left: -16, bottom: 0 }}>
                <CartesianGrid stroke={c.grid} vertical={false} />
                <XAxis dataKey="x" {...axis} />
                <YAxis allowDecimals={false} {...axis} />
                <Tooltip {...tooltipProps} />
                <Legend wrapperStyle={{ fontSize: 12, color: c.ink2 }} />
                {names.map((name, i) => (
                  <Bar
                    key={name}
                    dataKey={name}
                    stackId="a"
                    fill={providerColors[i]}
                    barSize={20}
                    stroke={c.surface}
                    strokeWidth={1}
                  />
                ))}
              </BarChart>
            </ResponsiveContainer>
          </VStack>

          <VStack align="stretch" gap={1}>
            <Text fontSize="sm" color="fg.muted">
              Top models (total tokens)
            </Text>
            <ResponsiveContainer width="100%" height={200}>
              <BarChart
                data={topModels.map((m) => ({ key: m.key, tokens: m.inputTokens + m.outputTokens }))}
                layout="vertical"
                margin={{ top: 4, right: 48, left: 8, bottom: 0 }}
              >
                <CartesianGrid stroke={c.grid} horizontal={false} />
                <XAxis type="number" {...axis} tickFormatter={fmtCompact} />
                <YAxis type="category" dataKey="key" width={120} {...axis} />
                <Tooltip {...tooltipProps} cursor={{ fill: c.grid, fillOpacity: 0.3 }} />
                <Bar dataKey="tokens" name="tokens" barSize={16} radius={[0, 4, 4, 0]}>
                  {topModels.map((m) => (
                    <Cell key={m.key} fill={c.s1} />
                  ))}
                </Bar>
              </BarChart>
            </ResponsiveContainer>
          </VStack>
        </SimpleGrid>
        {(series.data?.points ?? []).length === 0 && (
          <Text fontSize="sm" color="fg.muted">
            No usage recorded in this range yet.
          </Text>
        )}
      </Card.Body>
    </Card.Root>
  )
}
