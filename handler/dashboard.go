package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	if !h.coreConnected {
		nav := h.nav(r.URL.Path)
		content := templates.DashboardPage(templates.DashboardData{Disconnected: true})
		component := templates.Layout("Dashboard", nav, content)
		h.render(w, r, component)
		return
	}

	members, leaderID, err := h.Core.Discovery.Members(r.Context())
	if err != nil {
		slog.Warn("dashboard: Members call failed", "error", err)
		nav := h.nav(r.URL.Path)
		content := templates.DashboardPage(templates.DashboardData{Disconnected: true})
		component := templates.Layout("Dashboard", nav, content)
		h.render(w, r, component)
		return
	}

	data := templates.DashboardData{
		NodeCount:    len(members),
		LeaderID:     leaderID,
		SessionCount: 0,
	}
	if h.Sessions != nil {
		data.SessionCount = len(h.Sessions.List())
	}
	h.refreshMediaNavLinks(r.Context())
	h.mediaMu.RLock()
	data.LibraryCount = len(h.mediaModules)
	h.mediaMu.RUnlock()

	for _, m := range members {
		if m.GetId() == leaderID {
			data.LeaderHTTPAddr = m.GetHttpAddr()
			data.LeaderGRPCAddr = m.GetGrpcAddr()
			data.LeaderModuleCount = len(m.GetModules())
		}
	}

	content := templates.DashboardPage(data)
	nav := h.nav(r.URL.Path)
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

func (h *Handler) MonitorSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache")

	base := strings.TrimRight(h.HealthMonitorURL, "/")
	if base == "" {
		w.Write([]byte(`<div class="rounded-xl border border-gray-800 bg-gray-900/50 p-4 text-sm text-gray-500" data-testid="monitor-unconfigured">Health monitor URL not configured (ADMIN_UI_HEALTH_MONITOR_URL).</div>`))
		return
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + "/status")
	if err != nil {
		slog.Warn("monitor: fetch /status failed", "url", base+"/status", "error", err)
		w.Write([]byte(`<div class="rounded-xl border border-red-900/50 bg-red-950/30 p-4 text-sm text-red-400">Health monitor unreachable</div>`))
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		w.Write([]byte(`<div class="rounded-xl border border-red-900/50 bg-red-950/30 p-4 text-sm text-red-400">Health monitor HTTP ` + resp.Status + `</div>`))
		return
	}

	var st struct {
		Status        string `json:"status"`
		ModuleCount   int    `json:"module_count"`
		StaleCount    int64  `json:"stale_count"`
		DegradedCount int64  `json:"degraded_transitions"`
		EventsPublish int64  `json:"events_published"`
		Modules       []struct {
			ModuleID string `json:"module_id"`
			State    string `json:"state"`
			Stale    bool   `json:"stale"`
			Error    string `json:"error"`
		} `json:"modules"`
		RecentEvents []struct {
			EventType string `json:"event_type"`
			ModuleID  string `json:"module_id"`
			Message   string `json:"message"`
		} `json:"recent_events"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		w.Write([]byte(`<div class="rounded-xl border border-red-900/50 bg-red-950/30 p-4 text-sm text-red-400">Invalid health-monitor status JSON</div>`))
		return
	}

	data := templates.MonitorSummaryData{
		Status:        st.Status,
		ModuleCount:   st.ModuleCount,
		StaleCount:    st.StaleCount,
		DegradedCount: st.DegradedCount,
		EventsPublish: st.EventsPublish,
	}
	for _, m := range st.Modules {
		data.Modules = append(data.Modules, templates.MonitorModuleItem{
			ID:    m.ModuleID,
			State: m.State,
			Stale: m.Stale,
			Error: m.Error,
		})
	}
	for i, ev := range st.RecentEvents {
		if i >= 5 {
			break
		}
		data.Recent = append(data.Recent, templates.MonitorEventItem{
			Type:     ev.EventType,
			ModuleID: ev.ModuleID,
			Message:  ev.Message,
		})
	}
	h.render(w, r, templates.MonitorSummary(data))
}
