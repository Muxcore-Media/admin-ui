package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) mediaDial(w http.ResponseWriter, r *http.Request, moduleID string) (mediaadminv1.MediaAdminServiceClient, func(), bool) {
	ctx := r.Context()
	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		slog.Warn("media: resolve failed", "module", moduleID, "error", err)
		return nil, nil, false
	}
	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		slog.Warn("media: dial failed", "module", moduleID, "error", err)
		return nil, nil, false
	}
	return client, func() { _ = conn.Close() }, true
}

func (h *Handler) mediaInfo(client mediaadminv1.MediaAdminServiceClient, r *http.Request, moduleID string) (string, []mediaadminv1.Feature) {
	info, err := client.GetMediaTypeInfo(r.Context(), &mediaadminv1.GetMediaTypeInfoRequest{})
	if err != nil || info == nil {
		return moduleID, nil
	}
	return info.GetDisplayName(), info.GetFeatures()
}

func (h *Handler) MediaMissing(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	client, closer, ok := h.mediaDial(w, r, moduleID)
	if !ok {
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}
	defer closer()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	displayName, features := h.mediaInfo(client, r, moduleID)
	resp, err := client.ListMissing(r.Context(), &mediaadminv1.ListMissingRequest{
		Page: int32(page), PageSize: 50,
	})
	var items []*mediaadminv1.MissingItem
	var total, pageSize int
	if err != nil {
		slog.Warn("media: ListMissing failed", "module", moduleID, "error", err)
	} else {
		items = resp.GetItems()
		total = int(resp.GetTotal())
		pageSize = int(resp.GetPageSize())
		page = int(resp.GetPage())
	}
	content := templates.MediaMissingPage(displayName, moduleID, features, items, total, page, pageSize)
	h.render(w, r, templates.Layout(displayName+" — Missing", h.nav(r.URL.Path), content))
}

func (h *Handler) MediaTags(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	client, closer, ok := h.mediaDial(w, r, moduleID)
	if !ok {
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}
	defer closer()

	displayName, features := h.mediaInfo(client, r, moduleID)
	resp, err := client.ListTags(r.Context(), &mediaadminv1.ListTagsRequest{})
	var tags []*mediaadminv1.Tag
	if err != nil {
		slog.Warn("media: ListTags failed", "module", moduleID, "error", err)
	} else {
		tags = resp.GetTags()
	}
	content := templates.MediaTagsPage(displayName, moduleID, features, tags)
	h.render(w, r, templates.Layout(displayName+" — Tags", h.nav(r.URL.Path), content))
}

func (h *Handler) MediaTagsPost(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/media/"+moduleID+"/tags", http.StatusSeeOther)
		return
	}
	client, closer, ok := h.mediaDial(w, r, moduleID)
	if !ok {
		http.Redirect(w, r, "/media/"+moduleID+"/tags", http.StatusSeeOther)
		return
	}
	defer closer()

	switch r.FormValue("action") {
	case "create":
		label := r.FormValue("label")
		if _, err := client.CreateTag(r.Context(), &mediaadminv1.CreateTagRequest{Label: label}); err != nil {
			slog.Warn("media: CreateTag failed", "module", moduleID, "error", err)
		} else if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.media.tag.create", "media_tag", moduleID, map[string]string{
				"module": moduleID,
				"label":  label,
			})
		}
	case "delete":
		tagID := r.FormValue("tag_id")
		if _, err := client.DeleteTag(r.Context(), &mediaadminv1.DeleteTagRequest{TagId: tagID}); err != nil {
			slog.Warn("media: DeleteTag failed", "module", moduleID, "error", err)
		} else if sess := SessionFromContext(r.Context()); sess != nil {
			h.auditLog(r.Context(), sess.UserID, "admin.media.tag.delete", "media_tag", tagID, map[string]string{
				"module": moduleID,
			})
		}
	}
	http.Redirect(w, r, "/media/"+moduleID+"/tags", http.StatusSeeOther)
}

func (h *Handler) MediaCollections(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	client, closer, ok := h.mediaDial(w, r, moduleID)
	if !ok {
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}
	defer closer()

	displayName, features := h.mediaInfo(client, r, moduleID)
	resp, err := client.ListCollections(r.Context(), &mediaadminv1.ListCollectionsRequest{})
	var cols []*mediaadminv1.CollectionSummary
	if err != nil {
		slog.Warn("media: ListCollections failed", "module", moduleID, "error", err)
	} else {
		cols = resp.GetCollections()
	}
	content := templates.MediaCollectionsPage(displayName, moduleID, features, cols)
	h.render(w, r, templates.Layout(displayName+" — Collections", h.nav(r.URL.Path), content))
}

func (h *Handler) MediaCollectionDetail(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	collectionID := r.PathValue("collectionID")
	client, closer, ok := h.mediaDial(w, r, moduleID)
	if !ok {
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}
	defer closer()

	displayName, features := h.mediaInfo(client, r, moduleID)
	resp, err := client.GetCollectionItems(r.Context(), &mediaadminv1.GetCollectionItemsRequest{CollectionId: collectionID})
	if err != nil {
		slog.Warn("media: GetCollectionItems failed", "module", moduleID, "error", err)
		http.Redirect(w, r, "/media/"+moduleID+"/collections", http.StatusSeeOther)
		return
	}
	monitored := false
	if id, err := strconv.Atoi(collectionID); err == nil && id > 0 {
		if addr, err := h.mediaModuleAddr(r.Context(), moduleID); err == nil {
			if conn, movieClient, err := h.dialMovieModule(addr); err == nil {
				defer func() { _ = conn.Close() }()
				if prefs, err := movieClient.GetCollectionPrefs(r.Context(), &mgmntv1.GetCollectionPrefsRequest{CollectionId: int32(id)}); err == nil {
					monitored = prefs.GetMonitored()
				}
			}
		}
	}
	content := templates.MediaCollectionDetailPage(displayName, moduleID, features, resp.GetName(), collectionID, resp.GetItems(), monitored, r.URL.Query().Get("status"))
	h.render(w, r, templates.Layout(resp.GetName()+" — Collections", h.nav(r.URL.Path), content))
}

func (h *Handler) MediaCollectionMonitor(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	collectionID := r.PathValue("collectionID")
	redirectBase := fmt.Sprintf("/media/%s/collections/%s", moduleID, collectionID)
	_ = r.ParseForm()
	monitored := r.FormValue("monitored") == "true" || r.FormValue("monitored") == "1"
	id, err := strconv.Atoi(collectionID)
	if err != nil || id == 0 {
		http.Redirect(w, r, "/media/"+moduleID+"/collections", http.StatusSeeOther)
		return
	}
	addr, err := h.mediaModuleAddr(r.Context(), moduleID)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	conn, client, err := h.dialMovieModule(addr)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()
	if _, err := client.SetCollectionMonitored(r.Context(), &mgmntv1.SetCollectionMonitoredRequest{
		CollectionId: int32(id), Monitored: monitored, SearchOnAdd: true,
	}); err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectBase+"?status=collection_updated", http.StatusSeeOther)
}

func (h *Handler) MediaCollectionSync(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	collectionID := r.PathValue("collectionID")
	redirectBase := fmt.Sprintf("/media/%s/collections/%s", moduleID, collectionID)
	_ = r.ParseForm()
	id, err := strconv.Atoi(collectionID)
	if err != nil || id == 0 {
		http.Redirect(w, r, "/media/"+moduleID+"/collections", http.StatusSeeOther)
		return
	}
	addr, err := h.mediaModuleAddr(r.Context(), moduleID)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	conn, client, err := h.dialMovieModule(addr)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()
	resp, err := client.SyncCollection(r.Context(), &mgmntv1.SyncCollectionRequest{
		CollectionId: int32(id), AddMissing: r.FormValue("add_missing") != "0",
	})
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("synced added=%d present=%d", resp.GetAdded(), resp.GetAlreadyPresent())
	http.Redirect(w, r, redirectBase+"?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) MediaCalendar(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	client, closer, ok := h.mediaDial(w, r, moduleID)
	if !ok {
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}
	defer closer()

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

	displayName, features := h.mediaInfo(client, r, moduleID)
	resp, err := client.GetCalendar(r.Context(), &mediaadminv1.GetCalendarRequest{
		StartDate: start, EndDate: end, IncludeUnmonitored: includeUnmon,
	})
	var items []*mediaadminv1.CalendarItem
	if err != nil {
		slog.Warn("media: GetCalendar failed", "module", moduleID, "error", err)
	} else {
		items = resp.GetItems()
	}
	content := templates.MediaCalendarPage(displayName, moduleID, features, items, start, end)
	h.render(w, r, templates.Layout(displayName+" — Calendar", h.nav(r.URL.Path), content))
}
