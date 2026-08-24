package handler

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	streamsLibrariesReads      = 6
	streamsLibrariesPageTimeout = playbackMonitorDialTimeout + streamsLibrariesReads*playbackMonitorReadTimeout + time.Second
)

func (h *Handler) StreamsLibrariesPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), streamsLibrariesPageTimeout)
	defer pageCancel()

	data := templates.StreamsLibrariesPageData{}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, playbackMonitorDialTimeout)
	client, closer, err := h.withPlaybackMonitorClient(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftNote = true
		if h.Core != nil {
			data.Error = err.Error()
		}
		h.renderStreamsLibraries(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	storageResp, storageErr := client.GetLibraryStorageSummary(readCtx, &monitorv1.GetLibraryStorageSummaryRequest{})
	readCancel()
	if storageErr == nil {
		data.StorageTotal = formatStorageBytes(storageResp.GetTotalBytes())
		data.StorageItems = int(storageResp.GetTotalItems())
		data.StorageDuplicateWaste = formatStorageBytes(storageResp.GetDuplicateWasteBytes())
		for _, row := range storageResp.GetLibraries() {
			data.StorageLibraries = append(data.StorageLibraries, templates.StreamLibraryStorageRow{
				LibraryName: row.GetLibraryName(),
				ServerID:    row.GetServerId(),
				ItemCount:   int(row.GetItemCount()),
				TotalBytes:  formatStorageBytes(row.GetTotalBytes()),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	histResp, histErr := client.GetLibraryStorageHistory(readCtx, &monitorv1.GetLibraryStorageHistoryRequest{Days: 30, PredictDays: 90})
	readCancel()
	if histErr == nil {
		data.StorageProjected = formatStorageBytes(histResp.GetPrediction().GetProjectedBytes())
		growth := histResp.GetPrediction().GetGrowthBytesPerDay()
		if growth > 0 {
			data.StorageGrowthPerDay = formatStorageBytes(int64(growth))
		}
		for _, row := range histResp.GetHistory() {
			data.StorageHistory = append(data.StorageHistory, templates.StreamStorageHistoryRow{
				Day:        row.GetDay(),
				ItemCount:  int(row.GetItemCount()),
				TotalBytes: formatStorageBytes(row.GetTotalBytes()),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	libResp, err := client.ListLibraryStats(readCtx, &monitorv1.ListLibraryStatsRequest{Days: 30, Limit: 50})
	readCancel()
	if err != nil {
		data.Error = err.Error()
		h.renderStreamsLibraries(w, r, data)
		return
	}
	for _, row := range libResp.GetLibraries() {
		data.Libraries = append(data.Libraries, templates.StreamLibraryRow{
			ServerID:     row.GetServerId(),
			LibraryName:  row.GetLibraryName(),
			PlayCount:    int(row.GetPlayCount()),
			WatchMinutes: formatWatchMinutes(row.GetWatchMinutes()),
		})
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	topResp, topErr := client.ListTopContent(readCtx, &monitorv1.ListTopContentRequest{Days: 30, Limit: 10})
	readCancel()
	if topErr == nil {
		for _, row := range topResp.GetMovies() {
			data.Movies = append(data.Movies, topContentRowFromProto(row))
		}
		for _, row := range topResp.GetShows() {
			data.Shows = append(data.Shows, topContentRowFromProto(row))
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	dupResp, dupErr := client.ListLibraryDuplicates(readCtx, &monitorv1.ListLibraryDuplicatesRequest{Limit: 15})
	readCancel()
	if dupErr == nil {
		for _, g := range dupResp.GetGroups() {
			servers := map[string]bool{}
			for _, c := range g.GetCopies() {
				servers[c.GetServerId()] = true
			}
			serverList := make([]string, 0, len(servers))
			for id := range servers {
				serverList = append(serverList, id)
			}
			sort.Strings(serverList)
			data.Duplicates = append(data.Duplicates, templates.StreamDuplicateGroupRow{
				Title:     g.GetTitle(),
				CopyCount: int(g.GetCopyCount()),
				Servers:   strings.Join(serverList, ", "),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, playbackMonitorReadTimeout)
	staleResp, staleErr := client.ListStaleLibraryItems(readCtx, &monitorv1.ListStaleLibraryItemsRequest{StaleDays: 90, Limit: 15})
	readCancel()
	if staleErr == nil {
		data.StaleNever = int(staleResp.GetNeverWatchedCount())
		data.StaleOld = int(staleResp.GetStaleCount())
		for _, item := range staleResp.GetItems() {
			data.Stale = append(data.Stale, templates.StreamStaleRow{
				Title:     item.GetTitle(),
				Category:  item.GetCategory(),
				DaysStale: int(item.GetDaysStale()),
				ServerID:  item.GetServerId(),
			})
		}
	}
	h.renderStreamsLibraries(w, r, data)
}

func topContentRowFromProto(row *monitorv1.TopContentRow) templates.StreamTopContentRow {
	if row == nil {
		return templates.StreamTopContentRow{}
	}
	return templates.StreamTopContentRow{
		Title:        row.GetTitle(),
		MediaType:    row.GetMediaType(),
		PlayCount:    int(row.GetPlayCount()),
		WatchMinutes: formatWatchMinutes(row.GetWatchMinutes()),
	}
}

func formatWatchMinutes(minutes float64) string {
	if minutes < 60 {
		return fmt.Sprintf("%.0fm", minutes)
	}
	h := int(minutes) / 60
	m := int(minutes) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

func formatStorageBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for cur := n / unit; cur >= unit; cur /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (h *Handler) renderStreamsLibraries(w http.ResponseWriter, r *http.Request, data templates.StreamsLibrariesPageData) {
	content := templates.StreamsLibrariesPage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Library analytics", nav, content))
}
