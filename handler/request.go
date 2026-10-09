package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// callerID returns the identity request-media authorizes against: the
// auth-local user ID (auth-local Can looks users up by ID, not username).
// Username is only a fallback for sessions that lack an ID.
func callerID(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	if id := strings.TrimSpace(sess.UserID); id != "" {
		return id
	}
	return strings.TrimSpace(sess.Username)
}

// applyCallerHeader forwards the signed-in user's identity to downstream
// modules (e.g. request-media): the auth-local bearer token (authoritative,
// ADR-0019) plus X-Caller-Id=<user id>, kept for one release for
// compatibility. Tokens are never logged.
func (h *Handler) applyCallerHeader(req *http.Request, sess *session.Session) {
	if req == nil || sess == nil {
		return
	}
	if id := callerID(sess); id != "" {
		req.Header.Set("X-Caller-Id", id)
	}
	if tok := strings.TrimSpace(sess.AuthLocalToken); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
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
					typ := strings.ToLower(strings.TrimSpace(hit.Type))
					if typ == "" {
						// Canonical request-media searches movies and omits type.
						// The selected query must never relabel those IDs as TV.
						typ = "movie"
					}
					if typ != data.MediaType {
						if data.MediaType == "tv" && data.Error == "" {
							data.Error = "TV results are unavailable from this request service. Movie or unclassified results cannot be requested as TV shows."
						}
						continue
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
	w.Header().Set("Cache-Control", "no-store")

	if !h.parseRequestMutationForm(w, r, "/request") {
		return
	}

	moduleID, base, _, err := h.requestMediaBase(ctx)
	if err != nil {
		h.renderRequestMutationError(w, r, http.StatusServiceUnavailable, "The request service is unavailable. No request was sent.", "/request")
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
		requestedBy = callerID(sess)
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
		"mediaType": itemType, "requestedBy": requestedBy, "isAdmin": isAdmin,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+requestHTTPCreate, bytes.NewReader(payload))
	if err != nil {
		h.renderRequestMutationError(w, r, http.StatusServiceUnavailable, "The request service is unavailable. No request was sent.", "/request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	h.applyTenantHeaders(req, sess)
	h.applyCallerHeader(req, sess)
	if requestedBy != "" {
		req.Header.Set("X-MuxCore-User", requestedBy)
	}
	if isAdmin {
		req.Header.Set("X-MuxCore-Roles", "admin")
	}
	code, err := requestMutationHTTPDo(req)
	if !h.requestMutationSucceeded(w, r, code, err, "/request") {
		return
	}

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
	w.Header().Set("Cache-Control", "no-store")
	id := r.PathValue("id")
	if id == "" {
		h.renderRequestMutationError(w, r, http.StatusBadRequest, "The request ID is missing. No request was sent.", "/request")
		return
	}
	_, base, _, err := h.requestMediaBase(ctx)
	if err != nil {
		h.renderRequestMutationError(w, r, http.StatusServiceUnavailable, "The request service is unavailable. No request was sent.", "/request")
		return
	}
	by := "admin"
	var sess *session.Session
	if sess = SessionFromContext(r.Context()); sess != nil {
		by = callerID(sess)
		if by == "" {
			by = "admin"
		}
	}
	payload, _ := json.Marshal(map[string]string{"by": by})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/requests/"+url.PathEscape(id)+"/"+action, bytes.NewReader(payload))
	if err != nil {
		h.renderRequestMutationError(w, r, http.StatusServiceUnavailable, "The request service is unavailable. No request was sent.", "/request")
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
	code, err := requestMutationHTTPDo(req)
	if !h.requestMutationSucceeded(w, r, code, err, "/request") {
		return
	}
	http.Redirect(w, r, "/request", http.StatusSeeOther)
}

func (h *Handler) renderRequest(w http.ResponseWriter, r *http.Request, data templates.RequestPageData) {
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Request Media", nav, templates.RequestPage(data)))
}
