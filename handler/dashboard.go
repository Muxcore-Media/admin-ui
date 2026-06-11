package handler

import (
	"log/slog"
	"net/http"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	nodeCount := 0
	leaderID := ""
	connected := h.Core != nil

	if connected {
		members, leader, err := h.Core.Discovery.Members(r.Context())
		if err != nil {
			slog.Warn("dashboard: Members call failed", "error", err)
		} else {
			nodeCount = len(members)
			leaderID = leader
		}
	}

	content := templates.DashboardPage(nodeCount, leaderID, !connected)
	nav := templates.Nav(navLinks, "/")
	component := templates.Layout("Dashboard", nav, content)
	component.Render(r.Context(), w)
}

func (h *Handler) HealthGrid(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache")

	members, _, err := h.Core.Discovery.Members(r.Context())
	if err != nil {
		slog.Warn("health: Members call failed", "error", err)
		w.Write([]byte(`<div class="text-sm text-red-400">Failed to load health data</div>`))
		return
	}

	seen := make(map[string]bool)
	var items []templates.ModuleHealthItem

	for _, node := range members {
		for _, modID := range node.GetModules() {
			if seen[modID] {
				continue
			}
			seen[modID] = true

			healthErr := node.GetModuleHealth()[modID]
			item := templates.ModuleHealthItem{
				ID:      modID,
				Name:    modID,
				State:   "running",
				Healthy: healthErr == "",
				Error:   healthErr,
			}
			items = append(items, item)
		}
	}

	component := templates.HealthGrid(items)
	component.Render(r.Context(), w)
}

func (h *Handler) AuthStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"available":true}`))
}
