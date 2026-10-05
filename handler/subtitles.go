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

	"github.com/Muxcore-Media/admin-ui/internal/meshdial"

	subtv1 "github.com/Muxcore-Media/media-subtitles/proto/subtv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaSubtitles          = "media.subtitles"
	subtitlesDialTimeout       = 3 * time.Second
	subtitlesReadTimeout       = 5 * time.Second
	subtitlesPageTimeout       = subtitlesDialTimeout + 7*subtitlesReadTimeout + time.Second
	subtitlesDetailPageTimeout = subtitlesDialTimeout + 4*subtitlesReadTimeout + subtitlesSearchTimeout + time.Second
	subtitlesSeriesPageTimeout = subtitlesDialTimeout + subtitlesReadTimeout + time.Second
	subtitlesSearchTimeout     = 20 * time.Second
	subtitlesActionTimeout     = subtitlesDialTimeout + subtitlesReadTimeout + time.Second
	subtitlesSyncTimeout       = 30 * time.Second
	subtitlesBatchTimeout      = 60 * time.Second
	subtitlesTestTimeout       = 15 * time.Second
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
	conn, err := meshdial.NewClient(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return subtv1.NewSubtitleServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (h *Handler) SubtitlesPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesPageTimeout)
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

	readCtx, readCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
	wanted, err := client.ListWanted(readCtx, &subtv1.ListWantedRequest{Page: 1, PageSize: 50})
	readCancel()
	if err != nil {
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

	readCtx, readCancel = context.WithTimeout(pageCtx, subtitlesReadTimeout)
	hist, err := client.ListHistory(readCtx, &subtv1.ListHistoryRequest{Page: 1, PageSize: 40})
	readCancel()
	if err == nil {
		data.HistoryTot = int(hist.GetTotal())
		for _, e := range hist.GetEntries() {
			data.History = append(data.History, templates.SubtitlesHistoryItem{
				Title: e.GetTitle(), Language: e.GetLanguage(), Provider: e.GetProvider(),
				Action: e.GetAction(), Score: int(e.GetScore()), At: e.GetCreatedAt(),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, subtitlesReadTimeout)
	bl, err := client.ListBlacklist(readCtx, &subtv1.ListBlacklistRequest{Page: 1, PageSize: 40})
	readCancel()
	if err == nil {
		for _, e := range bl.GetEntries() {
			data.Blacklist = append(data.Blacklist, templates.SubtitlesBlacklistItem{
				ID: e.GetId(), Provider: e.GetProvider(), Title: e.GetTitle(),
				Language: e.GetLanguage(), Reason: e.GetReason(), At: e.GetCreatedAt(),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, subtitlesReadTimeout)
	prov, err := client.ListProviders(readCtx, &subtv1.ListProvidersRequest{})
	readCancel()
	if err == nil {
		for _, p := range prov.GetProviders() {
			data.Providers = append(data.Providers, templates.SubtitlesProviderItem{
				ID: p.GetId(), Name: p.GetName(), Enabled: p.GetEnabled(), Implemented: p.GetImplemented(),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, subtitlesReadTimeout)
	media, err := client.ListMedia(readCtx, &subtv1.ListMediaRequest{Page: 1, PageSize: 80})
	readCancel()
	if err == nil {
		data.MediaTot = int(media.GetTotal())
		for _, it := range media.GetItems() {
			data.Media = append(data.Media, templates.SubtitlesMediaItem{
				ID: it.GetId(), Title: it.GetTitle(), Type: it.GetMediaType(),
				Monitored: it.GetMonitored(), ProfileID: it.GetLanguageProfileId(),
				Season: int(it.GetSeason()), Episode: int(it.GetEpisode()), SeriesID: it.GetSeriesId(),
			})
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, subtitlesReadTimeout)
	profs, err := client.ListLanguageProfiles(readCtx, &subtv1.ListLanguageProfilesRequest{})
	readCancel()
	if err == nil {
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

	readCtx, readCancel = context.WithTimeout(pageCtx, subtitlesReadTimeout)
	langs, err := client.ListLanguages(readCtx, &subtv1.ListLanguagesRequest{})
	readCancel()
	if err == nil {
		data.LangTot = len(langs.GetLanguages())
	}

	q := r.URL.Query().Get("q")
	imdb := r.URL.Query().Get("imdb")
	lang := r.URL.Query().Get("lang")
	if q != "" || imdb != "" {
		season, _ := strconv.Atoi(r.URL.Query().Get("season"))
		episode, _ := strconv.Atoi(r.URL.Query().Get("episode"))
		sCtx, sCancel := context.WithTimeout(pageCtx, subtitlesSearchTimeout)
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
					Language: hit.GetLanguage(), Release: hit.GetReleaseName(), Score: subtitleCandidateScore(hit),
				})
			}
		}
	}

	h.renderSubtitles(w, r, data)
}

func (h *Handler) renderSubtitles(w http.ResponseWriter, r *http.Request, data templates.SubtitlesPageData) {
	_ = templates.Layout("Subtitles", h.nav(r.URL.Path), templates.SubtitlesPage(data)).Render(r.Context(), w)
}

func (h *Handler) SubtitlesSync(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesSyncTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.SyncLibrary(pageCtx, &subtv1.SyncLibraryRequest{})
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("synced movies=%d episodes=%d wanted=%d", resp.GetMovies(), resp.GetEpisodes(), resp.GetWanted())
	http.Redirect(w, r, "/subtitles?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) SubtitlesSearchWanted(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesBatchTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.SearchWanted(pageCtx, &subtv1.SearchWantedRequest{Limit: 25})
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("searched=%d downloaded=%d", resp.GetSearched(), resp.GetDownloaded())
	http.Redirect(w, r, "/subtitles?status="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (h *Handler) SubtitlesUpgrade(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesBatchTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.UpgradeSubtitles(pageCtx, &subtv1.UpgradeSubtitlesRequest{})
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
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
	_, err = client.SetProviderEnabled(readCtx, &subtv1.SetProviderEnabledRequest{Id: id, Enabled: enabled})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/subtitles?status=provider_updated", http.StatusSeeOther)
}

func (h *Handler) SubtitlesBlacklistRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
	_, err = client.RemoveBlacklist(readCtx, &subtv1.RemoveBlacklistRequest{Id: id})
	readCancel()
	if err != nil {
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
	ctx, cancel := context.WithTimeout(r.Context(), subtitlesBatchTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(ctx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
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
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
	_, err = client.ClearHistory(readCtx, &subtv1.ClearHistoryRequest{})
	readCancel()
	if err != nil {
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
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesBatchTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.DownloadSubtitle(pageCtx, &subtv1.DownloadSubtitleRequest{
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
	ctx, cancel := context.WithTimeout(r.Context(), subtitlesActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(ctx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(ctx, subtitlesReadTimeout)
	resp, err := client.UpsertLanguageProfile(readCtx, &subtv1.UpsertLanguageProfileRequest{
		Profile: &subtv1.LanguageProfile{
			Name: name, Languages: reqs, IsDefault: r.FormValue("is_default") == "true",
		},
	})
	readCancel()
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
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesDetailPageTimeout)
	defer cancel()
	data := templates.SubtitlesMediaDetailData{
		Flash: r.URL.Query().Get("status"),
		Error: r.URL.Query().Get("error"),
	}
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		data.Error = err.Error()
		_ = templates.Layout("Subtitles", h.nav("/subtitles"), templates.SubtitlesMediaDetailPage(data)).Render(r.Context(), w)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
	media, err := client.GetMedia(readCtx, &subtv1.GetMediaRequest{Id: id})
	readCancel()
	if err != nil {
		data.Error = err.Error()
		_ = templates.Layout("Subtitles", h.nav("/subtitles"), templates.SubtitlesMediaDetailPage(data)).Render(r.Context(), w)
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
		readCtx, readCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
		subs, err := client.ListSubtitles(readCtx, &subtv1.ListSubtitlesRequest{MediaFileId: it.GetMediaFileId(), Page: 1, PageSize: 100})
		readCancel()
		if err == nil {
			for _, s := range subs.GetSubtitles() {
				data.Subtitles = append(data.Subtitles, templates.SubtitlesFileItem{
					ID: s.GetId(), Language: s.GetLanguage(), Provider: firstNonEmpty(s.GetProvider(), s.GetSource()),
					Format: s.GetFormat(), Score: int(s.GetScore()), Forced: s.GetForced(), HI: s.GetHearingImpaired(),
					Path: s.GetFilePath(),
				})
			}
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, subtitlesReadTimeout)
	wanted, err := client.ListWanted(readCtx, &subtv1.ListWantedRequest{Page: 1, PageSize: 100})
	readCancel()
	if err == nil {
		for _, w := range wanted.GetItems() {
			if w.GetId() == id || w.GetMediaId() == id {
				data.Wanted = append(data.Wanted, templates.SubtitlesWantedItem{
					ID: w.GetId(), MediaID: w.GetMediaId(), Title: w.GetTitle(), Language: w.GetLanguage(),
					Type: w.GetMediaType(), Season: int(w.GetSeason()), Episode: int(w.GetEpisode()),
				})
			}
		}
	}

	readCtx, readCancel = context.WithTimeout(pageCtx, subtitlesReadTimeout)
	profs, err := client.ListLanguageProfiles(readCtx, &subtv1.ListLanguageProfilesRequest{})
	readCancel()
	if err == nil {
		for _, p := range profs.GetProfiles() {
			data.Profiles = append(data.Profiles, templates.SubtitlesProfileItem{ID: p.GetId(), Name: p.GetName(), IsDefault: p.GetIsDefault()})
		}
	}

	doSearch := r.URL.Query().Get("q") != ""
	if doSearch {
		query := it.GetTitle()
		if it.GetSeriesName() != "" {
			query = it.GetSeriesName()
		}
		sCtx, sCancel := context.WithTimeout(pageCtx, subtitlesSearchTimeout)
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
					Language: hit.GetLanguage(), Release: hit.GetReleaseName(), Score: subtitleCandidateScore(hit),
				})
			}
		}
	}

	_ = templates.Layout(it.GetTitle(), h.nav("/subtitles"), templates.SubtitlesMediaDetailPage(data)).Render(r.Context(), w)
}

func (h *Handler) SubtitlesSeriesDetail(w http.ResponseWriter, r *http.Request) {
	seriesID := r.PathValue("id")
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesSeriesPageTimeout)
	defer cancel()
	data := templates.SubtitlesMediaDetailData{
		Flash: r.URL.Query().Get("status"),
		Error: r.URL.Query().Get("error"),
		Item:  templates.SubtitlesMediaDetailItem{ID: seriesID, Title: seriesID, Type: "series", SeriesID: seriesID},
	}
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		data.Error = err.Error()
		_ = templates.Layout("Series", h.nav("/subtitles"), templates.SubtitlesMediaDetailPage(data)).Render(r.Context(), w)
		return
	}
	defer closer()

	readCtx, readCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
	eps, err := client.ListMedia(readCtx, &subtv1.ListMediaRequest{SeriesId: seriesID, Page: 1, PageSize: 500})
	readCancel()
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
	_ = templates.Layout(data.Item.Title, h.nav("/subtitles"), templates.SubtitlesMediaDetailPage(data)).Render(r.Context(), w)
}

func (h *Handler) SubtitlesMediaSearchWanted(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesBatchTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	resp, err := client.SearchWanted(pageCtx, &subtv1.SearchWantedRequest{MediaIds: []string{id}, Limit: 8})
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
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
	_, err = client.SetMediaLanguageProfile(readCtx, &subtv1.SetMediaLanguageProfileRequest{
		MediaId: id, LanguageProfileId: r.FormValue("profile_id"),
	})
	readCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/subtitles/media/"+url.PathEscape(id)+"?status=profile_updated", http.StatusSeeOther)
}

func (h *Handler) SubtitlesFileDelete(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("id")
	mediaID := r.FormValue("media_id")
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesActionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
	_, err = client.Delete(readCtx, &subtv1.DeleteRequest{Id: fileID})
	readCancel()
	if err != nil {
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
	pageCtx, cancel := context.WithTimeout(r.Context(), subtitlesTestTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(pageCtx, subtitlesDialTimeout)
	client, closer, err := h.withSubtitlesClient(dialCtx)
	dialCancel()
	if err != nil {
		http.Redirect(w, r, "/subtitles?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	readCtx, readCancel := context.WithTimeout(pageCtx, subtitlesReadTimeout)
	resp, err := client.TestArrConnection(readCtx, &subtv1.TestArrConnectionRequest{Target: target})
	readCancel()
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

// subtitleCandidateScore derives a display/download score for a search hit.
// SubtitleCandidate has no score field (media-subtitles v0.5+); the module's
// own Bazarr-compatible surface uses the provider rating as the score, so
// admin-ui does the same.
func subtitleCandidateScore(hit *subtv1.SubtitleCandidate) int {
	return int(hit.GetRating())
}
