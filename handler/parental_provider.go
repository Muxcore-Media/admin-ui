package handler

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/parental"

	"github.com/Muxcore-Media/admin-ui/internal/userdatahttp"
	"github.com/Muxcore-Media/admin-ui/session"
)

// The authoritative parental policy lives in userdata-local (ADR-0030). This
// file is the only place admin-ui talks to that resource: every request goes
// through the published checked client over mTLS (ADR-0033, internal/
// userdatahttp) and carries exactly the signed-in admin's identity-provider
// bearer plus the target user selector, never a browser header, a query string
// or a legacy fallback.

const parentalPolicyPath = "/api/parental-policy"

// parentalRatingTokens mirrors the provider's supported maximum-rating set
// (userdata-local parental.Normalize). The public package does not export the
// list, so TestParentalRatingTokensMatchProvider fails if the two ever differ
// for these tokens. The form offers only these values: no free text.
var parentalRatingTokens = []string{
	"G", "TV-Y", "TV-Y7", "TV-Y7-FV", "ALL", "E",
	"PG", "TV-G", "TV-PG", "E10+",
	"PG-13", "TV-14", "T",
	"R", "TV-MA", "M", "MA",
	"NC-17", "AO", "X",
}

// parentalRatingSupported reports whether the provider accepts token as a
// non-empty maximum rating. It asks the provider's own normalizer.
func parentalRatingSupported(token string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	_, err := parental.Normalize(parental.Policy{Version: 1, Mode: "restricted", Rules: &parental.Rules{
		MaxRating: token, BlockedTags: []string{}, AllowedTags: []string{},
	}})
	return err == nil
}

// policyView is a validated provider document.
type policyView = userdatahttp.Document

func policyStatus(err error) int { return userdatahttp.Status(err) }

// writeNotApplied reports whether a failed write is certain not to have been
// applied: nothing was sent, the provider's module admission refused it, or the
// provider refused it with a definite client-side status. Anything else
// (timeouts, connection loss, redirects, 5xx, an acknowledgement that does not
// match) leaves it unknown whether the PUT committed.
func writeNotApplied(err error) bool { return userdatahttp.NotApplied(err) }

// parentalPoliciesEqual compares two policies after provider normalization.
func parentalPoliciesEqual(a, b parental.Policy) bool { return userdatahttp.PoliciesEqual(a, b) }

// userdataOrigin resolves the provider origin: the configured
// ADMIN_UI_USERDATA_URL as given, else the discovered address with the scheme
// of the selected transport mode (https unless explicit insecure dev). A
// discovered address is never upgraded from or downgraded to plaintext, and a
// failure to resolve is unavailability, never a retry over HTTP.
func (h *Handler) userdataOrigin(ctx context.Context) (string, error) {
	if origin := userdatahttp.NormalizeOrigin(h.UserdataURL); origin != "" {
		return origin, nil
	}
	if origin := userdatahttp.NormalizeOrigin(os.Getenv(userdatahttp.OriginEnv)); origin != "" {
		return origin, nil
	}
	if h.Core == nil {
		return "", userdatahttp.Unresolved("no userdata origin is configured and core is not connected")
	}
	dialCtx, cancel := context.WithTimeout(ctx, userdataDialTimeout)
	defer cancel()
	mod, err := h.findFirstModule(dialCtx, capUserdataLocal)
	if err != nil {
		return "", userdatahttp.Unresolved("userdata-local is not registered with core")
	}
	insecure, err := h.userdata.Insecure()
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(mod.GetHttpAddr())
	if addr != "" && !strings.Contains(addr, "://") {
		addr = normalizeDialAddr(mod.GetId(), addr)
	}
	return userdatahttp.OriginFromAdvertised(addr, insecure)
}

// userdataClient returns the checked client for the current origin. It is
// built lazily, after Main has enrolled admin-ui's mesh identity.
func (h *Handler) userdataClient(ctx context.Context) (*httpclient.Client, error) {
	origin, err := h.userdataOrigin(ctx)
	if err != nil {
		return nil, err
	}
	return h.userdata.Client(origin)
}

// sessionBearer is the signed-in operator's identity-provider bearer: the
// only user authorization admin-ui ever sends to the provider.
func sessionBearer(sess *session.Session) (string, error) {
	if sess == nil || strings.TrimSpace(sess.AuthLocalToken) == "" {
		return "", userdatahttp.Unresolved("this session has no identity-provider bearer")
	}
	return strings.TrimSpace(sess.AuthLocalToken), nil
}

// logUserdataFailure records why the provider gave no application answer.
// Application answers (401, 403 policy.forbidden, 409, ...) are not logged
// here; nothing logged includes a bearer.
func logUserdataFailure(op string, err error) {
	if err == nil || policyStatus(err) != 0 {
		return
	}
	slog.Warn("userdata provider unavailable", "op", op, "reason", userdatahttp.Describe(err),
		"module_forbidden", userdatahttp.ModuleForbidden(err))
}

// getParentalPolicy reads target's policy with the admin's bearer.
func (h *Handler) getParentalPolicy(ctx context.Context, sess *session.Session, target string) (policyView, error) {
	bearer, err := sessionBearer(sess)
	if err != nil {
		return policyView{}, err
	}
	c, err := h.userdataClient(ctx)
	if err == nil {
		var view policyView
		view, err = userdatahttp.GetPolicy(ctx, c, bearer, userdatahttp.Target{UserID: target, TenantID: sess.TenantID})
		if err == nil {
			return view, nil
		}
	}
	logUserdataFailure("policy.get", err)
	return policyView{}, err
}

// putParentalPolicy replaces target's policy when the stored revision still
// equals expected. policy must already be normalized.
func (h *Handler) putParentalPolicy(ctx context.Context, sess *session.Session, target string, expected int64, policy parental.Policy) (policyView, error) {
	bearer, err := sessionBearer(sess)
	if err != nil {
		return policyView{}, err
	}
	c, err := h.userdataClient(ctx)
	if err == nil {
		var view policyView
		view, err = userdatahttp.PutPolicy(ctx, c, bearer, userdatahttp.Target{UserID: target, TenantID: sess.TenantID}, expected, policy)
		if err == nil {
			return view, nil
		}
	}
	logUserdataFailure("policy.put", err)
	return policyView{}, err
}

// parentalErrorMessage is the operator-facing explanation of a provider error.
// None of these states is ever shown as an empty or unrestricted policy. For
// writes it claims "nothing was changed" only when that is certain. Transport
// and module-admission failures are about this service, never the operator's
// role, and never sign the operator out.
func parentalErrorMessage(err error, writing bool) string {
	tail := " Nothing was changed."
	if writing && !writeNotApplied(err) {
		tail = " The change may not have been saved; reload the form to check the current policy before trying again."
	}
	action, done := "read", "read"
	if writing {
		action, done = "change", "changed"
	}
	switch {
	case userdatahttp.ModuleForbidden(err):
		return "Userdata unavailable: the parental policy service does not permit this service (admin-ui's mesh identity) to make this request. This is a deployment problem, not your admin role, and this account is not unrestricted." + tail
	case userdatahttp.NotConfigured(err):
		return "Userdata unavailable: admin-ui has no verified connection to the parental policy service (check ADMIN_UI_USERDATA_URL and admin-ui's mesh identity), so this account's policy could not be " + done + ". This is not an unrestricted account." + tail
	}
	switch policyStatus(err) {
	case http.StatusUnauthorized:
		return "The identity provider no longer accepts your session. Sign in again." + tail
	case http.StatusForbidden:
		return "You are not allowed to " + action + " this account's parental policy (it needs the admin role in the account's tenant)." + tail
	case http.StatusNotFound:
		return "The account or the parental policy service was not found." + tail
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return "The parental policy service rejected the request as invalid." + tail
	}
	return "The parental policy service is unavailable or returned an unusable answer, so this account's policy could not be " + done + ". This is not an unrestricted account." + tail
}
