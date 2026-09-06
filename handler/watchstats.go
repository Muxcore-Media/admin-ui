package handler

import (
	"context"
	"net/http"
	"sort"
	"time"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	watchStatsReads    = 4
	watchStatsTopLimit = 10
)

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
	top7, top7Err := client.ListTopContent(readCtx, &monitorv1.ListTopContentRequest{Days: 7, Limit: watchStatsTopLimit})
	readCancel()
	if top7Err == nil {
		data.TopTitles7d = watchStatsTitleRowsFromTopContent(top7, watchStatsTopLimit)
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	top30, top30Err := client.ListTopContent(readCtx, &monitorv1.ListTopContentRequest{Days: 30, Limit: watchStatsTopLimit})
	readCancel()
	if top30Err == nil {
		data.TopTitles30d = watchStatsTitleRowsFromTopContent(top30, watchStatsTopLimit)
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

// watchStatsTitleRowsFromTopContent flattens ListTopContentResponse movies/shows/other
// into a single ranked table matching the Watch Stats Top Titles template.
func watchStatsTitleRowsFromTopContent(resp *monitorv1.ListTopContentResponse, limit int) []templates.WatchStatsTitleRow {
	if resp == nil {
		return nil
	}
	n := len(resp.GetMovies()) + len(resp.GetShows()) + len(resp.GetOther())
	if n == 0 {
		return nil
	}
	rows := make([]templates.WatchStatsTitleRow, 0, n)
	appendBucket := func(items []*monitorv1.TopContentRow, fallbackType string) {
		for _, row := range items {
			if row == nil {
				continue
			}
			mediaType := row.GetMediaType()
			if mediaType == "" {
				mediaType = fallbackType
			}
			rows = append(rows, templates.WatchStatsTitleRow{
				Title:        row.GetTitle(),
				MediaType:    mediaType,
				PlayCount:    int(row.GetPlayCount()),
				WatchMinutes: formatStatValue("watch_minutes", row.GetWatchMinutes()),
			})
		}
	}
	appendBucket(resp.GetMovies(), "movie")
	appendBucket(resp.GetShows(), "show")
	appendBucket(resp.GetOther(), "other")
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].PlayCount != rows[j].PlayCount {
			return rows[i].PlayCount > rows[j].PlayCount
		}
		if rows[i].Title != rows[j].Title {
			return rows[i].Title < rows[j].Title
		}
		return rows[i].MediaType < rows[j].MediaType
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}
