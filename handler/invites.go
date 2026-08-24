package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	invitesDialTimeout = 3 * time.Second
	invitesReadTimeout = 5 * time.Second
	invitesPageTimeout = invitesDialTimeout + invitesReadTimeout + time.Second
)

func invitesHTTPDo(_ context.Context, req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: invitesReadTimeout}).Do(req)
}

func (h *Handler) authHTTPBase() string {
	if h.AuthInternalAddr != "" {
		return strings.TrimRight(h.AuthInternalAddr, "/")
	}
	return strings.TrimRight(h.AuthAddr, "/")
}

func (h *Handler) InvitesPage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), invitesPageTimeout)
	defer cancel()

	data := templates.InvitesPageData{
		AuthBase:    h.authHTTPBase(),
		CreatedLink: strings.TrimSpace(r.URL.Query().Get("created")),
		Message:     strings.TrimSpace(r.URL.Query().Get("msg")),
	}
	base := h.authHTTPBase()
	if base == "" {
		data.SoftEmpty = true
		data.Error = "auth HTTP base not configured"
		h.renderInvites(w, r, data)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/invites", nil)
	if err != nil {
		data.Error = err.Error()
		h.renderInvites(w, r, data)
		return
	}
	resp, err := invitesHTTPDo(ctx, req)
	if err != nil {
		data.SoftEmpty = true
		data.Error = "auth-local unreachable: " + err.Error()
		h.renderInvites(w, r, data)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		data.Error = fmt.Sprintf("list invites HTTP %d", resp.StatusCode)
		h.renderInvites(w, r, data)
		return
	}
	var payload struct {
		Invites []struct {
			ID        string `json:"id"`
			Prefix    string `json:"prefix"`
			CreatedBy string `json:"created_by"`
			Role      string `json:"role"`
			MaxUses   int    `json:"max_uses"`
			UseCount  int    `json:"use_count"`
			ExpiresAt string `json:"expires_at"`
			RevokedAt string `json:"revoked_at"`
			CreatedAt string `json:"created_at"`
		} `json:"invites"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		data.Error = "invalid invites JSON"
		h.renderInvites(w, r, data)
		return
	}
	for _, inv := range payload.Invites {
		data.Invites = append(data.Invites, templates.InviteRow{
			ID: inv.ID, Prefix: inv.Prefix, CreatedBy: inv.CreatedBy, Role: inv.Role,
			MaxUses: inv.MaxUses, UseCount: inv.UseCount, ExpiresAt: inv.ExpiresAt,
			CreatedAt: inv.CreatedAt, Revoked: strings.TrimSpace(inv.RevokedAt) != "" && inv.RevokedAt != "0001-01-01T00:00:00Z",
		})
	}
	h.renderInvites(w, r, data)
}

func (h *Handler) InvitesCreate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), invitesPageTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/invites", http.StatusSeeOther)
		return
	}
	base := h.authHTTPBase()
	if base == "" {
		http.Redirect(w, r, "/invites", http.StatusSeeOther)
		return
	}
	maxUses, _ := strconv.Atoi(r.FormValue("max_uses"))
	ttl, _ := strconv.Atoi(r.FormValue("ttl_hours"))
	createdBy := "admin"
	tenantID := strings.TrimSpace(r.FormValue("tenant_id"))
	if sess := SessionFromContext(r.Context()); sess != nil {
		createdBy = sess.Username
		if createdBy == "" {
			createdBy = sess.UserID
		}
		if tenantID == "" {
			tenantID = strings.TrimSpace(sess.TenantID)
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"createdBy": createdBy,
		"role":      r.FormValue("role"),
		"maxUses":   maxUses,
		"ttlHours":  ttl,
		"tenantId":  tenantID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/invites", bytes.NewReader(payload))
	if err != nil {
		http.Redirect(w, r, "/invites", http.StatusSeeOther)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
		req.Header.Set("X-Auth-Claims-Tenant", tenantID)
	}
	resp, err := invitesHTTPDo(ctx, req)
	if err != nil {
		http.Redirect(w, r, "/invites?msg="+url.QueryEscape("create failed"), http.StatusSeeOther)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		http.Redirect(w, r, "/invites?msg="+url.QueryEscape(string(body)), http.StatusSeeOther)
		return
	}
	var inv struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &inv)
	link := base + "/invite?token=" + url.QueryEscape(inv.Token)
	http.Redirect(w, r, "/invites?created="+url.QueryEscape(link), http.StatusSeeOther)
}

func (h *Handler) InvitesRevoke(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), invitesPageTimeout)
	defer cancel()
	id := r.PathValue("id")
	base := h.authHTTPBase()
	if base == "" || id == "" {
		http.Redirect(w, r, "/invites", http.StatusSeeOther)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/api/invites/"+url.PathEscape(id), nil)
	if err != nil {
		http.Redirect(w, r, "/invites", http.StatusSeeOther)
		return
	}
	resp, err := invitesHTTPDo(ctx, req)
	if err != nil {
		http.Redirect(w, r, "/invites", http.StatusSeeOther)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	http.Redirect(w, r, "/invites?msg=revoked", http.StatusSeeOther)
}

func (h *Handler) renderInvites(w http.ResponseWriter, r *http.Request, data templates.InvitesPageData) {
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Invites", nav, templates.InvitesPage(data)))
}
