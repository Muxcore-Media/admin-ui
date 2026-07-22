package handler

import (
	"log/slog"
	"net/http"
	"sort"
	"strconv"

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
	ctx := r.Context()
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	pageSize := 50
	eventType := r.URL.Query().Get("event_type")

	h.mediaMu.RLock()
	mods := copyMediaModules(h.mediaModules)
	h.mediaMu.RUnlock()

	if len(mods) == 0 {
		h.refreshMediaNavLinks(ctx)
		h.mediaMu.RLock()
		mods = copyMediaModules(h.mediaModules)
		h.mediaMu.RUnlock()
	}

	var rows []activityRow
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
		resp, err := client.ListHistory(ctx, &mediaadminv1.ListHistoryRequest{
			Page:      1,
			PageSize:  200,
			EventType: eventType,
		})
		conn.Close()
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

	content := templates.ActivityPage(templates.ActivityPageData{
		Entries:    items,
		EventType:  eventType,
		Page:       page,
		TotalPages: totalPages,
		Total:      total,
	})
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Activity", nav, content)
	h.render(w, r, component)
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
