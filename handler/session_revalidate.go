package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Muxcore-Media/admin-ui/internal/meshdial"
	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const sessionUnavailableMessage = "Identity or authorization service unavailable. Your sign-in was kept; try again."

// Each check includes discovery and its RPC. Do not pass this deadline to the
// protected handler: streams are checked at establishment only.
const sessionCheckTimeout = 8 * time.Second

// Replace inherited credentials with the current local binding, including
// clearing them for local-only sessions. Browser headers are never a source.
func sessionCheckContext(ctx context.Context, bearer string) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-auth-token")
	md.Delete("authorization")
	if strings.TrimSpace(bearer) != "" {
		md.Set("x-auth-token", bearer)
		md.Set("authorization", "Bearer "+bearer)
	}
	return metadata.NewOutgoingContext(ctx, md)
}

// revalidateBoundSession checks the provider bearer attached to a local admin
// session. A definitively invalid bearer signs the operator out. A provider
// outage, cancellation or unexpected status keeps the cookie and returns 503.
// The returned session is a deep copy carrying current public claims.
func (h *Handler) revalidateBoundSession(w http.ResponseWriter, r *http.Request, adminToken string, snap session.Session) (session.Session, bool) {
	bearer := snap.AuthLocalToken
	ctx, cancel := context.WithTimeout(sessionCheckContext(r.Context(), bearer), sessionCheckTimeout)
	defer cancel()

	client, conn, err := h.authClient(ctx)
	if err != nil {
		slog.Warn("session revalidation discovery unavailable", "user", snap.UserID, "path", r.URL.Path)
		writeSessionUnavailable(w)
		return session.Session{}, false
	}
	defer func() { _ = conn.Close() }()

	resp, err := client.Validate(ctx, &authv1.ValidateRequest{Token: bearer})
	if !h.sessionBindingCurrent(w, r, adminToken, snap) {
		return session.Session{}, false
	}
	if err != nil {
		h.sessionCheckFailed(w, r, adminToken, snap, sessionRPCStatus(err))
		return session.Session{}, false
	}
	if resp == nil {
		writeSessionUnavailable(w)
		return session.Session{}, false
	}
	if !resp.GetValid() || strings.TrimSpace(resp.GetUserId()) == "" || resp.GetUserId() != snap.UserID {
		reason := "invalid"
		if resp != nil && resp.GetValid() && resp.GetUserId() != snap.UserID {
			reason = "identity-mismatch"
		}
		h.rejectBoundSession(w, r, adminToken, snap, reason)
		return session.Session{}, false
	}

	updated, result := h.Sessions.CommitValidatedClaims(adminToken, bearer, resp.GetUserId(), resp.GetUsername(), resp.GetTenantId(), resp.GetRoles())
	switch result {
	case session.CommitApplied:
		return updated, true
	case session.CommitGone:
		h.clearSessionCookie(w)
		redirectToLogin(w, r)
		return session.Session{}, false
	case session.CommitIdentityMismatch:
		h.rejectBoundSession(w, r, adminToken, snap, "identity-mismatch")
		return session.Session{}, false
	default:
		slog.Warn("session revalidation observed a replaced provider bearer", "user", snap.UserID, "path", r.URL.Path)
		writeSessionUnavailable(w)
		return session.Session{}, false
	}
}

func (h *Handler) rejectBoundSession(w http.ResponseWriter, r *http.Request, adminToken string, snap session.Session, reason string) {
	// The response concerns the old binding. A concurrent replacement must not
	// be revoked even if it happened after the post-RPC snapshot check.
	if !h.Sessions.RevokeIfBound(adminToken, snap.UserID, snap.AuthLocalToken) {
		if _, exists := h.Sessions.Snapshot(adminToken); exists {
			writeSessionUnavailable(w)
			return
		}
	} else {
		h.auditLog(r.Context(), snap.UserID, "admin.session.rejected", "session", "", map[string]string{"reason": reason})
	}
	h.clearSessionCookie(w)
	redirectToLogin(w, r)
}

func (h *Handler) sessionBindingCurrent(w http.ResponseWriter, r *http.Request, adminToken string, expected session.Session) bool {
	current, ok := h.Sessions.Snapshot(adminToken)
	if !ok {
		h.clearSessionCookie(w)
		redirectToLogin(w, r)
		return false
	}
	if current.UserID != expected.UserID || current.AuthLocalToken != expected.AuthLocalToken {
		writeSessionUnavailable(w)
		return false
	}
	return true
}

func (h *Handler) checkAuthorized(ctx context.Context, sess *session.Session) int {
	ctx, cancel := context.WithTimeout(sessionCheckContext(ctx, sess.AuthLocalToken), sessionCheckTimeout)
	defer cancel()
	mod, err := h.findFirstModule(ctx, capAuthorizer)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return http.StatusServiceUnavailable
	}
	conn, err := meshdial.NewClient(addr)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	defer func() { _ = conn.Close() }()
	resp, err := authv1.NewAuthServiceClient(conn).Can(ctx, &authv1.CanRequest{
		UserId: sess.UserID, Action: "admin.access", Resource: "admin.ui",
	})
	if err != nil {
		return sessionRPCStatus(err)
	}
	if resp == nil {
		return http.StatusServiceUnavailable
	}
	if !resp.GetAllowed() {
		return http.StatusForbidden
	}
	return http.StatusOK
}

func sessionRPCStatus(err error) int {
	switch status.Code(err) {
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	default:
		return http.StatusServiceUnavailable
	}
}

func (h *Handler) sessionCheckFailed(w http.ResponseWriter, r *http.Request, adminToken string, snap session.Session, code int) {
	switch code {
	case http.StatusUnauthorized:
		h.rejectBoundSession(w, r, adminToken, snap, "unauthenticated")
	case http.StatusForbidden:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		h.render(w, r, templates.Forbidden())
	default:
		writeSessionUnavailable(w)
	}
}

func (h *Handler) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func writeSessionUnavailable(w http.ResponseWriter) {
	http.Error(w, sessionUnavailableMessage, http.StatusServiceUnavailable)
}
