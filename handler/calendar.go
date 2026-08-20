package handler

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

// UnifiedCalendarPage aggregates GetCalendar across media.library modules (movies+TV).
func (h *Handler) UnifiedCalendarPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), automationDialTimeout+2*automationReadTimeout+time.Second)
	defer cancel()

	now := time.Now().UTC()
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	if start == "" {
		start = now.AddDate(0, 0, -7).Format("2006-01-02")
	}
	if end == "" {
		end = now.AddDate(0, 0, 21).Format("2006-01-02")
	}
	includeUnmon := r.URL.Query().Get("unmonitored") == "1"

	data := templates.UnifiedCalendarData{
		Start: start,
		End:   end,
	}

	mods := h.mediaLibraryModules(pageCtx)
	if len(mods) == 0 {
		data.Error = "no media.library modules registered"
		h.renderUnifiedCalendar(w, r, data)
		return
	}

	var items []templates.UnifiedCalendarItem
	var errs []string
	for _, mod := range mods {
		moduleID := mod.GetId()
		addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
		if addr == "" {
			continue
		}
		conn, client, err := h.dialMediaModule(addr)
		if err != nil {
			errs = append(errs, moduleID+": "+err.Error())
			continue
		}
		kind := automationItemType(moduleID, mod.GetName())
		cCtx, cCancel := context.WithTimeout(pageCtx, automationReadTimeout)
		resp, err := client.GetCalendar(cCtx, &mediaadminv1.GetCalendarRequest{
			StartDate: start, EndDate: end, IncludeUnmonitored: includeUnmon,
		})
		cCancel()
		_ = conn.Close()
		if err != nil {
			// Movies may return Unimplemented — soft-skip.
			if strings.Contains(strings.ToLower(err.Error()), "unimplemented") ||
				strings.Contains(strings.ToLower(err.Error()), "not supported") {
				slog.Debug("calendar: module unimplemented", "module", moduleID)
				continue
			}
			errs = append(errs, moduleID+": "+err.Error())
			continue
		}
		display := mod.GetName()
		if display == "" {
			display = moduleID
		}
		for _, it := range resp.GetItems() {
			items = append(items, templates.UnifiedCalendarItem{
				ModuleID:   moduleID,
				ModuleName: display,
				Kind:       kind,
				ID:         it.GetId(),
				ParentID:   it.GetParentId(),
				Title:      it.GetTitle(),
				Subtitle:   it.GetSubtitle(),
				Date:       it.GetDate(),
				Monitored:  it.GetMonitored(),
				HasFile:    it.GetHasFile(),
				Season:     it.GetMetadata()["season_number"],
				Episode:    it.GetMetadata()["episode_number"],
			})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Date == items[j].Date {
			return items[i].Title < items[j].Title
		}
		return items[i].Date < items[j].Date
	})
	data.Items = items
	if len(errs) > 0 && len(items) == 0 {
		data.Error = strings.Join(errs, "; ")
	} else if len(errs) > 0 {
		data.Warning = strings.Join(errs, "; ")
	}
	h.renderUnifiedCalendar(w, r, data)
}

func (h *Handler) mediaLibraryModules(ctx context.Context) []*discoveryv1.ModuleInfoProto {
	h.mediaMu.RLock()
	cached := append([]*discoveryv1.ModuleInfoProto(nil), h.mediaModules...)
	h.mediaMu.RUnlock()
	if len(cached) > 0 {
		return cached
	}
	if h.Core == nil {
		return nil
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaLibrary)
	if err != nil {
		slog.Warn("calendar: FindByCapability failed", "error", err)
		return nil
	}
	return mods
}

func (h *Handler) renderUnifiedCalendar(w http.ResponseWriter, r *http.Request, data templates.UnifiedCalendarData) {
	content := templates.UnifiedCalendarPage(data)
	h.render(w, r, templates.Layout("Calendar", h.nav(r.URL.Path), content))
}
