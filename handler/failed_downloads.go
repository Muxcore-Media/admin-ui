package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

// FailedDownloadsPage lists recent failed or stuck grabs from media.automation.
func (h *Handler) FailedDownloadsPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), automationDialTimeout+automationReadTimeout+time.Second)
	defer cancel()

	data := templates.FailedDownloadsPageData{
		Flash: r.URL.Query().Get("status"),
		Error: r.URL.Query().Get("error"),
	}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, automationDialTimeout)
	client, closer, err := h.withAutomationClient(dialCtx)
	dialCancel()
	if err != nil {
		slog.Warn("failed-downloads: resolve/dial failed", "error", err)
		if h.Core == nil {
			data.SoftNote = true
		} else {
			data.Error = automationResolveErr(err)
		}
		h.renderFailedDownloads(w, r, data)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, automationReadTimeout)
	hist, err := client.GetHistory(readCtx, &automationv1.GetHistoryRequest{Page: 1, PageSize: 100})
	readCancel()
	if err != nil {
		slog.Warn("failed-downloads: GetHistory failed", "error", err)
		data.Error = "history unavailable: " + err.Error()
		h.renderFailedDownloads(w, r, data)
		return
	}

	for _, rec := range hist.GetRecords() {
		st := rec.GetStatus()
		stuck := st == "import_failed" || st == "stalled"
		warn := stuck || st == "failed"
		if !warn {
			continue
		}
		src := rec.GetIndexer()
		if src == "" {
			src = rec.GetDownloadProtocol()
		}
		data.Rows = append(data.Rows, templates.FailedDownloadRow{
			ID:           rec.GetId(),
			WantedItemID: rec.GetWantedItemId(),
			GUID:         rec.GetGuid(),
			Title:        rec.GetTitle(),
			Status:       st,
			StatusLabel:  rec.GetStatusLabel(),
			StatusDetail: rec.GetStatusDetail(),
			Source:       src,
			At:           rec.GetCreatedAt(),
			Stuck:        stuck,
			Warning:      warn,
		})
	}

	h.renderFailedDownloads(w, r, data)
}

func (h *Handler) renderFailedDownloads(w http.ResponseWriter, r *http.Request, data templates.FailedDownloadsPageData) {
	content := templates.FailedDownloadsPage(data)
	h.render(w, r, templates.Layout("Failed downloads", h.nav(r.URL.Path), content))
}

// FailedDownloadsRetry retries a stuck import via the automation service.
// Form fields: history_id
func (h *Handler) FailedDownloadsRetry(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/failed-downloads", http.StatusSeeOther)
		return
	}
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/failed-downloads?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.RetryImport(ctx, &automationv1.RetryImportRequest{
		HistoryId: r.FormValue("history_id"),
	})
	if err != nil {
		http.Redirect(w, r, "/failed-downloads?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := resp.GetMessage()
	if msg == "" {
		msg = "retry_ok"
	}
	http.Redirect(w, r, "/failed-downloads?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

// FailedDownloadsDismiss blocklists a failed release via the automation service.
// Form fields: wanted_item_id, guid
func (h *Handler) FailedDownloadsDismiss(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), automationReadTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/failed-downloads", http.StatusSeeOther)
		return
	}
	wanted := r.FormValue("wanted_item_id")
	guid := r.FormValue("guid")
	if wanted == "" || guid == "" {
		http.Redirect(w, r, "/failed-downloads?error="+url.QueryEscape("wanted_item_id and guid required"), http.StatusSeeOther)
		return
	}
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/failed-downloads?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.BlocklistRelease(ctx, &automationv1.BlocklistReleaseRequest{
		WantedItemId: wanted,
		Guid:         guid,
		Reason:       "operator-dismiss",
	}); err != nil {
		http.Redirect(w, r, "/failed-downloads?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/failed-downloads?status=dismissed", http.StatusSeeOther)
}
