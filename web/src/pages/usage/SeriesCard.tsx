import { NativeSelect } from "@chakra-ui/react"
import { useSearchParams } from "react-router-dom"
import type { UsageSeriesGroupBy, UsageSeriesMetric } from "../../api/types"
import { useUsageSeries } from "../../api/usage"
import type { UsageFilters } from "../../api/usage"
import { useChartTheme } from "../../components/chartTheme"
import { fmtCompact, fmtInt } from "./format"
import { ChartCard, TimeChart, allZero, pivotSeries } from "./UsageChartKit"

const METRICS: { value: UsageSeriesMetric; label: string }[] = [
  { value: "requests", label: "Requests" },
  { value: "totalTokens", label: "Total tokens" },
  { value: "cachedTokens", label: "Cached tokens" },
  { value: "errors", label: "Errors" },
]
const TOP_N = 5
const METRIC_PARAM = "metric"

const GROUP_LABEL: Record<UsageSeriesGroupBy, string> = {
  user: "user",
  model: "model",
  provider: "provider",
  surface: "surface",
}

/** The series metric lives in the URL (?metric=) so it survives reloads and tab switches. */
function useSeriesMetric(): [UsageSeriesMetric, (m: UsageSeriesMetric) => void] {
  const [sp, setSp] = useSearchParams()
  const metric = METRICS.find((m) => m.value === sp.get(METRIC_PARAM))?.value ?? "requests"
  const set = (m: UsageSeriesMetric) =>
    setSp((prev) => {
      const next = new URLSearchParams(prev)
      if (m === "requests") next.delete(METRIC_PARAM)
      else next.set(METRIC_PARAM, m)
      return next
    })
  return [metric, set]
}

// SeriesCard: stacked top-N series (+ "other") for one grouping, with a
// metric picker.
export default function SeriesCard({
  filters,
  groupBy,
  title,
}: {
  filters: UsageFilters
  groupBy: UsageSeriesGroupBy
  title?: string
}) {
  const theme = useChartTheme()
  const [metric, setMetric] = useSeriesMetric()
  const query = useUsageSeries(filters, { groupBy, metric, top: TOP_N })
  const { rows, series } = pivotSeries(query.data, theme)
  const metricLabel = METRICS.find((m) => m.value === metric)?.label ?? metric

  return (
    <ChartCard
      title={title ?? `${metricLabel} by ${GROUP_LABEL[groupBy]}`}
      subtitle={`Top ${TOP_N} ${GROUP_LABEL[groupBy]}s, the rest folded into "Other"`}
      loading={query.isPending}
      error={query.error?.message}
      isEmpty={allZero(rows, series)}
      actions={
        <NativeSelect.Root size="xs" w="auto">
          <NativeSelect.Field
            aria-label="Chart metric"
            value={metric}
            onChange={(e) => {
              const m = METRICS.find((x) => x.value === e.target.value)
              if (m) setMetric(m.value)
            }}
          >
            {METRICS.map((m) => (
              <option key={m.value} value={m.value}>
                {m.label}
              </option>
            ))}
          </NativeSelect.Field>
          <NativeSelect.Indicator />
        </NativeSelect.Root>
      }
    >
      <TimeChart
        rows={rows}
        series={series}
        bucket={query.data?.bucket ?? "hour"}
        kind="bar"
        stacked
        format={metric === "totalTokens" || metric === "cachedTokens" ? fmtCompact : fmtInt}
      />
    </ChartCard>
  )
}
