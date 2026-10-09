package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	approvalsPageTimeout = requestDialTimeout + requestReadTimeout + time.Second
)

// approvalsHTTPPost issues an authenticated POST to request-media and discards the response body.
func (h *Handler) approvalsHTTPPost(ctx context.Context, base, path string, payload []byte, sess *session.Session) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	h.applyTenantHeaders(req, sess)
	h.applyCallerHeader(req, sess)
	if sess != nil {
		by := callerID(sess)
		if by != "" {
			req.Header.Set("X-MuxCore-User", by)
		}
		roles := "user"
		for _, role := range sess.Roles {
			if strings.EqualFold(role, "admin") {
				roles = "admin"
				break
			}
		}
		req.Header.Set("X-MuxCore-Roles", roles)
	}
	return requestMutationHTTPDo(req)
}

// ApprovalsPage renders the admin approval queue.
func (h *Handler) ApprovalsPage(w http.ResponseWriter, r *http.Request) {
	pageCtx, cancel := context.WithTimeout(r.Context(), approvalsPageTimeout)
	defer cancel()

	data := templates.ApprovalsPageData{}

	dialCtx, dialCancel := context.WithTimeout(pageCtx, requestDialTimeout)
	_, base, _, err := h.requestMediaBase(dialCtx)
	dialCancel()
	if err != nil {
		data.SoftEmpty = true
		h.renderApprovals(w, r, data)
		return
	}

	// Pending queue — filter server-side where supported; client-side fallback.
	pendingBody, code, listErr := h.requestGET(pageCtx, base, requestHTTPListPath+"?status=pending")
	if listErr != nil || code >= 300 {
		data.Error = "request-media unreachable — pending queue unavailable"
		h.renderApprovals(w, r, data)
		return
	}
	var pendingRows []requestMediaJSON
	if jsonErr := json.Unmarshal(pendingBody, &pendingRows); jsonErr != nil {
		data.Error = "invalid JSON from request-media"
		h.renderApprovals(w, r, data)
		return
	}
	for _, p := range pendingRows {
		if strings.EqualFold(p.Status, "pending") || p.Status == "" {
			data.Pending = append(data.Pending, toApprovalRow(p))
		}
	}

	// Recent non-pending requests (all, then filter out pending).
	recentBody, rCode, rErr := h.requestGET(pageCtx, base, requestHTTPListPath)
	if rErr == nil && rCode < 300 {
		var all []requestMediaJSON
		if json.Unmarshal(recentBody, &all) == nil {
			for _, row := range all {
				if !strings.EqualFold(row.Status, "pending") {
					data.Recent = append(data.Recent, toApprovalRow(row))
				}
			}
		}
	}

	h.renderApprovals(w, r, data)
}

// ApprovalsApprove handles POST /approvals/{id}/approve.
func (h *Handler) ApprovalsApprove(w http.ResponseWriter, r *http.Request) {
	h.approvalsDecide(w, r, "approve")
}

// ApprovalsDeny handles POST /approvals/{id}/deny.
func (h *Handler) ApprovalsDeny(w http.ResponseWriter, r *http.Request) {
	h.approvalsDecide(w, r, "deny")
}

func (h *Handler) approvalsDecide(w http.ResponseWriter, r *http.Request, action string) {
	ctx, cancel := context.WithTimeout(r.Context(), approvalsPageTimeout)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")

	id := r.PathValue("id")
	if id == "" {
		h.renderRequestMutationError(w, r, http.StatusBadRequest, "The request ID is missing. No request was sent.", "/approvals")
		return
	}

	_, base, _, err := h.requestMediaBase(ctx)
	if err != nil {
		h.renderRequestMutationError(w, r, http.StatusServiceUnavailable, "The request service is unavailable. No request was sent.", "/approvals")
		return
	}

	sess := SessionFromContext(r.Context())

	by := "admin"
	if sess != nil {
		if sess.Username != "" {
			by = sess.Username
		} else if sess.UserID != "" {
			by = sess.UserID
		}
	}

	body := map[string]string{"by": by}
	if action == "deny" {
		if r.Body != nil && !h.parseRequestMutationForm(w, r, "/approvals") {
			return
		}
		if reason := strings.TrimSpace(r.FormValue("reason")); reason != "" {
			body["reason"] = reason
		}
	}
	payload, _ := json.Marshal(body)

	path := "/api/requests/" + url.PathEscape(id) + "/" + action
	code, postErr := h.approvalsHTTPPost(ctx, base, path, payload, sess)
	if !h.requestMutationSucceeded(w, r, code, postErr, "/approvals") {
		return
	}

	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}

func toApprovalRow(r requestMediaJSON) templates.ApprovalRow {
	return templates.ApprovalRow{
		ID:          r.ID,
		ItemType:    r.ItemType,
		Title:       r.Title,
		Year:        r.Year,
		Status:      r.Status,
		RequestedBy: r.RequestedBy,
		ApprovedBy:  r.ApprovedBy,
		DenyReason:  r.DenyReason,
	}
}

func (h *Handler) renderApprovals(w http.ResponseWriter, r *http.Request, data templates.ApprovalsPageData) {
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Approval Queue", nav, templates.ApprovalsPage(data)))
}
