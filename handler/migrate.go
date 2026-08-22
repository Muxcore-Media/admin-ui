package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	"github.com/Muxcore-Media/admin-ui/arrmigrate"
	templates "github.com/Muxcore-Media/admin-ui/templ"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	musicv1 "github.com/Muxcore-Media/media-music/proto/gen/muxcore/music/v1"
)

type movieImporterAdapter struct {
	client mgmntv1.MovieManagementServiceClient
}

func (a movieImporterAdapter) ImportMovie(ctx context.Context, title string, year, tmdbID int, qualityProfileID, rootFolder string, monitored bool) (string, error) {
	resp, err := a.client.AddMovie(ctx, &mgmntv1.AddMovieRequest{
		TmdbId:           int32(tmdbID),
		Title:            title,
		Year:             int32(year),
		QualityProfileId: qualityProfileID,
		RootFolderPath:   rootFolder,
	})
	if err != nil {
		return "", err
	}
	id := resp.GetMovieId()
	if id != "" {
		_, _ = a.client.UpdateMovie(ctx, &mgmntv1.UpdateMovieRequest{
			MovieId:  id,
			Monitored: proto.Bool(monitored),
		})
	}
	return id, nil
}

type tvImporterAdapter struct {
	client tvmgmtv1.TvManagementServiceClient
}

func (a tvImporterAdapter) ImportSeries(ctx context.Context, title string, year, tmdbID int, qualityProfileID, rootFolder string, monitored bool) (string, error) {
	resp, err := a.client.AddTVShow(ctx, &tvmgmtv1.AddTVShowRequest{
		TmdbId:           int32(tmdbID),
		Name:             title,
		Year:             int32(year),
		QualityProfileId: qualityProfileID,
		RootFolderPath:   rootFolder,
	})
	if err != nil {
		return "", err
	}
	id := resp.GetSeriesId()
	if id != "" {
		_, _ = a.client.UpdateTVShow(ctx, &tvmgmtv1.UpdateTVShowRequest{
			SeriesId:  id,
			Monitored: proto.Bool(monitored),
		})
	}
	return id, nil
}

type musicImporterAdapter struct {
	client musicv1.MusicManagementServiceClient
}

func (a musicImporterAdapter) ImportArtist(ctx context.Context, name, musicbrainzID, qualityProfileID, rootFolder string, monitored bool) (string, error) {
	resp, err := a.client.AddArtist(ctx, &musicv1.AddArtistRequest{
		Name:             name,
		MusicbrainzId:    musicbrainzID,
		Monitored:        monitored,
		QualityProfileId: qualityProfileID,
		RootFolderPath:   rootFolder,
	})
	if err != nil {
		return "", err
	}
	if ar := resp.GetArtist(); ar != nil {
		return ar.GetId(), nil
	}
	return "", nil
}

func (h *Handler) resolveMigrateImporters(ctx context.Context) (arrmigrate.MovieImporter, arrmigrate.TVImporter, arrmigrate.MusicImporter, func(), error) {
	if h.MigrateMovies != nil || h.MigrateTV != nil || h.MigrateMusic != nil {
		return h.MigrateMovies, h.MigrateTV, h.MigrateMusic, func() {}, nil
	}
	var movies arrmigrate.MovieImporter
	var tv arrmigrate.TVImporter
	var music arrmigrate.MusicImporter
	var closers []func()

	if addr, err := h.findCapabilityDialAddr(ctx, "media.library.movies"); err == nil {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			closers = append(closers, func() { _ = conn.Close() })
			movies = movieImporterAdapter{client: mgmntv1.NewMovieManagementServiceClient(conn)}
		}
	}
	if addr, err := h.findCapabilityDialAddr(ctx, "media.library.tv"); err == nil {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			closers = append(closers, func() { _ = conn.Close() })
			tv = tvImporterAdapter{client: tvmgmtv1.NewTvManagementServiceClient(conn)}
		}
	}
	if addr, err := h.findCapabilityDialAddr(ctx, "media.library.music"); err == nil {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			closers = append(closers, func() { _ = conn.Close() })
			music = musicImporterAdapter{client: musicv1.NewMusicManagementServiceClient(conn)}
		}
	}
	closeAll := func() {
		for _, c := range closers {
			c()
		}
	}
	if movies == nil && tv == nil && music == nil {
		closeAll()
		return nil, nil, nil, nil, fmt.Errorf("no media.library movies, tv, or music module")
	}
	return movies, tv, music, closeAll, nil
}

func (h *Handler) findCapabilityDialAddr(ctx context.Context, cap string) (string, error) {
	if h.Core == nil {
		return "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, cap)
	if err != nil {
		return "", err
	}
	if len(mods) == 0 {
		return "", fmt.Errorf("no module with capability %s", cap)
	}
	addr := normalizeDialAddr(mods[0].GetId(), mods[0].GetHttpAddr())
	if addr == "" {
		return "", fmt.Errorf("%s has no dial address", cap)
	}
	return addr, nil
}

func (h *Handler) resolveProfileByName(ctx context.Context, name string) string {
	if name == "" {
		return ""
	}
	if h.ResolveProfileID != nil {
		return h.ResolveProfileID(ctx, name)
	}
	for _, p := range h.listProfileOptions(ctx) {
		if strings.EqualFold(p.Name, name) {
			return p.ID
		}
	}
	return ""
}

func (h *Handler) MigratePage(w http.ResponseWriter, r *http.Request) {
	data := templates.MigratePageData{
		Service: "radarr",
		DryRun:  true,
	}
	h.render(w, r, templates.Layout("Arr Migrate", h.nav(r.URL.Path), templates.MigratePage(data)))
}

func (h *Handler) MigratePost(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/migrate", http.StatusSeeOther)
		return
	}
	data := templates.MigratePageData{
		Service: strings.ToLower(strings.TrimSpace(r.FormValue("service"))),
		BaseURL: strings.TrimSpace(r.FormValue("base_url")),
		APIKey:  strings.TrimSpace(r.FormValue("api_key")),
		DryRun:  r.FormValue("dry_run") == "1",
	}
	if data.Service == "" {
		data.Service = "radarr"
	}

	cli := &arrmigrate.Client{HTTP: h.ArrHTTPClient}
	var items []arrmigrate.Item
	var err error
	switch data.Service {
	case "radarr":
		items, err = cli.FetchRadarr(ctx, data.BaseURL, data.APIKey)
	case "sonarr":
		items, err = cli.FetchSonarr(ctx, data.BaseURL, data.APIKey)
	case "lidarr":
		items, err = cli.FetchLidarr(ctx, data.BaseURL, data.APIKey)
	default:
		err = fmt.Errorf("service must be radarr, sonarr, or lidarr")
	}
	if err != nil {
		data.Error = err.Error()
		h.render(w, r, templates.Layout("Arr Migrate", h.nav("/migrate"), templates.MigratePage(data)))
		return
	}

	movies, tv, music, closer, resolveErr := h.resolveMigrateImporters(ctx)
	if !data.DryRun {
		if resolveErr != nil {
			data.Error = resolveErr.Error()
			h.render(w, r, templates.Layout("Arr Migrate", h.nav("/migrate"), templates.MigratePage(data)))
			return
		}
		defer closer()
	} else if closer != nil {
		closer()
	}

	res := arrmigrate.Run(ctx, items, data.DryRun, movies, tv, music, h.resolveProfileByName)
	data.Result = migrateResultToView(res)
	if sess := SessionFromContext(r.Context()); sess != nil && !data.DryRun {
		h.auditLog(r.Context(), sess.UserID, "admin.migrate.arr", "migrate", data.Service, map[string]string{
			"fetched":  fmt.Sprintf("%d", res.Fetched),
			"imported": fmt.Sprintf("%d", res.Imported),
			"skipped":  fmt.Sprintf("%d", res.Skipped),
		})
	}
	if len(res.Errors) > 0 {
		slog.Info("arr migrate completed with errors", "service", data.Service, "errors", len(res.Errors))
	}
	h.render(w, r, templates.Layout("Arr Migrate", h.nav("/migrate"), templates.MigratePage(data)))
}

func migrateResultToView(res arrmigrate.Result) *templates.MigrateResultView {
	v := &templates.MigrateResultView{
		DryRun:   res.DryRun,
		Fetched:  res.Fetched,
		Imported: res.Imported,
		Skipped:  res.Skipped,
		Errors:   res.Errors,
	}
	const previewCap = 100
	for i, it := range res.Items {
		if i >= previewCap {
			break
		}
		v.Items = append(v.Items, templates.MigrateItemRow{
			Source:             it.Source,
			Title:              it.Title,
			Year:               it.Year,
			TMDBID:             it.TMDBID,
			TVDBID:             it.TVDBID,
			Monitored:          it.Monitored,
			QualityProfileName: it.QualityProfileName,
			RootFolderPath:     it.RootFolderPath,
		})
	}
	return v
}
