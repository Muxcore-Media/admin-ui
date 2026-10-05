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

const (
	wantedDialTimeout = 3 * time.Second
	wantedReadTimeout = 5 * time.Second
	// Covers discovery dial plus two media.library modules (info + ListMissing each).
	wantedPageTimeout = wantedDialTimeout + 2*(wantedDialTimeout+2*wantedReadTimeout) + time.Second
)

func (h *Handler) UnifiedWantedPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), wantedPageTimeout)
	defer cancel()

	kindFilter := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kindFilter != "movie" && kindFilter != "tv" {
		kindFilter = "all"
	}

	data := templates.UnifiedWantedData{
		KindFilter: kindFilter,
	}

	if h.Core == nil {
		data.SoftNote = true
		h.renderUnifiedWanted(w, r, data)
		return
	}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, wantedDialTimeout)
	mods := h.mediaLibraryModules(dialCtx)
	dialCancel()
	if len(mods) == 0 {
		data.SoftNote = true
		data.Error = "no media.library modules registered"
		h.renderUnifiedWanted(w, r, data)
		return
	}

	items, summary, errs := h.fetchUnifiedMissing(pageCtx, mods, kindFilter)
	data.Items = items
	data.Summary = summary
	if len(errs) > 0 && len(items) == 0 {
		data.Error = strings.Join(errs, "; ")
	} else if len(errs) > 0 {
		data.Warning = strings.Join(errs, "; ")
	}
	if len(items) == 0 && len(errs) == 0 {
		data.Empty = true
	}
	h.renderUnifiedWanted(w, r, data)
}

func (h *Handler) fetchUnifiedMissing(ctx context.Context, mods []*discoveryv1.ModuleInfoProto, kindFilter string) ([]templates.UnifiedWantedItem, templates.UnifiedWantedSummary, []string) {
	var items []templates.UnifiedWantedItem
	var errs []string
	summary := templates.UnifiedWantedSummary{Libraries: len(mods)}

	for _, mod := range mods {
		moduleID := mod.GetId()
		kind := automationItemType(moduleID, mod.GetName())
		if kindFilter != "all" && kind != kindFilter {
			continue
		}
		addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
		if addr == "" {
			continue
		}
		conn, client, err := h.dialMediaModule(addr)
		if err != nil {
			errs = append(errs, moduleID+": "+err.Error())
			continue
		}

		readCtx, readCancel := context.WithTimeout(ctx, wantedReadTimeout)
		info, err := client.GetMediaTypeInfo(readCtx, &mediaadminv1.GetMediaTypeInfoRequest{})
		readCancel()
		if err != nil {
			_ = conn.Close()
			errs = append(errs, moduleID+": "+err.Error())
			continue
		}
		if !mediaFeatureEnabled(info.GetFeatures(), "missing") {
			_ = conn.Close()
			continue
		}

		readCtx, readCancel = context.WithTimeout(ctx, wantedReadTimeout)
		resp, err := client.ListMissing(readCtx, &mediaadminv1.ListMissingRequest{
			Page: 1, PageSize: 100,
		})
		readCancel()
		_ = conn.Close()
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unimplemented") ||
				strings.Contains(strings.ToLower(err.Error()), "not supported") {
				slog.Debug("wanted: ListMissing unimplemented", "module", moduleID)
				continue
			}
			errs = append(errs, moduleID+": "+err.Error())
			continue
		}

		display := mod.GetName()
		if info.GetDisplayName() != "" {
			display = info.GetDisplayName()
		}
		if display == "" {
			display = moduleID
		}

		for _, it := range resp.GetItems() {
			row := templates.UnifiedWantedItem{
				ModuleID:   moduleID,
				ModuleName: display,
				Kind:       kind,
				ID:         it.GetId(),
				ParentID:   it.GetParentId(),
				Title:      it.GetTitle(),
				Year:       int(it.GetYear()),
				Season:     it.GetMetadata()["season_number"],
				Episode:    it.GetMetadata()["episode_number"],
				AirDate:    it.GetMetadata()["air_date"],
			}
			items = append(items, row)
			summary.Total++
			switch kind {
			case "movie":
				summary.Movies++
			case "tv":
				summary.Episodes++
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].ModuleName != items[j].ModuleName {
			return items[i].ModuleName < items[j].ModuleName
		}
		if items[i].Title != items[j].Title {
			return items[i].Title < items[j].Title
		}
		if items[i].Season != items[j].Season {
			return items[i].Season < items[j].Season
		}
		return items[i].Episode < items[j].Episode
	})
	return items, summary, errs
}

func (h *Handler) renderUnifiedWanted(w http.ResponseWriter, r *http.Request, data templates.UnifiedWantedData) {
	content := templates.UnifiedWantedPage(data)
	h.render(w, r, templates.Layout("Wanted / Missing", h.nav(r.URL.Path), content))
}
