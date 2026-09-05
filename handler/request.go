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
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaRequest     = "media.request"
	requestDialTimeout  = 3 * time.Second
	requestReadTimeout  = 5 * time.Second
	requestPageTimeout  = requestDialTimeout + 2*requestReadTimeout + time.Second
	requestHTTPListPath = "/api/requests"
	requestHTTPSearch   = "/api/search"
	requestHTTPCreate   = "/api/request"
)

func requestHTTPDo(_ context.Context, req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: requestReadTimeout}).Do(req)
}

type requestMediaJSON struct {
	ID          string `json:"id"`
	ItemType    string `json:"itemType"`
	TMDBID      int    `json:"tmdbId"`
	Title       string `json:"title"`
	Year        int    `json:"year"`
	Status      string `json:"status"`
	RequestedBy string `json:"requestedBy"`
	ApprovedBy  string `json:"approvedBy"`
	DenyReason  string `json:"denyReason"`
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
	sess := SessionFromContext(ctx)
	h.applyTenantHeaders(req, sess)
	h.applyCallerHeader(req, sess)
	resp, err := requestHTTPDo(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, err
}

// applyCallerHeader sets X-Caller-Id from the authenticated session so
// downstream modules (e.g. request-media) can enforce per-caller authz.
func (h *Handler) applyCallerHeader(req *http.Request, sess *session.Session) {
	if req == nil || sess == nil {
		return
	}
	caller := sess.Username
	if caller == "" {
		caller = sess.UserID
	}
	if caller != "" {
		req.Header.Set("X-Caller-Id", caller)
	}
}

// applyTenantHeaders forwards session tenant + claim headers to downstream modules.
func (h *Handler) applyTenantHeaders(req *http.Request, sess *session.Session) {
	if req == nil {
		return
	}
	tid := ""
	if sess != nil {
		tid = strings.TrimSpace(sess.TenantID)
	}
	if tid == "" && os.Getenv("TENANT_MODE") == "1" {
		tid = "default"
	}
	if tid == "" {
		return
	}
	req.Header.Set("X-Tenant-ID", tid)
	req.Header.Set("X-Auth-Claims-Tenant", tid)
}

func (h *Handler) RequestPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), requestPageTimeout)
	defer cancel()

	data := templates.RequestPageData{
		Query:     strings.TrimSpace(r.URL.Query().Get("q")),
		MediaType: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type"))),
	}
	if data.MediaType == "" {
		data.MediaType = "movie"
	}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, requestDialTimeout)
	moduleID, base, name, err := h.requestMediaBase(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftEmpty = true
		data.Error = err.Error()
		h.renderRequest(w, r, data)
		return
	}
	data.ModuleID = moduleID
	data.ModuleName = name
	data.BaseURL = base

	if listBody, code, listErr := h.requestGET(pageCtx, base, requestHTTPListPath); listErr != nil {
		data.Error = "request-media unreachable: " + listErr.Error()
	} else if code >= 300 {
		data.Error = fmt.Sprintf("request-media list HTTP %d", code)
	} else {
		var rows []requestMediaJSON
		if jsonErr := json.Unmarshal(listBody, &rows); jsonErr != nil {
			data.Error = "invalid request list JSON"
		} else {
			for _, row := range rows {
				rr := templates.RequestRow{
					ID: row.ID, ItemType: row.ItemType, Title: row.Title,
					Year: row.Year, Status: row.Status, TMDBID: row.TMDBID,
					RequestedBy: row.RequestedBy, ApprovedBy: row.ApprovedBy,
				}
				data.Requests = append(data.Requests, rr)
				if strings.EqualFold(row.Status, "pending") {
					data.Pending = append(data.Pending, rr)
				}
			}
		}
	}

	if data.Query != "" {
		path := requestHTTPSearch + "?q=" + url.QueryEscape(data.Query) + "&type=" + url.QueryEscape(data.MediaType)
		body, code, searchErr := h.requestGET(pageCtx, base, path)
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

	sess := SessionFromContext(r.Context())
	requestedBy := ""
	isAdmin := true // admin-ui operators are admins; still send identity
	if sess != nil {
		requestedBy = sess.Username
		if requestedBy == "" {
			requestedBy = sess.UserID
		}
		isAdmin = false
		for _, role := range sess.Roles {
			if strings.EqualFold(role, "admin") {
				isAdmin = true
				break
			}
		}
	}

	payload, _ := json.Marshal(map[string]any{
		"tmdbId": tmdbID, "title": r.FormValue("title"), "year": year,
		"overview": r.FormValue("overview"), "poster": r.FormValue("poster"),
		"type": itemType, "requestedBy": requestedBy, "isAdmin": isAdmin,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+requestHTTPCreate, bytes.NewReader(payload))
	if err != nil {
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	h.applyTenantHeaders(req, sess)
	if requestedBy != "" {
		req.Header.Set("X-MuxCore-User", requestedBy)
	}
	if isAdmin {
		req.Header.Set("X-MuxCore-Roles", "admin")
	}
	resp, err := requestHTTPDo(ctx, req)
	if err != nil {
		slog.Warn("request-media create failed", "error", err)
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	redir := "/request?type=" + url.QueryEscape(itemType)
	if title := r.FormValue("title"); title != "" {
		redir += "&q=" + url.QueryEscape(title)
	}
	http.Redirect(w, r, redir, http.StatusSeeOther)
}

func (h *Handler) RequestApprove(w http.ResponseWriter, r *http.Request) {
	h.requestDecide(w, r, "approve")
}

func (h *Handler) RequestDeny(w http.ResponseWriter, r *http.Request) {
	h.requestDecide(w, r, "deny")
}

func (h *Handler) requestDecide(w http.ResponseWriter, r *http.Request, action string) {
	ctx, cancel := context.WithTimeout(r.Context(), requestPageTimeout)
	defer cancel()
	id := r.PathValue("id")
	if id == "" {
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}
	_, base, _, err := h.requestMediaBase(ctx)
	if err != nil {
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}
	by := "admin"
	var sess *session.Session
	if sess = SessionFromContext(r.Context()); sess != nil {
		by = sess.Username
		if by == "" {
			by = sess.UserID
		}
	}
	payload, _ := json.Marshal(map[string]string{"by": by})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/requests/"+url.PathEscape(id)+"/"+action, bytes.NewReader(payload))
	if err != nil {
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	h.applyTenantHeaders(req, sess)
	h.applyCallerHeader(req, sess)
	if sess != nil {
		roles := "user"
		for _, role := range sess.Roles {
			if strings.EqualFold(role, "admin") {
				roles = "admin"
				break
			}
		}
		req.Header.Set("X-MuxCore-Roles", roles)
		req.Header.Set("X-MuxCore-User", by)
	}
	resp, err := requestHTTPDo(ctx, req)
	if err != nil {
		slog.Warn("request-media "+action+" failed", "error", err)
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	http.Redirect(w, r, "/request", http.StatusSeeOther)
}

func (h *Handler) renderRequest(w http.ResponseWriter, r *http.Request, data templates.RequestPageData) {
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Request Media", nav, templates.RequestPage(data)))
}
