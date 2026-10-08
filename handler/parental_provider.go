package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"github.com/Muxcore-Media/admin-ui/session"
	"github.com/Muxcore-Media/userdata-local/parental"
)

// The authoritative parental policy lives in userdata-local (ADR-0030). This
// file is the only place admin-ui talks to that resource: every request carries
// exactly the signed-in admin's identity-provider bearer plus the target user
// selector, never a browser header, a query string or a legacy fallback.

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

// policyHTTPError is a non-2xx answer from the provider.
type policyHTTPError struct {
	Status int
	Code   string
}

func (e *policyHTTPError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("parental policy provider: HTTP %d (%s)", e.Status, e.Code)
	}
	return fmt.Sprintf("parental policy provider: HTTP %d", e.Status)
}

// errPolicyUnavailable marks everything that is not a clean provider answer:
// no provider, connection failure, timeout, redirect, oversized or malformed
// bodies and envelope or scope mismatches. It must never read as unrestricted.
var errPolicyUnavailable = errors.New("parental policy provider unavailable")

func policyUnavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errPolicyUnavailable, fmt.Sprintf(format, args...))
}

func policyStatus(err error) int {
	var he *policyHTTPError
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

var policyCodePattern = regexp.MustCompile(`^[a-z0-9_.]{1,64}$`)

// policyView is a validated provider document.
type policyView struct {
	State     string // "unconfigured" or "configured"
	Revision  int64
	Policy    *parental.Policy
	UpdatedAt string
}

type policyEnvelope struct {
	UserID    string          `json:"user_id"`
	TenantID  string          `json:"tenant_id"`
	State     string          `json:"state"`
	Revision  int64           `json:"revision"`
	Policy    json.RawMessage `json:"policy"`
	UpdatedAt string          `json:"updated_at"`
}

// parseParentalDocument validates the response envelope strictly (ADR-0031 §3):
// known fields only, a coherent state/revision/policy triple and the exact
// target and tenant that were requested.
func parseParentalDocument(raw []byte, userID, tenantID string) (policyView, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var env policyEnvelope
	if err := dec.Decode(&env); err != nil {
		return policyView{}, policyUnavailable("malformed document")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return policyView{}, policyUnavailable("trailing data after document")
	}
	if env.UserID != userID || env.TenantID != tenantID {
		return policyView{}, policyUnavailable("document scope does not match the request")
	}
	switch env.State {
	case "unconfigured":
		if env.Revision != 0 || !bytes.Equal(bytes.TrimSpace(env.Policy), []byte("null")) {
			return policyView{}, policyUnavailable("incoherent unconfigured document")
		}
		return policyView{State: "unconfigured"}, nil
	case "configured":
		if env.Revision <= 0 {
			return policyView{}, policyUnavailable("incoherent configured document")
		}
		p, err := parental.DecodePolicy(env.Policy)
		if err != nil {
			return policyView{}, policyUnavailable("invalid stored policy")
		}
		return policyView{State: "configured", Revision: env.Revision, Policy: &p, UpdatedAt: env.UpdatedAt}, nil
	}
	return policyView{}, policyUnavailable("unknown policy state")
}

func (h *Handler) parentalPolicyURL(ctx context.Context) (string, error) {
	base := h.UserdataURL
	if base == "" {
		base = h.userdataBaseURL(ctx)
	}
	if base == "" {
		return "", policyUnavailable("userdata-local is not available")
	}
	return base + parentalPolicyPath, nil
}

// parentalPolicyDo performs one provider request on behalf of sess for target.
func (h *Handler) parentalPolicyDo(ctx context.Context, sess *session.Session, method, target string, body []byte) ([]byte, error) {
	if sess == nil || strings.TrimSpace(sess.AuthLocalToken) == "" {
		return nil, policyUnavailable("this session has no identity-provider bearer")
	}
	endpoint, err := h.parentalPolicyURL(ctx)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, userdataReadTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, endpoint, reader)
	if err != nil {
		return nil, policyUnavailable("bad request")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(sess.AuthLocalToken))
	req.Header.Set(muxcoreUserIDHeader, target)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Timeout: userdataReadTimeout,
		// A redirect would re-send the bearer to a location the operator never
		// configured; treat it as a failure.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, policyUnavailable("request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, parental.MaxBodyBytes+1))
	if err != nil || len(raw) > parental.MaxBodyBytes {
		return nil, policyUnavailable("unreadable or oversized response")
	}
	if resp.StatusCode != http.StatusOK {
		he := &policyHTTPError{Status: resp.StatusCode}
		var e struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(raw, &e) == nil && policyCodePattern.MatchString(e.Code) {
			he.Code = e.Code
		}
		return nil, he
	}
	return raw, nil
}

// getParentalPolicy reads target's policy with the admin's bearer.
func (h *Handler) getParentalPolicy(ctx context.Context, sess *session.Session, target string) (policyView, error) {
	raw, err := h.parentalPolicyDo(ctx, sess, http.MethodGet, target, nil)
	if err != nil {
		return policyView{}, err
	}
	return parseParentalDocument(raw, target, sess.TenantID)
}

// putParentalPolicy replaces target's policy when the stored revision still
// equals expected. policy must already be normalized.
func (h *Handler) putParentalPolicy(ctx context.Context, sess *session.Session, target string, expected int64, policy parental.Policy) (policyView, error) {
	body, err := json.Marshal(parental.Update{ExpectedRevision: expected, Policy: policy})
	if err != nil {
		return policyView{}, policyUnavailable("encode update")
	}
	raw, err := h.parentalPolicyDo(ctx, sess, http.MethodPut, target, body)
	if err != nil {
		return policyView{}, err
	}
	view, err := parseParentalDocument(raw, target, sess.TenantID)
	if err != nil {
		return policyView{}, err
	}
	// The answer must be the write we asked for, not just any valid document.
	if view.State != "configured" || view.Revision != expected+1 || !parentalPoliciesEqual(*view.Policy, policy) {
		return policyView{}, policyUnavailable("write acknowledgement does not match the request")
	}
	return view, nil
}

// parentalPoliciesEqual compares two policies after provider normalization.
func parentalPoliciesEqual(a, b parental.Policy) bool {
	na, errA := parental.Normalize(a)
	nb, errB := parental.Normalize(b)
	return errA == nil && errB == nil && reflect.DeepEqual(na, nb)
}

// writeNotApplied reports whether a failed write is certain not to have been
// applied: the provider refused it with a definite client-side status. Anything
// else (timeouts, connection loss, 5xx, an acknowledgement that does not match)
// leaves it unknown whether the PUT committed.
func writeNotApplied(err error) bool {
	switch policyStatus(err) {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusRequestEntityTooLarge:
		return true
	}
	return false
}

// parentalErrorMessage is the operator-facing explanation of a provider error.
// None of these states is ever shown as an empty or unrestricted policy. For
// writes it claims "nothing was changed" only when that is certain.
func parentalErrorMessage(err error, writing bool) string {
	tail := " Nothing was changed."
	if writing && !writeNotApplied(err) {
		tail = " The change may not have been saved; reload the form to check the current policy before trying again."
	}
	action, done := "read", "read"
	if writing {
		action, done = "change", "changed"
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
