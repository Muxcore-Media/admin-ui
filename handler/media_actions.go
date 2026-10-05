package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) dialMovieModule(addr string) (*grpc.ClientConn, mgmntv1.MovieManagementServiceClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn, mgmntv1.NewMovieManagementServiceClient(conn), nil
}

func (h *Handler) loadMovieFiles(ctx context.Context, moduleID, itemID, displayName string) []templates.MediaFileView {
	if automationItemType(moduleID, displayName) != "movie" {
		return nil
	}
	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		return nil
	}
	conn, client, err := h.dialMovieModule(addr)
	if err != nil {
		return nil
	}
	defer func() { _ = conn.Close() }()
	resp, err := client.ListFiles(ctx, &mgmntv1.ListFilesRequest{MovieId: itemID})
	if err != nil {
		slog.Debug("media: ListFiles failed", "module", moduleID, "id", itemID, "error", err)
		return nil
	}
	out := make([]templates.MediaFileView, 0, len(resp.GetFiles()))
	for _, f := range resp.GetFiles() {
		out = append(out, templates.MediaFileView{
			ID: f.GetId(), Path: f.GetFilePath(), Quality: f.GetQuality(),
			Size: f.GetSizeBytes(), Container: f.GetContainer(),
		})
	}
	return out
}

func (h *Handler) MediaFileDelete(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	fileID := r.PathValue("fileID")
	ctx := r.Context()
	redirectBase := fmt.Sprintf("/media/%s/item/%s", moduleID, itemID)
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, redirectBase, http.StatusSeeOther)
		return
	}
	deleteFiles := r.FormValue("delete_files") == "1"
	addr, err := h.mediaModuleAddr(ctx, moduleID)
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
	if _, err := client.RemoveFile(ctx, &mgmntv1.RemoveFileRequest{FileId: fileID, DeleteFiles: deleteFiles}); err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectBase+"?status=file_removed", http.StatusSeeOther)
}

func (h *Handler) dialTVModule(addr string) (*grpc.ClientConn, tvmgmtv1.TvManagementServiceClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn, tvmgmtv1.NewTvManagementServiceClient(conn), nil
}

func seasonViewsFromTV(series *tvmgmtv1.TVSeries) []templates.SeasonView {
	if series == nil {
		return nil
	}
	out := make([]templates.SeasonView, 0, len(series.GetSeasons()))
	for _, s := range series.GetSeasons() {
		if s.GetSeasonNumber() <= 0 {
			continue
		}
		eps := make([]templates.EpisodeView, 0, len(s.GetEpisodes()))
		for _, e := range s.GetEpisodes() {
			eps = append(eps, templates.EpisodeView{
				ID:        e.GetId(),
				Number:    int(e.GetEpisodeNumber()),
				Absolute:  int(e.GetAbsoluteNumber()),
				Name:      e.GetName(),
				AirDate:   e.GetAirDate(),
				Monitored: e.GetMonitored(),
				HasFile:   e.GetHasFile(),
			})
		}
		out = append(out, templates.SeasonView{
			ID:        s.GetId(),
			Number:    int(s.GetSeasonNumber()),
			Name:      s.GetName(),
			Monitored: s.GetMonitored(),
			Episodes:  eps,
		})
	}
	return out
}

func (h *Handler) loadTVSeasons(ctx context.Context, moduleID, itemID, displayName string) []templates.SeasonView {
	if automationItemType(moduleID, displayName) != "tv" {
		return nil
	}
	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		return nil
	}
	conn, client, err := h.dialTVModule(addr)
	if err != nil {
		slog.Warn("media: dial TV management failed", "module", moduleID, "error", err)
		return nil
	}
	defer func() { _ = conn.Close() }()
	resp, err := client.GetTVShow(ctx, &tvmgmtv1.GetTVShowRequest{SeriesId: itemID})
	if err != nil {
		slog.Warn("media: GetTVShow failed", "module", moduleID, "id", itemID, "error", err)
		return nil
	}
	return seasonViewsFromTV(resp.GetSeries())
}

func (h *Handler) MediaItemRefresh(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	ctx := r.Context()
	redirectBase := fmt.Sprintf("/media/%s/item/%s", moduleID, itemID)

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()

	if _, err := client.RefreshItem(ctx, &mediaadminv1.RefreshItemRequest{Id: itemID}); err != nil {
		slog.Warn("media: RefreshItem failed", "module", moduleID, "id", itemID, "error", err)
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.media.item.refresh", "media_item", itemID, map[string]string{
			"module": moduleID,
		})
	}
	http.Redirect(w, r, redirectBase+"?status="+url.QueryEscape("refreshed"), http.StatusSeeOther)
}

func (h *Handler) MediaItemDelete(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}
	deleteFiles := r.FormValue("delete_files") == "1" || r.FormValue("delete_files") == "true"

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		http.Redirect(w, r, "/media/"+moduleID+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		http.Redirect(w, r, "/media/"+moduleID+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()

	if _, err := client.DeleteItem(ctx, &mediaadminv1.DeleteItemRequest{
		Id: itemID, DeleteFiles: deleteFiles,
	}); err != nil {
		slog.Warn("media: DeleteItem failed", "module", moduleID, "id", itemID, "error", err)
		http.Redirect(w, r, fmt.Sprintf("/media/%s/item/%s?error=%s", moduleID, itemID, url.QueryEscape(err.Error())), http.StatusSeeOther)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.media.item.delete", "media_item", itemID, map[string]string{
			"module":       moduleID,
			"delete_files": strconv.FormatBool(deleteFiles),
		})
	}
	http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
}

func (h *Handler) MediaSeasonMonitor(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	seasonID := r.PathValue("seasonID")
	ctx := r.Context()
	redirectBase := fmt.Sprintf("/media/%s/item/%s", moduleID, itemID)

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, redirectBase, http.StatusSeeOther)
		return
	}
	monitored := r.FormValue("monitored") == "true" || r.FormValue("monitored") == "1"

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	conn, client, err := h.dialTVModule(addr)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()

	if _, err := client.UpdateSeasonMonitored(ctx, &tvmgmtv1.UpdateSeasonMonitoredRequest{
		SeasonId: seasonID, Monitored: monitored,
	}); err != nil {
		slog.Warn("media: UpdateSeasonMonitored failed", "season", seasonID, "error", err)
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectBase+"#seasons", http.StatusSeeOther)
}

func (h *Handler) MediaEpisodeMonitor(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	episodeID := r.PathValue("episodeID")
	ctx := r.Context()
	redirectBase := fmt.Sprintf("/media/%s/item/%s", moduleID, itemID)

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, redirectBase, http.StatusSeeOther)
		return
	}
	monitored := r.FormValue("monitored") == "true" || r.FormValue("monitored") == "1"

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	conn, client, err := h.dialTVModule(addr)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()

	if _, err := client.UpdateEpisodeMonitored(ctx, &tvmgmtv1.UpdateEpisodeMonitoredRequest{
		EpisodeId: episodeID, Monitored: monitored,
	}); err != nil {
		slog.Warn("media: UpdateEpisodeMonitored failed", "episode", episodeID, "error", err)
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectBase+"#seasons", http.StatusSeeOther)
}

func (h *Handler) MediaEpisodeFileDelete(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	episodeID := r.PathValue("episodeID")
	ctx := r.Context()
	redirectBase := fmt.Sprintf("/media/%s/item/%s", moduleID, itemID)

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, redirectBase+"#seasons", http.StatusSeeOther)
		return
	}
	deleteFiles := r.FormValue("delete_files") == "1" || r.FormValue("delete_files") == "true"

	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error())+"#seasons", http.StatusSeeOther)
		return
	}
	conn, client, err := h.dialTVModule(addr)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error())+"#seasons", http.StatusSeeOther)
		return
	}
	defer func() { _ = conn.Close() }()

	if _, err := client.RemoveEpisodeFile(ctx, &tvmgmtv1.RemoveEpisodeFileRequest{
		EpisodeId: episodeID, DeleteFiles: deleteFiles,
	}); err != nil {
		slog.Warn("media: RemoveEpisodeFile failed", "episode", episodeID, "error", err)
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error())+"#seasons", http.StatusSeeOther)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.media.episode.file.delete", "media_episode", episodeID, map[string]string{
			"module":       moduleID,
			"series_id":    itemID,
			"delete_files": strconv.FormatBool(deleteFiles),
		})
	}
	http.Redirect(w, r, redirectBase+"?status=file_removed#seasons", http.StatusSeeOther)
}

func (h *Handler) loadAlternateTitles(ctx context.Context, moduleID, itemID, displayName string) []templates.AlternateTitleView {
	kind := automationItemType(moduleID, displayName)
	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		return nil
	}
	switch kind {
	case "movie":
		conn, client, err := h.dialMovieModule(addr)
		if err != nil {
			return nil
		}
		defer func() { _ = conn.Close() }()
		resp, err := client.ListAlternateTitles(ctx, &mgmntv1.ListAlternateTitlesRequest{MovieId: itemID})
		if err != nil {
			return nil
		}
		out := make([]templates.AlternateTitleView, 0, len(resp.GetTitles()))
		for _, t := range resp.GetTitles() {
			out = append(out, templates.AlternateTitleView{ID: t.GetId(), Title: t.GetTitle(), Source: t.GetSource()})
		}
		return out
	case "tv":
		conn, client, err := h.dialTVModule(addr)
		if err != nil {
			return nil
		}
		defer func() { _ = conn.Close() }()
		resp, err := client.ListAlternateTitles(ctx, &tvmgmtv1.ListAlternateTitlesRequest{SeriesId: itemID})
		if err != nil {
			return nil
		}
		out := make([]templates.AlternateTitleView, 0, len(resp.GetTitles()))
		for _, t := range resp.GetTitles() {
			out = append(out, templates.AlternateTitleView{ID: t.GetId(), Title: t.GetTitle(), Source: t.GetSource()})
		}
		return out
	default:
		return nil
	}
}

func (h *Handler) MediaAlternateTitleAdd(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	ctx := r.Context()
	redirectBase := fmt.Sprintf("/media/%s/item/%s", moduleID, itemID)
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, redirectBase+"#alternate-titles", http.StatusSeeOther)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Redirect(w, r, redirectBase+"#alternate-titles", http.StatusSeeOther)
		return
	}
	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	displayName := moduleID
	if conn, admin, err := h.dialMediaModule(addr); err == nil {
		defer func() { _ = conn.Close() }()
		if info, err := admin.GetMediaTypeInfo(ctx, &mediaadminv1.GetMediaTypeInfoRequest{}); err == nil && info != nil {
			displayName = info.GetDisplayName()
		}
	}
	kind := automationItemType(moduleID, displayName)
	switch kind {
	case "movie":
		conn, client, err := h.dialMovieModule(addr)
		if err != nil {
			http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := client.AddAlternateTitle(ctx, &mgmntv1.AddAlternateTitleRequest{MovieId: itemID, Title: title}); err != nil {
			http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
	case "tv":
		conn, client, err := h.dialTVModule(addr)
		if err != nil {
			http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := client.AddAlternateTitle(ctx, &tvmgmtv1.AddAlternateTitleRequest{SeriesId: itemID, Title: title}); err != nil {
			http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
	default:
		http.Redirect(w, r, redirectBase+"?error=unsupported", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectBase+"?status=title_added#alternate-titles", http.StatusSeeOther)
}

func (h *Handler) MediaAlternateTitleDelete(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")
	titleID := r.PathValue("titleID")
	ctx := r.Context()
	redirectBase := fmt.Sprintf("/media/%s/item/%s", moduleID, itemID)
	addr, err := h.mediaModuleAddr(ctx, moduleID)
	if err != nil {
		http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	displayName := moduleID
	if conn, admin, err := h.dialMediaModule(addr); err == nil {
		defer func() { _ = conn.Close() }()
		if info, err := admin.GetMediaTypeInfo(ctx, &mediaadminv1.GetMediaTypeInfoRequest{}); err == nil && info != nil {
			displayName = info.GetDisplayName()
		}
	}
	kind := automationItemType(moduleID, displayName)
	switch kind {
	case "movie":
		conn, client, err := h.dialMovieModule(addr)
		if err != nil {
			http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := client.RemoveAlternateTitle(ctx, &mgmntv1.RemoveAlternateTitleRequest{MovieId: itemID, TitleId: titleID}); err != nil {
			http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
	case "tv":
		conn, client, err := h.dialTVModule(addr)
		if err != nil {
			http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := client.RemoveAlternateTitle(ctx, &tvmgmtv1.RemoveAlternateTitleRequest{SeriesId: itemID, TitleId: titleID}); err != nil {
			http.Redirect(w, r, redirectBase+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
	default:
		http.Redirect(w, r, redirectBase+"?error=unsupported", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectBase+"?status=title_removed#alternate-titles", http.StatusSeeOther)
}

func parseFormMonitored(r *http.Request) string {
	v := strings.TrimSpace(r.FormValue("monitored"))
	if v == "true" || v == "1" || strings.EqualFold(v, "on") || strings.EqualFold(v, "yes") {
		return "true"
	}
	return "false"
}
