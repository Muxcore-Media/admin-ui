package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	automationv1 "github.com/Muxcore-Media/media-automation/proto/automationv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

// UnifiedQueuePage is the day-2 ops view: wanted queue + history with stuck/warning actions.
func (h *Handler) UnifiedQueuePage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), automationDialTimeout+2*automationReadTimeout+time.Second)
	defer cancel()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	filter := r.URL.Query().Get("filter")

	data := templates.UnifiedQueueData{
		Filter:      filter,
		Page:        page,
		Flash:       r.URL.Query().Get("status"),
		Error:       r.URL.Query().Get("error"),
	}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, automationDialTimeout)
	client, closer, err := h.withAutomationClient(dialCtx)
	dialCancel()
	if err != nil {
		slog.Warn("queue: resolve/dial failed", "error", err)
		data.Error = err.Error()
		h.renderUnifiedQueue(w, r, data)
		return
	}
	defer closer()

	qCtx, qCancel := context.WithTimeout(pageCtx, automationReadTimeout)
	q, err := client.GetQueue(qCtx, &automationv1.GetQueueRequest{
		Page: int32(page), PageSize: 50, Filter: filter,
	})
	qCancel()
	if err != nil {
		slog.Warn("queue: GetQueue failed", "error", err)
		data.Error = "queue unavailable: " + err.Error()
	} else {
		data.Total = int(q.GetTotal())
		data.PageSize = int(q.GetPageSize())
		if data.PageSize < 1 {
			data.PageSize = 50
		}
		data.TotalPages = 1
		if data.Total > 0 {
			data.TotalPages = (data.Total + data.PageSize - 1) / data.PageSize
		}
		for _, it := range q.GetItems() {
			data.Wanted = append(data.Wanted, templates.UnifiedQueueWanted{
				ID: it.GetId(), ItemID: it.GetItemId(), ItemType: it.GetItemType(),
				Title: it.GetTitle(), Year: int(it.GetYear()), TMDBID: int(it.GetTmdbId()),
				Monitored: it.GetMonitored(), Missing: it.GetMissing(), UpdatedAt: it.GetUpdatedAt(),
			})
		}
	}

	hCtx, hCancel := context.WithTimeout(pageCtx, automationReadTimeout)
	hist, err := client.GetHistory(hCtx, &automationv1.GetHistoryRequest{Page: 1, PageSize: 50})
	hCancel()
	if err != nil {
		slog.Warn("queue: GetHistory failed", "error", err)
		if data.Error == "" {
			data.Error = "history unavailable: " + err.Error()
		}
	} else {
		for _, rec := range hist.GetRecords() {
			st := rec.GetStatus()
			warn := st == "import_failed" || st == "stalled" || st == "failed"
			src := rec.GetIndexer()
			if src == "" {
				src = rec.GetDownloadProtocol()
			}
			data.History = append(data.History, templates.UnifiedQueueHistory{
				ID: rec.GetId(), WantedItemID: rec.GetWantedItemId(), GUID: rec.GetGuid(),
				Status: st, Title: rec.GetTitle(), Source: src, At: rec.GetCreatedAt(),
				DownloadID: rec.GetDownloadId(), Warning: warn, Stuck: st == "import_failed" || st == "stalled",
			})
		}
	}

	h.renderUnifiedQueue(w, r, data)
}

func (h *Handler) renderUnifiedQueue(w http.ResponseWriter, r *http.Request, data templates.UnifiedQueueData) {
	content := templates.UnifiedQueuePage(data)
	h.render(w, r, templates.Layout("Queue", h.nav(r.URL.Path), content))
}

func (h *Handler) UnifiedQueueRemove(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), automationReadTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/queue", http.StatusSeeOther)
		return
	}
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/queue?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.RemoveFromQueue(ctx, &automationv1.RemoveFromQueueRequest{QueueId: r.FormValue("queue_id")}); err != nil {
		http.Redirect(w, r, "/queue?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/queue?status=removed", http.StatusSeeOther)
}

func (h *Handler) UnifiedQueueRetryImport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/queue", http.StatusSeeOther)
		return
	}
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/queue?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.RetryImport(ctx, &automationv1.RetryImportRequest{HistoryId: r.FormValue("history_id")})
	if err != nil {
		http.Redirect(w, r, "/queue?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := resp.GetMessage()
	if msg == "" {
		msg = "retry_ok"
	}
	http.Redirect(w, r, "/queue?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) UnifiedQueueBlocklist(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), automationReadTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/queue", http.StatusSeeOther)
		return
	}
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/queue?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	wanted := r.FormValue("wanted_item_id")
	guid := r.FormValue("guid")
	if wanted == "" || guid == "" {
		http.Redirect(w, r, "/queue?error="+url.QueryEscape("wanted_item_id and guid required"), http.StatusSeeOther)
		return
	}
	reason := r.FormValue("reason")
	if reason == "" {
		reason = "operator"
	}
	if _, err := client.BlocklistRelease(ctx, &automationv1.BlocklistReleaseRequest{
		WantedItemId: wanted, Guid: guid, Reason: reason,
	}); err != nil {
		http.Redirect(w, r, "/queue?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/queue?status=blocklisted", http.StatusSeeOther)
}
