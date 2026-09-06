package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	mediaDialTimeout = 3 * time.Second
	mediaReadTimeout = 5 * time.Second
	mediaPageTimeout = mediaDialTimeout + 2*mediaReadTimeout + time.Second
)

func automationItemType(moduleID, displayName string) string {
	s := strings.ToLower(moduleID + " " + displayName)
	switch {
	case strings.Contains(s, "movie"):
		return "movie"
	case strings.Contains(s, "tv") || strings.Contains(s, "show"):
		return "tv"
	case strings.Contains(s, "music"):
		return "music"
	default:
		return "movie"
	}
}

func itemTMDBID(item *mediaadminv1.MediaItem) int32 {
	if item == nil {
		return 0
	}
	if v := item.GetMetadata()["tmdb_id"]; v != "" {
		id, _ := strconv.Atoi(v)
		return int32(id)
	}
	return 0
}

func (h *Handler) mediaModuleAddr(ctx context.Context, moduleID string) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mod, err := h.Core.Discovery.Resolve(ctx, moduleID)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", moduleID, err)
	}
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return "", fmt.Errorf("module %q has no HTTPAddr", moduleID)
	}
	return addr, nil
}

func (h *Handler) dialMediaModule(addr string) (*grpc.ClientConn, mediaadminv1.MediaAdminServiceClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn, mediaadminv1.NewMediaAdminServiceClient(conn), nil
}

func (h *Handler) MediaLibraryList(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	pageCtx, pageCancel := context.WithTimeout(r.Context(), mediaPageTimeout)
	defer pageCancel()

	dialCtx, dialCancel := context.WithTimeout(pageCtx, mediaDialTimeout)
	addr, err := h.mediaModuleAddr(dialCtx, moduleID)
	dialCancel()
	if err != nil {
		slog.Warn("media: resolve failed", "module", moduleID, "error", err)
		content := templates.MediaListPage(moduleID, nil, 0, 0, 0, moduleID, nil, "", "")
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Media", nav, content)
		h.render(w, r, component)
		return
	}

	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		slog.Warn("media: dial failed", "module", moduleID, "error", err)
		content := templates.MediaListPage(moduleID, nil, 0, 0, 0, moduleID, nil, "", "")
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Media", nav, content)
		h.render(w, r, component)
		return
	}
	defer func() { _ = conn.Close() }()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize := 50
	search := r.URL.Query().Get("q")
	tagID := r.URL.Query().Get("tag")

	readCtx, readCancel := context.WithTimeout(pageCtx, mediaReadTimeout)
	info, err := client.GetMediaTypeInfo(readCtx, &mediaadminv1.GetMediaTypeInfoRequest{})
	readCancel()
	displayName := moduleID
	var features []mediaadminv1.Feature
	if err == nil {
		displayName = info.GetDisplayName()
		features = info.GetFeatures()
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, mediaReadTimeout)
	resp, err := client.ListItems(readCtx, &mediaadminv1.ListItemsRequest{
		Page:     int32(page),
		PageSize: int32(pageSize),
		Search:   search,
		SortBy:   r.URL.Query().Get("sort"),
		TagId:    tagID,
	})
	readCancel()
	if err != nil {
		slog.Warn("media: ListItems failed", "module", moduleID, "error", err)
		content := templates.MediaListPage(displayName, nil, 0, 0, 0, moduleID, features, search, tagID)
		nav := h.nav(r.URL.Path)
		component := templates.Layout(displayName, nav, content)
		h.render(w, r, component)
		return
	}

	items := resp.GetItems()
	if items == nil {
		items = []*mediaadminv1.MediaItem{}
	}
	content := templates.MediaListPage(displayName, items, int(resp.GetTotal()), int(resp.GetPage()), int(resp.GetPageSize()), moduleID, features, search, tagID)
	nav := h.nav(r.URL.Path)
	component := templates.Layout(displayName, nav, content)
	h.render(w, r, component)
}

func (h *Handler) MediaLibraryItem(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	ctx := r.Context()

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		slog.Warn("media: resolve failed", "module", moduleID, "error", err)
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		slog.Warn("media: dial failed", "module", moduleID, "error", err)
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()

	info, _ := client.GetMediaTypeInfo(ctx, &mediaadminv1.GetMediaTypeInfoRequest{})
	displayName := moduleID
	var features []mediaadminv1.Feature
	if info != nil {
		displayName = info.GetDisplayName()
		features = info.GetFeatures()
	}

	item, err := client.GetItem(ctx, &mediaadminv1.GetItemRequest{Id: itemID})
	if err != nil {
		slog.Warn("media: GetItem failed", "module", moduleID, "id", itemID, "error", err)
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	var history []templates.ActivityEntry
	if hist, err := client.ListHistory(ctx, &mediaadminv1.ListHistoryRequest{
		Page: 1, PageSize: 50, ItemId: itemID,
	}); err == nil {
		for _, rec := range hist.GetRecords() {
			history = append(history, templates.ActivityEntry{
				ID:          rec.GetId(),
				EventType:   rec.GetEventType(),
				ItemID:      rec.GetItemId(),
				Title:       rec.GetTitle(),
				SourceTitle: rec.GetSourceTitle(),
				Quality:     rec.GetQuality(),
				Indexer:     rec.GetIndexer(),
				FilePath:    rec.GetFilePath(),
				CreatedAt:   rec.GetCreatedAt(),
				ModuleID:    moduleID,
				ModuleName:  displayName,
			})
		}
	}

	profiles := h.listProfileOptions(ctx)
	roots := h.listRootOptions(ctx, mediaKindFromModule(moduleID, displayName))

	var releases []*mediaadminv1.IndexerResult
	if searchResp, err := client.SearchIndexers(ctx, &mediaadminv1.SearchIndexersRequest{
		ItemId: itemID,
		Limit:  20,
	}); err != nil {
		slog.Warn("media: SearchIndexers failed", "module", moduleID, "id", itemID, "error", err)
	} else {
		releases = searchResp.GetResults()
	}

	seasons := h.loadTVSeasons(ctx, moduleID, itemID, displayName)
	files := h.loadMovieFiles(ctx, moduleID, itemID, displayName)
	titles := h.loadAlternateTitles(ctx, moduleID, itemID, displayName)
	trailers := h.loadTrailers(ctx, moduleID, itemID, displayName, item.GetItem())

	content := templates.MediaDetailPage(
		item.GetItem(), moduleID, history, profiles, roots, features, displayName,
		releases, seasons, files, titles, trailers,
		r.URL.Query().Get("dispatched"),
		r.URL.Query().Get("status"),
		r.URL.Query().Get("error"),
	)
	nav := h.nav(r.URL.Path)
	component := templates.Layout(item.GetItem().GetTitle()+" — "+displayName, nav, content)
	h.render(w, r, component)
}

func (h *Handler) MediaItemDispatch(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), automationDispatchTO)
	defer cancel()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	redirectBase := fmt.Sprintf("/media/%s/item/%s", moduleID, itemID)
	redirectErr := func(msg string) {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
	}

	title := r.FormValue("title")
	itemType := r.FormValue("item_type")
	tmdbID, _ := strconv.Atoi(r.FormValue("tmdb_id"))
	year, _ := strconv.Atoi(r.FormValue("year"))
	forceFixture := r.FormValue("fixture") == "1" || r.FormValue("mode") == "fixture"
	searchBest := r.FormValue("mode") == "best"

	if title == "" || itemType == "" {
		addr, err := h.mediaModuleAddr(ctx, moduleID)
		if err != nil {
			redirectErr(err.Error())
			return
		}
		conn, client, err := h.dialMediaModule(addr)
		if err != nil {
			redirectErr(err.Error())
			return
		}
		defer func() { _ = conn.Close() }()

		info, _ := client.GetMediaTypeInfo(ctx, &mediaadminv1.GetMediaTypeInfoRequest{})
		displayName := moduleID
		if info != nil {
			displayName = info.GetDisplayName()
		}
		if itemType == "" {
			itemType = automationItemType(moduleID, displayName)
		}

		got, err := client.GetItem(ctx, &mediaadminv1.GetItemRequest{Id: itemID})
		if err != nil {
			slog.Warn("media: GetItem for dispatch failed", "module", moduleID, "id", itemID, "error", err)
			redirectErr("item unavailable")
			return
		}
		it := got.GetItem()
		if title == "" {
			title = it.GetTitle()
		}
		if tmdbID == 0 {
			tmdbID = int(itemTMDBID(it))
		}
		if year == 0 && it.GetYear() > 0 {
			year = int(it.GetYear())
		}
	}

	var release *automationRelease
	if !forceFixture && !searchBest {
		release = releaseFromForm(r)
	}

	disp, err := h.dispatchAutomation(ctx, itemType, itemID, title, int32(tmdbID), int32(year), release, forceFixture)
	if err != nil {
		slog.Warn("media: dispatch failed", "module", moduleID, "id", itemID, "error", err)
		redirectErr(err.Error())
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.media.item.dispatch", "media_item", itemID, map[string]string{
			"module":      moduleID,
			"download_id": disp.GetDownloadId(),
			"status":      disp.GetStatus(),
		})
	}

	http.Redirect(w, r, fmt.Sprintf("%s?dispatched=%s&status=%s",
		redirectBase, url.QueryEscape(disp.GetDownloadId()), url.QueryEscape(disp.GetStatus())), http.StatusSeeOther)
}

func (h *Handler) MediaLibraryUpdate(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
		return
	}

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">module unavailable</div>`))
		return
	}

	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">connection failed</div>`))
		return
	}
	defer func() { _ = conn.Close() }()

	metadata := make(map[string]string)
	for k := range r.Form {
		if k != "title" && k != "description" && k != "year" && k != "genres" {
			metadata[k] = r.FormValue(k)
		}
	}
	// Checkbox fields are omitted when unchecked; always persist monitor state when the form includes the control.
	if _, hasMonitor := r.Form["monitored"]; hasMonitor || r.FormValue("has_monitored") == "1" {
		metadata["monitored"] = parseFormMonitored(r)
	}

	_, err = client.UpdateMetadata(ctx, &mediaadminv1.UpdateMetadataRequest{
		Id:          itemID,
		Title:       r.FormValue("title"),
		Description: r.FormValue("description"),
		Metadata:    metadata,
	})
	if err != nil {
		slog.Warn("media: UpdateMetadata failed", "module", moduleID, "id", itemID, "error", err)
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">update failed</div>`))
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.media.library.update", "media_item", itemID, map[string]string{
			"module": moduleID,
			"title":  r.FormValue("title"),
		})
	}

	_, _ = w.Write([]byte(`<div class="text-xs text-green-400">saved</div>`))
}

func (h *Handler) MediaLibraryArtwork(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	ctx := r.Context()

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">module unavailable</div>`))
		return
	}

	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">connection failed</div>`))
		return
	}
	defer func() { _ = conn.Close() }()

	resp, err := client.ListArtwork(ctx, &mediaadminv1.ListArtworkRequest{Id: itemID})
	if err != nil {
		slog.Warn("media: ListArtwork failed", "module", moduleID, "id", itemID, "error", err)
		_, _ = w.Write([]byte(`<div class="text-xs text-red-400">failed to load artwork</div>`))
		return
	}

	content := templates.ArtworkGrid(resp.GetArtwork())
	h.render(w, r, content)
}
