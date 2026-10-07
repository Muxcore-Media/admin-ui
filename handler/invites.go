package handler

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	templates "github.com/Muxcore-Media/admin-ui/templ"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

const (
	invitesDialTimeout = 3 * time.Second
	invitesReadTimeout = 5 * time.Second
	invitesPageTimeout = invitesDialTimeout + invitesReadTimeout + time.Second
)

// Redemption still uses the provider's existing public HTTP flow.
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
		CreatedLink: strings.TrimSpace(r.URL.Query().Get("created")),
		Message:     strings.TrimSpace(r.URL.Query().Get("msg")),
	}
	client, conn, err := h.authClient(ctx)
	if err != nil {
		data.SoftEmpty = true
		data.Error = "Identity provider unavailable. Try again later."
		h.renderInvites(w, r, data)
		return
	}
	defer func() { _ = conn.Close() }()
	resp, err := client.ListInvites(authContextWithToken(ctx), &authv1.ListInvitesRequest{})
	if h.renderIdentityUnsupported(w, r, "Invite links", identityInvites, err) {
		return
	}
	if err != nil {
		data.SoftEmpty = true
		data.Error = identityReadError("load invites", err)
		h.renderInvites(w, r, data)
		return
	}
	for _, inv := range resp.GetInvites() {
		data.Invites = append(data.Invites, templates.InviteRow{
			ID: inv.GetId(), Prefix: inv.GetPrefix(), CreatedBy: inv.GetCreatedBy(), Role: inv.GetRole(),
			MaxUses: int(inv.GetMaxUses()), UseCount: int(inv.GetUseCount()), ExpiresAt: inv.GetExpiresAt(),
			CreatedAt: inv.GetCreatedAt(), Revoked: strings.TrimSpace(inv.GetRevokedAt()) != "" && inv.GetRevokedAt() != "0001-01-01T00:00:00Z",
		})
	}
	h.renderInvites(w, r, data)
}

func (h *Handler) InvitesCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}
	var maxUses int64
	if value := strings.TrimSpace(r.FormValue("max_uses")); value != "" {
		var err error
		maxUses, err = strconv.ParseInt(value, 10, 32)
		if err != nil {
			http.Error(w, "invalid maximum uses", http.StatusBadRequest)
			return
		}
	}
	// The form/HTTP convention is <=0 unlimited; the RPC reserves 0 for its
	// single-use default and represents unlimited with a negative value.
	if maxUses <= 0 {
		maxUses = -1
	}
	var ttlHours int64
	if value := strings.TrimSpace(r.FormValue("ttl_hours")); value != "" {
		var err error
		ttlHours, err = strconv.ParseInt(value, 10, 64)
		if err != nil || ttlHours > int64((1<<63-1)/time.Hour) {
			http.Error(w, "invalid invite lifetime", http.StatusBadRequest)
			return
		}
	}
	var ttlSeconds int64
	if ttlHours > 0 {
		ttlSeconds = ttlHours * 3600
	}
	tenantID := strings.TrimSpace(r.FormValue("tenant_id"))
	if sess := SessionFromContext(r.Context()); sess != nil && tenantID == "" {
		tenantID = strings.TrimSpace(sess.TenantID)
	}
	ctx, cancel := context.WithTimeout(r.Context(), invitesPageTimeout)
	defer cancel()
	client, conn, err := h.authClient(ctx)
	if err != nil {
		http.Error(w, "identity provider unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = conn.Close() }()
	resp, err := client.CreateInvite(authContextWithToken(ctx), &authv1.CreateInviteRequest{
		Role: strings.TrimSpace(r.FormValue("role")), TenantId: tenantID,
		MaxUses: int32(maxUses), TtlSeconds: ttlSeconds,
	})
	if h.renderIdentityUnsupported(w, r, "Invite links", identityInvites, err) {
		return
	}
	if err != nil {
		http.Error(w, "could not create invite", identityErrorStatus(err))
		return
	}
	if resp.GetError() != "" {
		http.Error(w, resp.GetError(), http.StatusBadRequest)
		return
	}
	if resp.GetToken() == "" {
		http.Error(w, "identity provider returned no invite token", http.StatusBadGateway)
		return
	}
	link := h.publicOrigin(r) + "/invite/redeem?token=" + url.QueryEscape(resp.GetToken())
	http.Redirect(w, r, "/invites?created="+url.QueryEscape(link), http.StatusSeeOther)
}

func (h *Handler) InvitesRevoke(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "invite id required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), invitesPageTimeout)
	defer cancel()
	client, conn, err := h.authClient(ctx)
	if err != nil {
		http.Error(w, "identity provider unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = conn.Close() }()
	resp, err := client.RevokeInvite(authContextWithToken(ctx), &authv1.RevokeInviteRequest{InviteId: id})
	if h.renderIdentityUnsupported(w, r, "Invite links", identityInvites, err) {
		return
	}
	if err != nil {
		http.Error(w, "could not revoke invite", identityErrorStatus(err))
		return
	}
	if resp.GetError() != "" {
		http.Error(w, resp.GetError(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/invites?msg=revoked", http.StatusSeeOther)
}

func (h *Handler) renderInvites(w http.ResponseWriter, r *http.Request, data templates.InvitesPageData) {
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Invites", nav, templates.InvitesPage(data)))
}
