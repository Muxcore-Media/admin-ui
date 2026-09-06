package handler

import (
	"context"
	"net/http"
	"time"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const nowPlayingDashboardLimit = 5

func (h *Handler) NowPlayingPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+playbackMonitorReadTimeout+time.Second)
	defer cancel()

	data := templates.NowPlayingPageData{}
	active, softNote, errMsg := h.fetchActiveSessions(pageCtx, 50)
	data.Active = active
	data.SoftNote = softNote
	data.Error = errMsg

	h.renderNowPlaying(w, r, data)
}

func (h *Handler) NowPlayingDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("Cache-Control", "no-cache")

	pageCtx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+playbackMonitorReadTimeout+time.Second)
	defer cancel()

	active, softNote, errMsg := h.fetchActiveSessions(pageCtx, nowPlayingDashboardLimit)
	data := templates.NowPlayingDashboardData{
		SoftNote: softNote,
		Error:    errMsg,
		Active:   active,
		Count:    len(active),
	}
	h.render(w, r, templates.NowPlayingDashboardPanel(data))
}

func (h *Handler) fetchActiveSessions(ctx context.Context, limit int32) ([]templates.StreamSessionRow, bool, string) {
	dialCtx, dialCancel := context.WithTimeout(ctx, playbackMonitorDialTimeout)
	client, closer, err := h.withPlaybackMonitorClient(dialCtx)
	dialCancel()
	if err != nil {
		errMsg := ""
		if h.Core != nil {
			errMsg = err.Error()
		}
		return nil, true, errMsg
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(ctx, playbackMonitorReadTimeout)
	resp, err := client.ListActiveSessions(readCtx, &monitorv1.ListActiveSessionsRequest{Limit: limit})
	readCancel()
	if err != nil {
		return nil, false, err.Error()
	}

	rows := make([]templates.StreamSessionRow, 0, len(resp.GetSessions()))
	for _, s := range resp.GetSessions() {
		rows = append(rows, sessionRowFromProto(s))
	}
	return rows, false, ""
}

func (h *Handler) renderNowPlaying(w http.ResponseWriter, r *http.Request, data templates.NowPlayingPageData) {
	content := templates.NowPlayingPage(data)
	nav := h.nav("/now-playing")
	h.render(w, r, templates.Layout("Now Playing", nav, content))
}
