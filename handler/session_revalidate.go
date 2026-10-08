package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const sessionUnavailableMessage = "Identity provider unavailable. Your sign-in was kept; try again."

// revalidateBoundSession checks the provider bearer attached to a local admin
// session. A definitively invalid bearer signs the operator out. A provider
// outage, cancellation or unexpected status keeps the cookie and returns 503.
// The returned session is a deep copy carrying current public claims.
func (h *Handler) revalidateBoundSession(w http.ResponseWriter, r *http.Request, adminToken string, snap session.Session) (session.Session, bool) {
	bearer := strings.TrimSpace(snap.AuthLocalToken)
	ctx, cancel := context.WithTimeout(r.Context(), usersDialTimeout+usersReadTimeout)
	defer cancel()

	client, conn, err := h.authClient(ctx)
	if err != nil {
		slog.Warn("session revalidation unavailable", "user", snap.UserID, "path", r.URL.Path, "error", err)
		writeSessionUnavailable(w)
		return session.Session{}, false
	}
	defer func() { _ = conn.Close() }()

	resp, err := client.Validate(ctx, &authv1.ValidateRequest{Token: bearer})
	if err != nil {
		if status.Code(err) == codes.Unauthenticated {
			h.rejectBoundSession(w, r, adminToken, snap.UserID, "unauthenticated")
			return session.Session{}, false
		}
		slog.Warn("session revalidation unavailable", "user", snap.UserID, "path", r.URL.Path, "code", status.Code(err))
		writeSessionUnavailable(w)
		return session.Session{}, false
	}
	if resp == nil || !resp.GetValid() || strings.TrimSpace(resp.GetUserId()) == "" || resp.GetUserId() != snap.UserID {
		reason := "invalid"
		if resp != nil && resp.GetValid() && resp.GetUserId() != snap.UserID {
			reason = "identity-mismatch"
		}
		h.rejectBoundSession(w, r, adminToken, snap.UserID, reason)
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
		h.rejectBoundSession(w, r, adminToken, snap.UserID, "identity-mismatch")
		return session.Session{}, false
	default:
		slog.Warn("session revalidation observed a replaced provider bearer", "user", snap.UserID, "path", r.URL.Path)
		writeSessionUnavailable(w)
		return session.Session{}, false
	}
}

func (h *Handler) rejectBoundSession(w http.ResponseWriter, r *http.Request, adminToken, userID, reason string) {
	if h.Sessions != nil {
		h.Sessions.Revoke(adminToken)
	}
	h.auditLog(r.Context(), userID, "admin.session.rejected", "session", "", map[string]string{"reason": reason})
	h.clearSessionCookie(w)
	redirectToLogin(w, r)
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
