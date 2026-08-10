package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaRequest     = "media.request"
	requestPageTimeout  = 8 * time.Second
	requestHTTPListPath = "/api/requests"
	requestHTTPSearch   = "/api/search"
	requestHTTPCreate   = "/api/request"
)

type requestMediaJSON struct {
	ID       string `json:"id"`
	ItemType string `json:"itemType"`
	TMDBID   int    `json:"tmdbId"`
	Title    string `json:"title"`
	Year     int    `json:"year"`
	Status   string `json:"status"`
}

type requestSearchHitJSON struct {
	ID       int     `json:"id"`
	Title    string  `json:"title"`
	Year     int     `json:"year"`
	Overview string  `json:"overview"`
	Poster   string  `json:"poster"`
	VoteAvg  float64 `json:"voteAvg"`
	Type     string  `json:"type"`
}

func (h *Handler) requestMediaBase(ctx context.Context) (moduleID, baseURL, name string, err error) {
	if base := strings.TrimRight(h.RequestMediaURL, "/"); base != "" {
		return "request-media", base, "Request Media", nil
	}
	if h.Core == nil {
		return "", "", "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaRequest)
	if err != nil {
		return "", "", "", err
	}
	if len(mods) == 0 {
		return "", "", "", fmt.Errorf("no module with capability %s", capMediaRequest)
	}
	mod := mods[0]
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return "", "", "", fmt.Errorf("request-media has no dial address")
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	return mod.GetId(), strings.TrimRight(addr, "/"), mod.GetName(), nil
}

func (h *Handler) requestGET(ctx context.Context, base, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, err
}

func (h *Handler) RequestPage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestPageTimeout)
	defer cancel()

	data := templates.RequestPageData{
		Query:     strings.TrimSpace(r.URL.Query().Get("q")),
		MediaType: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type"))),
	}
	if data.MediaType == "" {
		data.MediaType = "movie"
	}

	moduleID, base, name, err := h.requestMediaBase(ctx)
	if err != nil {
		data.SoftEmpty = true
		data.Error = err.Error()
		h.renderRequest(w, r, data)
		return
	}
	data.ModuleID = moduleID
	data.ModuleName = name
	data.BaseURL = base

	if listBody, code, listErr := h.requestGET(ctx, base, requestHTTPListPath); listErr != nil {
		data.Error = "request-media unreachable: " + listErr.Error()
	} else if code >= 300 {
		data.Error = fmt.Sprintf("request-media list HTTP %d", code)
	} else {
		var rows []requestMediaJSON
		if jsonErr := json.Unmarshal(listBody, &rows); jsonErr != nil {
			data.Error = "invalid request list JSON"
		} else {
			for _, row := range rows {
				data.Requests = append(data.Requests, templates.RequestRow{
					ID: row.ID, ItemType: row.ItemType, Title: row.Title,
					Year: row.Year, Status: row.Status, TMDBID: row.TMDBID,
				})
			}
		}
	}

	if data.Query != "" {
		path := requestHTTPSearch + "?q=" + url.QueryEscape(data.Query) + "&type=" + url.QueryEscape(data.MediaType)
		body, code, searchErr := h.requestGET(ctx, base, path)
		if searchErr != nil {
			data.Error = "search failed: " + searchErr.Error()
		} else if code >= 300 {
			data.Error = fmt.Sprintf("search HTTP %d", code)
		} else {
			var payload struct {
				Results []requestSearchHitJSON `json:"results"`
				Error   string                 `json:"error"`
			}
			if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
				data.Error = "invalid search JSON"
			} else {
				if payload.Error != "" && data.Error == "" {
					data.Error = payload.Error
				}
				for _, hit := range payload.Results {
					typ := hit.Type
					if typ == "" {
						typ = data.MediaType
					}
					data.Results = append(data.Results, templates.RequestSearchHit{
						ID: hit.ID, Title: hit.Title, Year: hit.Year,
						Overview: hit.Overview, Poster: hit.Poster, Type: typ,
					})
				}
			}
		}
	}

	h.renderRequest(w, r, data)
}

func (h *Handler) RequestCreate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestPageTimeout)
	defer cancel()

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}

	moduleID, base, _, err := h.requestMediaBase(ctx)
	if err != nil {
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}
	_ = moduleID

	tmdbID, _ := strconv.Atoi(r.FormValue("tmdb_id"))
	year, _ := strconv.Atoi(r.FormValue("year"))
	itemType := strings.ToLower(strings.TrimSpace(r.FormValue("type")))
	if itemType == "" {
		itemType = "movie"
	}
	payload, _ := json.Marshal(map[string]any{
		"tmdbId":   tmdbID,
		"title":    r.FormValue("title"),
		"year":     year,
		"overview": r.FormValue("overview"),
		"poster":   r.FormValue("poster"),
		"type":     itemType,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+requestHTTPCreate, bytes.NewReader(payload))
	if err != nil {
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("request-media create failed", "error", err)
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	redir := "/request?type=" + url.QueryEscape(itemType)
	if title := r.FormValue("title"); title != "" {
		redir += "&q=" + url.QueryEscape(title)
	}
	http.Redirect(w, r, redir, http.StatusSeeOther)
}

func (h *Handler) renderRequest(w http.ResponseWriter, r *http.Request, data templates.RequestPageData) {
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Request Media", nav, templates.RequestPage(data)))
}
