package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// providerSessionContext binds administrative RPCs to exactly the current
// end-user bearer. Inherited metadata and a module identity cannot replace it.
// requireAuth revalidates a bound bearer before these handlers run; this
// helper only forwards the bearer already accepted for the request.
func providerSessionContext(ctx context.Context) (context.Context, bool) {
	sess := SessionFromContext(ctx)
	if sess == nil || strings.TrimSpace(sess.AuthLocalToken) == "" {
		return ctx, false
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("x-auth-token", sess.AuthLocalToken)
	return metadata.NewOutgoingContext(ctx, md), true
}

func (h *Handler) DevicesPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("scope") == "local" {
		var sessions []session.SessionInfo
		var errMsg string
		if h.Sessions == nil {
			errMsg = "Local admin-panel session store unavailable."
		} else {
			sessions = h.Sessions.List()
		}
		h.render(w, r, templates.Layout("Local admin-panel sessions", h.nav("/devices"), templates.DevicesLivePage(sessions, errMsg)))
		return
	}
	data := templates.ProviderSessionsData{FilterUserID: r.URL.Query().Get("user_id")}
	if scope := r.URL.Query().Get("scope"); scope != "" && scope != "provider" {
		data.Error = "Unknown session view."
		h.renderProviderSessions(w, r, data, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer cancel()
	ctx, ok := providerSessionContext(ctx)
	if !ok {
		data.Error = "Sign in again to manage identity-provider sessions."
		h.renderProviderSessions(w, r, data, http.StatusUnauthorized)
		return
	}
	client, conn, err := h.authClient(ctx)
	if err != nil {
		data.Error = "Identity provider unavailable. Try again later."
		h.renderProviderSessions(w, r, data, http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = conn.Close() }()
	resp, err := client.ListSessions(ctx, &authv1.ListSessionsRequest{
		UserId: data.FilterUserID, PageSize: 100, PageToken: r.URL.Query().Get("page_token"),
	})
	if err != nil {
		h.providerSessionsFailure(w, r, data, "load sessions", err)
		return
	}
	for _, s := range resp.GetSessions() {
		data.Sessions = append(data.Sessions, templates.ProviderSessionRow{
			ID: s.GetSessionId(), UserID: s.GetUserId(), Username: s.GetUsername(),
			Kind: s.GetKind(), CreatedAt: s.GetCreatedAt(), ExpiresAt: s.GetExpiresAt(),
		})
	}
	if resp.GetNextPageToken() != "" {
		data.NextPageURL = templates.ProviderSessionListURL(data.FilterUserID, resp.GetNextPageToken())
	}
	if r.URL.Query().Get("revoked") == "1" {
		data.Message = "Session revocation request completed. Apps apply it when they next validate the provider session."
	}
	h.renderProviderSessions(w, r, data, http.StatusOK)
}

func (h *Handler) DevicesProviderRevoke(w http.ResponseWriter, r *http.Request) {
	data := templates.ProviderSessionsData{}
	if err := r.ParseForm(); err != nil {
		data.Error = "Invalid session revocation form."
		h.renderProviderSessions(w, r, data, http.StatusBadRequest)
		return
	}
	data.FilterUserID = r.PostForm.Get("filter_user_id")
	userID, sessionID := r.PostForm.Get("user_id"), r.PostForm.Get("session_id")
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" {
		data.Error = "Both user ID and session ID are required."
		h.renderProviderSessions(w, r, data, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout+time.Second)
	defer cancel()
	ctx, ok := providerSessionContext(ctx)
	if !ok {
		data.Error = "Sign in again to manage identity-provider sessions."
		h.renderProviderSessions(w, r, data, http.StatusUnauthorized)
		return
	}
	client, conn, err := h.authClient(ctx)
	if err != nil {
		data.Error = "Identity provider unavailable. Try again later."
		h.renderProviderSessions(w, r, data, http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = conn.Close() }()
	_, err = client.RevokeSession(ctx, &authv1.RevokeSessionRequest{UserId: userID, SessionId: sessionID})
	if err != nil {
		h.providerSessionsFailure(w, r, data, "revoke this session", err)
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.session.revoke", "session", sessionID, map[string]string{"user_id": userID})
	}
	location := templates.ProviderSessionListURL(data.FilterUserID, "")
	separator := "?"
	if strings.Contains(location, "?") {
		separator = "&"
	}
	http.Redirect(w, r, location+separator+"revoked=1", http.StatusSeeOther)
}

func (h *Handler) providerSessionsFailure(w http.ResponseWriter, r *http.Request, data templates.ProviderSessionsData, action string, err error) {
	if status.Code(err) == codes.Unimplemented {
		data.Unsupported = true
		h.renderProviderSessions(w, r, data, http.StatusOK)
		return
	}
	code := identityErrorStatus(err)
	data.Error = identityReadError(action, err)
	if status.Code(err) == codes.InvalidArgument {
		code = http.StatusBadRequest
		data.Error = "The identity provider rejected the session parameters. Check the user filter or return to the first page."
	}
	h.renderProviderSessions(w, r, data, code)
}

func (h *Handler) renderProviderSessions(w http.ResponseWriter, r *http.Request, data templates.ProviderSessionsData, code int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	h.render(w, r, templates.Layout("Identity-provider sessions", h.nav("/devices"), templates.ProviderSessionsPage(data)))
}

// DevicesRevoke and DevicesRename operate only on local admin-panel IDs. They
// never interpret these cookie hashes as provider management IDs.
func (h *Handler) DevicesRevoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("token")
	if id != "" && h.Sessions != nil {
		h.Sessions.RevokeByID(id)
	}
	http.Redirect(w, r, "/devices?scope=local", http.StatusSeeOther)
}

func (h *Handler) DevicesRename(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("token")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid local session form", http.StatusBadRequest)
		return
	}
	if id != "" && h.Sessions != nil {
		h.Sessions.RenameByID(id, r.FormValue("label"))
	}
	http.Redirect(w, r, "/devices?scope=local", http.StatusSeeOther)
}
