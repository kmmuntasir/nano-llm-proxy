package gateway

import (
	"net/http"
	"time"
)

// handleDashboard serves GET /api/dashboard — everything the GUI home page
// needs in one shot: uptime, per-provider pool health with live key views,
// and the recent-activity ring (newest first).
func (g *gateway) handleDashboard(w http.ResponseWriter, r *http.Request) {
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
		provs = append(provs, ph)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uptimeS":   int(time.Since(startTime).Seconds()),
		"providers": provs,
		"activity":  g.usage.recent(activityRingSize),
	})
}
