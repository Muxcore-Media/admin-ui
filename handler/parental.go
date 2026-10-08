package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"github.com/Muxcore-Media/userdata-local/parental"

	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
)

// Parental controls. The authoritative restriction policy is the userdata-local
// provider resource (ADR-0030); this page reads and writes only that resource
// (ADR-0031 §5.5). The admin-local parental.json is a retained legacy source
// that the trusted migration reads (parental_migrate.go) and the PIN lock still
// uses until FR-AUTH-010. Restriction fields are never written to it or to the
// user's writable userdata blob any more.

var parentalMu sync.Mutex

// parentalSettings is the legacy parental.json entry. Only the PIN fields are
// still maintained by the UI; the rest are read by the migration.
type parentalSettings struct {
	MaxParentalRating string `json:"max_parental_rating"`
	BlockedTags       string `json:"blocked_tags"`
	AllowedTags       string `json:"allowed_tags"`
	AllowUnrated      bool   `json:"allow_unrated"`
	KidsMode          bool   `json:"kids_mode"`
	// PINHash is a salted SHA-256 hex digest; empty means no PIN is set.
	PINHash string `json:"pin_hash,omitempty"`
}

// hashParentalPIN returns a salted SHA-256 hex digest of a PIN.
// userID is the per-user salt so hashes are not reusable across accounts.
func hashParentalPIN(userID, pin string) string {
	h := sha256.Sum256([]byte(userID + ":" + pin))
	return hex.EncodeToString(h[:])
}

// validateParentalPIN returns an error if pin is not exactly 4–6 ASCII digits.
func validateParentalPIN(pin string) error {
	if len(pin) < 4 || len(pin) > 6 {
		return fmt.Errorf("PIN must be 4–6 digits")
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return fmt.Errorf("PIN must contain digits only")
		}
	}
	return nil
}

func loadParentalMap() map[string]parentalSettings {
	parentalMu.Lock()
	defer parentalMu.Unlock()
	raw, err := os.ReadFile(parentalFilePath())
	if err != nil {
		return map[string]parentalSettings{}
	}
	var m map[string]parentalSettings
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return map[string]parentalSettings{}
	}
	return m
}

// readParentalLegacy reads parental.json for the migration. Unlike
// loadParentalMap it fails closed: an unreadable or malformed file is an
// error, never "no entries", so a damaged file cannot turn every account into
// an unrestricted candidate. A missing file is a legitimately empty source.
func readParentalLegacy() ([]byte, map[string]parentalSettings, error) {
	parentalMu.Lock()
	defer parentalMu.Unlock()
	raw, err := os.ReadFile(parentalFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, map[string]parentalSettings{}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read parental.json: %w", err)
	}
	var m map[string]parentalSettings
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, nil, errors.New("parental.json is not a JSON object of per-user entries")
	}
	return raw, m, nil
}

func saveParentalMap(m map[string]parentalSettings) error {
	parentalMu.Lock()
	defer parentalMu.Unlock()
	return writeFile0600(parentalFilePath(), m)
}

// writeFile0600 atomically writes v as indented JSON with mode 0600 in a 0700
// directory, the same discipline parental.json has always had.
func writeFile0600(path string, v any) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func hasRole(roles []string, want string) bool {
	for _, r := range roles {
		if strings.EqualFold(strings.TrimSpace(r), want) {
			return true
		}
	}
	return false
}

// splitParentalTags splits a comma-separated tag list. Normalization (trim,
// lower-case, de-duplicate, sort) is the provider package's job.
func splitParentalTags(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// formError is an operator-facing validation message (a sentence, so it is not
// subject to the lowercase-error-string convention).
type formError string

func (e formError) Error() string { return string(e) }

// parentalFormInput is what the operator submitted, kept so a rejected or
// conflicting submission can be shown back without being applied.
type parentalFormInput struct {
	Mode         string
	MaxRating    string
	BlockedTags  string
	AllowedTags  string
	AllowUnrated bool
	KidsMode     bool
}

func parentalFormFromRequest(r *http.Request) parentalFormInput {
	return parentalFormInput{
		Mode:         strings.TrimSpace(r.FormValue("mode")),
		MaxRating:    strings.TrimSpace(r.FormValue("max_rating")),
		BlockedTags:  strings.TrimSpace(r.FormValue("blocked_tags")),
		AllowedTags:  strings.TrimSpace(r.FormValue("allowed_tags")),
		AllowUnrated: r.FormValue("allow_unrated") == "1",
		KidsMode:     r.FormValue("kids_mode") == "1",
	}
}

// policy builds the normalized provider policy for the submission.
func (f parentalFormInput) policy() (parental.Policy, error) {
	switch f.Mode {
	case "unrestricted":
		return parental.Normalize(parental.Policy{Version: 1, Mode: "unrestricted"})
	case "restricted":
	default:
		return parental.Policy{}, formError("Choose whether this account is unrestricted or restricted.")
	}
	if f.MaxRating != "" && !containsString(parentalRatingTokens, f.MaxRating) {
		return parental.Policy{}, formError("Choose a maximum rating from the list.")
	}
	p, err := parental.Normalize(parental.Policy{Version: 1, Mode: "restricted", Rules: &parental.Rules{
		KidsMode:     f.KidsMode,
		MaxRating:    f.MaxRating,
		BlockedTags:  splitParentalTags(f.BlockedTags),
		AllowedTags:  splitParentalTags(f.AllowedTags),
		AllowUnrated: f.AllowUnrated,
	}})
	if err != nil {
		return parental.Policy{}, formError("The tag lists are not valid: " + err.Error() + " (at most 64 tags of up to 128 characters each).")
	}
	return p, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// parentalDataFromView fills the form fields from a provider document.
func parentalDataFromView(userID string, v policyView) templates.ParentalData {
	d := templates.ParentalData{
		UserID:        userID,
		State:         v.State,
		Revision:      v.Revision,
		UpdatedAt:     v.UpdatedAt,
		RatingOptions: parentalRatingTokens,
	}
	if v.Policy != nil {
		d.Mode = v.Policy.Mode
		d.StoredMode = v.Policy.Mode
		if rules := v.Policy.Rules; rules != nil {
			d.KidsMode = rules.KidsMode
			d.MaxRating = rules.MaxRating
			d.BlockedTags = strings.Join(rules.BlockedTags, ", ")
			d.AllowedTags = strings.Join(rules.AllowedTags, ", ")
			d.AllowUnrated = rules.AllowUnrated
		}
	}
	return d
}

// parentalTargetPrivilege reports whether the target account holds a role for
// which a restriction is not a security boundary (ADR-0031 §2.7): "yes",
// "unknown" when the roles could not be read, or "".
func (h *Handler) parentalTargetPrivilege(ctx context.Context, userID string) string {
	ctx, cancel := context.WithTimeout(ctx, usersDialTimeout+usersReadTimeout)
	defer cancel()
	client, conn, err := h.authClient(ctx)
	if err != nil {
		return "unknown"
	}
	defer func() { _ = conn.Close() }()
	resp, err := client.ListUsers(authContextWithToken(ctx), &authv1.ListUsersRequest{})
	if err != nil {
		return "unknown"
	}
	for _, u := range resp.GetUsers() {
		if u.GetId() == userID {
			if hasRole(u.GetRoles(), "admin") || hasRole(u.GetRoles(), "manager") {
				return "yes"
			}
			return ""
		}
	}
	return "unknown"
}

func (h *Handler) renderParental(w http.ResponseWriter, r *http.Request, d templates.ParentalData) {
	d.PINSet = loadParentalMap()[d.UserID].PINHash != ""
	if d.RatingOptions == nil {
		d.RatingOptions = parentalRatingTokens
	}
	if d.LoadError == "" {
		d.Privileged = h.parentalTargetPrivilege(r.Context(), d.UserID)
	}
	w.Header().Set("Cache-Control", "no-store")
	if err := templates.UserParentalForm(d).Render(r.Context(), w); err != nil {
		return
	}
}

// loadAndRenderParental reads the provider document and renders it, or an
// error state that offers no form: without a verified revision there is
// nothing safe to edit, and a failure must never look like "no restrictions".
func (h *Handler) loadAndRenderParental(w http.ResponseWriter, r *http.Request, userID string, patch func(*templates.ParentalData)) {
	sess := SessionFromContext(r.Context())
	view, err := h.getParentalPolicy(r.Context(), sess, userID)
	if err != nil {
		d := templates.ParentalData{UserID: userID, LoadError: parentalErrorMessage(err, false)}
		if patch != nil {
			patch(&d)
		}
		h.renderParental(w, r, d)
		return
	}
	d := parentalDataFromView(userID, view)
	if patch != nil {
		patch(&d)
	}
	h.renderParental(w, r, d)
}

// UsersParental shows (GET) and saves (POST) the provider-backed policy.
func (h *Handler) UsersParental(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if r.Method != http.MethodPost {
		h.loadAndRenderParental(w, r, userID, nil)
		return
	}
	sess := SessionFromContext(r.Context())
	_ = r.ParseForm()

	expected, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("expected_revision")), 10, 64)
	if err != nil || expected < 0 {
		h.loadAndRenderParental(w, r, userID, func(d *templates.ParentalData) {
			d.Error = "The form did not say which revision it was based on, so nothing was saved. The current policy is shown; make your change again."
		})
		return
	}

	input := parentalFormFromRequest(r)
	policy, err := input.policy()
	if err != nil {
		h.renderParental(w, r, h.parentalDataFromInput(userID, expected, input, err.Error()))
		return
	}

	saved, err := h.putParentalPolicy(r.Context(), sess, userID, expected, policy)
	switch {
	case err == nil:
		if sess != nil {
			details := map[string]string{
				"mode":     policy.Mode,
				"revision": strconv.FormatInt(saved.Revision, 10),
			}
			if policy.Rules != nil {
				details["max_rating"] = policy.Rules.MaxRating
				details["kids_mode"] = strconv.FormatBool(policy.Rules.KidsMode)
			}
			h.auditLog(r.Context(), sess.UserID, "admin.parental.save", "user", userID, details)
		}
		d := parentalDataFromView(userID, saved)
		d.Saved = true
		h.renderParental(w, r, d)
	case policyStatus(err) == http.StatusConflict:
		// Someone else changed the policy first. Show what is stored now (a
		// fresh revision) and do not retry the write on the operator's behalf.
		attempted := describeParentalPolicy(policy)
		h.loadAndRenderParental(w, r, userID, func(d *templates.ParentalData) {
			d.Conflict = true
			d.Attempted = attempted
		})
	default:
		d := h.parentalDataFromInput(userID, expected, input, parentalErrorMessage(err, true))
		h.renderParental(w, r, d)
	}
}

// parentalDataFromInput rebuilds the form from a submission that was not
// applied, keeping the revision the operator was editing.
func (h *Handler) parentalDataFromInput(userID string, expected int64, in parentalFormInput, msg string) templates.ParentalData {
	state := "configured"
	if expected == 0 {
		state = "unconfigured"
	}
	return templates.ParentalData{
		UserID:        userID,
		State:         state,
		Revision:      expected,
		Mode:          in.Mode,
		MaxRating:     in.MaxRating,
		BlockedTags:   in.BlockedTags,
		AllowedTags:   in.AllowedTags,
		AllowUnrated:  in.AllowUnrated,
		KidsMode:      in.KidsMode,
		RatingOptions: parentalRatingTokens,
		Error:         msg,
	}
}

func describeParentalPolicy(p parental.Policy) string {
	if p.Mode != "restricted" || p.Rules == nil {
		return "unrestricted"
	}
	parts := []string{"restricted"}
	if p.Rules.KidsMode {
		parts = append(parts, "kids mode")
	}
	if p.Rules.MaxRating != "" {
		parts = append(parts, "max rating "+p.Rules.MaxRating)
	}
	if len(p.Rules.BlockedTags) > 0 {
		parts = append(parts, "blocked tags "+strings.Join(p.Rules.BlockedTags, ", "))
	}
	if len(p.Rules.AllowedTags) > 0 {
		parts = append(parts, "allowed tags "+strings.Join(p.Rules.AllowedTags, ", "))
	}
	if p.Rules.AllowUnrated {
		parts = append(parts, "allow unrated")
	}
	return strings.Join(parts, "; ")
}

// UsersParentalPIN sets or clears the legacy PIN lock. It never touches the
// restriction policy and never sends the PIN or its hash to the policy
// provider. Behaviour is unchanged until FR-AUTH-010 replaces it.
func (h *Handler) UsersParentalPIN(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	_ = r.ParseForm()

	m := loadParentalMap()
	p := m[userID]
	updated := p
	switch {
	case r.FormValue("clear_pin") == "1":
		updated.PINHash = ""
	case strings.TrimSpace(r.FormValue("pin")) != "":
		newPIN := strings.TrimSpace(r.FormValue("pin"))
		if err := validateParentalPIN(newPIN); err != nil {
			msg := err.Error()
			h.loadAndRenderParental(w, r, userID, func(d *templates.ParentalData) { d.PINError = msg })
			return
		}
		updated.PINHash = hashParentalPIN(userID, newPIN)
	default:
		h.loadAndRenderParental(w, r, userID, func(d *templates.ParentalData) {
			d.PINError = "Enter a new PIN or choose to clear the current one."
		})
		return
	}

	m[userID] = updated
	if err := saveParentalMap(m); err != nil {
		msg := "PIN not saved: " + err.Error()
		h.loadAndRenderParental(w, r, userID, func(d *templates.ParentalData) { d.PINError = msg })
		return
	}
	if err := h.syncParentalPINToUserdata(r.Context(), userID, updated.PINHash); err != nil {
		msg := "userdata sync failed: " + err.Error()
		h.loadAndRenderParental(w, r, userID, func(d *templates.ParentalData) { d.PINError = msg })
		return
	}
	if sess := SessionFromContext(r.Context()); sess != nil {
		h.auditLog(r.Context(), sess.UserID, "admin.parental.pin", "user", userID, map[string]string{
			"pin_set": strconv.FormatBool(updated.PINHash != ""),
		})
	}
	h.loadAndRenderParental(w, r, userID, func(d *templates.ParentalData) { d.PINSaved = true })
}

// parentalActor returns the validated session and whether it holds the
// provider admin role.
func parentalActor(r *http.Request) (*session.Session, bool) {
	sess := SessionFromContext(r.Context())
	if sess == nil {
		return nil, false
	}
	return sess, hasRole(sess.Roles, "admin")
}
