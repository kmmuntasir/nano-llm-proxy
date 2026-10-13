package gateway

import (
	"net/http"
	"time"
)

// handleDashboard serves GET /api/dashboard — everything the GUI home page
// needs in one shot: uptime, per-provider pool health with live key views,
// and the recent-activity ring (newest first).
//
// Superadmins see everything. Other users get pool health counts only (no
// per-key views) and just their own activity entries, with the upstream key
// hash stripped — the ring is global and otherwise leaks other users'
// emails, aliases and fail reasons.
// dashboardActivityLimit caps the activity list for every caller.
const dashboardActivityLimit = activityRingSize

func (g *gateway) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := contextUser(r)
	admin := user != nil && user.Role == "superadmin"
	type provHealth struct {
		Name    string    `json:"name"`
		Type    string    `json:"type"`
		Enabled bool      `json:"enabled"`
		Healthy int       `json:"healthy"`
		Total   int       `json:"total"`
		Keys    []KeyView `json:"keys"`
	}
	provs := []provHealth{}
	for _, ref := range g.allProviders() {
		snap := ref.pool.snapshot()
		ph := provHealth{Name: ref.name, Type: ref.typ, Enabled: true, Keys: snap}
		for _, v := range snap {
			ph.Total++
			if v.Status == statusHealthy {
				ph.Healthy++
			}
		}
		if !admin {
			ph.Keys = []KeyView{}
		}
		provs = append(provs, ph)
	}
	var activity []activityEntry
	if admin {
		activity = g.usage.recent(dashboardActivityLimit)
	} else {
		var uid int64 = -1 // no session user: match nothing
		if user != nil {
			uid = user.ID
		}
		activity = g.usage.recentWhere(dashboardActivityLimit, func(e activityEntry) bool {
			return e.UserID == uid
		})
		for i := range activity {
			activity[i].KeyHash = ""
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uptimeS":   int(time.Since(startTime).Seconds()),
		"providers": provs,
		"activity":  activity,
	})
}
