import { Fragment, useState } from "react"
import type { KeyboardEvent, ReactNode } from "react"
import {
  Box,
  Button,
  Card,
  HStack,
  IconButton,
  NativeSelect,
  Skeleton,
  Spinner,
  Table,
  Text,
  VStack,
} from "@chakra-ui/react"
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronDown, ChevronLeft, ChevronRight } from "lucide-react"
import type { SortOrder } from "../api/types"

export interface PaginatedColumn<T, S extends string = string> {
  /** unique column id (also the React key) */
  id: string
  header: string
  render: (row: T) => ReactNode
  /** server-side sort field; omit for unsortable columns */
  sortKey?: S
  /** order applied when this column becomes the sort column (default desc) */
  defaultOrder?: SortOrder
  align?: "start" | "end"
  /** desktop cells stay on one line (the table scrolls sideways instead); false lets it wrap */
  nowrap?: boolean
  /** desktop min width, so wrapping text columns aren't squeezed to slivers */
  minW?: string
  /** cell adds no information on a phone card */
  hideOnMobile?: boolean
}

interface Props<T, S extends string> {
  columns: PaginatedColumn<T, S>[]
  rows: T[]
  rowKey: (row: T, index: number) => string
  total: number
  page: number
  pageSize: number
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  sort?: S
  order?: SortOrder
  onSortChange?: (sort: S, order: SortOrder) => void
  /** first load, nothing to show yet */
  loading?: boolean
  /** background refetch (previous page still on screen) */
  fetching?: boolean
  error?: string
  empty?: string
  onRowClick?: (row: T) => void
  /** when set, rows get a disclosure toggle revealing this content */
  renderExpanded?: (row: T) => ReactNode
  pageSizeOptions?: number[]
}

export const PAGE_SIZE_OPTIONS = [10, 25, 50, 100, 200]
const SKELETON_ROWS = 5

/** Total pages for a result set (at least 1, so "page 1 of 1" reads right when empty). */
export function pageCount(total: number, pageSize: number): number {
  if (pageSize <= 0) return 1
  return Math.max(1, Math.ceil(total / pageSize))
}

/** 1-based inclusive row span shown on a page: {start: 0, end: 0} when empty. */
export function pageSpan(page: number, pageSize: number, total: number) {
  if (total <= 0) return { start: 0, end: 0 }
  const start = Math.min((page - 1) * pageSize + 1, total)
  const end = Math.min(page * pageSize, total)
  return { start, end }
}

/**
 * Compact page-number list: always first and last, the current page with one
 * neighbour on each side, and "gap" markers where pages are skipped.
 * e.g. (6, 20) → [1, gap, 5, 6, 7, gap, 20].
 */
export function pageWindow(page: number, count: number): (number | "gap")[] {
  const keep = new Set([1, count, page - 1, page, page + 1])
  // avoid a gap that hides exactly one page
  if (page - 3 === 1) keep.add(2)
  if (page + 3 === count) keep.add(count - 1)
  const pages = [...keep].filter((p) => p >= 1 && p <= count).sort((a, b) => a - b)
  const out: (number | "gap")[] = []
  pages.forEach((p, i) => {
    if (i > 0 && p - pages[i - 1] > 1) out.push("gap")
    out.push(p)
  })
  return out
}

function ariaSort(active: boolean, order?: SortOrder): "ascending" | "descending" | "none" {
  if (!active) return "none"
  return order === "asc" ? "ascending" : "descending"
}

// PaginatedTable is DataTable's server-driven sibling: the parent owns
// page/pageSize/sort (usually in the URL) and passes one page of rows plus
// the total. Desktop renders a table with sortable headers; phones get one
// card per row, with a sort select standing in for the header buttons.
export function PaginatedTable<T, S extends string = string>({
  columns,
  rows,
  rowKey,
  total,
  page,
  pageSize,
  onPageChange,
  onPageSizeChange,
  sort,
  order,
  onSortChange,
  loading = false,
  fetching = false,
  error,
  empty = "Nothing here yet",
  onRowClick,
  renderExpanded,
  pageSizeOptions = PAGE_SIZE_OPTIONS,
}: Props<T, S>) {
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const count = pageCount(total, pageSize)
  const span = pageSpan(page, pageSize, total)
  const sortable = columns.filter((c) => c.sortKey !== undefined)
  const isEmpty = !loading && rows.length === 0

  const toggle = (key: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })

  const clickSort = (c: PaginatedColumn<T, S>) => {
    if (!onSortChange || c.sortKey === undefined) return
    if (c.sortKey === sort) {
      onSortChange(c.sortKey, order === "asc" ? "desc" : "asc")
      return
    }
    onSortChange(c.sortKey, c.defaultOrder ?? "desc")
  }

  const rowKeyDown = (e: KeyboardEvent, row: T) => {
    if (!onRowClick) return
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault()
      onRowClick(row)
    }
  }

  const messageNode = (text: string, color = "fg.muted") => (
    <Text color={color} fontSize="sm" textAlign="center" py={4}>
      {text}
    </Text>
  )
  const statusNode = error ? messageNode(error, "red.fg") : isEmpty ? messageNode(empty) : null
  const skeletons = Array.from({ length: SKELETON_ROWS }, (_, i) => i)

  return (
    <VStack align="stretch" gap={3}>
      {/* mobile: sort select + one card per row */}
      <Box hideFrom="md">
        <VStack align="stretch" gap={2}>
          {onSortChange && sortable.length > 0 && (
            <HStack gap={2}>
              <NativeSelect.Root size="xs" flex={1}>
                <NativeSelect.Field
                  aria-label="Sort by"
                  value={sort ?? ""}
                  onChange={(e) => {
                    const c = sortable.find((x) => x.sortKey === e.target.value)
                    if (c?.sortKey !== undefined) onSortChange(c.sortKey, c.defaultOrder ?? "desc")
                  }}
                >
                  {sortable.map((c) => (
                    <option key={c.id} value={c.sortKey}>
                      Sort: {c.header}
                    </option>
                  ))}
                </NativeSelect.Field>
                <NativeSelect.Indicator />
              </NativeSelect.Root>
              <IconButton
                size="xs"
                variant="outline"
                aria-label={order === "asc" ? "Sort descending" : "Sort ascending"}
                onClick={() => sort && onSortChange(sort, order === "asc" ? "desc" : "asc")}
              >
                {order === "asc" ? <ArrowUp /> : <ArrowDown />}
              </IconButton>
            </HStack>
          )}
          {statusNode}
          {loading &&
            !error &&
            skeletons.map((i) => <Skeleton key={i} h="96px" rounded="md" />)}
          {!error &&
            rows.map((row, i) => {
              const key = rowKey(row, i)
              const isOpen = expanded.has(key)
              return (
                <Card.Root
                  key={key}
                  size="sm"
                  cursor={onRowClick ? "pointer" : undefined}
                  onClick={onRowClick ? () => onRowClick(row) : undefined}
                  opacity={fetching ? 0.6 : 1}
                >
                  <Card.Body gap={2} px={4} py={3}>
                    {columns
                      .filter((c) => !c.hideOnMobile)
                      .map((c) => (
                        <HStack key={c.id} justify="space-between" align="start" gap={3}>
                          <Text fontSize="xs" color="fg.muted" flexShrink={0} pt={0.5}>
                            {c.header}
                          </Text>
                          <Box textAlign="right" minW={0} wordBreak="break-word">
                            {c.render(row)}
                          </Box>
                        </HStack>
                      ))}
                    {renderExpanded && (
                      <>
                        <Button
                          size="2xs"
                          variant="ghost"
                          alignSelf="start"
                          aria-expanded={isOpen}
                          onClick={(e) => {
                            e.stopPropagation()
                            toggle(key)
                          }}
                        >
                          {isOpen ? <ChevronDown /> : <ChevronRight />}
                          {isOpen ? "Hide details" : "Details"}
                        </Button>
                        {isOpen && <Box pt={1}>{renderExpanded(row)}</Box>}
                      </>
                    )}
                  </Card.Body>
                </Card.Root>
              )
            })}
        </VStack>
      </Box>

      {/* desktop: the real table — cells don't wrap, the box scrolls sideways
          instead, so no column is squeezed or cut off */}
      <Box hideBelow="md" overflowX="auto" overflowY="hidden" maxW="full">
        <Table.Root size="sm" opacity={fetching ? 0.6 : 1} transition="opacity 0.15s">
          <Table.Header>
            <Table.Row>
              {renderExpanded && <Table.ColumnHeader w="32px" aria-label="Details" />}
              {columns.map((c) => {
                const active = c.sortKey !== undefined && c.sortKey === sort
                return (
                  <Table.ColumnHeader
                    key={c.id}
                    textAlign={c.align === "end" ? "end" : undefined}
                    aria-sort={c.sortKey !== undefined ? ariaSort(active, order) : undefined}
                    whiteSpace="nowrap"
                  >
                    {c.sortKey !== undefined && onSortChange ? (
                      <Button
                        variant="ghost"
                        size="2xs"
                        px={1}
                        mx={-1}
                        fontWeight={active ? "semibold" : "medium"}
                        onClick={() => clickSort(c)}
                      >
                        {c.header}
                        {active ? (
                          order === "asc" ? (
                            <ArrowUp />
                          ) : (
                            <ArrowDown />
                          )
                        ) : (
                          <Box as="span" color="fg.subtle" display="inline-flex">
                            <ArrowUpDown />
                          </Box>
                        )}
                      </Button>
                    ) : (
                      c.header
                    )}
                  </Table.ColumnHeader>
                )
              })}
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {statusNode && (
              <Table.Row>
                <Table.Cell colSpan={columns.length + (renderExpanded ? 1 : 0)}>
                  {statusNode}
                </Table.Cell>
              </Table.Row>
            )}
            {loading &&
              !error &&
              skeletons.map((i) => (
                <Table.Row key={i}>
                  {renderExpanded && <Table.Cell />}
                  {columns.map((c) => (
                    <Table.Cell key={c.id}>
                      <Skeleton h="14px" />
                    </Table.Cell>
                  ))}
                </Table.Row>
              ))}
            {!error &&
              rows.map((row, i) => {
                const key = rowKey(row, i)
                const isOpen = expanded.has(key)
                return (
                  <Fragment key={key}>
                    <Table.Row
                      cursor={onRowClick ? "pointer" : undefined}
                      _hover={onRowClick ? { bg: "bg.subtle" } : undefined}
                      tabIndex={onRowClick ? 0 : undefined}
                      onClick={onRowClick ? () => onRowClick(row) : undefined}
                      onKeyDown={onRowClick ? (e) => rowKeyDown(e, row) : undefined}
                    >
                      {renderExpanded && (
                        <Table.Cell>
                          <IconButton
                            size="2xs"
                            variant="ghost"
                            aria-label={isOpen ? "Hide details" : "Show details"}
                            aria-expanded={isOpen}
                            onClick={(e) => {
                              e.stopPropagation()
                              toggle(key)
                            }}
                          >
                            {isOpen ? <ChevronDown /> : <ChevronRight />}
                          </IconButton>
                        </Table.Cell>
                      )}
                      {columns.map((c) => (
                        <Table.Cell
                          key={c.id}
                          textAlign={c.align === "end" ? "end" : undefined}
                          whiteSpace={(c.nowrap ?? true) ? "nowrap" : "normal"}
                          minW={c.minW}
                        >
                          {c.render(row)}
                        </Table.Cell>
                      ))}
                    </Table.Row>
                    {renderExpanded && isOpen && (
                      <Table.Row bg="bg.subtle">
                        <Table.Cell />
                        <Table.Cell colSpan={columns.length} whiteSpace="normal">
                          {renderExpanded(row)}
                        </Table.Cell>
                      </Table.Row>
                    )}
                  </Fragment>
                )
              })}
          </Table.Body>
        </Table.Root>
      </Box>

      {/* footer: span, page size, page buttons */}
      <HStack justify="space-between" flexWrap="wrap" gap={2}>
        <HStack gap={2}>
          <Text fontSize="xs" color="fg.muted" aria-live="polite">
            {total > 0
              ? `${span.start.toLocaleString()}–${span.end.toLocaleString()} of ${total.toLocaleString()}`
              : "0 results"}
          </Text>
          {fetching && <Spinner size="xs" color="fg.muted" />}
        </HStack>
        <HStack gap={2} flexWrap="wrap">
          <NativeSelect.Root size="xs" w="auto">
            <NativeSelect.Field
              aria-label="Rows per page"
              value={String(pageSize)}
              onChange={(e) => onPageSizeChange(Number(e.target.value))}
            >
              {pageSizeOptions.map((n) => (
                <option key={n} value={n}>
                  {n} / page
                </option>
              ))}
            </NativeSelect.Field>
            <NativeSelect.Indicator />
          </NativeSelect.Root>
          <HStack gap={1} as="nav" aria-label="Pagination">
            <IconButton
              size="xs"
              variant="outline"
              aria-label="Previous page"
              disabled={page <= 1}
              onClick={() => onPageChange(page - 1)}
            >
              <ChevronLeft />
            </IconButton>
            {pageWindow(page, count).map((p, i) =>
              p === "gap" ? (
                <Text key={`gap-${i}`} fontSize="xs" color="fg.muted" px={1}>
                  …
                </Text>
              ) : (
                <Button
                  key={p}
                  size="xs"
                  minW="28px"
                  px={1}
                  variant={p === page ? "solid" : "ghost"}
                  aria-current={p === page ? "page" : undefined}
                  aria-label={`Page ${p}`}
                  onClick={() => onPageChange(p)}
                >
                  {p}
                </Button>
              ),
            )}
            <IconButton
              size="xs"
              variant="outline"
              aria-label="Next page"
              disabled={page >= count}
              onClick={() => onPageChange(page + 1)}
            >
              <ChevronRight />
            </IconButton>
          </HStack>
        </HStack>
      </HStack>
    </VStack>
  )
}
