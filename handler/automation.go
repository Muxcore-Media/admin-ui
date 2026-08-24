package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaAutomation    = "media.automation"
	capIndexer            = "indexer"
	capDownloader         = "downloader"
	automationDialTimeout = 3 * time.Second
	automationReadTimeout = 4 * time.Second
	automationDispatchTO  = 25 * time.Second
	automationPeerTimeout = 1500 * time.Millisecond
)

func automationResolveErr(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("media automation module discovery timed out after %s", automationDialTimeout)
	}
	if errors.Is(err, context.Canceled) {
		return "media automation module discovery canceled"
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.DeadlineExceeded:
			return fmt.Sprintf("media automation module discovery timed out after %s", automationDialTimeout)
		case codes.Canceled:
			return "media automation module discovery canceled"
		}
	}
	return err.Error()
}

func (h *Handler) automationModuleAddr(ctx context.Context) (string, string, error) {
	if h.Core == nil {
		return "", "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaAutomation)
	if err != nil {
		return "", "", err
	}
	if len(mods) == 0 {
		return "", "", fmt.Errorf("no module with capability %s", capMediaAutomation)
	}
	mod := mods[0]
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return "", "", fmt.Errorf("automation module has no dial address")
	}
	return mod.GetId(), addr, nil
}

func (h *Handler) withAutomationClient(ctx context.Context) (automationv1.AutomationServiceClient, func(), error) {
	_, addr, err := h.automationModuleAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return automationv1.NewAutomationServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (h *Handler) AutomationQueuePage(w http.ResponseWriter, r *http.Request) {
	// Bound the whole page so a stuck ImportPath / mesh dial cannot hang the admin UI.
	pageCtx, cancel := context.WithTimeout(r.Context(), automationDialTimeout+2*automationReadTimeout+time.Second)
	defer cancel()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	filter := r.URL.Query().Get("filter")

	data := templates.AutomationPageData{
		Filter:      filter,
		Page:        page,
		Flash:       r.URL.Query().Get("dispatched"),
		FlashStatus: r.URL.Query().Get("status"),
		Error:       r.URL.Query().Get("error"),
	}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, automationDialTimeout)
	client, closer, err := h.withAutomationClient(dialCtx)
	dialCancel()
	if err != nil {
		slog.Warn("automation: resolve/dial failed", "error", err)
		data.Error = automationResolveErr(err)
		peerCtx, peerCancel := context.WithTimeout(pageCtx, automationPeerTimeout)
		data.Indexers, data.Downloaders = h.acquisitionPeers(peerCtx)
		peerCancel()
		h.renderAutomation(w, r, data)
		return
	}
	defer closer()

	qCtx, qCancel := context.WithTimeout(pageCtx, automationReadTimeout)
	q, err := client.GetQueue(qCtx, &automationv1.GetQueueRequest{
		Page:     int32(page),
		PageSize: 50,
		Filter:   filter,
	})
	qCancel()
	if err != nil {
		slog.Warn("automation: GetQueue failed", "error", err)
		if data.Error == "" {
			data.Error = "queue unavailable: " + err.Error()
		}
	} else {
		data.Total = int(q.GetTotal())
		data.PageSize = int(q.GetPageSize())
		if data.PageSize < 1 {
			data.PageSize = 50
		}
		data.TotalPages = 1
		if data.Total > 0 {
			data.TotalPages = (data.Total + data.PageSize - 1) / data.PageSize
		}
		for _, it := range q.GetItems() {
			data.Items = append(data.Items, templates.AutomationQueueItem{
				ID:        it.GetId(),
				ItemID:    it.GetItemId(),
				ItemType:  it.GetItemType(),
				Title:     it.GetTitle(),
				Year:      int(it.GetYear()),
				TMDBID:    int(it.GetTmdbId()),
				Monitored: it.GetMonitored(),
				Missing:   it.GetMissing(),
				UpdatedAt: it.GetUpdatedAt(),
			})
		}
	}

	hCtx, hCancel := context.WithTimeout(pageCtx, automationReadTimeout)
	hist, err := client.GetHistory(hCtx, &automationv1.GetHistoryRequest{Page: 1, PageSize: 20})
	hCancel()
	if err != nil {
		slog.Warn("automation: GetHistory failed", "error", err)
		if data.Error == "" {
			data.Error = "history unavailable: " + err.Error()
		}
	} else {
		for _, rec := range hist.GetRecords() {
			src := rec.GetIndexer()
			if src == "" {
				src = rec.GetDownloadProtocol()
			}
			data.History = append(data.History, templates.AutomationHistoryItem{
				ID:     rec.GetId(),
				Status: rec.GetStatus(),
				Title:  rec.GetTitle(),
				Source: src,
				At:     rec.GetCreatedAt(),
			})
		}
	}

	peerCtx, peerCancel := context.WithTimeout(pageCtx, automationPeerTimeout)
	data.Indexers, data.Downloaders = h.acquisitionPeers(peerCtx)
	peerCancel()

	bCtx, bCancel := context.WithTimeout(pageCtx, automationReadTimeout)
	if bl, err := client.ListBlocklist(bCtx, &automationv1.ListBlocklistRequest{Page: 1, PageSize: 50}); err != nil {
		slog.Debug("automation: ListBlocklist failed", "error", err)
	} else {
		for _, e := range bl.GetEntries() {
			data.Blocklist = append(data.Blocklist, templates.AutomationBlocklistItem{
				WantedItemID: e.GetWantedItemId(),
				GUID:         e.GetGuid(),
				Loop:         int(e.GetLoop()),
				Reason:       e.GetReason(),
				Title:        e.GetTitle(),
				CreatedAt:   e.GetCreatedAt(),
			})
		}
	}
	bCancel()

	dCtx, dCancel := context.WithTimeout(pageCtx, automationReadTimeout)
	if dp, err := client.ListDelayProfiles(dCtx, &automationv1.ListDelayProfilesRequest{}); err != nil {
		slog.Debug("automation: ListDelayProfiles failed", "error", err)
	} else {
		for _, p := range dp.GetProfiles() {
			data.DelayProfiles = append(data.DelayProfiles, templates.AutomationDelayProfile{
				Protocol: p.GetProtocol(), WaitMinutes: int(p.GetWaitMinutes()),
			})
		}
	}
	dCancel()

	cCtx, cCancel := context.WithTimeout(pageCtx, automationReadTimeout)
	if cu, err := client.ListCutoffUnmet(cCtx, &automationv1.ListCutoffUnmetRequest{Page: 1, PageSize: 50}); err != nil {
		slog.Debug("automation: ListCutoffUnmet failed", "error", err)
	} else {
		for _, it := range cu.GetItems() {
			data.Cutoff = append(data.Cutoff, templates.AutomationCutoffItem{
				QueueID: it.GetQueueId(), ItemID: it.GetItemId(), ItemType: it.GetItemType(),
				Title: it.GetTitle(), Year: int(it.GetYear()),
				CurrentScore: int(it.GetCurrentScore()), CutoffScore: int(it.GetCutoffScore()),
				ProfileID: it.GetQualityProfileId(),
			})
		}
	}
	cCancel()

	h.renderAutomation(w, r, data)
}

func (h *Handler) acquisitionPeers(ctx context.Context) (indexers, downloaders []templates.AutomationPeerItem) {
	if h.Core == nil {
		return nil, nil
	}
	indexers = peerItemsByCapability(ctx, h, capIndexer, "indexer")
	downloaders = peerItemsByCapability(ctx, h, capDownloader, "downloader")
	return indexers, downloaders
}

func peerItemsByCapability(ctx context.Context, h *Handler, capability, kind string) []templates.AutomationPeerItem {
	mods, err := h.Core.Discovery.FindByCapability(ctx, capability)
	if err != nil {
		slog.Debug("automation: acquisition peer discovery", "capability", capability, "error", err)
		return nil
	}
	out := make([]templates.AutomationPeerItem, 0, len(mods))
	for _, mod := range mods {
		name := mod.GetName()
		if name == "" {
			name = mod.GetId()
		}
		out = append(out, templates.AutomationPeerItem{
			ID:   mod.GetId(),
			Name: name,
			Kind: kind,
		})
	}
	return out
}

func (h *Handler) renderAutomation(w http.ResponseWriter, r *http.Request, data templates.AutomationPageData) {
	content := templates.AutomationPage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Automation", nav, content)
	h.render(w, r, component)
}

type automationRelease struct {
	Guid             string
	Title            string
	DownloadURL      string
	DownloadProtocol string
	Size             int64
	Score            int32
	IndexerName      string
}

func releaseFromForm(r *http.Request) *automationRelease {
	guid := r.FormValue("guid")
	if guid == "" {
		return nil
	}
	size, _ := strconv.ParseInt(r.FormValue("size"), 10, 64)
	score, _ := strconv.Atoi(r.FormValue("score"))
	return &automationRelease{
		Guid:             guid,
		Title:            r.FormValue("release_title"),
		DownloadURL:      r.FormValue("download_url"),
		DownloadProtocol: r.FormValue("download_protocol"),
		Size:             size,
		Score:            int32(score),
		IndexerName:      r.FormValue("indexer"),
	}
}

func (h *Handler) dispatchAutomation(ctx context.Context, itemType, itemID, title string, tmdbID, year int32, release *automationRelease, forceFixture bool) (*automationv1.DispatchResponse, error) {
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		return nil, err
	}
	defer closer()

	var match *automationRelease
	if release != nil {
		match = release
	} else if !forceFixture {
		searchCtx, searchCancel := context.WithTimeout(ctx, 10*time.Second)
		search, err := client.SearchItem(searchCtx, &automationv1.SearchItemRequest{
			ItemType: itemType,
			Query:    title,
			TmdbId:   tmdbID,
			Year:     year,
			Limit:    10,
		})
		searchCancel()
		if err != nil {
			slog.Warn("automation: SearchItem failed", "error", err)
		} else if len(search.GetMatches()) > 0 {
			best := search.GetMatches()[0]
			for _, m := range search.GetMatches()[1:] {
				if m.GetScore() > best.GetScore() {
					best = m
				}
			}
			match = &automationRelease{
				Guid:             best.GetGuid(),
				Title:            best.GetTitle(),
				DownloadURL:      best.GetDownloadUrl(),
				DownloadProtocol: best.GetDownloadProtocol(),
				Size:             best.GetSize(),
				Score:            best.GetScore(),
				IndexerName:      best.GetIndexerName(),
			}
		}
	}

	req := &automationv1.DispatchRequest{
		ItemType: itemType,
		ItemId:   itemID,
		TmdbId:   tmdbID,
		Title:    title,
	}
	if match != nil {
		req.Guid = match.Guid
		req.Title = match.Title
		req.DownloadUrl = match.DownloadURL
		req.DownloadProtocol = match.DownloadProtocol
		if req.DownloadProtocol == "" {
			req.DownloadProtocol = "torrent"
		}
		req.Size = match.Size
		req.Score = match.Score
		req.IndexerName = match.IndexerName
	} else {
		dn := strings.ReplaceAll(title, " ", ".")
		if year > 0 {
			dn = fmt.Sprintf("%s.%d.1080p.Fixture", dn, year)
		} else {
			dn = dn + ".Fixture"
		}
		req.Guid = fmt.Sprintf("fixture-%d", time.Now().UnixNano())
		req.DownloadUrl = fmt.Sprintf("magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=%s", url.QueryEscape(dn))
		req.DownloadProtocol = "torrent"
		req.Size = 8192
		req.Score = 100
		req.IndexerName = "fixture"
	}

	return client.Dispatch(ctx, req)
}

// AutomationDispatch runs SearchItem → Dispatch best match, or a fixture magnet when search is empty.
func (h *Handler) AutomationDispatch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), automationDispatchTO)
	defer cancel()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	itemType := r.FormValue("item_type")
	if itemType == "" {
		itemType = "movie"
	}
	itemID := r.FormValue("item_id")
	title := r.FormValue("title")
	if title == "" {
		title = "Fight Club"
	}
	tmdbID, _ := strconv.Atoi(r.FormValue("tmdb_id"))
	year, _ := strconv.Atoi(r.FormValue("year"))
	forceFixture := r.FormValue("fixture") == "1" || r.FormValue("mode") == "fixture"

	var release *automationRelease
	if !forceFixture && r.FormValue("mode") != "best" {
		release = releaseFromForm(r)
	}

	disp, err := h.dispatchAutomation(ctx, itemType, itemID, title, int32(tmdbID), int32(year), release, forceFixture)
	if err != nil {
		slog.Warn("automation: Dispatch failed", "error", err)
		http.Redirect(w, r, "/automation?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/automation?dispatched=%s&status=%s",
		url.QueryEscape(disp.GetDownloadId()), url.QueryEscape(disp.GetStatus())), http.StatusSeeOther)
}

func (h *Handler) AutomationQueueRemove(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), automationReadTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/automation", http.StatusSeeOther)
		return
	}
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/automation?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.RemoveFromQueue(ctx, &automationv1.RemoveFromQueueRequest{QueueId: r.FormValue("queue_id")}); err != nil {
		http.Redirect(w, r, "/automation?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/automation?status=removed", http.StatusSeeOther)
}

func (h *Handler) AutomationBlocklistClear(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), automationReadTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/automation", http.StatusSeeOther)
		return
	}
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/automation?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	req := &automationv1.ClearBlocklistRequest{
		WantedItemId: r.FormValue("wanted_item_id"),
		Guid:          r.FormValue("guid"),
		ClearAll:      r.FormValue("clear_all") == "1",
	}
	if _, err := client.ClearBlocklist(ctx, req); err != nil {
		http.Redirect(w, r, "/automation?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/automation?status=blocklist_cleared", http.StatusSeeOther)
}

func (h *Handler) AutomationDelayUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), automationReadTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/automation", http.StatusSeeOther)
		return
	}
	mins, _ := strconv.Atoi(r.FormValue("wait_minutes"))
	client, closer, err := h.withAutomationClient(ctx)
	if err != nil {
		http.Redirect(w, r, "/automation?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	defer closer()
	if _, err := client.UpsertDelayProfile(ctx, &automationv1.UpsertDelayProfileRequest{
		Protocol: r.FormValue("protocol"), WaitMinutes: int32(mins),
	}); err != nil {
		http.Redirect(w, r, "/automation?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/automation?status=delay_saved", http.StatusSeeOther)
}
