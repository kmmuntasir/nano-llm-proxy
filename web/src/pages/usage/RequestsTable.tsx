import type { ReactNode } from "react"
import { Box, HStack, SimpleGrid, Text, VStack } from "@chakra-ui/react"
import type { UsageRequestRow, UsageRequestSort } from "../../api/types"
import { useUsageRequests } from "../../api/usage"
import type { UsageFilters } from "../../api/usage"
import { PaginatedTable } from "../../components/PaginatedTable"
import type { PaginatedColumn } from "../../components/PaginatedTable"
import { fmtCompact, fmtDuration, fmtShortDateTime } from "./format"
import { ProviderBadge, RangeNotice, RequestStatusBadge } from "./UsageParts"
import { userLabel } from "./BreakdownTable"
import { useTableState } from "./urlState"

const SORTS: readonly UsageRequestSort[] = ["ts", "durationMs", "totalTokens"]

type Col = PaginatedColumn<UsageRequestRow, UsageRequestSort>

function Detail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Box minW={0}>
      <Text fontSize="xs" color="fg.muted">
        {label}
      </Text>
      <Box fontSize="sm" wordBreak="break-word">
        {children}
      </Box>
    </Box>
  )
}

function RequestDetails({ row, isSuperadmin }: { row: UsageRequestRow; isSuperadmin: boolean }) {
  return (
    <SimpleGrid columns={{ base: 2, lg: 4 }} gap={3} textAlign="start">
      <Detail label="Upstream status">{row.upstreamStatus || "none"}</Detail>
      <Detail label="Attempts">{row.attempts}</Detail>
      <Detail label="TTFB">{fmtDuration(row.ttfbMs)}</Detail>
      <Detail label="Stream">{row.stream ? "yes" : "no"}</Detail>
      {isSuperadmin && (
        <Detail label="Upstream key">
          <Text as="span" fontFamily="mono" fontSize="xs">
            {row.upstreamKey || "—"}
          </Text>
        </Detail>
      )}
      <Detail label="Request id">{row.id}</Detail>
      <Box gridColumn="1 / -1">
        <Detail label="Fail reason">
          {row.failReason ? (
            <Text as="span" color="red.fg" whiteSpace="pre-wrap">
              {row.failReason}
            </Text>
          ) : (
            "—"
          )}
        </Detail>
      </Box>
    </SimpleGrid>
  )
}

function columns(isSuperadmin: boolean): Col[] {
  const cols: Col[] = [
    { id: "ts", header: "Time", sortKey: "ts", nowrap: true, render: (r) => fmtShortDateTime(r.ts) },
  ]
  if (isSuperadmin) {
    cols.push({ id: "user", header: "User", render: (r) => userLabel(r) })
  }
  cols.push(
    { id: "key", header: "Key", hideOnMobile: true, render: (r) => r.alias || (r.keyId ? `#${r.keyId}` : "—") },
    {
      id: "model",
      header: "Model",
      minW: "220px",
      render: (r) => (
        <HStack gap={1} flexWrap={{ base: "wrap", md: "nowrap" }} justify={{ base: "end", md: "start" }}>
          <ProviderBadge provider={r.provider} />
          <Text as="span" fontFamily="mono" fontSize="xs" wordBreak="break-word">
            {r.model}
          </Text>
        </HStack>
      ),
    },
    {
      id: "surface",
      header: "Surface",
      hideOnMobile: true,
      render: (r) => (r.surface ? `${r.surface}${r.stream ? " · stream" : ""}` : "—"),
    },
    { id: "status", header: "Status", render: (r) => <RequestStatusBadge status={r.status} /> },
    {
      id: "tokens",
      header: "Tokens in / out",
      sortKey: "totalTokens",
      align: "end",
      render: (r) => `${fmtCompact(r.inputTokens)} / ${fmtCompact(r.outputTokens)}`,
    },
    {
      id: "duration",
      header: "Duration",
      sortKey: "durationMs",
      align: "end",
      render: (r) => fmtDuration(r.durationMs),
    },
  )
  return cols
}

// RequestsTable: the per-request log (/api/usage/requests), newest first by
// default; rows expand to show the failure and upstream details.
export function RequestsTable({
  filters,
  prefix = "",
  isSuperadmin,
  defaultPageSize = 50,
  showRangeNotice = false,
}: {
  filters: UsageFilters
  prefix?: string
  isSuperadmin: boolean
  defaultPageSize?: number
  /** surface the server's capped-range echo above the table */
  showRangeNotice?: boolean
}) {
  const table = useTableState(prefix, { sorts: SORTS, defaultSort: "ts", defaultPageSize })
  const query = useUsageRequests(filters, {
    sort: table.sort,
    order: table.order,
    page: table.page,
    pageSize: table.pageSize,
  })
  const data = query.data

  return (
    <VStack align="stretch" gap={2}>
      {showRangeNotice && <RangeNotice filters={filters} range={data?.range} />}
      <PaginatedTable
        columns={columns(isSuperadmin)}
        rows={data?.rows ?? []}
        rowKey={(r) => String(r.id)}
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
        empty="No requests in this range"
        renderExpanded={(r) => <RequestDetails row={r} isSuperadmin={isSuperadmin} />}
      />
    </VStack>
  )
}
