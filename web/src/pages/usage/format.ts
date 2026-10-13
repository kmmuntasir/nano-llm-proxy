// Number/time formatting for the Usage section, all via Intl so they follow
// the browser locale.

import type { UsageBucket } from "../../api/types"

const compactFmt = new Intl.NumberFormat(undefined, {
  notation: "compact",
  maximumFractionDigits: 1,
})
const intFmt = new Intl.NumberFormat()
const pctFmt = new Intl.NumberFormat(undefined, { style: "percent", maximumFractionDigits: 1 })
const timeFmt = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" })
const dayFmt = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" })
const dayTimeFmt = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
})
const weekdayFmt = new Intl.DateTimeFormat(undefined, {
  weekday: "short",
  month: "short",
  day: "numeric",
})
const shortFmt = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
})
const fullFmt = new Intl.DateTimeFormat(undefined, {
  year: "numeric",
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
})

// Requests rejected before routing (bad model string, unknown provider)
// are recorded with an empty provider.
export const REJECTED_PROVIDER = "rejected"

export function providerLabel(provider: string): string {
  return provider || REJECTED_PROVIDER
}

/** 1234567 → "1.2M" */
export function fmtCompact(n: number): string {
  return compactFmt.format(n)
}

export function fmtInt(n: number): string {
  return intFmt.format(n)
}

/** ratio 0..1 → "12.5%" */
export function fmtPct(ratio: number): string {
  return pctFmt.format(Number.isFinite(ratio) ? ratio : 0)
}

export function errorRate(errors: number, requests: number): number {
  return requests > 0 ? errors / requests : 0
}

/** 850 → "850 ms", 1234 → "1.23 s", 65000 → "65.0 s" */
export function fmtDuration(ms: number): string {
  if (!ms) return "—"
  if (ms < 1000) return `${Math.round(ms)} ms`
  return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)} s`
}

export function fmtDateTime(ts: number): string {
  if (!ts) return "—"
  return fullFmt.format(new Date(ts * 1000))
}

/** Compact table timestamp without the year ("Oct 13, 14:05:09"). */
export function fmtShortDateTime(ts: number): string {
  if (!ts) return "—"
  return shortFmt.format(new Date(ts * 1000))
}

function isLocalMidnight(ts: number): boolean {
  const d = new Date(ts * 1000)
  return d.getHours() === 0 && d.getMinutes() === 0
}

/**
 * Axis tick for a bucket timestamp: days → "Oct 13"; hours → "14:00", with
 * the date prepended on the first tick and wherever a new day starts.
 */
export function fmtBucketTick(ts: number, bucket: UsageBucket, firstTs: number): string {
  const d = new Date(ts * 1000)
  if (bucket === "day") return dayFmt.format(d)
  if (ts === firstTs || isLocalMidnight(ts)) return dayTimeFmt.format(d)
  return timeFmt.format(d)
}

/** Tooltip heading for a bucket: hours → "Oct 13, 14:00", days → "Mon, Oct 13". */
export function fmtBucketLabel(ts: number, bucket: UsageBucket): string {
  const d = new Date(ts * 1000)
  return bucket === "day" ? weekdayFmt.format(d) : dayTimeFmt.format(d)
}

/** datetime-local input value (browser timezone) for a unix timestamp. */
export function toLocalInput(ts: number): string {
  const d = new Date(ts * 1000)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** datetime-local value → unix seconds, or undefined when blank/invalid. */
export function fromLocalInput(v: string): number | undefined {
  if (!v) return undefined
  const ms = new Date(v).getTime()
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : undefined
}
