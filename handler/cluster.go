package handler

import (
	"log/slog"
	"net/http"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) ClusterPage(w http.ResponseWriter, r *http.Request) {
	members, leaderID, err := h.Core.Discovery.Members(r.Context())
	if err != nil {
		slog.Warn("cluster: Members call failed", "error", err)
		nav := templates.Nav(navLinks, "/cluster")
		content := templates.ClusterPage(templates.ClusterPageData{})
		component := templates.Layout("Cluster", nav, content)
		h.render(w, r, component)
		return
	}

	nodes := make([]templates.NodeCardData, 0, len(members))
	for _, m := range members {
		modules := make([]templates.NodeModuleEntry, 0, len(m.GetModules()))
		for _, modID := range m.GetModules() {
			healthErr := m.GetModuleHealth()[modID]
			modules = append(modules, templates.NodeModuleEntry{
				ID:      modID,
				Healthy: healthErr == "",
			})
		}

		nodes = append(nodes, templates.NodeCardData{
			ID:          m.GetId(),
			GRPCAddr:    m.GetGrpcAddr(),
			HTTPAddr:    m.GetHttpAddr(),
			IsLeader:    m.GetId() == leaderID,
			ModuleCount: len(modules),
			Modules:     modules,
		})
	}

	data := templates.ClusterPageData{Nodes: nodes}
	content := templates.ClusterPage(data)
	nav := templates.Nav(navLinks, "/cluster")
	component := templates.Layout("Cluster", nav, content)
	h.render(w, r, component)
}

func (h *Handler) ClusterNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache")

	members, leaderID, err := h.Core.Discovery.Members(r.Context())
	if err != nil {
		slog.Warn("cluster nodes: Members call failed", "error", err)
		w.Write([]byte(`<div class="col-span-full text-sm text-red-400">Failed to load nodes</div>`))
		return
	}

	for _, m := range members {
		modules := make([]templates.NodeModuleEntry, 0, len(m.GetModules()))
		for _, modID := range m.GetModules() {
			healthErr := m.GetModuleHealth()[modID]
			modules = append(modules, templates.NodeModuleEntry{
				ID:      modID,
				Healthy: healthErr == "",
			})
		}

		card := templates.NodeCard(templates.NodeCardData{
			ID:          m.GetId(),
			GRPCAddr:    m.GetGrpcAddr(),
			HTTPAddr:    m.GetHttpAddr(),
			IsLeader:    m.GetId() == leaderID,
			ModuleCount: len(modules),
			Modules:     modules,
		})
		h.render(w, r, card)
	}
}
