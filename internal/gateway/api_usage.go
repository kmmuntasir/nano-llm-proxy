package gateway

import (
	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
	"net/http"
	"strconv"
	"time"
)

// Usage reporting endpoints. Date ranges are unix seconds (?from=&to=);
// defaults cover the last 24h and ranges are capped to 92 days so a stray
// client cannot request the whole table. Non-superadmin sessions are scoped
// to their own user_id everywhere.

const usageMaxRangeDays = 92

// usageRange parses ?from/?to, applies defaults, caps the span, and clamps
// `to` to now (future ranges would silently hide rows). Negative bounds are
// refused, and every comparison is written so it cannot overflow: an
// unchecked `to-from` near MinInt64 would skip the cap and let the bucket
// fill-in allocate without bound.
func usageRange(r *http.Request) (from, to int64, ok bool) {
	now := time.Now().Unix()
	to = now
	from = to - 24*3600
	if v := r.URL.Query().Get("to"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, 0, false
		}
		to = min(n, now)
	}
	if v := r.URL.Query().Get("from"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, 0, false
		}
		from = n
	}
	if from >= to {
		return 0, 0, false
	}
	// to is in [1, now], so to-span cannot overflow; from < to - span is the
	// overflow-free form of to-from > span.
	const span = int64(usageMaxRangeDays) * 86400
	if from < to-span {
		from = to - span
	}
	return from, to, true
}

// scopedUser returns nil (all users) for superadmins and their own id for
// everyone else.
func scopedUser(u *store.User) *int64 {
	if u != nil && isAdminRole(u.Role) {
		return nil
	}
	if u == nil {
		return nil
	}
	id := u.ID
	return &id
}

func (g *gateway) handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	from, to, ok := usageRange(r)
	if !ok {
		apiErr(w, http.StatusBadRequest, "Invalid date range: 'from' must be before 'to' (unix seconds)")
		return
	}
	user := contextUser(r)
	scope := scopedUser(user)
	totals, err := g.store.UsageTotals(from, to, scope)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	models, err := g.store.UsageByModel(from, to, scope, 5)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	providers, err := g.store.UsageByProvider(from, to, scope)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to,
		"totals":    totals,
		"topModels": models,
		"providers": providers,
	})
}

func (g *gateway) handleUsageUsers(w http.ResponseWriter, r *http.Request) {
	from, to, ok := usageRange(r)
	if !ok {
		apiErr(w, http.StatusBadRequest, "Invalid date range")
		return
	}
	rows, err := g.store.UsageByUser(from, to)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": rows})
}

func (g *gateway) handleUsageKeys(w http.ResponseWriter, r *http.Request) {
	from, to, ok := usageRange(r)
	if !ok {
		apiErr(w, http.StatusBadRequest, "Invalid date range")
		return
	}
	rows, err := g.store.UsageByKey(from, to, scopedUser(contextUser(r)))
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": rows})
}

func (g *gateway) handleUsageActivity(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	rows, err := g.store.UsageActivity(limit, scopedUser(contextUser(r)))
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": rows})
}

// handleUsageTimeseries serves GET /api/usage/timeseries — per-bucket request
// and token counts for the dashboard charts. bucket=hour|day (default: auto —
// hour when the range is ≤48h, day otherwise); tz is the client's offset from
// UTC in seconds so buckets land on the viewer's local midnights.
func (g *gateway) handleUsageTimeseries(w http.ResponseWriter, r *http.Request) {
	from, to, ok := usageRange(r)
	if !ok {
		apiErr(w, http.StatusBadRequest, "Invalid date range")
		return
	}
	bucket := int64(86400)
	if to-from <= 48*3600 {
		bucket = 3600
	}
	switch r.URL.Query().Get("bucket") {
	case "hour":
		bucket = 3600
	case "day":
		bucket = 86400
	}
	tz := int64(0)
	if v := r.URL.Query().Get("tz"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > -86400 && n < 86400 {
			tz = n
		}
	}
	scope := scopedUser(contextUser(r))
	points, err := g.store.UsageTimeseries(from, to, bucket, tz, scope)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	providers, err := g.store.UsageProviderTimeseries(from, to, bucket, tz, scope)
	if err != nil {
		apiErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bucket": bucket, "points": points, "providers": providers,
	})
}
