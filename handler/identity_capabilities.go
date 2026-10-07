package handler

import (
	"net/http"

	templates "github.com/Muxcore-Media/admin-ui/templ"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	identityUsers   = "user management"
	identityTOTP    = "two-factor authentication"
	identityTokens  = "API token management"
	identityInvites = "invite management"
)

// Unsupported features are provider responses, not provider names. Do not cache
// this globally: discovery can select a different provider on the next request.
func (h *Handler) renderIdentityUnsupported(w http.ResponseWriter, r *http.Request, title, feature string, err error) bool {
	if status.Code(err) != codes.Unimplemented {
		return false
	}
	if title == "" {
		h.render(w, r, templates.IdentityUnavailable(feature))
	} else {
		h.render(w, r, templates.Layout(title, h.nav(r.URL.Path), templates.IdentityUnavailablePage(title, feature)))
	}
	return true
}

func identityErrorStatus(err error) int {
	switch status.Code(err) {
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusBadGateway
	}
}

func identityReadError(action string, err error) string {
	switch status.Code(err) {
	case codes.PermissionDenied:
		return "You do not have permission to " + action + "."
	case codes.Unauthenticated:
		return "Your identity session is no longer authenticated. Sign in again."
	default:
		return "Could not " + action + ". Try again when the identity provider is available."
	}
}
