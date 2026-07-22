package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) mediaModuleAddr(ctx context.Context, moduleID string) (string, error) {
	mod, err := h.Core.Discovery.Resolve(ctx, moduleID)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", moduleID, err)
	}
	addr := mod.GetHttpAddr()
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
	ctx := r.Context()

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		slog.Warn("media: resolve failed", "module", moduleID, "error", err)
		content := templates.MediaListPage("Error", nil, 0, 0, 0, moduleID, nil, "", "")
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Media", nav, content)
		h.render(w, r, component)
		return
	}

	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		slog.Warn("media: dial failed", "module", moduleID, "error", err)
		content := templates.MediaListPage("Error", nil, 0, 0, 0, moduleID, nil, "", "")
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Media", nav, content)
		h.render(w, r, component)
		return
	}
	defer conn.Close()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize := 50
	search := r.URL.Query().Get("q")
	tagID := r.URL.Query().Get("tag")

	info, err := client.GetMediaTypeInfo(ctx, &mediaadminv1.GetMediaTypeInfoRequest{})
	displayName := moduleID
	var features []string
	if err == nil {
		displayName = info.GetDisplayName()
		features = info.GetFeatures()
	}

	resp, err := client.ListItems(ctx, &mediaadminv1.ListItemsRequest{
		Page:     int32(page),
		PageSize: int32(pageSize),
		Search:   search,
		SortBy:   r.URL.Query().Get("sort"),
		TagId:    tagID,
	})
	if err != nil {
		slog.Warn("media: ListItems failed", "module", moduleID, "error", err)
		content := templates.MediaListPage(displayName, nil, 0, 0, 0, moduleID, features, search, tagID)
		nav := h.nav(r.URL.Path)
		component := templates.Layout(displayName, nav, content)
		h.render(w, r, component)
		return
	}

	content := templates.MediaListPage(displayName, resp.GetItems(), int(resp.GetTotal()), int(resp.GetPage()), int(resp.GetPageSize()), moduleID, features, search, tagID)
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
	defer conn.Close()

	info, _ := client.GetMediaTypeInfo(ctx, &mediaadminv1.GetMediaTypeInfoRequest{})
	displayName := moduleID
	var features []string
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

	content := templates.MediaDetailPage(item.GetItem(), moduleID, history, profiles, roots, features, displayName)
	nav := h.nav(r.URL.Path)
	component := templates.Layout(item.GetItem().GetTitle()+" — "+displayName, nav, content)
	h.render(w, r, component)
}

func (h *Handler) MediaLibraryUpdate(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">invalid form data</div>`))
		return
	}

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">module unavailable</div>`))
		return
	}

	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">connection failed</div>`))
		return
	}
	defer conn.Close()

	metadata := make(map[string]string)
	for k := range r.Form {
		if k != "title" && k != "description" && k != "year" && k != "genres" {
			metadata[k] = r.FormValue(k)
		}
	}

	_, err = client.UpdateMetadata(ctx, &mediaadminv1.UpdateMetadataRequest{
		Id:          itemID,
		Title:       r.FormValue("title"),
		Description: r.FormValue("description"),
		Metadata:    metadata,
	})
	if err != nil {
		slog.Warn("media: UpdateMetadata failed", "module", moduleID, "id", itemID, "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">update failed</div>`))
		return
	}

	w.Write([]byte(`<div class="text-xs text-green-400">saved</div>`))
}

func (h *Handler) MediaLibraryArtwork(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	ctx := r.Context()

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">module unavailable</div>`))
		return
	}

	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		w.Write([]byte(`<div class="text-xs text-red-400">connection failed</div>`))
		return
	}
	defer conn.Close()

	resp, err := client.ListArtwork(ctx, &mediaadminv1.ListArtworkRequest{Id: itemID})
	if err != nil {
		slog.Warn("media: ListArtwork failed", "module", moduleID, "id", itemID, "error", err)
		w.Write([]byte(`<div class="text-xs text-red-400">failed to load artwork</div>`))
		return
	}

	content := templates.ArtworkGrid(resp.GetArtwork())
	h.render(w, r, content)
}
