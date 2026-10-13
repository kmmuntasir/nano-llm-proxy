// Query hooks for the usage monitoring v2 endpoints (GET /api/usage/*).
// Every hook takes the shared UsageFilters (range + tz + facet filters) plus
// its own endpoint params; keys come from usageKeys so the cache shape lives
// in one place. Paged/sorted views keep the previous page on screen while the
// next one loads (placeholderData: keepPreviousData).

import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { api } from "./client"
import type {
  SortOrder,
  UsageBreakdown,
  UsageBreakdownGroupBy,
  UsageBreakdownSort,
  UsageBucket,
  UsageFacets,
  UsageOverview,
  UsageRequestSort,
  UsageRequests,
  UsageSeries,
  UsageSeriesGroupBy,
  UsageSeriesMetric,
  UsageStatusFilter,
  UsageSurface,
} from "./types"

export interface UsageFilters {
  from: number
  to: number
  /** seconds east of UTC — bucket alignment */
  tz: number
  userId?: number
  keyId?: number
  provider?: string
  model?: string
  surface?: UsageSurface
  status?: UsageStatusFilter
}

export interface BreakdownParams {
  groupBy: UsageBreakdownGroupBy
  sort?: UsageBreakdownSort
  order?: SortOrder
  page?: number
  pageSize?: number
  q?: string
}

export interface SeriesParams {
  groupBy: UsageSeriesGroupBy
  metric?: UsageSeriesMetric
  top?: number
  bucket?: UsageBucket
}

export interface RequestsParams {
  sort?: UsageRequestSort
  order?: SortOrder
  page?: number
  pageSize?: number
}

interface QueryOpts {
  enabled?: boolean
}

type ParamValue = string | number | undefined

/** Browser offset in seconds east of UTC (getTimezoneOffset is west-positive minutes). */
export function browserTz(): number {
  return -new Date().getTimezoneOffset() * 60
}

/** Builds a query string, dropping undefined/empty values. */
export function toQuery(params: Record<string, ParamValue>): string {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue
    sp.set(k, String(v))
  }
  return sp.toString()
}

function filterParams(f: UsageFilters): Record<string, ParamValue> {
  return {
    from: f.from,
    to: f.to,
    tz: f.tz,
    userId: f.userId,
    keyId: f.keyId,
    provider: f.provider,
    model: f.model,
    surface: f.surface,
    status: f.status,
  }
}

export const usageKeys = {
  all: ["usage"] as const,
  overview: (f: UsageFilters, bucket?: UsageBucket) =>
    ["usage", "overview", f, bucket ?? null] as const,
  breakdown: (f: UsageFilters, p: BreakdownParams) => ["usage", "breakdown", f, p] as const,
  series: (f: UsageFilters, p: SeriesParams) => ["usage", "series", f, p] as const,
  requests: (f: UsageFilters, p: RequestsParams) => ["usage", "requests", f, p] as const,
  facets: (from: number, to: number, tz: number) => ["usage", "facets", from, to, tz] as const,
}

export function useUsageOverview(f: UsageFilters, bucket?: UsageBucket, opts: QueryOpts = {}) {
  return useQuery({
    queryKey: usageKeys.overview(f, bucket),
    queryFn: () =>
      api<UsageOverview>(`/api/usage/overview?${toQuery({ ...filterParams(f), bucket })}`),
    placeholderData: keepPreviousData,
    enabled: opts.enabled,
  })
}

export function useUsageBreakdown(f: UsageFilters, p: BreakdownParams, opts: QueryOpts = {}) {
  return useQuery({
    queryKey: usageKeys.breakdown(f, p),
    queryFn: () =>
      api<UsageBreakdown>(
        `/api/usage/breakdown?${toQuery({
          ...filterParams(f),
          groupBy: p.groupBy,
          sort: p.sort,
          order: p.order,
          page: p.page,
          pageSize: p.pageSize,
          q: p.q,
        })}`,
      ),
    placeholderData: keepPreviousData,
    enabled: opts.enabled,
  })
}

export function useUsageSeries(f: UsageFilters, p: SeriesParams, opts: QueryOpts = {}) {
  return useQuery({
    queryKey: usageKeys.series(f, p),
    queryFn: () =>
      api<UsageSeries>(
        `/api/usage/series?${toQuery({
          ...filterParams(f),
          groupBy: p.groupBy,
          metric: p.metric,
          top: p.top,
          bucket: p.bucket,
        })}`,
      ),
    placeholderData: keepPreviousData,
    enabled: opts.enabled,
  })
}

export function useUsageRequests(f: UsageFilters, p: RequestsParams, opts: QueryOpts = {}) {
  return useQuery({
    queryKey: usageKeys.requests(f, p),
    queryFn: () =>
      api<UsageRequests>(
        `/api/usage/requests?${toQuery({
          ...filterParams(f),
          sort: p.sort,
          order: p.order,
          page: p.page,
          pageSize: p.pageSize,
        })}`,
      ),
    placeholderData: keepPreviousData,
    enabled: opts.enabled,
  })
}

/** Facets take the range only — the server forces self-scope for non-superadmins. */
export function useUsageFacets(from: number, to: number, tz: number) {
  return useQuery({
    queryKey: usageKeys.facets(from, to, tz),
    queryFn: () => api<UsageFacets>(`/api/usage/facets?${toQuery({ from, to, tz })}`),
    placeholderData: keepPreviousData,
    staleTime: 60_000,
  })
}
