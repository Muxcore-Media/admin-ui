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

	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaSubtitles   = "media.subtitles"
	subtitlesDialTimeout = 3 * time.Second
	subtitlesReadTimeout = 5 * time.Second
)

func (h *Handler) withSubtitlesClient(ctx context.Context) (subtv1.SubtitleServiceClient, func(), error) {
	if h.Core == nil {
		return nil, nil, fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaSubtitles)
	if err != nil {
		return nil, nil, err
	}
	if len(mods) == 0 {
		return nil, nil, fmt.Errorf("no module with capability %s", capMediaSubtitles)
	}
	mod := mods[0]
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return nil, nil, fmt.Errorf("subtitles module has no dial address")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return subtv1.NewSubtitleServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (h *Handler) SubtitlesPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesDialTimeout+2*subtitlesReadTimeout)
	defer cancel()

	data := templates.SubtitlesPageData{
		Flash: r.URL.Query().Get("status"),
		Error: r.URL.Query().Get("error"),
	}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		data.Error = err.Error()
		h.renderSubtitles(w, r, data)
		return
	}
	defer closer()

	rCtx, rCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
	defer rCancel()

	if wanted, err := client.ListWanted(rCtx, &subtv1.ListWantedRequest{Page: 1, PageSize: 50}); err != nil {
		slog.Warn("subtitles ListWanted", "error", err)
		if data.Error == "" {
			data.Error = err.Error()
		}
	} else {
		data.WantedTot = int(wanted.GetTotal())
		for _, it := range wanted.GetItems() {
			data.Wanted = append(data.Wanted, templates.SubtitlesWantedItem{
				ID: it.GetId(), MediaID: it.GetMediaId(), Title: it.GetTitle(), Language: it.GetLanguage(),
				Type: it.GetMediaType(), Season: int(it.GetSeason()), Episode: int(it.GetEpisode()),
			})
		}
	}

	if hist, err := client.ListHistory(rCtx, &subtv1.ListHistoryRequest{Page: 1, PageSize: 40}); err == nil {
		data.HistoryTot = int(hist.GetTotal())
		for _, e := range hist.GetEntries() {
			data.History = append(data.History, templates.SubtitlesHistoryItem{
				Title: e.GetTitle(), Language: e.GetLanguage(), Provider: e.GetProvider(),
				Action: e.GetAction(), Score: int(e.GetScore()), At: e.GetCreatedAt(),
			})
		}
	}

	if bl, err := client.ListBlacklist(rCtx, &subtv1.ListBlacklistRequest{Page: 1, PageSize: 40}); err == nil {
		for _, e := range bl.GetEntries() {
			data.Blacklist = append(data.Blacklist, templates.SubtitlesBlacklistItem{
				ID: e.GetId(), Provider: e.GetProvider(), Title: e.GetTitle(),
				Language: e.GetLanguage(), Reason: e.GetReason(), At: e.GetCreatedAt(),
			})
		}
	}

	if prov, err := client.ListProviders(rCtx, &subtv1.ListProvidersRequest{}); err == nil {
		for _, p := range prov.GetProviders() {
			data.Providers = append(data.Providers, templates.SubtitlesProviderItem{
				ID: p.GetId(), Name: p.GetName(), Enabled: p.GetEnabled(), Implemented: p.GetImplemented(),
			})
		}
	}

	if media, err := client.ListMedia(rCtx, &subtv1.ListMediaRequest{Page: 1, PageSize: 80}); err == nil {
		data.MediaTot = int(media.GetTotal())
		for _, it := range media.GetItems() {
			data.Media = append(data.Media, templates.SubtitlesMediaItem{
				ID: it.GetId(), Title: it.GetTitle(), Type: it.GetMediaType(),
				Monitored: it.GetMonitored(), ProfileID: it.GetLanguageProfileId(),
				Season: int(it.GetSeason()), Episode: int(it.GetEpisode()), SeriesID: it.GetSeriesId(),
			})
		}
	}

	if profs, err := client.ListLanguageProfiles(rCtx, &subtv1.ListLanguageProfilesRequest{}); err == nil {
		for _, p := range profs.GetProfiles() {
			langs := make([]string, 0, len(p.GetLanguages()))
			for _, lr := range p.GetLanguages() {
				label := lr.GetLanguage()
				if lr.GetHearingImpaired() {
					label += "+HI"
				}
				if lr.GetForced() {
					label += "+forced"
				}
				langs = append(langs, label)
			}
			data.Profiles = append(data.Profiles, templates.SubtitlesProfileItem{
				ID: p.GetId(), Name: p.GetName(), Languages: strings.Join(langs, ", "), IsDefault: p.GetIsDefault(),
			})
		}
	}

	if langs, err := client.ListLanguages(rCtx, &subtv1.ListLanguagesRequest{}); err == nil {
		data.LangTot = len(langs.GetLanguages())
	}

	q := r.URL.Query().Get("q")
	imdb := r.URL.Query().Get("imdb")
	lang := r.URL.Query().Get("lang")
	if q != "" || imdb != "" {
		season, _ := strconv.Atoi(r.URL.Query().Get("season"))
		episode, _ := strconv.Atoi(r.URL.Query().Get("episode"))
		sCtx, sCancel := context.WithTimeout(pageCtx, 20*time.Second)
		search, err := client.Search(sCtx, &subtv1.SearchRequest{
			Query: q, ImdbId: imdb, Language: lang, Season: int32(season), Episode: int32(episode),
		})
		sCancel()
		if err != nil {
			if data.Error == "" {
				data.Error = err.Error()
			}
		} else {
			for i, hit := range search.GetResults() {
				if i >= 40 {
					break
				}
				data.SearchHits = append(data.SearchHits, templates.SubtitlesSearchHit{
					FileID: hit.GetFileId(), Provider: firstNonEmpty(hit.GetProvider(), hit.GetSource()),
					Language: hit.GetLanguage(), Release: hit.GetReleaseName(), Score: int(hit.GetScore()),
				})
			}
		}
	}

	h.renderSubtitles(w, r, data)
}

func (h *Handler) renderSubtitles(w http.ResponseWriter, r *http.Request, data templates.SubtitlesPageData) {
	templates.Layout("Subtitles", h.nav(r.URL.Path), templates.SubtitlesPage(data)).Render(r.Context(), w)
}

func (h *Handler) SubtitlesSync(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.SyncLibrary(ctx, &subtv1.SyncLibraryRequest{})
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("synced movies=%d episodes=%d wanted=%d", resp.GetMovies(), resp.GetEpisodes(), resp.GetWanted())
	http.Redirect(w, r, "/subtitles?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) SubtitlesSearchWanted(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.SearchWanted(ctx, &subtv1.SearchWantedRequest{Limit: 25})
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("searched=%d downloaded=%d", resp.GetSearched(), resp.GetDownloaded())
	http.Redirect(w, r, "/subtitles?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) SubtitlesUpgrade(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.UpgradeSubtitles(ctx, &subtv1.UpgradeSubtitlesRequest{})
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("checked=%d upgraded=%d", resp.GetChecked(), resp.GetUpgraded())
	http.Redirect(w, r, "/subtitles?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) SubtitlesProviderToggle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	enabled := r.FormValue("enabled") == "true"
	ctx, cancel := context.WithTimeout(r.Context(), subtitlesReadTimeout)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.SetProviderEnabled(ctx, &subtv1.SetProviderEnabledRequest{Id: id, Enabled: enabled}); err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/subtitles?status=provider_updated", http.StatusSeeOther)
}

func (h *Handler) SubtitlesBlacklistRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), subtitlesReadTimeout)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.RemoveBlacklist(ctx, &subtv1.RemoveBlacklistRequest{Id: id}); err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/subtitles?status=blacklist_removed", http.StatusSeeOther)
}

func (h *Handler) SubtitlesMassEdit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	ids := r.Form["media_id"]
	action := r.FormValue("action")
	if len(ids) == 0 {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape("select at least one media item"), http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()

	switch action {
	case "monitor", "unmonitor":
		resp, err := client.MassEditMedia(ctx, &subtv1.MassEditMediaRequest{
			MediaIds: ids, SetMonitored: true, Monitored: action == "monitor",
		})
		if err != nil {
			http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/subtitles?status="+url.QueryEscape(fmt.Sprintf("updated=%d", resp.GetUpdated())), http.StatusSeeOther)
	case "profile":
		pid := r.FormValue("profile_id")
		resp, err := client.MassEditMedia(ctx, &subtv1.MassEditMediaRequest{
			MediaIds: ids, LanguageProfileId: pid,
		})
		if err != nil {
			http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/subtitles?status="+url.QueryEscape(fmt.Sprintf("updated=%d", resp.GetUpdated())), http.StatusSeeOther)
	case "search":
		resp, err := client.SearchWanted(ctx, &subtv1.SearchWantedRequest{MediaIds: ids, Limit: int32(len(ids) * 4)})
		if err != nil {
			http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		msg := fmt.Sprintf("searched=%d downloaded=%d", resp.GetSearched(), resp.GetDownloaded())
		http.Redirect(w, r, "/subtitles?status="+url.QueryEscape(msg), http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape("unknown action"), http.StatusSeeOther)
	}
}

func (h *Handler) SubtitlesClearHistory(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), subtitlesReadTimeout)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.ClearHistory(ctx, &subtv1.ClearHistoryRequest{}); err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/subtitles?status=history_cleared", http.StatusSeeOther)
}

func (h *Handler) SubtitlesDownload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	score, _ := strconv.Atoi(r.FormValue("score"))
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.DownloadSubtitle(ctx, &subtv1.DownloadSubtitleRequest{
		Provider: r.FormValue("provider"), FileId: r.FormValue("file_id"),
		Language: r.FormValue("language"), ReleaseName: r.FormValue("release"),
		Score: int32(score), MediaFileId: r.FormValue("media_file_id"),
	})
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := "downloaded"
	if resp.GetSubtitle() != nil {
		msg = fmt.Sprintf("downloaded %s", resp.GetSubtitle().GetId())
	}
	ret := r.FormValue("return_to")
	if ret == "" || !strings.HasPrefix(ret, "/subtitles") {
		ret = "/subtitles"
	}
	http.Redirect(w, r, ret+"?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) SubtitlesUpsertProfile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	rawLangs := strings.TrimSpace(r.FormValue("languages"))
	if name == "" || rawLangs == "" {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape("name and languages required"), http.StatusSeeOther)
		return
	}
	var reqs []*subtv1.LanguageRequirement
	for _, part := range strings.Split(rawLangs, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		lr := &subtv1.LanguageRequirement{}
		bits := strings.Split(part, "+")
		lr.Language = bits[0]
		for _, b := range bits[1:] {
			switch b {
			case "hi", "hearing_impaired", "sdh":
				lr.HearingImpaired = true
			case "forced", "force":
				lr.Forced = true
			}
		}
		reqs = append(reqs, lr)
	}
	if len(reqs) == 0 {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape("no valid languages"), http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), subtitlesReadTimeout)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.UpsertLanguageProfile(ctx, &subtv1.UpsertLanguageProfileRequest{
		Profile: &subtv1.LanguageProfile{
			Name: name, Languages: reqs, IsDefault: r.FormValue("is_default") == "true",
		},
	})
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := "profile_saved"
	if resp.GetProfile() != nil {
		msg = fmt.Sprintf("profile=%s", resp.GetProfile().GetName())
	}
	http.Redirect(w, r, "/subtitles?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) SubtitlesMediaDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesDialTimeout+25*time.Second)
	defer cancel()
	data := templates.SubtitlesMediaDetailData{
		Flash: r.URL.Query().Get("status"),
		Error: r.URL.Query().Get("error"),
	}
	client, closer, err := h.withSubtitlesClient(pageCtx)
	if err != nil {
		data.Error = err.Error()
		templates.Layout("Subtitles", h.nav("/subtitles"), templates.SubtitlesMediaDetailPage(data)).Render(r.Context(), w)
		return
	}
	defer closer()

	media, err := client.GetMedia(pageCtx, &subtv1.GetMediaRequest{Id: id})
	if err != nil {
		data.Error = err.Error()
		templates.Layout("Subtitles", h.nav("/subtitles"), templates.SubtitlesMediaDetailPage(data)).Render(r.Context(), w)
		return
	}
	it := media.GetItem()
	data.Item = templates.SubtitlesMediaDetailItem{
		ID: it.GetId(), Title: it.GetTitle(), Type: it.GetMediaType(),
		ImdbID: it.GetImdbId(), TmdbID: int(it.GetTmdbId()), Year: int(it.GetYear()),
		SeriesID: it.GetSeriesId(), Series: it.GetSeriesName(),
		Season: int(it.GetSeason()), Episode: int(it.GetEpisode()),
		VideoPath: it.GetVideoPath(), FileID: it.GetMediaFileId(),
		ProfileID: it.GetLanguageProfileId(), Monitored: it.GetMonitored(), HasFile: it.GetHasFile(),
	}

	if it.GetMediaFileId() != "" {
		if subs, err := client.ListSubtitles(pageCtx, &subtv1.ListSubtitlesRequest{MediaFileId: it.GetMediaFileId(), Page: 1, PageSize: 100}); err == nil {
			for _, s := range subs.GetSubtitles() {
				data.Subtitles = append(data.Subtitles, templates.SubtitlesFileItem{
					ID: s.GetId(), Language: s.GetLanguage(), Provider: firstNonEmpty(s.GetProvider(), s.GetSource()),
					Format: s.GetFormat(), Score: int(s.GetScore()), Forced: s.GetForced(), HI: s.GetHearingImpaired(),
					Path: s.GetFilePath(),
				})
			}
		}
	}

	if wanted, err := client.ListWanted(pageCtx, &subtv1.ListWantedRequest{Page: 1, PageSize: 100}); err == nil {
		for _, w := range wanted.GetItems() {
			if w.GetId() == id || w.GetMediaId() == id {
				data.Wanted = append(data.Wanted, templates.SubtitlesWantedItem{
					ID: w.GetId(), MediaID: w.GetMediaId(), Title: w.GetTitle(), Language: w.GetLanguage(),
					Type: w.GetMediaType(), Season: int(w.GetSeason()), Episode: int(w.GetEpisode()),
				})
			}
		}
	}

	if profs, err := client.ListLanguageProfiles(pageCtx, &subtv1.ListLanguageProfilesRequest{}); err == nil {
		for _, p := range profs.GetProfiles() {
			data.Profiles = append(data.Profiles, templates.SubtitlesProfileItem{ID: p.GetId(), Name: p.GetName(), IsDefault: p.GetIsDefault()})
		}
	}

	if it.GetSeriesId() != "" && it.GetMediaType() == "episode" {
		// no-op; series list is on series page
	}

	doSearch := r.URL.Query().Get("q") != ""
	if doSearch {
		query := it.GetTitle()
		if it.GetSeriesName() != "" {
			query = it.GetSeriesName()
		}
		sCtx, sCancel := context.WithTimeout(pageCtx, 20*time.Second)
		search, err := client.Search(sCtx, &subtv1.SearchRequest{
			Query: query, ImdbId: it.GetImdbId(), Season: it.GetSeason(), Episode: it.GetEpisode(),
		})
		sCancel()
		if err != nil {
			if data.Error == "" {
				data.Error = err.Error()
			}
		} else {
			for i, hit := range search.GetResults() {
				if i >= 40 {
					break
				}
				data.SearchHits = append(data.SearchHits, templates.SubtitlesSearchHit{
					FileID: hit.GetFileId(), Provider: firstNonEmpty(hit.GetProvider(), hit.GetSource()),
					Language: hit.GetLanguage(), Release: hit.GetReleaseName(), Score: int(hit.GetScore()),
				})
			}
		}
	}

	templates.Layout(it.GetTitle(), h.nav("/subtitles"), templates.SubtitlesMediaDetailPage(data)).Render(r.Context(), w)
}

func (h *Handler) SubtitlesSeriesDetail(w http.ResponseWriter, r *http.Request) {
	seriesID := r.PathValue("id")
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesDialTimeout+10*time.Second)
	defer cancel()
	data := templates.SubtitlesMediaDetailData{
		Flash: r.URL.Query().Get("status"),
		Error: r.URL.Query().Get("error"),
		Item:  templates.SubtitlesMediaDetailItem{ID: seriesID, Title: seriesID, Type: "series", SeriesID: seriesID},
	}
	client, closer, err := h.withSubtitlesClient(pageCtx)
	if err != nil {
		data.Error = err.Error()
		templates.Layout("Series", h.nav("/subtitles"), templates.SubtitlesMediaDetailPage(data)).Render(r.Context(), w)
		return
	}
	defer closer()

	eps, err := client.ListMedia(pageCtx, &subtv1.ListMediaRequest{SeriesId: seriesID, Page: 1, PageSize: 500})
	if err != nil {
		data.Error = err.Error()
	} else {
		for _, it := range eps.GetItems() {
			if data.Item.Title == seriesID && it.GetSeriesName() != "" {
				data.Item.Title = it.GetSeriesName()
				data.Item.Series = it.GetSeriesName()
			}
			data.Episodes = append(data.Episodes, templates.SubtitlesMediaItem{
				ID: it.GetId(), Title: it.GetTitle(), Type: it.GetMediaType(),
				Monitored: it.GetMonitored(), Season: int(it.GetSeason()), Episode: int(it.GetEpisode()),
				SeriesID: it.GetSeriesId(),
			})
		}
	}
	templates.Layout(data.Item.Title, h.nav("/subtitles"), templates.SubtitlesMediaDetailPage(data)).Render(r.Context(), w)
}

func (h *Handler) SubtitlesMediaSearchWanted(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.SearchWanted(ctx, &subtv1.SearchWantedRequest{MediaIds: []string{id}, Limit: 8})
	if err != nil {
		http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("searched=%d downloaded=%d", resp.GetSearched(), resp.GetDownloaded())
	http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) SubtitlesMediaSetProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), subtitlesReadTimeout)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.SetMediaLanguageProfile(ctx, &subtv1.SetMediaLanguageProfileRequest{
		MediaId: id, LanguageProfileId: r.FormValue("profile_id"),
	}); err != nil {
		http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?status=profile_updated", http.StatusSeeOther)
}

func (h *Handler) SubtitlesFileDelete(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("id")
	mediaID := r.FormValue("media_id")
	ctx, cancel := context.WithTimeout(r.Context(), subtitlesReadTimeout)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.Delete(ctx, &subtv1.DeleteRequest{Id: fileID}); err != nil {
		ret := "/subtitles"
		if mediaID != "" {
			ret = "/subtitles/media/" + url.PathEscape(mediaID)
		}
		http.Redirect(w, r, ret+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	ret := "/subtitles?status=deleted"
	if mediaID != "" {
		ret = "/subtitles/media/" + url.PathEscape(mediaID) + "?status=deleted"
	}
	http.Redirect(w, r, ret, http.StatusSeeOther)
}

func (h *Handler) SubtitlesTestArr(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	target := r.FormValue("target")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client, closer, err := h.withSubtitlesClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.TestArrConnection(ctx, &subtv1.TestArrConnectionRequest{Target: target})
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if !resp.GetOk() {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(resp.GetMessage()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/subtitles?status="+url.QueryEscape(resp.GetMessage()), http.StatusSeeOther)
}
