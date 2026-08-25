package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaTagging      = "media.tagging"
	taggingMeshListTags  = "ListTags"
	taggingMeshCreateTag = "CreateTag"
	taggingMeshListRules = "ListRules"
	taggingMeshUpsert    = "UpsertRule"
	taggingMeshDelete    = "DeleteRule"
	taggingDialTimeout   = 3 * time.Second
	taggingReadTimeout   = 5 * time.Second
	taggingPageTimeout   = taggingDialTimeout + 2*taggingReadTimeout + time.Second
)

func taggingHTTPDo(_ context.Context, req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: taggingReadTimeout}).Do(req)
}

type taggingTagJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Color    string `json:"color"`
}

type taggingRuleJSON struct {
	ID      string `json:"id"`
	TagID   string `json:"tag_id"`
	Field   string `json:"field"`
	Match   string `json:"match"`
	Pattern string `json:"pattern"`
	Enabled bool   `json:"enabled"`
}

func (h *Handler) taggingModule(ctx context.Context) (id, grpcAddr, name string, err error) {
	if h.Core == nil {
		return "", "", "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaTagging)
	if err != nil {
		return "", "", "", err
	}
	if len(mods) == 0 {
		return "", "", "", fmt.Errorf("no module with capability %s", capMediaTagging)
	}
	mod := mods[0]
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return "", "", "", fmt.Errorf("tagging module has no dial address")
	}
	return mod.GetId(), addr, mod.GetName(), nil
}

func (h *Handler) taggingMeshCall(ctx context.Context, moduleID, httpAddr, method string, payload []byte) ([]byte, error) {
	if httpAddr != "" {
		conn, err := grpc.NewClient(httpAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			defer func() { _ = conn.Close() }()
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
				slog.Debug("tagging: direct mesh dial failed, trying core mesh", "module", moduleID, "error", err)
			} else if resp.GetError() != "" {
				slog.Debug("tagging: direct mesh error, trying core mesh", "module", moduleID, "error", resp.GetError())
			}
		}
	}
	if h.Core == nil || h.Core.Mesh == nil {
		return nil, fmt.Errorf("mesh unavailable")
	}
	return h.Core.Mesh.Call(ctx, moduleID, method, payload)
}

func taggingHTTPBaseFromGRPC(grpcAddr string) string {
	host, portStr, err := net.SplitHostPort(grpcAddr)
	if err != nil {
		return ""
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port+1))
}

func (h *Handler) taggingFetchTags(ctx context.Context, moduleID, grpcAddr string) ([]templates.TaggingTagRow, error) {
	raw, err := h.taggingMeshCall(ctx, moduleID, grpcAddr, taggingMeshListTags, []byte(`{}`))
	if err == nil {
		return decodeTaggingTags(raw)
	}
	base := taggingHTTPBaseFromGRPC(grpcAddr)
	if base == "" {
		return nil, err
	}
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/tags", nil)
	if reqErr != nil {
		return nil, err
	}
	resp, httpErr := taggingHTTPDo(ctx, req)
	if httpErr != nil {
		return nil, fmt.Errorf("mesh: %v; http: %w", err, httpErr)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mesh: %v; http status %d", err, resp.StatusCode)
	}
	return decodeTaggingTags(body)
}

func (h *Handler) taggingFetchRules(ctx context.Context, moduleID, grpcAddr string) ([]templates.TaggingRuleRow, error) {
	raw, err := h.taggingMeshCall(ctx, moduleID, grpcAddr, taggingMeshListRules, nil)
	if err == nil {
		return decodeTaggingRules(raw)
	}
	base := taggingHTTPBaseFromGRPC(grpcAddr)
	if base == "" {
		return nil, err
	}
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/rules", nil)
	if reqErr != nil {
		return nil, err
	}
	resp, httpErr := taggingHTTPDo(ctx, req)
	if httpErr != nil {
		return nil, fmt.Errorf("mesh: %v; http: %w", err, httpErr)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mesh: %v; http status %d", err, resp.StatusCode)
	}
	return decodeTaggingRules(body)
}

func decodeTaggingTags(raw []byte) ([]templates.TaggingTagRow, error) {
	var items []taggingTagJSON
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]templates.TaggingTagRow, 0, len(items))
	for _, t := range items {
		out = append(out, templates.TaggingTagRow{
			ID: t.ID, Name: t.Name, Category: t.Category, Color: t.Color,
		})
	}
	return out, nil
}

func decodeTaggingRules(raw []byte) ([]templates.TaggingRuleRow, error) {
	var items []taggingRuleJSON
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]templates.TaggingRuleRow, 0, len(items))
	for _, r := range items {
		out = append(out, templates.TaggingRuleRow{
			ID: r.ID, TagID: r.TagID, Field: r.Field,
			Match: r.Match, Pattern: r.Pattern, Enabled: r.Enabled,
		})
	}
	return out, nil
}

func (h *Handler) TaggingPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), taggingPageTimeout)
	defer cancel()

	data := templates.TaggingPageData{Flash: r.URL.Query().Get("ok")}
	dialCtx, dialCancel := context.WithTimeout(pageCtx, taggingDialTimeout)
	moduleID, addr, name, err := h.taggingModule(dialCtx)
	dialCancel()
	if err != nil {
		slog.Warn("tagging: resolve failed", "error", err)
		data.Error = err.Error()
		data.SoftEmpty = true
		h.renderTagging(w, r, data)
		return
	}
	data.ModuleID = moduleID
	data.ModuleName = name

	tags, err := h.taggingFetchTags(pageCtx, moduleID, addr)
	if err != nil {
		slog.Warn("tagging: list tags failed", "module", moduleID, "error", err)
		data.Error = err.Error()
		data.SoftEmpty = true
		h.renderTagging(w, r, data)
		return
	}
	rules, err := h.taggingFetchRules(pageCtx, moduleID, addr)
	if err != nil {
		slog.Warn("tagging: list rules failed", "module", moduleID, "error", err)
		data.Error = err.Error()
		data.SoftEmpty = true
		h.renderTagging(w, r, data)
		return
	}
	tagNames := map[string]string{}
	for _, t := range tags {
		tagNames[t.ID] = t.Name
	}
	for i := range rules {
		rules[i].TagName = tagNames[rules[i].TagID]
	}
	data.Tags = tags
	data.Rules = rules
	h.renderTagging(w, r, data)
}

func (h *Handler) TaggingCreateTag(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), taggingPageTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	moduleID, addr, _, err := h.taggingModule(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	payload, _ := json.Marshal(map[string]string{
		"name": r.FormValue("name"), "category": r.FormValue("category"), "color": r.FormValue("color"),
	})
	if _, err := h.taggingMeshCall(ctx, moduleID, addr, taggingMeshCreateTag, payload); err != nil {
		base := taggingHTTPBaseFromGRPC(addr)
		if base == "" {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/tags", strings.NewReader(string(payload)))
		if reqErr != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, httpErr := taggingHTTPDo(ctx, req)
		if httpErr != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				_ = resp.Body.Close()
			}
			http.Error(w, fmt.Sprintf("create tag failed: %v", err), http.StatusBadGateway)
			return
		}
		_ = resp.Body.Close()
	}
	http.Redirect(w, r, "/tagging?ok=Tag+created", http.StatusSeeOther)
}

func (h *Handler) TaggingCreateRule(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), taggingPageTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	moduleID, addr, _, err := h.taggingModule(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	enabled := r.FormValue("enabled") == "1" || r.FormValue("enabled") == "on"
	payload, _ := json.Marshal(map[string]any{
		"tag_id":  r.FormValue("tag_id"),
		"field":   r.FormValue("field"),
		"match":   r.FormValue("match"),
		"pattern": r.FormValue("pattern"),
		"enabled": enabled,
	})
	if _, err := h.taggingMeshCall(ctx, moduleID, addr, taggingMeshUpsert, payload); err != nil {
		base := taggingHTTPBaseFromGRPC(addr)
		if base == "" {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/rules", strings.NewReader(string(payload)))
		if reqErr != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, httpErr := taggingHTTPDo(ctx, req)
		if httpErr != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				_ = resp.Body.Close()
			}
			http.Error(w, fmt.Sprintf("create rule failed: %v", err), http.StatusBadGateway)
			return
		}
		_ = resp.Body.Close()
	}
	http.Redirect(w, r, "/tagging?ok=Rule+created", http.StatusSeeOther)
}

func (h *Handler) TaggingDeleteRule(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), taggingPageTimeout)
	defer cancel()
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	moduleID, addr, _, err := h.taggingModule(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	payload, _ := json.Marshal(map[string]string{"id": id})
	if _, err := h.taggingMeshCall(ctx, moduleID, addr, taggingMeshDelete, payload); err != nil {
		base := taggingHTTPBaseFromGRPC(addr)
		if base == "" {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/api/rules/"+id, nil)
		if reqErr != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		resp, httpErr := taggingHTTPDo(ctx, req)
		if httpErr != nil || (resp != nil && resp.StatusCode != http.StatusOK) {
			if resp != nil {
				_ = resp.Body.Close()
			}
			http.Error(w, fmt.Sprintf("delete rule failed: %v", err), http.StatusBadGateway)
			return
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
	}
	http.Redirect(w, r, "/tagging?ok=Rule+deleted", http.StatusSeeOther)
}

func (h *Handler) renderTagging(w http.ResponseWriter, r *http.Request, data templates.TaggingPageData) {
	content := templates.TaggingRulesPage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Tagging rules", nav, content))
}
