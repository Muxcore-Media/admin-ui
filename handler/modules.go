package handler

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) ModuleList(w http.ResponseWriter, r *http.Request) {
	modules := h.loadModuleList(r.Context())

	// HTMX fragment refresh (table only). Boosted full-page navigations
	// (hx-boost on <body>) must still get the layout + sidebar.
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") != "true" {
		w.Header().Set("Content-Type", "text/html")
		h.render(w, r, templates.ModuleTable(modules))
		return
	}

	content := templates.ModuleListPage(modules)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Modules", nav, content)
	h.render(w, r, component)
}

func (h *Handler) loadModuleList(ctx context.Context) []templates.ModuleListItem {
	if h.Core == nil {
		return nil
	}

	readCtx, readCancel := context.WithTimeout(ctx, moduleListReadTimeout)
	resp, err := h.Core.Discovery.Raw().ListAll(readCtx, &discoveryv1.ListAllRequest{})
	readCancel()
	if err != nil {
		slog.Warn("modules: ListAll failed, falling back to Members", "error", err)
		return h.loadModuleListFromMembers(ctx)
	}

	var modules []templates.ModuleListItem
	for _, e := range resp.GetEntries() {
		info := e.GetInfo()
		if info == nil {
			continue
		}
		state := e.GetState()
		if state == "" {
			state = "running"
		}
		modules = append(modules, templates.ModuleListItem{
			ID:           info.GetId(),
			Name:         firstNonEmpty(info.GetName(), info.GetId()),
			Version:      info.GetVersion(),
			State:        state,
			Roles:        info.GetRoles(),
			Capabilities: info.GetCapabilities(),
			Healthy:      e.GetHealthError() == "",
		})
	}
	sort.Slice(modules, func(i, j int) bool {
		return strings.ToLower(modules[i].Name) < strings.ToLower(modules[j].Name)
	})
	return modules
}

func (h *Handler) loadModuleListFromMembers(ctx context.Context) []templates.ModuleListItem {
	readCtx, readCancel := context.WithTimeout(ctx, moduleListReadTimeout)
	members, _, err := h.Core.Discovery.Members(readCtx)
	readCancel()
	if err != nil {
		slog.Warn("modules: Members call failed", "error", err)
		return nil
	}

	seen := make(map[string]bool)
	var modules []templates.ModuleListItem

	for _, node := range members {
		for _, modID := range node.GetModules() {
			if seen[modID] {
				continue
			}
			seen[modID] = true

			resolveCtx, resolveCancel := context.WithTimeout(ctx, moduleListReadTimeout)
			info, err := h.Core.Discovery.Resolve(resolveCtx, modID)
			resolveCancel()
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
				Name:         firstNonEmpty(info.GetName(), info.GetId()),
				Version:      info.GetVersion(),
				State:        "running",
				Roles:        info.GetRoles(),
				Capabilities: info.GetCapabilities(),
				Healthy:      healthErr == "",
			})
		}
	}
	sort.Slice(modules, func(i, j int) bool {
		return strings.ToLower(modules[i].Name) < strings.ToLower(modules[j].Name)
	})
	return modules
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
		nav := h.nav(r.URL.Path)
		content := templates.NotFound()
		component := templates.Layout(title, nav, content)
		h.render(w, r, component)
		return
	}
	if info == nil {
		title := "Module Not Found"
		nav := h.nav(r.URL.Path)
		content := templates.NotFound()
		component := templates.Layout(title, nav, content)
		h.render(w, r, component)
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
	nav := h.nav(r.URL.Path)
	component := templates.Layout(info.GetName(), nav, content)
	h.render(w, r, component)
}
