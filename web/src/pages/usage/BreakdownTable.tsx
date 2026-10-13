import { HStack, Text, VStack } from "@chakra-ui/react"
import type {
  UsageBreakdownGroupBy,
  UsageBreakdownRow,
  UsageBreakdownSort,
} from "../../api/types"
import { useUsageBreakdown } from "../../api/usage"
import type { UsageFilters } from "../../api/usage"
import { PaginatedTable } from "../../components/PaginatedTable"
import type { PaginatedColumn } from "../../components/PaginatedTable"
import { errorRate, fmtCompact, fmtDuration, fmtInt, fmtPct, fmtShortDateTime } from "./format"
import { ProviderBadge, RangeNotice } from "./UsageParts"
import { useTableState } from "./urlState"

const SORTS: readonly UsageBreakdownSort[] = [
  "requests",
  "errors",
  "errorRate",
  "inputTokens",
  "outputTokens",
  "totalTokens",
  "avgDurationMs",
  "lastTs",
  "label",
]

type Col = PaginatedColumn<UsageBreakdownRow, UsageBreakdownSort>

export function userLabel(r: { userId: number; email: string }): string {
  return r.email || `user #${r.userId}`
}

function mono(text: string) {
  return (
    <Text as="span" fontFamily="mono" fontSize="xs" wordBreak="break-word">
      {text}
    </Text>
  )
}

function modelCell(r: UsageBreakdownRow) {
  return (
    <HStack gap={1} flexWrap={{ base: "wrap", md: "nowrap" }} justify={{ base: "end", md: "start" }}>
      <ProviderBadge provider={r.provider} />
      {mono(r.model)}
    </HStack>
  )
}

function labelColumns(groupBy: UsageBreakdownGroupBy, isSuperadmin: boolean): Col[] {
  const label = { sortKey: "label" as const, defaultOrder: "asc" as const }
  switch (groupBy) {
    case "user":
      return [{ id: "user", header: "User", render: (r) => userLabel(r), minW: "160px", ...label }]
    case "model":
      return [{ id: "model", header: "Model", render: modelCell, minW: "220px", ...label }]
    case "user_model":
      return [
        { id: "user", header: "User", render: (r) => userLabel(r), minW: "160px", ...label },
        { id: "model", header: "Model", render: modelCell, minW: "220px" },
      ]
    case "provider":
      return [
        {
          id: "provider",
          header: "Provider",
          render: (r) => <ProviderBadge provider={r.provider} />,
          ...label,
        },
      ]
    case "key": {
      const cols: Col[] = [
        { id: "key", header: "Key", render: (r) => r.alias || `#${r.keyId}`, ...label },
      ]
      if (isSuperadmin) {
        cols.push({ id: "owner", header: "Owner", render: (r) => userLabel(r) })
      }
      return cols
    }
    case "upstream_key":
      return [
        { id: "upstreamKey", header: "Upstream key", render: (r) => r.upstreamKey || "—", ...label },
        { id: "provider", header: "Provider", render: (r) => <ProviderBadge provider={r.provider} /> },
      ]
  }
}

const METRIC_COLUMNS: Col[] = [
  { id: "requests", header: "Requests", sortKey: "requests", align: "end", render: (r) => fmtInt(r.requests) },
  {
    id: "errors",
    header: "Errors",
    sortKey: "errors",
    align: "end",
    hideOnMobile: true,
    render: (r) => fmtInt(r.errors),
  },
  {
    id: "errorRate",
    header: "Error rate",
    sortKey: "errorRate",
    align: "end",
    render: (r) => (
      <Text as="span" color={r.errors > 0 ? "red.fg" : undefined}>
        {fmtPct(errorRate(r.errors, r.requests))}
      </Text>
    ),
  },
  {
    id: "in",
    header: "Tokens in",
    sortKey: "inputTokens",
    align: "end",
    hideOnMobile: true,
    render: (r) => fmtCompact(r.inputTokens),
  },
  {
    id: "out",
    header: "Tokens out",
    sortKey: "outputTokens",
    align: "end",
    hideOnMobile: true,
    render: (r) => fmtCompact(r.outputTokens),
  },
  {
    id: "total",
    header: "Total tokens",
    sortKey: "totalTokens",
    align: "end",
    render: (r) => fmtCompact(r.inputTokens + r.outputTokens),
  },
  {
    id: "latency",
    header: "Avg latency",
    sortKey: "avgDurationMs",
    align: "end",
    render: (r) => fmtDuration(r.avgDurationMs),
  },
  {
    id: "ttfb",
    header: "Avg TTFB",
    align: "end",
    hideOnMobile: true,
    render: (r) => fmtDuration(r.avgTtfbMs),
  },
  {
    id: "last",
    header: "Last seen",
    sortKey: "lastTs",
    nowrap: true,
    render: (r) => fmtShortDateTime(r.lastTs),
  },
]

function rowKey(groupBy: UsageBreakdownGroupBy, r: UsageBreakdownRow): string {
  switch (groupBy) {
    case "user":
      return `u${r.userId}`
    case "model":
      return `m${r.provider}/${r.model}`
    case "user_model":
      return `um${r.userId}:${r.provider}/${r.model}`
    case "provider":
      return `p${r.provider}`
    case "key":
      return `k${r.keyId}`
    case "upstream_key":
      return `uk${r.provider}:${r.upstreamKey}`
  }
}

// BreakdownTable: one /api/usage/breakdown grouping as a server-paged,
// server-sorted table. Its page/sort state lives in the URL under `prefix`.
export function BreakdownTable({
  filters,
  groupBy,
  prefix = "",
  isSuperadmin,
  q,
  onRowClick,
  defaultPageSize = 25,
  empty = "No usage in this range",
  showRangeNotice = false,
}: {
  filters: UsageFilters
  groupBy: UsageBreakdownGroupBy
  prefix?: string
  isSuperadmin: boolean
  q?: string
  onRowClick?: (row: UsageBreakdownRow) => void
  defaultPageSize?: number
  empty?: string
  /** surface the server's capped-range echo above the table */
  showRangeNotice?: boolean
}) {
  const table = useTableState(prefix, {
    sorts: SORTS,
    defaultSort: "requests",
    defaultPageSize,
  })
  const query = useUsageBreakdown(filters, {
    groupBy,
    sort: table.sort,
    order: table.order,
    page: table.page,
    pageSize: table.pageSize,
    q: q || undefined,
  })
  const data = query.data
  const totals = data?.totals

  return (
    <VStack align="stretch" gap={2}>
      {showRangeNotice && <RangeNotice filters={filters} range={data?.range} />}
      {totals && (
        <Text fontSize="xs" color="fg.muted">
          All rows: {fmtInt(totals.requests)} requests · {fmtInt(totals.errors)} errors ·{" "}
          {fmtCompact(totals.inputTokens)} in / {fmtCompact(totals.outputTokens)} out tokens
        </Text>
      )}
      <PaginatedTable
        columns={[...labelColumns(groupBy, isSuperadmin), ...METRIC_COLUMNS]}
        rows={data?.rows ?? []}
        rowKey={(r) => rowKey(groupBy, r)}
        total={data?.total ?? 0}
        page={table.page}
        pageSize={table.pageSize}
        onPageChange={table.setPage}
        onPageSizeChange={table.setPageSize}
        sort={table.sort}
        order={table.order}
        onSortChange={table.setSort}
        loading={query.isPending}
        fetching={query.isFetching && !query.isPending}
        error={query.error?.message}
        empty={empty}
        onRowClick={onRowClick}
      />
    </VStack>
  )
}
