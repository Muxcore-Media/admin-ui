package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

// MusicWantedPage lists monitored albums without track files from media-music.
func (h *Handler) MusicWantedPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), musicPageTimeout)
	defer cancel()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	artistFilter := r.URL.Query().Get("artist_id")

	data := templates.MusicWantedPageData{
		Page:     page,
		ArtistID: artistFilter,
	}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, musicDialTimeout)
	moduleID, addr, name, err := h.musicModule(dialCtx)
	dialCancel()
	if err != nil {
		slog.Warn("music-wanted: resolve failed", "error", err)
		data.Error = err.Error()
		data.SoftEmpty = true
		h.renderMusicWanted(w, r, data)
		return
	}
	data.ModuleID = moduleID
	data.ModuleName = name

	conn, client, err := h.dialMediaModule(addr)
	if err != nil {
		slog.Warn("music-wanted: dial failed", "module", moduleID, "error", err)
		data.Error = err.Error()
		data.SoftEmpty = true
		h.renderMusicWanted(w, r, data)
		return
	}
	defer func() { _ = conn.Close() }()

	readCtx, readCancel := context.WithTimeout(pageCtx, musicReadTimeout)
	info, err := client.GetMediaTypeInfo(readCtx, &mediaadminv1.GetMediaTypeInfoRequest{})
	readCancel()
	if err != nil {
		slog.Warn("music-wanted: GetMediaTypeInfo failed", "module", moduleID, "error", err)
		data.Error = "media type info unavailable: " + err.Error()
		h.renderMusicWanted(w, r, data)
		return
	}
	if !mediaFeatureEnabled(info.GetFeatures(), "missing") {
		data.Error = "missing feature not supported by music module"
		data.SoftEmpty = true
		h.renderMusicWanted(w, r, data)
		return
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, musicReadTimeout)
	resp, err := client.ListMissing(readCtx, &mediaadminv1.ListMissingRequest{
		Page: int32(page), PageSize: 50, ParentId: artistFilter,
	})
	readCancel()
	if err != nil {
		slog.Warn("music-wanted: ListMissing failed", "module", moduleID, "error", err)
		data.Error = "missing list unavailable: " + err.Error()
		h.renderMusicWanted(w, r, data)
		return
	}

	data.Total = int(resp.GetTotal())
	data.PageSize = int(resp.GetPageSize())
	if data.PageSize < 1 {
		data.PageSize = 50
	}
	data.Page = int(resp.GetPage())
	if data.Page < 1 {
		data.Page = page
	}

	for _, it := range resp.GetItems() {
		meta := it.GetMetadata()
		data.Rows = append(data.Rows, templates.MusicWantedRow{
			AlbumID:       it.GetId(),
			ArtistID:      it.GetParentId(),
			ArtistName:    meta["artist_name"],
			AlbumTitle:    it.GetTitle(),
			Year:          int(it.GetYear()),
			MusicBrainzID: meta["musicbrainz_id"],
			Status:        "missing",
		})
	}

	h.renderMusicWanted(w, r, data)
}

func (h *Handler) renderMusicWanted(w http.ResponseWriter, r *http.Request, data templates.MusicWantedPageData) {
	content := templates.MusicWantedPage(data)
	h.render(w, r, templates.Layout("Music — Wanted", h.nav(r.URL.Path), content))
}
