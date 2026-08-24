package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
)

func (h *Handler) playbackMonitorHTTPBase(ctx context.Context) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capPlaybackMonitor)
	if err != nil {
		return "", err
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("no module with capability %s", capPlaybackMonitor)
	}
	addr := normalizeDialAddr(mods[0].GetId(), mods[0].GetHttpAddr())
	if addr == "" {
		return "", fmt.Errorf("playback-monitor has no HTTP address")
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/"), nil
}

func (h *Handler) StreamsLiveEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	base, err := h.playbackMonitorHTTPBase(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	target, err := url.Parse(base)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.URL.Path = "/events/streams"
		req.URL.RawQuery = ""
	}
	proxy.ServeHTTP(w, r)
}

func (h *Handler) StreamsActiveJSON(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+playbackMonitorReadTimeout+time.Second)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, playbackMonitorDialTimeout)
	client, closer, err := h.withPlaybackMonitorClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	resp, err := client.ListActiveSessions(readCtx, &monitorv1.ListActiveSessionsRequest{Limit: 100})
	readCancel()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	rows := make([]templatesStreamSessionJSON, 0, len(resp.GetSessions()))
	for _, s := range resp.GetSessions() {
		rows = append(rows, sessionRowJSONFromProto(s))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessions": rows,
		"count":    len(rows),
	})
}

type templatesStreamSessionJSON struct {
	User     string `json:"user"`
	Title    string `json:"title"`
	State    string `json:"state"`
	Position string `json:"position"`
	Platform string `json:"platform"`
	Player   string `json:"player"`
	IP       string `json:"ip"`
}

func sessionRowJSONFromProto(s *monitorv1.SessionRecord) templatesStreamSessionJSON {
	row := sessionRowFromProto(s)
	return templatesStreamSessionJSON{
		User:     row.User,
		Title:    row.Title,
		State:    row.State,
		Position: row.Position,
		Platform: row.Platform,
		Player:   row.Player,
		IP:       row.IP,
	}
}
