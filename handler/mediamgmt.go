package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"google.golang.org/grpc"

	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	metadatav1 "github.com/Muxcore-Media/metadata-tmdb/proto/metadatav1"

	indexerv1 "github.com/Muxcore-Media/indexer-prowlarr/proto/indexerv1"

	downloaderv1 "github.com/Muxcore-Media/downloader-native-torrent/proto/downloaderv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) dialMetadata(ctx *http.Request) (*grpc.ClientConn, metadatav1.MetadataServiceClient, error) {
	mod, err := h.Core.Discovery.Resolve(ctx.Context(), "metadata-tmdb")
	if err != nil {
		mods, err2 := h.Core.Discovery.FindByCapability(ctx.Context(), "metadata")
		if err2 != nil || len(mods) == 0 {
			return nil, nil, err
		}
		conn, err3 := h.cachedConn(ctx.Context(), mods[0].GetHttpAddr())
		if err3 != nil {
			return nil, nil, err3
		}
		return conn, metadatav1.NewMetadataServiceClient(conn), nil
	}
	conn, err := h.cachedConn(ctx.Context(), mod.GetHttpAddr())
	if err != nil {
		return nil, nil, err
	}
	return conn, metadatav1.NewMetadataServiceClient(conn), nil
}

func (h *Handler) dialMediaMgmt(ctx *http.Request) (*grpc.ClientConn, mgmntv1.MovieManagementServiceClient, error) {
	mod, err := h.Core.Discovery.Resolve(ctx.Context(), "media-movies")
	if err != nil {
		mods, err2 := h.Core.Discovery.FindByCapability(ctx.Context(), "media.library")
		if err2 != nil || len(mods) == 0 {
			return nil, nil, err
		}
		conn, err3 := h.cachedConn(ctx.Context(), mods[0].GetHttpAddr())
		if err3 != nil {
			return nil, nil, err3
		}
		return conn, mgmntv1.NewMovieManagementServiceClient(conn), nil
	}
	conn, err := h.cachedConn(ctx.Context(), mod.GetHttpAddr())
	if err != nil {
		return nil, nil, err
	}
	return conn, mgmntv1.NewMovieManagementServiceClient(conn), nil
}

func (h *Handler) dialIndexer(ctx *http.Request) (*grpc.ClientConn, indexerv1.IndexerServiceClient, error) {
	mod, err := h.Core.Discovery.Resolve(ctx.Context(), "indexer-prowlarr")
	if err != nil {
		mods, err2 := h.Core.Discovery.FindByCapability(ctx.Context(), "indexer")
		if err2 != nil || len(mods) == 0 {
			return nil, nil, err
		}
		conn, err3 := h.cachedConn(ctx.Context(), mods[0].GetHttpAddr())
		if err3 != nil {
			return nil, nil, err3
		}
		return conn, indexerv1.NewIndexerServiceClient(conn), nil
	}
	conn, err := h.cachedConn(ctx.Context(), mod.GetHttpAddr())
	if err != nil {
		return nil, nil, err
	}
	return conn, indexerv1.NewIndexerServiceClient(conn), nil
}

// MediaSearchAdd handles GET /media/{moduleID}/search - Search & Add page
func (h *Handler) MediaSearchAdd(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	query := r.URL.Query().Get("q")
	mediaTypeStr := r.URL.Query().Get("type")
	if mediaTypeStr == "" {
		mediaTypeStr = "movie"
	}

	nav := h.nav(r.URL.Path)

	if query == "" {
		content := templates.MediaSearchPage(moduleID, mediaTypeStr, nil, query)
		component := templates.Layout("Search & Add", nav, content)
		h.render(w, r, component)
		return
	}

	_, metaClient, err := h.dialMetadata(r)
	if err != nil {
		slog.Warn("media-search: metadata dial failed", "error", err)
		content := templates.MediaSearchPage(moduleID, mediaTypeStr, nil, query)
		component := templates.Layout("Search & Add", nav, content)
		h.render(w, r, component)
		return
	}

	resp, err := metaClient.Search(r.Context(), &metadatav1.SearchRequest{
		Query: query,
		Type:  parseMetaType(mediaTypeStr),
	})
	if err != nil {
		slog.Warn("media-search: Search failed", "error", err)
		content := templates.MediaSearchPage(moduleID, mediaTypeStr, nil, query)
		component := templates.Layout("Search & Add", nav, content)
		h.render(w, r, component)
		return
	}

	content := templates.MediaSearchPage(moduleID, mediaTypeStr, resp.GetResults(), query)
	component := templates.Layout("Search & Add", nav, content)
	h.render(w, r, component)
}

// MediaAdd handles POST /media/{moduleID}/add - Add a media item from search
func (h *Handler) MediaAdd(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r)
	moduleID := r.PathValue("moduleID")
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form data")
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	tmdbIDStr := r.FormValue("tmdb_id")
	if tmdbIDStr == "" {
		toast(w, "error", "Missing tmdb_id")
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	tmdbID, err := strconv.Atoi(tmdbIDStr)
	if err != nil {
		toast(w, "error", "Invalid tmdb_id")
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	_, mediaClient, err := h.dialMediaMgmt(r)
	if err != nil {
		toast(w, "error", "Media module unavailable")
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	genres := r.Form["genres"]
	if genres == nil {
		genres = []string{}
	}

	resp, err := mediaClient.AddMovie(r.Context(), &mgmntv1.AddMovieRequest{
		TmdbId:       int32(tmdbID),
		Title:        r.FormValue("title"),
		Year:         int32(parseInt32(r.FormValue("year"), 0)),
		Overview:     r.FormValue("overview"),
		PosterPath:   r.FormValue("poster_path"),
		BackdropPath: r.FormValue("backdrop_path"),
		Genres:       genres,
	})
	if err != nil {
		slog.Warn("media: AddMovie failed", "tmdb_id", tmdbID, "error", err)
		toast(w, "error", "Add failed: "+err.Error())
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "media.add", moduleID, resp.GetMovieId(), map[string]string{
		"title":   r.FormValue("title"),
		"tmdb_id": tmdbIDStr,
	})
	toast(w, "success", fmt.Sprintf("Added: %s", r.FormValue("title")))
	http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
}

// MediaDelete handles POST /media/{moduleID}/{id}/delete
func (h *Handler) MediaDelete(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")

	_, mediaClient, err := h.dialMediaMgmt(r)
	if err != nil {
		toast(w, "error", "Media module unavailable")
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	_, err = mediaClient.RemoveMovie(r.Context(), &mgmntv1.RemoveMovieRequest{MovieId: itemID})
	if err != nil {
		slog.Warn("media: RemoveMovie failed", "id", itemID, "error", err)
		toast(w, "error", "Remove failed")
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "media.delete", moduleID, itemID, nil)
	toast(w, "success", "Item removed")
	http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
}

// MediaRefresh handles POST /media/{moduleID}/{id}/refresh
func (h *Handler) MediaRefresh(w http.ResponseWriter, r *http.Request) {
	itemID := r.PathValue("id")

	_, mediaClient, err := h.dialMediaMgmt(r)
	if err != nil {
		toast(w, "error", "Media module unavailable")
		return
	}

	_, err = mediaClient.RefreshMetadata(r.Context(), &mgmntv1.RefreshMetadataRequest{MovieId: itemID})
	if err != nil {
		slog.Warn("media: RefreshMetadata failed", "id", itemID, "error", err)
		toast(w, "error", "Refresh failed")
		return
	}

	toast(w, "success", "Metadata refresh queued")
}

// MediaSearchIndexers handles GET /media/{moduleID}/{id}/search-indexers
func (h *Handler) MediaSearchIndexers(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	itemID := r.PathValue("id")

	nav := h.nav(r.URL.Path)

	_, mediaClient, err := h.dialMediaMgmt(r)
	if err != nil {
		content := templates.ErrorPage(503, "Media module unavailable")
		component := templates.Layout("Search Indexers", nav, content)
		h.render(w, r, component)
		return
	}

	item, err := mediaClient.GetMovie(r.Context(), &mgmntv1.GetMovieRequest{MovieId: itemID})
	if err != nil {
		slog.Warn("media-search: GetMovie failed", "id", itemID, "error", err)
		content := templates.ErrorPage(404, "Item not found")
		component := templates.Layout("Search Indexers", nav, content)
		h.render(w, r, component)
		return
	}

	title := item.GetMovie().GetTitle()
	year := item.GetMovie().GetYear()

	_, indexerClient, err := h.dialIndexer(r)
	if err != nil {
		content := templates.MediaIndexerResults(title, moduleID, nil)
		component := templates.Layout("Search Indexers", nav, content)
		h.render(w, r, component)
		return
	}

	query := title
	if year > 0 {
		query += fmt.Sprintf(" %d", year)
	}

	searchResp, err := indexerClient.Search(r.Context(), &indexerv1.SearchRequest{
		Query: query,
		Type:  "movie",
		Year:  year,
	})
	if err != nil {
		slog.Warn("media-search: indexer search failed", "error", err)
		content := templates.MediaIndexerResults(title, moduleID, nil)
		component := templates.Layout("Search Indexers", nav, content)
		h.render(w, r, component)
		return
	}

	content := templates.MediaIndexerResults(title, moduleID, searchResp.GetResults())
	component := templates.Layout("Search Indexers", nav, content)
	h.render(w, r, component)
}

// MediaDownload handles POST /media/{moduleID}/{id}/download - Send a release to the downloader
func (h *Handler) MediaDownload(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("moduleID")
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form data")
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	downloadURL := r.FormValue("download_url")
	title := r.FormValue("title")
	if downloadURL == "" {
		toast(w, "error", "Missing download URL")
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	_, client, err := h.dialDownloader(r.Context())
	if err != nil {
		toast(w, "error", "Downloader unavailable")
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	resp, err := client.AddTorrent(r.Context(), &downloaderv1.AddTorrentRequest{
		Uri:   downloadURL,
		Label: moduleID,
	})
	if err != nil {
		slog.Warn("media: add download failed", "error", err)
		toast(w, "error", "Download failed: "+err.Error())
		http.Redirect(w, r, "/media/"+moduleID, http.StatusSeeOther)
		return
	}

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "media.download", moduleID, resp.GetId(), map[string]string{
		"title": title,
	})
	toast(w, "success", fmt.Sprintf("Downloading: %s", title))
	http.Redirect(w, r, "/downloads", http.StatusSeeOther)
}

// MediaSearchResultsJSON handles GET /media/search-results?q=... for HTMX search-as-you-type
func (h *Handler) MediaSearchResultsJSON(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	_, client, err := h.dialMetadata(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	resp, err := client.Search(r.Context(), &metadatav1.SearchRequest{
		Query: query,
		Type:  metadatav1.MediaType_MEDIA_TYPE_MOVIE,
	})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	type result struct {
		ID        int32   `json:"id"`
		Title     string  `json:"title"`
		Year      int32   `json:"year"`
		MediaType string  `json:"media_type"`
		Poster    string  `json:"poster"`
		VoteAvg   float64 `json:"vote_average"`
	}

	results := make([]result, 0, len(resp.GetResults()))
	for _, r := range resp.GetResults() {
		yr := int32(0)
		if len(r.GetReleaseDate()) >= 4 {
			if y, err := strconv.Atoi(r.GetReleaseDate()[:4]); err == nil {
				yr = int32(y)
			}
		}
		mt := "unknown"
		switch r.GetMediaType() {
		case metadatav1.MediaType_MEDIA_TYPE_MOVIE:
			mt = "movie"
		case metadatav1.MediaType_MEDIA_TYPE_TV:
			mt = "tv"
		}
		results = append(results, result{
			ID:        r.GetId(),
			Title:     r.GetTitle(),
			Year:      yr,
			MediaType: mt,
			Poster:    r.GetPosterPath(),
			VoteAvg:   r.GetVoteAverage(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func parseMetaType(t string) metadatav1.MediaType {
	switch t {
	case "tv", "TV":
		return metadatav1.MediaType_MEDIA_TYPE_TV
	case "movie", "Movie", "MOVIE":
		return metadatav1.MediaType_MEDIA_TYPE_MOVIE
	default:
		return metadatav1.MediaType_MEDIA_TYPE_UNSPECIFIED
	}
}
