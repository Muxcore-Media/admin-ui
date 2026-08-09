package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"

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
	Value       string   `json:"Value"`
	Description string   `json:"Description"`
	Required    bool     `json:"Required"`
	Options     []string `json:"Options"`
	Group       string   `json:"Group"`
}

type updateSettingReq struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func (h *Handler) settingsMeshCall(ctx context.Context, moduleID, httpAddr, method string, payload []byte) ([]byte, error) {
	if httpAddr != "" {
		conn, err := grpc.NewClient(httpAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			defer conn.Close()
			client := meshv1.NewModuleMeshClient(conn)
			resp, err := client.Call(ctx, &meshv1.CallRequest{
				TargetModule: moduleID,
				Method:       method,
				Payload:      payload,
			})
			if err == nil && resp.GetError() == "" {
				return resp.GetPayload(), nil
			}
			if err != nil {
				slog.Debug("settings: direct mesh dial failed, trying core mesh", "module", moduleID, "error", err)
			} else if resp.GetError() != "" {
				slog.Debug("settings: direct mesh error, trying core mesh", "module", moduleID, "error", resp.GetError())
			}
		}
	}
	return h.Core.Mesh.Call(ctx, moduleID, method, payload)
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

		raw, err := h.settingsMeshCall(r.Context(), mod.GetId(), normalizeDialAddr(mod.GetId(), mod.GetHttpAddr()), methodGet, nil)
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
			val := d.Value
			if val == "" {
				val = d.Default
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
				Value:       val,
			}
			mg.Groups[group] = append(mg.Groups[group], f)
		}

		groups = append(groups, mg)
	}

	content := templates.SettingsPage(groups)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Settings", nav, content)
	h.render(w, r, component)
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

	httpAddr := ""
	if mods, err := h.Core.Discovery.FindByCapability(r.Context(), capSettings); err == nil {
		for _, mod := range mods {
			if mod.GetId() == moduleID {
				httpAddr = normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
				break
			}
		}
	}

	_, err = h.settingsMeshCall(r.Context(), moduleID, httpAddr, methodUpdate, payload)
	if err != nil {
		slog.Warn("settings: update failed", "module", moduleID, "key", key, "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">update failed</div>`))
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.settings.update", "settings", moduleID+"/"+key, map[string]string{
			"module": moduleID,
			"key":    key,
		})
	}

	w.Write([]byte(fmt.Sprintf(`<div class="text-xs text-green-400">%s updated</div>`, key)))
}
