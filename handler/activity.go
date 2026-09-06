package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

type activityRow struct {
	ModuleID   string
	ModuleName string
	Record     *mediaadminv1.HistoryRecord
}

func (h *Handler) ActivityPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, pageCancel := context.WithTimeout(r.Context(), mediaPageTimeout)
	defer pageCancel()

	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	pageSize := 50
	eventType := r.URL.Query().Get("event_type")

	data := templates.ActivityPageData{
		EventType: eventType,
		Page:      page,
		Flash:     r.URL.Query().Get("status"),
		Error:     r.URL.Query().Get("error"),
	}

	// Fetch failed imports from the automation service.
	data.FailedImports = h.activityFailedImports(pageCtx)

	// Fetch per-module media library history.
	h.mediaMu.RLock()
	mods := copyMediaModules(h.mediaModules)
	h.mediaMu.RUnlock()

	if len(mods) == 0 {
		h.refreshMediaNavLinks(pageCtx)
		h.mediaMu.RLock()
		mods = copyMediaModules(h.mediaModules)
		h.mediaMu.RUnlock()
	}

	var rows []activityRow
	dialed := 0
	for _, mod := range mods {
		addr := mod.HTTPAddr
		if addr == "" {
			continue
		}
		conn, client, err := h.dialMediaModule(addr)
		if err != nil {
			slog.Warn("activity: dial failed", "module", mod.ID, "error", err)
			continue
		}
		dialed++
		readCtx, readCancel := context.WithTimeout(pageCtx, mediaReadTimeout)
		resp, err := client.ListHistory(readCtx, &mediaadminv1.ListHistoryRequest{
			Page:      1,
			PageSize:  200,
			EventType: eventType,
		})
		readCancel()
		_ = conn.Close()
		if err != nil {
			slog.Warn("activity: ListHistory failed", "module", mod.ID, "error", err)
			continue
		}
		for _, rec := range resp.GetRecords() {
			rows = append(rows, activityRow{
				ModuleID:   mod.ID,
				ModuleName: mod.Name,
				Record:     rec,
			})
		}
	}

	// Upstream-down: modules were discovered but none could be dialed.
	if len(mods) > 0 && dialed == 0 {
		data.UpstreamDown = true
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Record.GetCreatedAt() > rows[j].Record.GetCreatedAt()
	})

	total := len(rows)
	totalPages := 1
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	items := make([]templates.ActivityEntry, 0, end-start)
	for _, row := range rows[start:end] {
		items = append(items, templates.ActivityEntry{
			ID:          row.Record.GetId(),
			EventType:   row.Record.GetEventType(),
			ItemID:      row.Record.GetItemId(),
			Title:       row.Record.GetTitle(),
			SourceTitle: row.Record.GetSourceTitle(),
			Quality:     row.Record.GetQuality(),
			Indexer:     row.Record.GetIndexer(),
			FilePath:    row.Record.GetFilePath(),
			CreatedAt:   row.Record.GetCreatedAt(),
			ModuleID:    row.ModuleID,
			ModuleName:  row.ModuleName,
		})
	}

	data.Entries = items
	data.Page = page
	data.TotalPages = totalPages
	data.Total = total

	content := templates.ActivityPage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Activity", nav, content)
	h.render(w, r, component)
}

// activityFailedImports fetches stuck/warning records from the automation
// service and maps them to FailedImportEntry.  It returns nil (not an error)
// when the automation module is unavailable — the UI shows a soft empty state.
func (h *Handler) activityFailedImports(ctx context.Context) []templates.FailedImportEntry {
	dialCtx, dialCancel := context.WithTimeout(ctx, automationDialTimeout)
	client, closer, err := h.withAutomationClient(dialCtx)
	dialCancel()
	if err != nil {
		slog.Debug("activity: automation dial failed, omitting failed imports", "error", err)
		return nil
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(ctx, automationReadTimeout)
	hist, err := client.GetHistory(readCtx, &automationv1.GetHistoryRequest{Page: 1, PageSize: 50})
	readCancel()
	if err != nil {
		slog.Warn("activity: GetHistory failed", "error", err)
		return nil
	}

	var out []templates.FailedImportEntry
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
		out = append(out, templates.FailedImportEntry{
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
	return out
}

// ActivityRetry retries a stuck import via the automation service.
// Form fields: history_id
func (h *Handler) ActivityRetry(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*automationReadTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/activity", http.StatusSeeOther)
		return
	}
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/activity?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.RetryImport(ctx, &automationv1.RetryImportRequest{
		HistoryId: r.FormValue("history_id"),
	})
	if err != nil {
		http.Redirect(w, r, "/activity?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := resp.GetMessage()
	if msg == "" {
		msg = "retry_ok"
	}
	http.Redirect(w, r, "/activity?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

// ActivityDismiss blocklists a failed release via the automation service,
// effectively dismissing it from the triage panel.
// Form fields: wanted_item_id, guid
func (h *Handler) ActivityDismiss(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), automationReadTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/activity", http.StatusSeeOther)
		return
	}
	wanted := r.FormValue("wanted_item_id")
	guid := r.FormValue("guid")
	if wanted == "" || guid == "" {
		http.Redirect(w, r, "/activity?error="+url.QueryEscape("wanted_item_id and guid required"), http.StatusSeeOther)
		return
	}
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/activity?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.BlocklistRelease(ctx, &automationv1.BlocklistReleaseRequest{
		WantedItemId: wanted,
		Guid:         guid,
		Reason:       "operator-dismiss",
	}); err != nil {
		http.Redirect(w, r, "/activity?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/activity?status=dismissed", http.StatusSeeOther)
}

type discoveryModuleCopy struct {
	ID       string
	Name     string
	HTTPAddr string
}

func copyMediaModules(mods []*discoveryv1.ModuleInfoProto) []discoveryModuleCopy {
	out := make([]discoveryModuleCopy, 0, len(mods))
	for _, m := range mods {
		if m == nil {
			continue
		}
		out = append(out, discoveryModuleCopy{
			ID:       m.GetId(),
			Name:     m.GetName(),
			HTTPAddr: m.GetHttpAddr(),
		})
	}
	return out
}
