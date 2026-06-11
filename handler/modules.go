package handler

import (
	"log/slog"
	"net/http"
	"strings"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) ModuleList(w http.ResponseWriter, r *http.Request) {
	members, _, err := h.Core.Discovery.Members(r.Context())
	if err != nil {
		slog.Warn("modules: Members call failed", "error", err)
		content := templates.ModuleListPage(nil)
		nav := templates.Nav(navLinks, "/modules")
		component := templates.Layout("Modules", nav, content)
		component.Render(r.Context(), w)
		return
	}

	seen := make(map[string]bool)
	var modules []templates.ModuleListItem

	for _, node := range members {
		for _, modID := range node.GetModules() {
			if seen[modID] {
				continue
			}
			seen[modID] = true

			info, err := h.Core.Discovery.Resolve(r.Context(), modID)
			if err != nil {
				slog.Warn("modules: Resolve failed", "module", modID, "error", err)
				modules = append(modules, templates.ModuleListItem{
					ID:      modID,
					Name:    modID,
					Version: "?",
					State:   "unknown",
				})
				continue
			}
			if info == nil {
				continue
			}

			healthErr := node.GetModuleHealth()[modID]
			modules = append(modules, templates.ModuleListItem{
				ID:           info.GetId(),
				Name:         info.GetName(),
				Version:      info.GetVersion(),
				State:        "running",
				Roles:        info.GetRoles(),
				Capabilities: info.GetCapabilities(),
				Healthy:      healthErr == "",
			})
		}
	}

	content := templates.ModuleListPage(modules)
	nav := templates.Nav(navLinks, "/modules")
	component := templates.Layout("Modules", nav, content)
	component.Render(r.Context(), w)
}

func (h *Handler) ModuleDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/modules/")
	if id == "" {
		http.Redirect(w, r, "/modules", http.StatusSeeOther)
		return
	}

	info, err := h.Core.Discovery.Resolve(r.Context(), id)
	if err != nil {
		slog.Warn("module detail: Resolve failed", "module", id, "error", err)
		title := "Module Not Found"
		nav := templates.Nav(navLinks, "/modules")
		content := templates.NotFound()
		component := templates.Layout(title, nav, content)
		component.Render(r.Context(), w)
		return
	}
	if info == nil {
		title := "Module Not Found"
		nav := templates.Nav(navLinks, "/modules")
		content := templates.NotFound()
		component := templates.Layout(title, nav, content)
		component.Render(r.Context(), w)
		return
	}

	detail := templates.ModuleDetail{
		ID:             info.GetId(),
		Name:           info.GetName(),
		Version:        info.GetVersion(),
		State:          "running",
		Healthy:        true,
		Description:    info.GetDescription(),
		Author:         info.GetAuthor(),
		Roles:          info.GetRoles(),
		Capabilities:   info.GetCapabilities(),
		DependsOn:      info.GetDependsOn(),
		HTTPAddr:       info.GetHttpAddr(),
		MinCoreVersion: info.GetMinCoreVersion(),
	}

	content := templates.ModuleDetailPage(detail)
	nav := templates.Nav(navLinks, "/modules")
	component := templates.Layout(info.GetName(), nav, content)
	component.Render(r.Context(), w)
}
