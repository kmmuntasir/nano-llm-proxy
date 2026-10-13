// URL search-param state for the Usage section. Filters (range + facet
// selects) are shared by every tab and survive tab switches; table state
// (page/pageSize/sort/order) is per table, namespaced by a prefix so a page
// with several tables ("m.page", "k.page", ...) keeps them independent.

import { useMemo } from "react"
import { useSearchParams } from "react-router-dom"
import { useSession } from "../../App"
import { browserTz } from "../../api/usage"
import type { UsageFilters } from "../../api/usage"
import type { SortOrder, UsageStatusFilter, UsageSurface } from "../../api/types"

export const RANGE_PRESETS = [
  { id: "1h", label: "Last 1h" },
  { id: "24h", label: "Last 24h" },
  { id: "7d", label: "Last 7d" },
  { id: "30d", label: "Last 30d" },
  { id: "90d", label: "Last 90d" },
  { id: "today", label: "Today" },
  { id: "yesterday", label: "Yesterday" },
] as const

export type RangePreset = (typeof RANGE_PRESETS)[number]["id"]

const DEFAULT_PRESET: RangePreset = "24h"
const HOUR = 3600
const DAY = 86400

export const FILTER_KEYS = [
  "range",
  "from",
  "to",
  "userId",
  "keyId",
  "provider",
  "model",
  "surface",
  "status",
] as const

export type FilterKey = (typeof FILTER_KEYS)[number]
export type FilterPatch = Partial<Record<FilterKey, string | undefined>>

/** The facet selects — what "clear filters" clears (the range stays). */
export const SELECT_FILTER_KEYS: FilterKey[] = [
  "userId",
  "keyId",
  "provider",
  "model",
  "surface",
  "status",
]

export const SURFACES: UsageSurface[] = ["chat", "messages", "responses", "mcp"]
export const STATUSES: UsageStatusFilter[] = ["ok", "error"]

/** "now" rounded up to the minute, so preset ranges (and query keys) stay stable between renders. */
export function nowMinute(): number {
  return Math.ceil(Date.now() / 60_000) * 60
}

function localMidnight(nowSec: number): number {
  const d = new Date(nowSec * 1000)
  d.setHours(0, 0, 0, 0)
  return Math.floor(d.getTime() / 1000)
}

export function presetRange(preset: RangePreset, now: number): { from: number; to: number } {
  switch (preset) {
    case "1h":
      return { from: now - HOUR, to: now }
    case "24h":
      return { from: now - DAY, to: now }
    case "7d":
      return { from: now - 7 * DAY, to: now }
    case "30d":
      return { from: now - 30 * DAY, to: now }
    case "90d":
      return { from: now - 90 * DAY, to: now }
    case "today":
      return { from: localMidnight(now), to: now }
    case "yesterday": {
      // midnight-to-midnight; going through Date keeps DST days right
      const end = localMidnight(now)
      return { from: localMidnight(end - 1), to: end }
    }
  }
}

function isPreset(v: string | null): v is RangePreset {
  return RANGE_PRESETS.some((p) => p.id === v)
}

function positiveInt(v: string | null): number | undefined {
  if (!v || !/^\d+$/.test(v)) return undefined
  const n = Number(v)
  return n > 0 ? n : undefined
}

function oneOf<T extends string>(v: string | null, allowed: readonly T[]): T | undefined {
  return allowed.find((a) => a === v)
}

export interface ParsedFilters {
  filters: UsageFilters
  preset: RangePreset | "custom"
}

/**
 * Reads filters from the URL. Explicit from/to (unix seconds) win over a
 * preset; anything malformed falls back to the default (last 24h). userId is
 * dropped for non-superadmins — the server forces self-scope anyway and
 * would 403 on someone else's id.
 */
export function parseFilters(
  sp: URLSearchParams,
  opts: { isSuperadmin: boolean; now: number; tz: number },
): ParsedFilters {
  const from = positiveInt(sp.get("from"))
  const to = positiveInt(sp.get("to"))
  const rangeParam = sp.get("range")
  let preset: RangePreset | "custom"
  let range: { from: number; to: number }
  if (from !== undefined && to !== undefined && from < to) {
    preset = "custom"
    range = { from, to }
  } else {
    preset = isPreset(rangeParam) ? rangeParam : DEFAULT_PRESET
    range = presetRange(preset, opts.now)
  }
  return {
    preset,
    filters: {
      ...range,
      tz: opts.tz,
      userId: opts.isSuperadmin ? positiveInt(sp.get("userId")) : undefined,
      keyId: positiveInt(sp.get("keyId")),
      provider: sp.get("provider") || undefined,
      model: sp.get("model") || undefined,
      surface: oneOf(sp.get("surface"), SURFACES),
      status: oneOf(sp.get("status"), STATUSES),
    },
  }
}

/** Query string ("?a=b" or "") carrying only the filter params — used for tab links and drill-downs. */
export function filterSearch(sp: URLSearchParams, extra: Record<string, string> = {}): string {
  const out = new URLSearchParams()
  for (const k of FILTER_KEYS) {
    const v = sp.get(k)
    if (v) out.set(k, v)
  }
  for (const [k, v] of Object.entries(extra)) {
    if (v) out.set(k, v)
    else out.delete(k)
  }
  const s = out.toString()
  return s ? `?${s}` : ""
}

const PAGE_PARAM = /(^|\.)page$/

/** Applies a filter change and sends every table on the page back to page 1. */
export function applyFilterPatch(sp: URLSearchParams, patch: FilterPatch): URLSearchParams {
  const next = new URLSearchParams(sp)
  for (const [k, v] of Object.entries(patch)) {
    if (v === undefined || v === "") next.delete(k)
    else next.set(k, v)
  }
  for (const k of [...next.keys()]) {
    if (PAGE_PARAM.test(k)) next.delete(k)
  }
  return next
}

export function hasSelectFilters(sp: URLSearchParams, locked: FilterKey[] = []): boolean {
  return SELECT_FILTER_KEYS.some((k) => !locked.includes(k) && sp.has(k))
}

// --- per-table state ---

export interface TableSpec<S extends string> {
  sorts: readonly S[]
  defaultSort: S
  defaultOrder?: SortOrder
  defaultPageSize?: number
}

export interface TableState<S extends string> {
  page: number
  pageSize: number
  sort: S
  order: SortOrder
}

const MAX_PAGE_SIZE = 200
const FALLBACK_PAGE_SIZE = 25

export function readTableParams<S extends string>(
  sp: URLSearchParams,
  prefix: string,
  spec: TableSpec<S>,
): TableState<S> {
  const size = positiveInt(sp.get(`${prefix}pageSize`))
  return {
    page: positiveInt(sp.get(`${prefix}page`)) ?? 1,
    pageSize: size && size <= MAX_PAGE_SIZE ? size : (spec.defaultPageSize ?? FALLBACK_PAGE_SIZE),
    sort: oneOf(sp.get(`${prefix}sort`), spec.sorts) ?? spec.defaultSort,
    order: oneOf(sp.get(`${prefix}order`), ["asc", "desc"] as const) ?? spec.defaultOrder ?? "desc",
  }
}

/** Writes table params; changing sort or page size resets to page 1. */
export function writeTableParams<S extends string>(
  sp: URLSearchParams,
  prefix: string,
  patch: Partial<TableState<S>>,
): URLSearchParams {
  const next = new URLSearchParams(sp)
  if (patch.sort !== undefined || patch.order !== undefined || patch.pageSize !== undefined) {
    next.delete(`${prefix}page`)
  }
  if (patch.sort !== undefined) next.set(`${prefix}sort`, patch.sort)
  if (patch.order !== undefined) next.set(`${prefix}order`, patch.order)
  if (patch.pageSize !== undefined) next.set(`${prefix}pageSize`, String(patch.pageSize))
  if (patch.page !== undefined) {
    if (patch.page <= 1) next.delete(`${prefix}page`)
    else next.set(`${prefix}page`, String(patch.page))
  }
  return next
}

// --- hooks ---

export function useIsSuperadmin(): boolean {
  const { data } = useSession()
  return data?.user.role === "superadmin"
}

export function useUsageFilters() {
  const [sp, setSp] = useSearchParams()
  const isSuperadmin = useIsSuperadmin()
  const now = nowMinute()
  const tz = browserTz()
  const key = sp.toString()
  const parsed = useMemo(
    () => parseFilters(new URLSearchParams(key), { isSuperadmin, now, tz }),
    [key, isSuperadmin, now, tz],
  )
  return {
    ...parsed,
    isSuperadmin,
    searchParams: sp,
    setFilters: (patch: FilterPatch) => setSp((prev) => applyFilterPatch(prev, patch)),
  }
}

export function useTableState<S extends string>(prefix: string, spec: TableSpec<S>) {
  const [sp, setSp] = useSearchParams()
  const state = readTableParams(sp, prefix, spec)
  const write = (patch: Partial<TableState<S>>) =>
    setSp((prev) => writeTableParams(prev, prefix, patch))
  return {
    ...state,
    setPage: (page: number) => write({ page }),
    setPageSize: (pageSize: number) => write({ pageSize }),
    setSort: (sort: S, order: SortOrder) => write({ sort, order }),
  }
}
