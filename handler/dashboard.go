package handler

import (
	"log/slog"
	"net/http"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	if !h.coreConnected {
		nav := templates.Nav(navLinks, "/")
		content := templates.DashboardPage(0, "", true)
		component := templates.Layout("Dashboard", nav, content)
		h.render(w, r, component)
		return
	}

	members, leader, err := h.Core.Discovery.Members(r.Context())
	if err != nil {
		slog.Warn("dashboard: Members call failed", "error", err)
		nav := templates.Nav(navLinks, "/")
		content := templates.DashboardPage(0, "", true)
		component := templates.Layout("Dashboard", nav, content)
		h.render(w, r, component)
		return
	}

	content := templates.DashboardPage(len(members), leader, false)
	nav := templates.Nav(navLinks, "/")
	component := templates.Layout("Dashboard", nav, content)
	h.render(w, r, component)
}

func (h *Handler) HealthGrid(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache")

	if h.Core == nil {
		w.Write([]byte(`<div class="col-span-full text-sm text-red-400">Core disconnected</div>`))
		return
	}

	members, _, err := h.Core.Discovery.Members(r.Context())
	if err != nil {
		slog.Warn("health: Members call failed", "error", err)
		w.Write([]byte(`<div class="col-span-full text-sm text-red-400">Failed to load health data</div>`))
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
			items = append(items, templates.ModuleHealthItem{
				ID:      modID,
				Name:    modID,
				State:   "running",
				Healthy: healthErr == "",
				Error:   healthErr,
			})
		}
	}

	component := templates.HealthGrid(items)
	h.render(w, r, component)
}
