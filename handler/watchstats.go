package handler

import (
	"context"
	"net/http"
	"time"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const watchStatsReads = 4

func (h *Handler) WatchStatsPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+watchStatsReads*playbackMonitorReadTimeout+time.Second)
	defer cancel()

	data := templates.WatchStatsPageData{}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, playbackMonitorDialTimeout)
	client, closer, err := h.withPlaybackMonitorClient(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderWatchStats(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	hist, histErr := client.ListHistory(readCtx, &monitorv1.ListHistoryRequest{Limit: 20})
	readCancel()
	if histErr == nil {
		for _, s := range hist.GetSessions() {
			data.RecentPlays = append(data.RecentPlays, sessionRowFromProto(s))
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	top7, top7Err := client.ListTopContent(readCtx, &monitorv1.ListTopContentRequest{Days: 7, Limit: 10})
	readCancel()
	if top7Err == nil {
		for _, row := range top7.GetItems() {
			data.TopTitles7d = append(data.TopTitles7d, templates.WatchStatsTitleRow{
				Title:        row.GetTitle(),
				MediaType:    row.GetMediaType(),
				PlayCount:    int(row.GetPlayCount()),
				WatchMinutes: formatStatValue("watch_minutes", row.GetWatchMinutes()),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	top30, top30Err := client.ListTopContent(readCtx, &monitorv1.ListTopContentRequest{Days: 30, Limit: 10})
	readCancel()
	if top30Err == nil {
		for _, row := range top30.GetItems() {
			data.TopTitles30d = append(data.TopTitles30d, templates.WatchStatsTitleRow{
				Title:        row.GetTitle(),
				MediaType:    row.GetMediaType(),
				PlayCount:    int(row.GetPlayCount()),
				WatchMinutes: formatStatValue("watch_minutes", row.GetWatchMinutes()),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	users, usersErr := client.ListUserWatchStats(readCtx, &monitorv1.ListUserWatchStatsRequest{Days: 30, Limit: 50})
	readCancel()
	if usersErr == nil {
		for _, u := range users.GetUsers() {
			data.UserTotals = append(data.UserTotals, templates.WatchStatsUserRow{
				Username:     u.GetUsername(),
				PlayCount:    int(u.GetPlayCount()),
				WatchMinutes: formatStatValue("watch_minutes", u.GetWatchMinutes()),
			})
		}
	}

	h.renderWatchStats(w, r, data)
}

func (h *Handler) renderWatchStats(w http.ResponseWriter, r *http.Request, data templates.WatchStatsPageData) {
	content := templates.WatchStatsPage(data)
	nav := h.nav("/watchstats")
	h.render(w, r, templates.Layout("Watch Stats", nav, content))
}
