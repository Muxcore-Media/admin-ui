package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const releaseSearchTimeout = automationDialTimeout + automationReadTimeout + time.Second

// ReleaseSearchPage handles GET /release-search.
// When the "q" query parameter is present it calls automation SearchItem and
// surfaces all scored matches.  If the automation module is unreachable the
// page renders with a soft-empty error rather than a hard failure.
func (h *Handler) ReleaseSearchPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), releaseSearchTimeout)
	defer cancel()

	query := r.URL.Query().Get("q")
	itemType := r.URL.Query().Get("type")
	if itemType == "" {
		itemType = "movie"
	}

	data := templates.ReleaseSearchPageData{
		Query:       query,
		ItemType:    itemType,
		Flash:       r.URL.Query().Get("dispatched"),
		FlashStatus: r.URL.Query().Get("status"),
		Error:       r.URL.Query().Get("error"),
	}

	if query != "" {
		dialCtx, dialCancel := context.WithTimeout(pageCtx, automationDialTimeout)
		client, closer, err := h.withAutomationClient(dialCtx)
		dialCancel()
		if err != nil {
			slog.Warn("release-search: resolve/dial failed", "error", err)
			data.Error = automationResolveErr(err)
		} else {
			defer closer()
			data.Results = h.searchReleases(pageCtx, client, query, itemType)
		}
	}

	h.renderReleaseSearch(w, r, data)
}

// searchReleases calls automation SearchItem and maps the response to view-model results.
// It returns nil (soft empty) when SearchItem fails.
func (h *Handler) searchReleases(ctx context.Context, client automationv1.AutomationServiceClient, query, itemType string) []templates.ReleaseSearchResult {
	searchCtx, searchCancel := context.WithTimeout(ctx, automationReadTimeout)
	resp, err := client.SearchItem(searchCtx, &automationv1.SearchItemRequest{
		ItemType: itemType,
		Query:    query,
		Limit:    50,
	})
	searchCancel()
	if err != nil {
		slog.Warn("release-search: SearchItem failed", "error", err)
		return nil
	}

	matches := resp.GetMatches()
	if len(matches) == 0 {
		return nil
	}

	out := make([]templates.ReleaseSearchResult, 0, len(matches))
	for _, m := range matches {
		out = append(out, templates.ReleaseSearchResult{
			Guid:             m.GetGuid(),
			Title:            m.GetTitle(),
			IndexerName:      m.GetIndexerName(),
			DownloadProtocol: m.GetDownloadProtocol(),
			Size:             m.GetSize(),
			Score:            m.GetScore(),
			DownloadURL:      m.GetDownloadUrl(),
			ItemType:         itemType,
		})
	}
	return out
}

// ReleaseSearchGrab handles POST /release-search/grab.
// It dispatches the operator-selected release via automation and redirects back
// to the search page with a flash message.
func (h *Handler) ReleaseSearchGrab(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), automationDispatchTO)
	defer cancel()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	query := r.FormValue("query")
	searchType := r.FormValue("search_type")
	if searchType == "" {
		searchType = "movie"
	}

	redirectBase := fmt.Sprintf("/release-search?q=%s&type=%s",
		url.QueryEscape(query), url.QueryEscape(searchType))
	redirectErr := func(msg string) {
		http.Redirect(w, r, redirectBase+"&error="+url.QueryEscape(msg), http.StatusSeeOther)
	}

	release := releaseFromForm(r)
	if release == nil {
		redirectErr("no release selected")
		return
	}

	itemType := r.FormValue("item_type")
	if itemType == "" {
		itemType = searchType
	}
	itemID := r.FormValue("item_id")
	title := r.FormValue("title")
	if title == "" {
		title = release.Title
	}

	disp, err := h.dispatchAutomation(ctx, itemType, itemID, title, 0, 0, release, false)
	if err != nil {
		slog.Warn("release-search: grab dispatch failed", "error", err)
		redirectErr(err.Error())
		return
	}

	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.release.grab", "release", release.Guid, map[string]string{
			"title":    title,
			"indexer":  release.IndexerName,
			"protocol": release.DownloadProtocol,
		})
	}

	http.Redirect(w, r,
		fmt.Sprintf("%s&dispatched=%s&status=%s",
			redirectBase,
			url.QueryEscape(disp.GetDownloadId()),
			url.QueryEscape(disp.GetStatus())),
		http.StatusSeeOther)
}

func (h *Handler) renderReleaseSearch(w http.ResponseWriter, r *http.Request, data templates.ReleaseSearchPageData) {
	content := templates.ReleaseSearchPage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Release Search", nav, content)
	h.render(w, r, component)
}
