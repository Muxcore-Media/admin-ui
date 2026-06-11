package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capSettings  = "settings"
	methodGet    = "Settings"
	methodUpdate = "UpdateSetting"
)

type settingDefJSON struct {
	Key         string   `json:"Key"`
	Label       string   `json:"Label"`
	Type        string   `json:"Type"`
	Default     string   `json:"Default"`
	Description string   `json:"Description"`
	Required    bool     `json:"Required"`
	Options     []string `json:"Options"`
	Group       string   `json:"Group"`
}

type updateSettingReq struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func (h *Handler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	modules, err := h.Core.Discovery.FindByCapability(r.Context(), capSettings)
	if err != nil {
		slog.Warn("settings: FindByCapability failed", "error", err)
	}

	var groups []templates.SettingsModuleGroup
	for _, mod := range modules {
		mg := templates.SettingsModuleGroup{
			ModuleID:   mod.GetId(),
			ModuleName: mod.GetName(),
			Groups:     make(map[string][]templates.SettingField),
		}

		raw, err := h.Core.Mesh.Call(r.Context(), mod.GetId(), methodGet, nil)
		if err != nil {
			slog.Warn("settings: mesh call failed", "module", mod.GetId(), "error", err)
			continue
		}

		var defs []settingDefJSON
		if err := json.Unmarshal(raw, &defs); err != nil {
			slog.Warn("settings: unmarshal failed", "module", mod.GetId(), "error", err)
			continue
		}

		for _, d := range defs {
			group := d.Group
			if group == "" {
				group = "General"
			}
			f := templates.SettingField{
				ModuleID:    mod.GetId(),
				Key:         d.Key,
				Label:       d.Label,
				Type:        d.Type,
				Default:     d.Default,
				Description: d.Description,
				Required:    d.Required,
				Options:     d.Options,
				Group:       d.Group,
				Value:       d.Default,
			}
			mg.Groups[group] = append(mg.Groups[group], f)
		}

		groups = append(groups, mg)
	}

	content := templates.SettingsPage(groups)
	nav := templates.Nav(navLinks, "/settings")
	component := templates.Layout("Settings", nav, content)
	component.Render(r.Context(), w)
}

func (h *Handler) SettingsUpdate(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	key := r.PathValue("key")

	if moduleID == "" || key == "" {
		http.Error(w, "missing module or key", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
		return
	}

	value := r.FormValue("value")

	req := updateSettingReq{
		Key:   key,
		Value: value,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		slog.Error("settings: marshal update req", "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">internal error</div>`))
		return
	}

	_, err = h.Core.Mesh.Call(r.Context(), moduleID, methodUpdate, payload)
	if err != nil {
		slog.Warn("settings: update failed", "module", moduleID, "key", key, "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">update failed</div>`))
		return
	}

	w.Write([]byte(fmt.Sprintf(`<div class="text-xs text-green-400">%s updated</div>`, key)))
}

func (h *Handler) SettingsModuleSettings(w http.ResponseWriter, r *http.Request) {
	moduleID := strings.TrimPrefix(r.URL.Path, "/api/settings/")
	if moduleID == "" {
		http.Error(w, "missing module id", http.StatusBadRequest)
		return
	}

	raw, err := h.Core.Mesh.Call(r.Context(), moduleID, methodGet, nil)
	if err != nil {
		slog.Warn("settings: query failed", "module", moduleID, "error", err)
		http.Error(w, "settings unavailable", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}
