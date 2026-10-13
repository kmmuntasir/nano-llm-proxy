package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// Usage monitoring v2 endpoints: /api/usage/overview|breakdown|series|
// requests|facets. Parameters are validated strictly — an unknown enum value
// or an out-of-range page is a 400, never silently defaulted — so the GUI
// learns about contract drift immediately. Non-superadmins are always scoped
// to themselves; asking for anyone else (or a cross-user grouping) is a 403.

var (
	usageSurfaces = map[string]bool{"chat": true, "messages": true, "responses": true, "mcp": true}
	usageStatuses = map[string]bool{"ok": true, "error": true}

	usageBreakdownGroupBys = map[string]bool{
		"user": true, "model": true, "user_model": true, "provider": true, "key": true, "upstream_key": true,
	}
	usageBreakdownAdminOnly = map[string]bool{"user": true, "user_model": true, "upstream_key": true}
	usageBreakdownSortNames = map[string]bool{
		"requests": true, "errors": true, "errorRate": true, "inputTokens": true, "outputTokens": true,
		"cachedTokens": true, "totalTokens": true, "avgDurationMs": true, "lastTs": true, "label": true,
	}

	usageSeriesGroupBys   = map[string]bool{"user": true, "model": true, "provider": true, "surface": true}
	usageSeriesAdminOnly  = map[string]bool{"user": true}
	usageSeriesMetricKeys = map[string]bool{"requests": true, "totalTokens": true, "cachedTokens": true, "errors": true}

	usageRequestSortNames = map[string]bool{"ts": true, "durationMs": true, "totalTokens": true, "cachedTokens": true}
)

const usageMaxPage = 1_000_000 // keeps OFFSET arithmetic far from overflow

// usageQuery is the parsed common filter set of a v2 usage request.
type usageQuery struct {
	filter     store.UsageFilter
	admin      bool
	tz         int64
	bucket     int64  // seconds
	bucketName string // "hour" | "day"
}

func (q usageQuery) rangeJSON() map[string]int64 {
	return map[string]int64{"from": q.filter.From, "to": q.filter.To}
}

// parseUsageQuery reads range, scope, filters, tz and bucket. On failure it
// has already written the 400/403 response.
func parseUsageQuery(w http.ResponseWriter, r *http.Request) (usageQuery, bool) {
	var q usageQuery
	from, to, ok := usageRange(r)
	if !ok {
		apiErr(w, http.StatusBadRequest, "Invalid date range: 'from' must be before 'to' (unix seconds)")
		return q, false
	}
	q.filter.From, q.filter.To = from, to
	user := contextUser(r)
	q.admin = user != nil && isAdminRole(user.Role)
	vals := r.URL.Query()

	if v := vals.Get("userId"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			apiErr(w, http.StatusBadRequest, "Invalid userId: must be a positive integer")
			return q, false
		}
		if !q.admin && (user == nil || id != user.ID) {
			apiErr(w, http.StatusForbidden, "You can only view your own usage")
			return q, false
		}
		q.filter.UserID = &id
	}
	if !q.admin {
		q.filter.UserID = scopedUser(user)
	}
	if v := vals.Get("keyId"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			apiErr(w, http.StatusBadRequest, "Invalid keyId: must be a positive integer")
			return q, false
		}
		q.filter.KeyID = id
	}
	q.filter.Provider = vals.Get("provider")
	q.filter.Model = vals.Get("model")
	if v := vals.Get("surface"); v != "" {
		if !usageSurfaces[v] {
			apiErr(w, http.StatusBadRequest, "Invalid surface: use chat, messages, responses or mcp")
			return q, false
		}
		q.filter.Surface = v
	}
	if v := vals.Get("status"); v != "" {
		if !usageStatuses[v] {
			apiErr(w, http.StatusBadRequest, "Invalid status: use ok or error")
			return q, false
		}
		q.filter.Status = v
	}
	if v := vals.Get("tz"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < -86400 || n > 86400 {
			apiErr(w, http.StatusBadRequest, "Invalid tz: seconds east of UTC, between -86400 and 86400")
			return q, false
		}
		q.tz = n
	}
	q.bucket, q.bucketName = 86400, "day"
	if to-from <= 48*3600 {
		q.bucket, q.bucketName = 3600, "hour"
	}
	switch vals.Get("bucket") {
	case "":
	case "hour":
		q.bucket, q.bucketName = 3600, "hour"
	case "day":
		q.bucket, q.bucketName = 86400, "day"
	default:
		apiErr(w, http.StatusBadRequest, "Invalid bucket: use hour or day")
		return q, false
	}
	return q, true
}

// intParam parses an optional bounded integer query parameter; on failure it
// writes the 400 itself.
func intParam(w http.ResponseWriter, r *http.Request, name string, def, lo, hi int) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		apiErr(w, http.StatusBadRequest, fmt.Sprintf("Invalid %s: must be an integer between %d and %d", name, lo, hi))
		return 0, false
	}
	return n, true
}

// enumParam reads an optional whitelisted query parameter.
func enumParam(w http.ResponseWriter, r *http.Request, name, def string, allowed map[string]bool) (string, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	if !allowed[v] {
		apiErr(w, http.StatusBadRequest, fmt.Sprintf("Invalid %s %q", name, v))
		return "", false
	}
	return v, true
}

// orderParam reads ?order=asc|desc (default desc); true = descending.
func orderParam(w http.ResponseWriter, r *http.Request) (bool, bool) {
	switch r.URL.Query().Get("order") {
	case "", "desc":
		return true, true
	case "asc":
		return false, true
	}
	apiErr(w, http.StatusBadRequest, "Invalid order: use asc or desc")
	return false, false
}

// usageStoreErr maps a store failure onto the API: refused parameters are
// the client's fault, anything else is ours.
func usageStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrUsageParam) {
		apiErr(w, http.StatusBadRequest, err.Error())
		return
	}
	apiErr(w, http.StatusInternalServerError, err.Error())
}

// handleUsageOverview serves GET /api/usage/overview.
func (g *gateway) handleUsageOverview(w http.ResponseWriter, r *http.Request) {
	q, ok := parseUsageQuery(w, r)
	if !ok {
		return
	}
	totals, points, err := g.store.UsageOverview(q.filter, q.bucket, q.tz)
	if err != nil {
		usageStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":  q.rangeJSON(),
		"bucket": q.bucketName,
		"totals": totals,
		"points": points,
	})
}

// handleUsageBreakdown serves GET /api/usage/breakdown.
func (g *gateway) handleUsageBreakdown(w http.ResponseWriter, r *http.Request) {
	q, ok := parseUsageQuery(w, r)
	if !ok {
		return
	}
	groupBy, ok := enumParam(w, r, "groupBy", "model", usageBreakdownGroupBys)
	if !ok {
		return
	}
	if usageBreakdownAdminOnly[groupBy] && !q.admin {
		apiErr(w, http.StatusForbidden, fmt.Sprintf("groupBy=%s is only available to admins", groupBy))
		return
	}
	sort, ok := enumParam(w, r, "sort", "requests", usageBreakdownSortNames)
	if !ok {
		return
	}
	desc, ok := orderParam(w, r)
	if !ok {
		return
	}
	page, ok := intParam(w, r, "page", 1, 1, usageMaxPage)
	if !ok {
		return
	}
	pageSize, ok := intParam(w, r, "pageSize", 25, 1, 200)
	if !ok {
		return
	}
	rows, total, totals, err := g.store.UsageBreakdown(store.UsageBreakdownQuery{
		Filter: q.filter, GroupBy: groupBy, Sort: sort, Desc: desc,
		Page: page, PageSize: pageSize, Q: r.URL.Query().Get("q"),
	})
	if err != nil {
		usageStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":    q.rangeJSON(),
		"groupBy":  groupBy,
		"page":     page,
		"pageSize": pageSize,
		"total":    total,
		"totals":   totals,
		"rows":     rows,
	})
}

// handleUsageSeries serves GET /api/usage/series.
func (g *gateway) handleUsageSeries(w http.ResponseWriter, r *http.Request) {
	q, ok := parseUsageQuery(w, r)
	if !ok {
		return
	}
	groupBy, ok := enumParam(w, r, "groupBy", "model", usageSeriesGroupBys)
	if !ok {
		return
	}
	if usageSeriesAdminOnly[groupBy] && !q.admin {
		apiErr(w, http.StatusForbidden, fmt.Sprintf("groupBy=%s is only available to admins", groupBy))
		return
	}
	metric, ok := enumParam(w, r, "metric", "requests", usageSeriesMetricKeys)
	if !ok {
		return
	}
	top, ok := intParam(w, r, "top", 5, 1, 10)
	if !ok {
		return
	}
	series, err := g.store.UsageSeriesByGroup(q.filter, groupBy, metric, q.bucket, q.tz, top)
	if err != nil {
		usageStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":   q.rangeJSON(),
		"bucket":  q.bucketName,
		"groupBy": groupBy,
		"metric":  metric,
		"series":  series,
	})
}

// handleUsageRequests serves GET /api/usage/requests — the raw request log.
func (g *gateway) handleUsageRequests(w http.ResponseWriter, r *http.Request) {
	q, ok := parseUsageQuery(w, r)
	if !ok {
		return
	}
	sort, ok := enumParam(w, r, "sort", "ts", usageRequestSortNames)
	if !ok {
		return
	}
	desc, ok := orderParam(w, r)
	if !ok {
		return
	}
	page, ok := intParam(w, r, "page", 1, 1, usageMaxPage)
	if !ok {
		return
	}
	pageSize, ok := intParam(w, r, "pageSize", 50, 1, 200)
	if !ok {
		return
	}
	rows, total, err := g.store.UsageRequests(q.filter, sort, desc, page, pageSize)
	if err != nil {
		usageStoreErr(w, err)
		return
	}
	if !q.admin {
		// upstream key labels describe the operator's pool, not the caller
		for i := range rows {
			rows[i].UpstreamKey = ""
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":    q.rangeJSON(),
		"page":     page,
		"pageSize": pageSize,
		"total":    total,
		"rows":     rows,
	})
}

// handleUsageFacets serves GET /api/usage/facets — distinct filter values in
// the range. Only the range is read; non-superadmins are forced to
// themselves and get no user list.
func (g *gateway) handleUsageFacets(w http.ResponseWriter, r *http.Request) {
	from, to, ok := usageRange(r)
	if !ok {
		apiErr(w, http.StatusBadRequest, "Invalid date range: 'from' must be before 'to' (unix seconds)")
		return
	}
	user := contextUser(r)
	admin := user != nil && isAdminRole(user.Role)
	fc, err := g.store.UsageFacets(from, to, scopedUser(user), admin)
	if err != nil {
		usageStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fc)
}
