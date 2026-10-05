package handler

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Muxcore-Media/admin-ui/internal/meshdial"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capPlaybackMonitor = "playback.monitor"

	playbackMonitorDialTimeout = 3 * time.Second
	playbackMonitorReadTimeout = 5 * time.Second
)

func (h *Handler) playbackMonitorAddr(ctx context.Context) (string, error) {
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
		return "", fmt.Errorf("playback-monitor has no dial address")
	}
	return addr, nil
}

func (h *Handler) withPlaybackMonitorClient(ctx context.Context) (monitorv1.PlaybackMonitorServiceClient, func(), error) {
	addr, err := h.playbackMonitorAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := meshdial.NewClient(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return monitorv1.NewPlaybackMonitorServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (h *Handler) StreamsPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+3*playbackMonitorReadTimeout+time.Second)
	defer cancel()

	data := templates.StreamsPageData{}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, playbackMonitorDialTimeout)
	client, closer, err := h.withPlaybackMonitorClient(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderStreams(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	stats, statsErr := client.GetHomeStats(readCtx, &monitorv1.GetHomeStatsRequest{Days: 30})
	readCancel()
	if statsErr == nil {
		for _, s := range stats.GetStats() {
			data.Stats = append(data.Stats, templates.StreamStat{
				Key:   s.GetKey(),
				Label: s.GetLabel(),
				Value: formatStatValue(s.GetKey(), s.GetValue()),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	active, activeErr := client.ListActiveSessions(readCtx, &monitorv1.ListActiveSessionsRequest{Limit: 50})
	readCancel()
	if activeErr == nil {
		for _, s := range active.GetSessions() {
			data.Active = append(data.Active, sessionRowFromProto(s))
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	hist, histErr := client.ListHistory(readCtx, &monitorv1.ListHistoryRequest{Limit: 15})
	readCancel()
	if histErr == nil {
		data.HistoryTotal = int(hist.GetTotal())
		for _, s := range hist.GetSessions() {
			data.History = append(data.History, sessionRowFromProto(s))
		}
	}

	h.renderStreams(w, r, data)
}

func (h *Handler) StreamsHistoryPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+playbackMonitorReadTimeout)
	defer cancel()

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 100
	data := templates.StreamsHistoryPageData{Query: q}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, playbackMonitorDialTimeout)
	client, closer, err := h.withPlaybackMonitorClient(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderStreamsHistory(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	resp, err := client.ListHistory(readCtx, &monitorv1.ListHistoryRequest{
		Query:  q,
		Limit:  int32(limit),
		Offset: 0,
	})
	readCancel()
	if err != nil {
		data.Error = err.Error()
		h.renderStreamsHistory(w, r, data)
		return
	}
	data.Total = int(resp.GetTotal())
	for _, s := range resp.GetSessions() {
		data.Sessions = append(data.Sessions, sessionRowFromProto(s))
	}
	h.renderStreamsHistory(w, r, data)
}

func (h *Handler) StreamsStatsPage(w http.ResponseWriter, r *http.Request) {
	const statsReads = 10
	pageCtx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+statsReads*playbackMonitorReadTimeout+time.Second)
	defer cancel()

	data := templates.StreamsStatsPageData{}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, playbackMonitorDialTimeout)
	client, closer, err := h.withPlaybackMonitorClient(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderStreamsStats(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	resp, err := client.GetStreamAnalytics(readCtx, &monitorv1.GetStreamAnalyticsRequest{Days: 30})
	readCancel()
	if err != nil {
		data.Error = err.Error()
		h.renderStreamsStats(w, r, data)
		return
	}
	for _, row := range resp.GetPlatforms() {
		data.Platforms = append(data.Platforms, templates.StreamBreakdownRow{
			Label: row.GetLabel(),
			Count: int(row.GetCount()),
		})
	}
	for _, row := range resp.GetTranscodes() {
		data.Transcodes = append(data.Transcodes, templates.StreamBreakdownRow{
			Label: row.GetLabel(),
			Count: int(row.GetCount()),
		})
	}
	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	plays, playsErr := client.GetPlaysByDate(readCtx, &monitorv1.GetPlaysByDateRequest{Days: 30})
	readCancel()
	if playsErr == nil {
		for _, row := range plays.GetRows() {
			data.PlaysByDate = append(data.PlaysByDate, templates.StreamBreakdownRow{
				Label: row.GetDate(),
				Count: int(row.GetCount()),
			})
		}
	}
	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	hours, hoursErr := client.GetPlaysByHour(readCtx, &monitorv1.GetPlaysByHourRequest{Days: 30})
	readCancel()
	if hoursErr == nil {
		for _, row := range hours.GetRows() {
			data.PlaysByHour = append(data.PlaysByHour, templates.StreamBreakdownRow{
				Label: row.GetLabel(),
				Count: int(row.GetCount()),
			})
		}
	}
	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	dow, dowErr := client.GetPlaysByDayOfWeek(readCtx, &monitorv1.GetPlaysByDayOfWeekRequest{Days: 30})
	readCancel()
	if dowErr == nil {
		for _, row := range dow.GetRows() {
			data.PlaysByDayWeek = append(data.PlaysByDayWeek, templates.StreamBreakdownRow{
				Label: row.GetLabel(),
				Count: int(row.GetCount()),
			})
		}
	}
	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	months, monthsErr := client.GetPlaysByMonth(readCtx, &monitorv1.GetPlaysByMonthRequest{Days: 365})
	readCancel()
	if monthsErr == nil {
		for _, row := range months.GetRows() {
			data.PlaysByMonth = append(data.PlaysByMonth, templates.StreamBreakdownRow{
				Label: row.GetLabel(),
				Count: int(row.GetCount()),
			})
		}
	}
	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	topUsers, topUsersErr := client.GetPlaysByTopUsers(readCtx, &monitorv1.GetPlaysByTopUsersRequest{Days: 30, Limit: 10})
	readCancel()
	if topUsersErr == nil {
		for _, row := range topUsers.GetRows() {
			data.TopUsers = append(data.TopUsers, templates.StreamBreakdownRow{
				Label: row.GetLabel(),
				Count: int(row.GetCount()),
			})
		}
	}
	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	concurrent, concurrentErr := client.GetConcurrentStreams(readCtx, &monitorv1.GetConcurrentStreamsRequest{Days: 30})
	readCancel()
	if concurrentErr == nil {
		for _, row := range concurrent.GetRows() {
			data.Concurrent = append(data.Concurrent, templates.StreamConcurrentRow{
				Date:      row.GetDate(),
				Total:     int(row.GetTotal()),
				Direct:    int(row.GetDirect()),
				Transcode: int(row.GetTranscode()),
			})
		}
	}
	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	resolution, resolutionErr := client.GetPlaysByStreamResolution(readCtx, &monitorv1.GetPlaysByStreamResolutionRequest{Days: 30, Limit: 10})
	readCancel()
	if resolutionErr == nil {
		for _, row := range resolution.GetRows() {
			data.PlaysByResolution = append(data.PlaysByResolution, templates.StreamBreakdownRow{
				Label: row.GetLabel(),
				Count: int(row.GetCount()),
			})
		}
	}
	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	source, sourceErr := client.GetPlaysBySourceResolution(readCtx, &monitorv1.GetPlaysBySourceResolutionRequest{Days: 30, Limit: 10})
	readCancel()
	if sourceErr == nil {
		for _, row := range source.GetRows() {
			data.PlaysBySourceResolution = append(data.PlaysBySourceResolution, templates.StreamBreakdownRow{
				Label: row.GetLabel(),
				Count: int(row.GetCount()),
			})
		}
	}
	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	combo, comboErr := client.GetPlaysByPlatformResolution(readCtx, &monitorv1.GetPlaysByPlatformResolutionRequest{Days: 30, Limit: 15})
	readCancel()
	if comboErr == nil {
		for _, row := range combo.GetRows() {
			data.PlaysByPlatformResolution = append(data.PlaysByPlatformResolution, templates.StreamBreakdownRow{
				Label: row.GetLabel(),
				Count: int(row.GetCount()),
			})
		}
	}
	h.renderStreamsStats(w, r, data)
}

func (h *Handler) StreamsUsersPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), playbackMonitorDialTimeout+playbackMonitorReadTimeout)
	defer cancel()

	data := templates.StreamsUsersPageData{}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, playbackMonitorDialTimeout)
	client, closer, err := h.withPlaybackMonitorClient(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderStreamsUsers(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	resp, err := client.ListUserWatchStats(readCtx, &monitorv1.ListUserWatchStatsRequest{Days: 30, Limit: 100})
	readCancel()
	if err != nil {
		data.Error = err.Error()
		h.renderStreamsUsers(w, r, data)
		return
	}
	for _, u := range resp.GetUsers() {
		data.Users = append(data.Users, templates.StreamUserRow{
			Username:     u.GetUsername(),
			PlayCount:    int(u.GetPlayCount()),
			WatchMinutes: formatStatValue("watch_minutes", u.GetWatchMinutes()),
		})
	}
	h.renderStreamsUsers(w, r, data)
}

func sessionRowFromProto(s *monitorv1.SessionRecord) templates.StreamSessionRow {
	if s == nil {
		return templates.StreamSessionRow{}
	}
	user := s.GetUserName()
	if user == "" {
		user = s.GetUserId()
	}
	started := ""
	if s.GetStartedAtUnix() > 0 {
		started = time.Unix(s.GetStartedAtUnix(), 0).UTC().Format("2006-01-02 15:04")
	}
	pos := formatDurationSeconds(s.GetPositionSeconds())
	if s.GetDurationSeconds() > 0 {
		pos = pos + " / " + formatDurationSeconds(s.GetDurationSeconds())
	}
	return templates.StreamSessionRow{
		ID:       s.GetId(),
		User:     user,
		Title:    s.GetTitle(),
		State:    protoSessionStateLabel(s.GetState()),
		Position: pos,
		Platform: s.GetPlatform(),
		Player:   s.GetPlayer(),
		IP:       s.GetIpAddress(),
		Started:  started,
	}
}

func formatStatValue(key string, v float64) string {
	switch key {
	case "watch_minutes":
		if v >= 60 {
			return fmt.Sprintf("%.1f h", v/60)
		}
		return fmt.Sprintf("%d min", int(math.Round(v)))
	default:
		return strconv.Itoa(int(math.Round(v)))
	}
}

func formatDurationSeconds(sec int64) string {
	if sec <= 0 {
		return "0:00"
	}
	m := sec / 60
	s := sec % 60
	if m >= 60 {
		h := m / 60
		m = m % 60
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func protoSessionStateLabel(st monitorv1.SessionState) string {
	switch st {
	case monitorv1.SessionState_SESSION_STATE_PLAYING:
		return "playing"
	case monitorv1.SessionState_SESSION_STATE_PAUSED:
		return "paused"
	case monitorv1.SessionState_SESSION_STATE_STOPPED:
		return "stopped"
	default:
		return ""
	}
}

func (h *Handler) renderStreams(w http.ResponseWriter, r *http.Request, data templates.StreamsPageData) {
	content := templates.StreamsPage(data)
	nav := h.nav("/streams")
	h.render(w, r, templates.Layout("Streams", nav, content))
}

func (h *Handler) renderStreamsHistory(w http.ResponseWriter, r *http.Request, data templates.StreamsHistoryPageData) {
	content := templates.StreamsHistoryPage(data)
	nav := h.nav("/streams/history")
	h.render(w, r, templates.Layout("Watch history", nav, content))
}

func (h *Handler) renderStreamsStats(w http.ResponseWriter, r *http.Request, data templates.StreamsStatsPageData) {
	content := templates.StreamsStatsPage(data)
	nav := h.nav("/streams/stats")
	h.render(w, r, templates.Layout("Stream analytics", nav, content))
}

func (h *Handler) renderStreamsUsers(w http.ResponseWriter, r *http.Request, data templates.StreamsUsersPageData) {
	content := templates.StreamsUsersPage(data)
	nav := h.nav("/streams/users")
	h.render(w, r, templates.Layout("Watchers", nav, content))
}
