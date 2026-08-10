package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
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

// settingsCandidates returns modules that advertise the "settings" capability.
// Peers that implement SettingsProvider must register that capability (catalog
// SettingsProviders do as of spool 2.4.58+).
func (h *Handler) settingsCandidates(ctx context.Context) []*discoveryv1.ModuleInfoProto {
	mods, err := h.Core.Discovery.FindByCapability(ctx, capSettings)
	if err != nil {
		slog.Warn("settings: FindByCapability failed", "error", err)
		return nil
	}
	out := make([]*discoveryv1.ModuleInfoProto, 0, len(mods))
	for _, mod := range mods {
		if mod.GetId() == "" {
			continue
		}
		out = append(out, mod)
	}
	sort.Slice(out, func(i, j int) bool {
		ni, nj := out[i].GetName(), out[j].GetName()
		if ni == nj {
			return out[i].GetId() < out[j].GetId()
		}
		return ni < nj
	})
	return out
}

func (h *Handler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	modules := h.settingsCandidates(r.Context())

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
		if len(defs) == 0 {
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
	if info, err := h.Core.Discovery.Resolve(r.Context(), moduleID); err == nil && info != nil {
		httpAddr = normalizeDialAddr(info.GetId(), info.GetHttpAddr())
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
