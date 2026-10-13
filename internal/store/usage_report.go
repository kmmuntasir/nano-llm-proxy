package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Usage monitoring v2 queries: filtered overview, grouped breakdowns with
// paging, top-N series, the raw request log, and filter facets. Every value
// that comes from a request is bound as a parameter; the only SQL fragments
// that vary are picked from the fixed maps below by validated keys.

// ErrUsageParam marks a query parameter the store refuses (unknown group,
// sort, or metric). Handlers validate first; this is the SQL-side backstop.
var ErrUsageParam = errors.New("invalid usage query parameter")

// UsageFilter narrows a usage query. Zero values mean "no filter".
type UsageFilter struct {
	From, To int64
	UserID   *int64 // nil = all users (superadmin views)
	KeyID    int64
	Provider string
	Model    string
	Surface  string
	Status   string // "" | "ok" (status < 400) | "error"
}

// where renders the filter against the usage_events alias "e".
func (f UsageFilter) where() (string, []any) {
	var b strings.Builder
	b.WriteString("e.ts BETWEEN ? AND ?")
	args := []any{f.From, f.To}
	if f.UserID != nil {
		b.WriteString(" AND e.user_id = ?")
		args = append(args, *f.UserID)
	}
	if f.KeyID != 0 {
		b.WriteString(" AND e.client_key_id = ?")
		args = append(args, f.KeyID)
	}
	if f.Provider != "" {
		b.WriteString(" AND e.provider = ?")
		args = append(args, f.Provider)
	}
	if f.Model != "" {
		b.WriteString(" AND e.model = ?")
		args = append(args, f.Model)
	}
	if f.Surface != "" {
		b.WriteString(" AND e.surface = ?")
		args = append(args, f.Surface)
	}
	switch f.Status {
	case "ok":
		b.WriteString(" AND e.status < 400")
	case "error":
		b.WriteString(" AND e.status >= 400")
	}
	return b.String(), args
}

// Label expressions: the live row wins, then the request-time snapshot, then
// a stable placeholder for rows whose user/key is gone (and pre-V6 rows).
const (
	usageEmailExpr = `CASE WHEN u.id IS NOT NULL THEN u.email
		ELSE COALESCE(NULLIF(MAX(e.user_email), ''), 'deleted user #' || e.user_id) END`
	usageAliasExpr = `CASE WHEN k.id IS NOT NULL THEN k.alias
		ELSE COALESCE(NULLIF(MAX(e.key_alias), ''), 'deleted key #' || e.client_key_id) END`
	usageJoins = ` FROM usage_events e
		LEFT JOIN users u ON u.id = e.user_id
		LEFT JOIN api_keys k ON k.id = e.client_key_id `
)

// --- overview ---

// UsageOverviewTotals is the headline block of /api/usage/overview.
type UsageOverviewTotals struct {
	Requests      int64 `json:"requests"`
	Errors        int64 `json:"errors"`
	InputTokens   int64 `json:"inputTokens"` // total prompt incl. cached
	OutputTokens  int64 `json:"outputTokens"`
	CachedTokens  int64 `json:"cachedTokens"` // cache-read subset of InputTokens
	AvgDurationMs int64 `json:"avgDurationMs"`
	P95DurationMs int64 `json:"p95DurationMs"`
	AvgTtfbMs     int64 `json:"avgTtfbMs"`
	Users         int64 `json:"users"`
	Models        int64 `json:"models"`
}

// UsageOverviewPoint is one time bucket of the overview chart.
type UsageOverviewPoint struct {
	TS            int64 `json:"ts"`
	Requests      int64 `json:"requests"`
	Errors        int64 `json:"errors"`
	InputTokens   int64 `json:"inputTokens"`
	OutputTokens  int64 `json:"outputTokens"`
	CachedTokens  int64 `json:"cachedTokens"`
	AvgDurationMs int64 `json:"avgDurationMs"`
}

// usageBucketOf mirrors usageBucketExpr in Go so zero-filled buckets line up
// with the SQL ones.
func usageBucketOf(ts, bucket, tz int64) int64 {
	return ((ts-tz)/bucket)*bucket + tz
}

// usageMaxBuckets bounds the zero-filled series. A 92-day range in hourly
// buckets is ~2.2k; anything far beyond that is a caller bug or an attack,
// and must not turn into an unbounded allocation.
const usageMaxBuckets = 10_000

// usageBuckets lists every bucket start covering [from, to]. It refuses
// (ErrUsageParam) ranges needing more than usageMaxBuckets buckets and never
// lets the bucket cursor overflow, so hostile bounds cannot hang or OOM.
func usageBuckets(from, to, bucket, tz int64) ([]int64, error) {
	if bucket <= 0 || from < 0 || from > to {
		return nil, fmt.Errorf("%w: range", ErrUsageParam)
	}
	var out []int64
	for b := usageBucketOf(from, bucket, tz); b <= to; b += bucket {
		if len(out) == usageMaxBuckets {
			return nil, fmt.Errorf("%w: range spans more than %d buckets", ErrUsageParam, usageMaxBuckets)
		}
		out = append(out, b)
		if b > math.MaxInt64-bucket {
			break // next start would wrap; to cannot exceed MaxInt64 anyway
		}
	}
	return out, nil
}

// UsageOverview returns the filtered totals (incl. latency percentiles and
// distinct user/model counts) and a zero-filled per-bucket series.
func (s *Store) UsageOverview(f UsageFilter, bucket, tz int64) (*UsageOverviewTotals, []UsageOverviewPoint, error) {
	if bucket <= 0 {
		return nil, nil, fmt.Errorf("%w: bucket", ErrUsageParam)
	}
	// bound the fill-in before touching the table
	buckets, err := usageBuckets(f.From, f.To, bucket, tz)
	if err != nil {
		return nil, nil, err
	}
	where, args := f.where()
	t := &UsageOverviewTotals{}
	// One scan for every headline number, including the successful-row
	// count that sizes the p95 offset below.
	var okRows int64
	err = s.db.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN e.status >= 400 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN e.status < 400 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(e.input_tokens), 0), COALESCE(SUM(e.output_tokens), 0),
			COALESCE(SUM(e.cached_tokens), 0),
			CAST(COALESCE(ROUND(AVG(e.duration_ms)), 0) AS INTEGER),
			CAST(COALESCE(ROUND(AVG(CASE WHEN e.ttfb_ms > 0 THEN e.ttfb_ms END)), 0) AS INTEGER),
			COUNT(DISTINCT e.user_id),
			COUNT(DISTINCT e.provider || '/' || e.model)
		FROM usage_events e WHERE `+where, args...).
		Scan(&t.Requests, &t.Errors, &okRows, &t.InputTokens, &t.OutputTokens, &t.CachedTokens,
			&t.AvgDurationMs, &t.AvgTtfbMs, &t.Users, &t.Models)
	if err != nil {
		return nil, nil, fmt.Errorf("usage overview totals: %w", err)
	}
	if okRows > 0 {
		// nearest-rank p95 over successful rows: one range read, one row out
		offset := (okRows*95+99)/100 - 1
		if err := s.db.QueryRow(`SELECT e.duration_ms FROM usage_events e WHERE `+where+
			` AND e.status < 400 ORDER BY e.duration_ms LIMIT 1 OFFSET ?`, append(args, offset)...).
			Scan(&t.P95DurationMs); err != nil {
			return nil, nil, fmt.Errorf("usage overview p95: %w", err)
		}
	}

	expr := usageBucketExpr(bucket, tz)
	rows, err := s.db.Query(`SELECT `+expr+` AS b, COUNT(*),
			COALESCE(SUM(CASE WHEN e.status >= 400 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(e.input_tokens), 0), COALESCE(SUM(e.output_tokens), 0),
			COALESCE(SUM(e.cached_tokens), 0),
			CAST(COALESCE(ROUND(AVG(e.duration_ms)), 0) AS INTEGER)
		FROM usage_events e WHERE `+where+` GROUP BY b ORDER BY b`, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("usage overview points: %w", err)
	}
	defer rows.Close()
	got := map[int64]UsageOverviewPoint{}
	for rows.Next() {
		var p UsageOverviewPoint
		if err := rows.Scan(&p.TS, &p.Requests, &p.Errors, &p.InputTokens, &p.OutputTokens, &p.CachedTokens, &p.AvgDurationMs); err != nil {
			return nil, nil, err
		}
		got[p.TS] = p
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	points := make([]UsageOverviewPoint, 0, len(buckets))
	for _, b := range buckets {
		p := got[b]
		p.TS = b
		points = append(points, p)
	}
	return t, points, nil
}

// --- breakdown ---

// UsageBreakdownQuery is one page of a grouped usage table.
type UsageBreakdownQuery struct {
	Filter   UsageFilter
	GroupBy  string // user | model | user_model | provider | key | upstream_key
	Sort     string // see usageBreakdownSorts
	Desc     bool
	Page     int // 1-based
	PageSize int
	Q        string // case-insensitive substring of the group label
}

// UsageBreakdownRow is one group. Fields that are not part of the grouping
// stay zero/empty.
type UsageBreakdownRow struct {
	UserID        int64  `json:"userId"`
	Email         string `json:"email"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	KeyID         int64  `json:"keyId"`
	Alias         string `json:"alias"`
	UpstreamKey   string `json:"upstreamKey"`
	Requests      int64  `json:"requests"`
	Errors        int64  `json:"errors"`
	InputTokens   int64  `json:"inputTokens"`
	OutputTokens  int64  `json:"outputTokens"`
	CachedTokens  int64  `json:"cachedTokens"`
	AvgDurationMs int64  `json:"avgDurationMs"`
	AvgTtfbMs     int64  `json:"avgTtfbMs"`
	FirstTs       int64  `json:"firstTs"`
	LastTs        int64  `json:"lastTs"`
}

// UsageBreakdownTotals sums every group matching a breakdown query. It is
// separate from UsageTotals so the v1 summary payload stays unchanged.
type UsageBreakdownTotals struct {
	Requests     int64 `json:"requests"`
	Errors       int64 `json:"errors"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	CachedTokens int64 `json:"cachedTokens"`
}

// usageGroup is the SQL shape of one groupBy: the dimension columns (always
// all seven, aliased, constants where irrelevant), the label used for
// search/sort, and the GROUP BY list.
type usageGroup struct {
	dims    string
	label   string
	groupBy string
}

var usageBreakdownGroups = map[string]usageGroup{
	"user": {
		dims:    `e.user_id AS user_id, ` + usageEmailExpr + ` AS email, '' AS provider, '' AS model, 0 AS key_id, '' AS alias, '' AS upstream_key`,
		label:   usageEmailExpr,
		groupBy: `e.user_id`,
	},
	"model": {
		dims:    `0 AS user_id, '' AS email, e.provider AS provider, e.model AS model, 0 AS key_id, '' AS alias, '' AS upstream_key`,
		label:   `e.provider || '/' || e.model`,
		groupBy: `e.provider, e.model`,
	},
	"user_model": {
		dims:    `e.user_id AS user_id, ` + usageEmailExpr + ` AS email, e.provider AS provider, e.model AS model, 0 AS key_id, '' AS alias, '' AS upstream_key`,
		label:   usageEmailExpr + ` || ' ' || e.provider || '/' || e.model`,
		groupBy: `e.user_id, e.provider, e.model`,
	},
	"provider": {
		dims:    `0 AS user_id, '' AS email, e.provider AS provider, '' AS model, 0 AS key_id, '' AS alias, '' AS upstream_key`,
		label:   `e.provider`,
		groupBy: `e.provider`,
	},
	"key": {
		dims:    `e.user_id AS user_id, ` + usageEmailExpr + ` AS email, '' AS provider, '' AS model, e.client_key_id AS key_id, ` + usageAliasExpr + ` AS alias, '' AS upstream_key`,
		label:   usageAliasExpr,
		groupBy: `e.client_key_id, e.user_id`,
	},
	"upstream_key": {
		dims:    `0 AS user_id, '' AS email, e.provider AS provider, '' AS model, 0 AS key_id, '' AS alias, e.upstream_key AS upstream_key`,
		label:   `e.provider || '/' || e.upstream_key`,
		groupBy: `e.provider, e.upstream_key`,
	},
}

// usageBreakdownSorts maps the API sort names onto columns of the grouped
// CTE "g". Only these fragments ever reach ORDER BY.
var usageBreakdownSorts = map[string]string{
	"requests":      "requests",
	"errors":        "errors",
	"errorRate":     "CAST(errors AS REAL) / requests",
	"inputTokens":   "input_tokens",
	"outputTokens":  "output_tokens",
	"cachedTokens":  "cached_tokens",
	"totalTokens":   "input_tokens + output_tokens",
	"avgDurationMs": "avg_duration_ms",
	"lastTs":        "last_ts",
	"label":         "label COLLATE NOCASE",
}

// escapeLike makes s a literal for LIKE ... ESCAPE '\'.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// UsageBreakdown returns one page of grouped rows, the number of groups
// matching (for paging), and totals over every matching group.
func (s *Store) UsageBreakdown(q UsageBreakdownQuery) ([]UsageBreakdownRow, int64, *UsageBreakdownTotals, error) {
	grp, ok := usageBreakdownGroups[q.GroupBy]
	if !ok {
		return nil, 0, nil, fmt.Errorf("%w: groupBy %q", ErrUsageParam, q.GroupBy)
	}
	sortExpr, ok := usageBreakdownSorts[q.Sort]
	if !ok {
		return nil, 0, nil, fmt.Errorf("%w: sort %q", ErrUsageParam, q.Sort)
	}
	if q.Page < 1 || q.PageSize < 1 {
		return nil, 0, nil, fmt.Errorf("%w: page", ErrUsageParam)
	}
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	where, args := q.Filter.where()
	cte := `WITH g AS (SELECT ` + grp.dims + `, ` + grp.label + ` AS label,
			COUNT(*) AS requests,
			COALESCE(SUM(CASE WHEN e.status >= 400 THEN 1 ELSE 0 END), 0) AS errors,
			COALESCE(SUM(e.input_tokens), 0) AS input_tokens,
			COALESCE(SUM(e.output_tokens), 0) AS output_tokens,
			COALESCE(SUM(e.cached_tokens), 0) AS cached_tokens,
			CAST(COALESCE(ROUND(AVG(e.duration_ms)), 0) AS INTEGER) AS avg_duration_ms,
			CAST(COALESCE(ROUND(AVG(CASE WHEN e.ttfb_ms > 0 THEN e.ttfb_ms END)), 0) AS INTEGER) AS avg_ttfb_ms,
			MIN(e.ts) AS first_ts, MAX(e.ts) AS last_ts` +
		usageJoins + ` WHERE ` + where + ` GROUP BY ` + grp.groupBy + `) `
	match := ""
	if q.Q != "" {
		match = ` WHERE label LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(q.Q)+"%")
	}

	var total int64
	totals := &UsageBreakdownTotals{}
	if err := s.db.QueryRow(cte+`SELECT COUNT(*), COALESCE(SUM(requests), 0), COALESCE(SUM(errors), 0),
			COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cached_tokens), 0) FROM g`+match, args...).
		Scan(&total, &totals.Requests, &totals.Errors, &totals.InputTokens, &totals.OutputTokens,
			&totals.CachedTokens); err != nil {
		return nil, 0, nil, fmt.Errorf("usage breakdown totals: %w", err)
	}
	offset := int64(q.Page-1) * int64(q.PageSize)
	rows, err := s.db.Query(cte+`SELECT user_id, email, provider, model, key_id, alias, upstream_key,
			requests, errors, input_tokens, output_tokens, cached_tokens, avg_duration_ms, avg_ttfb_ms,
			first_ts, last_ts
		FROM g`+match+` ORDER BY `+sortExpr+` `+dir+`, label COLLATE NOCASE ASC LIMIT ? OFFSET ?`,
		append(args, q.PageSize, offset)...)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("usage breakdown rows: %w", err)
	}
	defer rows.Close()
	out := []UsageBreakdownRow{}
	for rows.Next() {
		var r UsageBreakdownRow
		if err := rows.Scan(&r.UserID, &r.Email, &r.Provider, &r.Model, &r.KeyID, &r.Alias, &r.UpstreamKey,
			&r.Requests, &r.Errors, &r.InputTokens, &r.OutputTokens, &r.CachedTokens, &r.AvgDurationMs, &r.AvgTtfbMs,
			&r.FirstTs, &r.LastTs); err != nil {
			return nil, 0, nil, err
		}
		out = append(out, r)
	}
	return out, total, totals, rows.Err()
}

// --- series ---

// UsageSeriesPoint is one bucket of a grouped series.
type UsageSeriesPoint struct {
	TS    int64 `json:"ts"`
	Value int64 `json:"value"`
}

// UsageSeries is one group's zero-filled bucket series.
type UsageSeries struct {
	Key    string             `json:"key"`
	Label  string             `json:"label"`
	Points []UsageSeriesPoint `json:"points"`
}

// UsageSeriesOtherKey is the key of the series that folds every group
// outside the top N.
const UsageSeriesOtherKey = "other"

// usageSeriesGroups: the series key and display label per groupBy.
var usageSeriesGroups = map[string]struct{ key, label string }{
	"user":     {key: `CAST(e.user_id AS TEXT)`, label: usageEmailExpr},
	"model":    {key: `e.provider || '/' || e.model`, label: `e.provider || '/' || e.model`},
	"provider": {key: `e.provider`, label: `e.provider`},
	"surface":  {key: `e.surface`, label: `CASE WHEN e.surface = '' THEN 'unknown' ELSE e.surface END`},
}

var usageSeriesMetrics = map[string]string{
	"requests":     `COUNT(*)`,
	"totalTokens":  `COALESCE(SUM(e.input_tokens + e.output_tokens), 0)`,
	"cachedTokens": `COALESCE(SUM(e.cached_tokens), 0)`,
	"errors":       `COALESCE(SUM(CASE WHEN e.status >= 400 THEN 1 ELSE 0 END), 0)`,
}

// UsageSeriesByGroup returns the top groups by metric over the filtered range,
// each as a zero-filled bucket series, plus an "other" series folding the
// remaining groups when there are more than top. All series share the same
// bucket timestamps.
func (s *Store) UsageSeriesByGroup(f UsageFilter, groupBy, metric string, bucket, tz int64, top int) ([]UsageSeries, error) {
	grp, ok := usageSeriesGroups[groupBy]
	if !ok {
		return nil, fmt.Errorf("%w: groupBy %q", ErrUsageParam, groupBy)
	}
	m, ok := usageSeriesMetrics[metric]
	if !ok {
		return nil, fmt.Errorf("%w: metric %q", ErrUsageParam, metric)
	}
	if bucket <= 0 || top < 1 {
		return nil, fmt.Errorf("%w: bucket/top", ErrUsageParam)
	}
	// bound the fill-in before touching the table
	buckets, err := usageBuckets(f.From, f.To, bucket, tz)
	if err != nil {
		return nil, err
	}
	where, args := f.where()

	// pass 1: rank groups (one extra row tells us whether "other" exists)
	userJoin := ""
	if groupBy == "user" {
		userJoin = ` LEFT JOIN users u ON u.id = e.user_id`
	}
	rows, err := s.db.Query(`SELECT `+grp.key+` AS gk, `+grp.label+` AS gl, `+m+` AS mv
		FROM usage_events e`+userJoin+` WHERE `+where+` GROUP BY gk
		HAVING mv > 0 ORDER BY mv DESC, gk ASC LIMIT ?`, append(args, top+1)...)
	if err != nil {
		return nil, fmt.Errorf("usage series rank: %w", err)
	}
	type ranked struct{ key, label string }
	var groups []ranked
	for rows.Next() {
		var rk ranked
		var mv int64
		if err := rows.Scan(&rk.key, &rk.label, &mv); err != nil {
			rows.Close()
			return nil, err
		}
		groups = append(groups, rk)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hasOther := len(groups) > top
	if hasOther {
		groups = groups[:top]
	}

	index := make(map[int64]int, len(buckets))
	for i, b := range buckets {
		index[b] = i
	}
	newPoints := func() []UsageSeriesPoint {
		pts := make([]UsageSeriesPoint, len(buckets))
		for i, b := range buckets {
			pts[i].TS = b
		}
		return pts
	}
	out := make([]UsageSeries, 0, len(groups)+1)
	pos := make(map[string]int, len(groups))
	for _, gr := range groups {
		pos[gr.key] = len(out)
		out = append(out, UsageSeries{Key: gr.key, Label: gr.label, Points: newPoints()})
	}
	otherIdx := -1
	if hasOther {
		otherIdx = len(out)
		out = append(out, UsageSeries{Key: UsageSeriesOtherKey, Label: "Other", Points: newPoints()})
	}
	if len(groups) == 0 {
		return out, nil
	}

	// pass 2: per bucket, top keys by name and everything else as NULL
	// (folded into "other") — the result is bounded by buckets × (top+1).
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(groups)), ",")
	sargs := make([]any, 0, len(groups)+len(args))
	for _, gr := range groups {
		sargs = append(sargs, gr.key)
	}
	sargs = append(sargs, args...)
	expr := usageBucketExpr(bucket, tz)
	rows, err = s.db.Query(`SELECT b, gk, SUM(mv) FROM (
			SELECT `+expr+` AS b,
				CASE WHEN `+grp.key+` IN (`+placeholders+`) THEN `+grp.key+` END AS gk,
				`+m+` AS mv
			FROM usage_events e WHERE `+where+` GROUP BY b, `+grp.key+`)
		GROUP BY b, gk`, sargs...)
	if err != nil {
		return nil, fmt.Errorf("usage series points: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b, v int64
		var gk sql.NullString
		if err := rows.Scan(&b, &gk, &v); err != nil {
			return nil, err
		}
		i, ok := index[b]
		if !ok {
			continue
		}
		target := otherIdx
		if gk.Valid {
			target = pos[gk.String]
		}
		if target >= 0 {
			out[target].Points[i].Value += v
		}
	}
	return out, rows.Err()
}

// --- request log ---

// UsageRequestRow is one persisted request for the request log.
type UsageRequestRow struct {
	ID             int64  `json:"id"`
	TS             int64  `json:"ts"`
	UserID         int64  `json:"userId"`
	Email          string `json:"email"`
	KeyID          int64  `json:"keyId"`
	Alias          string `json:"alias"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Surface        string `json:"surface"`
	Stream         bool   `json:"stream"`
	Status         int    `json:"status"`
	UpstreamStatus int    `json:"upstreamStatus"`
	FailReason     string `json:"failReason"`
	UpstreamKey    string `json:"upstreamKey"`
	InputTokens    int64  `json:"inputTokens"`
	OutputTokens   int64  `json:"outputTokens"`
	CachedTokens   int64  `json:"cachedTokens"`
	DurationMs     int64  `json:"durationMs"`
	TtfbMs         int64  `json:"ttfbMs"`
	Attempts       int    `json:"attempts"`
}

var usageRequestSorts = map[string]string{
	"ts":           "e.ts",
	"durationMs":   "e.duration_ms",
	"totalTokens":  "(e.input_tokens + e.output_tokens)",
	"cachedTokens": "e.cached_tokens",
}

// UsageRequests returns one page of the filtered request log and the total
// number of matching rows.
func (s *Store) UsageRequests(f UsageFilter, sort string, desc bool, page, pageSize int) ([]UsageRequestRow, int64, error) {
	sortExpr, ok := usageRequestSorts[sort]
	if !ok {
		return nil, 0, fmt.Errorf("%w: sort %q", ErrUsageParam, sort)
	}
	if page < 1 || pageSize < 1 {
		return nil, 0, fmt.Errorf("%w: page", ErrUsageParam)
	}
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	where, args := f.where()
	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM usage_events e WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("usage requests count: %w", err)
	}
	// per-row labels: no GROUP BY here, so the snapshot is read directly
	rows, err := s.db.Query(`SELECT e.id, e.ts, e.user_id,
			CASE WHEN u.id IS NOT NULL THEN u.email
				ELSE COALESCE(NULLIF(e.user_email, ''), 'deleted user #' || e.user_id) END,
			e.client_key_id,
			CASE WHEN k.id IS NOT NULL THEN k.alias
				ELSE COALESCE(NULLIF(e.key_alias, ''), 'deleted key #' || e.client_key_id) END,
			e.provider, e.model, e.surface, e.stream, e.status, e.upstream_status, e.fail_reason,
			e.upstream_key, e.input_tokens, e.output_tokens, e.cached_tokens, e.duration_ms, e.ttfb_ms, e.attempts`+
		usageJoins+` WHERE `+where+` ORDER BY `+sortExpr+` `+dir+`, e.id `+dir+` LIMIT ? OFFSET ?`,
		append(args, pageSize, int64(page-1)*int64(pageSize))...)
	if err != nil {
		return nil, 0, fmt.Errorf("usage requests: %w", err)
	}
	defer rows.Close()
	out := []UsageRequestRow{}
	for rows.Next() {
		var r UsageRequestRow
		var stream int64
		if err := rows.Scan(&r.ID, &r.TS, &r.UserID, &r.Email, &r.KeyID, &r.Alias, &r.Provider, &r.Model,
			&r.Surface, &stream, &r.Status, &r.UpstreamStatus, &r.FailReason, &r.UpstreamKey,
			&r.InputTokens, &r.OutputTokens, &r.CachedTokens, &r.DurationMs, &r.TtfbMs, &r.Attempts); err != nil {
			return nil, 0, err
		}
		r.Stream = stream != 0
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// --- facets ---

// UsageFacetUser is one user present in the range.
type UsageFacetUser struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

// UsageFacetModel is one provider/model pair present in the range.
type UsageFacetModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// UsageFacetKey is one client key present in the range.
type UsageFacetKey struct {
	ID     int64  `json:"id"`
	Alias  string `json:"alias"`
	UserID int64  `json:"userId"`
}

// UsageFacets lists the distinct filter values present in a range.
type UsageFacets struct {
	Users     []UsageFacetUser  `json:"users"`
	Providers []string          `json:"providers"`
	Models    []UsageFacetModel `json:"models"`
	Keys      []UsageFacetKey   `json:"keys"`
	Surfaces  []string          `json:"surfaces"`
}

// UsageFacets returns the distinct users (only when includeUsers),
// providers, models, client keys, and surfaces with rows in the range.
// Slices are never nil so they encode as [].
func (s *Store) UsageFacets(from, to int64, userID *int64, includeUsers bool) (*UsageFacets, error) {
	where, args := UsageFilter{From: from, To: to, UserID: userID}.where()
	fc := &UsageFacets{
		Users: []UsageFacetUser{}, Providers: []string{}, Models: []UsageFacetModel{},
		Keys: []UsageFacetKey{}, Surfaces: []string{},
	}
	if includeUsers {
		rows, err := s.db.Query(`SELECT e.user_id, `+usageEmailExpr+` AS label
			FROM usage_events e LEFT JOIN users u ON u.id = e.user_id
			WHERE `+where+` GROUP BY e.user_id ORDER BY label COLLATE NOCASE`, args...)
		if err := scanFacet(rows, err, func(sc func(...any) error) error {
			var v UsageFacetUser
			if err := sc(&v.ID, &v.Email); err != nil {
				return err
			}
			fc.Users = append(fc.Users, v)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("usage facets users: %w", err)
		}
	}
	rows, err := s.db.Query(`SELECT DISTINCT e.provider FROM usage_events e WHERE `+where+` ORDER BY e.provider`, args...)
	if err := scanFacet(rows, err, func(sc func(...any) error) error {
		var v string
		if err := sc(&v); err != nil {
			return err
		}
		fc.Providers = append(fc.Providers, v)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("usage facets providers: %w", err)
	}
	rows, err = s.db.Query(`SELECT DISTINCT e.provider, e.model FROM usage_events e WHERE `+where+` ORDER BY e.provider, e.model`, args...)
	if err := scanFacet(rows, err, func(sc func(...any) error) error {
		var v UsageFacetModel
		if err := sc(&v.Provider, &v.Model); err != nil {
			return err
		}
		fc.Models = append(fc.Models, v)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("usage facets models: %w", err)
	}
	rows, err = s.db.Query(`SELECT e.client_key_id, `+usageAliasExpr+` AS label, e.user_id
		FROM usage_events e LEFT JOIN api_keys k ON k.id = e.client_key_id
		WHERE `+where+` GROUP BY e.client_key_id, e.user_id ORDER BY label COLLATE NOCASE, e.client_key_id`, args...)
	if err := scanFacet(rows, err, func(sc func(...any) error) error {
		var v UsageFacetKey
		if err := sc(&v.ID, &v.Alias, &v.UserID); err != nil {
			return err
		}
		fc.Keys = append(fc.Keys, v)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("usage facets keys: %w", err)
	}
	rows, err = s.db.Query(`SELECT DISTINCT e.surface FROM usage_events e WHERE `+where+` AND e.surface != '' ORDER BY e.surface`, args...)
	if err := scanFacet(rows, err, func(sc func(...any) error) error {
		var v string
		if err := sc(&v); err != nil {
			return err
		}
		fc.Surfaces = append(fc.Surfaces, v)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("usage facets surfaces: %w", err)
	}
	return fc, nil
}

// scanFacet drains rows through fn, closing them on every path.
func scanFacet(rows *sql.Rows, err error, fn func(scan func(...any) error) error) error {
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}
