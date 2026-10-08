package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"github.com/Muxcore-Media/userdata-local/parental"

	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
)

// Trusted migration of the admin-local parental.json into the authoritative
// provider policy (ADR-0031 §5). The only input is the protected parental.json
// plus AuthService.ListUsers; userdata blobs are never read. Every write is a
// revision-checked PUT made with the running admin's bearer, so the provider's
// own admin check applies. Nothing is overwritten, nothing is deleted.

const (
	parentalMigrateAction = "admin.parental.migrate"
	parentalMigrateRuns   = 50 // runs kept in parental-migration.json
)

// Plan kinds. Only the first two ever produce a provider write.
const (
	planRestricted   = "restricted"   // the legacy entry sets at least one restriction
	planUnrestricted = "unrestricted" // default or absent entry; needs the explicit opt-in
	planSkip         = "skip"         // unmappable; the account stays unconfigured (fails closed)
)

// Outcome codes reported per account.
const (
	outCreated     = "created"        // PUT with expected_revision 0 succeeded
	outWouldCreate = "would-create"   // dry run: account is unconfigured
	outEqual       = "already-equal"  // configured and equal after normalization
	outConflict    = "conflict"       // configured and different: not overwritten
	outStale       = "stale"          // provider answered 409
	outSkipped     = "skipped"        // unmappable legacy entry
	outNotSelected = "not-selected"   // unrestricted candidate, opt-in not ticked
	outUnauth      = "unauthorized"   // provider 401
	outForbidden   = "forbidden"      // provider 403
	outNotFound    = "not-found"      // provider 404
	outError       = "error"          // anything else, including unreachable
	outOptInOnly   = "would-optin"    // dry run: unrestricted candidate that needs the opt-in
	outPlanUnknown = "provider-error" // dry run: could not read the current policy
)

var parentalMigrateMu sync.Mutex

// planEntry is one account's mapped policy. The plan, in user-ID order, is part
// of the dry-run digest; display-only fields (user names, roles) are not.
type planEntry struct {
	UserID string           `json:"user_id"`
	Kind   string           `json:"kind"`
	Reason string           `json:"reason,omitempty"`
	Policy *parental.Policy `json:"policy,omitempty"`
}

type planUser struct {
	Username string
	Roles    []string
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// mapLegacyParental applies ADR-0031 §5.3 to one legacy entry. pin_hash is
// deliberately never read here.
func mapLegacyParental(userID string, entry parentalSettings, has bool) planEntry {
	e := planEntry{UserID: userID}
	unrestricted := func(reason string) planEntry {
		p := parental.Policy{Version: 1, Mode: "unrestricted"}
		e.Kind, e.Reason, e.Policy = planUnrestricted, reason, &p
		return e
	}
	skip := func(reason string) planEntry {
		e.Kind, e.Reason = planSkip, reason
		return e
	}
	if !has {
		return unrestricted("no legacy entry")
	}
	rating := strings.ToUpper(strings.TrimSpace(entry.MaxParentalRating))
	if rating != "" && !parentalRatingSupported(rating) {
		return skip(fmt.Sprintf("unsupported maximum rating %q; the account stays unconfigured", clip(rating, 40)))
	}
	blocked := splitParentalTags(entry.BlockedTags)
	allowed := splitParentalTags(entry.AllowedTags)
	if !entry.KidsMode && rating == "" && len(blocked) == 0 && len(allowed) == 0 {
		return unrestricted("legacy entry sets no restriction")
	}
	p, err := parental.Normalize(parental.Policy{Version: 1, Mode: "restricted", Rules: &parental.Rules{
		KidsMode:     entry.KidsMode,
		MaxRating:    rating,
		BlockedTags:  blocked,
		AllowedTags:  allowed,
		AllowUnrated: entry.AllowUnrated,
	}})
	if err != nil {
		return skip("invalid tag list (" + err.Error() + "); the account stays unconfigured")
	}
	e.Kind, e.Policy = planRestricted, &p
	return e
}

// buildMigrationPlan maps every listed account. Legacy entries for accounts
// that are not listed are reported as orphans and never sent anywhere.
func buildMigrationPlan(users []*authv1.UserInfo, legacy map[string]parentalSettings) ([]planEntry, map[string]planUser, []string) {
	plan := make([]planEntry, 0, len(users))
	info := make(map[string]planUser, len(users))
	for _, u := range users {
		id := u.GetId()
		if id == "" {
			continue
		}
		if _, dup := info[id]; dup {
			continue
		}
		info[id] = planUser{Username: u.GetUsername(), Roles: append([]string(nil), u.GetRoles()...)}
		entry, has := legacy[id]
		plan = append(plan, mapLegacyParental(id, entry, has))
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].UserID < plan[j].UserID })
	var orphans []string
	for id := range legacy {
		if _, ok := info[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	return plan, info, orphans
}

// migrationDigest binds a write run to the exact parental.json bytes and plan
// the operator reviewed in the dry run.
func migrationDigest(raw []byte, plan []planEntry) string {
	planJSON, _ := json.Marshal(plan)
	fileSum := sha256.Sum256(raw)
	h := sha256.New()
	h.Write([]byte(hex.EncodeToString(fileSum[:])))
	h.Write([]byte{'\n'})
	h.Write(planJSON)
	return hex.EncodeToString(h.Sum(nil))
}

type migrationResult struct {
	UserID   string `json:"user_id"`
	Username string `json:"username,omitempty"`
	Kind     string `json:"kind"`
	Outcome  string `json:"outcome"`
	Detail   string `json:"detail,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Revision int64  `json:"revision,omitempty"`
}

type migrationRun struct {
	At      string            `json:"at"`
	Actor   string            `json:"actor"`
	Digest  string            `json:"digest"`
	OptIn   bool              `json:"opt_in_unrestricted"`
	Results []migrationResult `json:"results"`
}

type migrationFile struct {
	Version int            `json:"version"`
	Runs    []migrationRun `json:"runs"`
}

func parentalMigrationFilePath() string {
	return adminDataFile("ADMIN_UI_PARENTAL_MIGRATION_FILE", "parental-migration.json")
}

// readMigrationRecord loads parental-migration.json. A missing file is an
// empty history; an unreadable or malformed one is an error so the run history
// is never silently replaced.
func readMigrationRecord() (migrationFile, error) {
	f := migrationFile{Version: 1}
	raw, err := os.ReadFile(parentalMigrationFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return migrationFile{}, fmt.Errorf("read parental-migration.json: %w", err)
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return migrationFile{}, fmt.Errorf("parental-migration.json is not valid JSON: %w", err)
	}
	return f, nil
}

func recordMigrationRun(run migrationRun) error {
	f, err := readMigrationRecord()
	if err != nil {
		return err
	}
	f.Version = 1
	f.Runs = append(f.Runs, run)
	if len(f.Runs) > parentalMigrateRuns {
		f.Runs = f.Runs[len(f.Runs)-parentalMigrateRuns:]
	}
	return writeFile0600(parentalMigrationFilePath(), f)
}

// outcomeFromError maps a provider error to a reportable outcome. For a write
// only a definite client-side refusal proves nothing was applied.
func outcomeFromError(err error, writing bool) (string, string) {
	switch policyStatus(err) {
	case http.StatusUnauthorized:
		return outUnauth, "identity provider rejected the session"
	case http.StatusForbidden:
		return outForbidden, "provider refused (admin role and same tenant required)"
	case http.StatusNotFound:
		return outNotFound, "account or policy service not found"
	case http.StatusConflict:
		return outStale, "the policy changed during the run; run the dry run again"
	}
	if writing && !writeNotApplied(err) {
		return outError, "no definite answer from the policy service: the write may or may not have been applied; run the dry run again to check"
	}
	return outError, "policy service unavailable or returned an unusable answer"
}

// migrateAccount evaluates one planned account and, unless dryRun, writes it.
// It GETs the current policy first (ADR-0031 §5.4) and never overwrites a
// configured policy that differs.
func (h *Handler) migrateAccount(ctx context.Context, sess *session.Session, e planEntry, optIn, dryRun bool) migrationResult {
	res := migrationResult{UserID: e.UserID, Kind: e.Kind, Detail: e.Reason}
	if e.Policy != nil {
		res.Mode = e.Policy.Mode
	}
	if e.Kind == planSkip {
		res.Outcome = outSkipped
		return res
	}
	if e.Kind == planUnrestricted && !optIn && !dryRun {
		res.Outcome, res.Detail = outNotSelected, "untouched: the explicit unrestricted opt-in was not ticked"
		return res
	}
	current, err := h.getParentalPolicy(ctx, sess, e.UserID)
	if err != nil {
		res.Outcome, res.Detail = outcomeFromError(err, false)
		if dryRun && res.Outcome == outError {
			res.Outcome = outPlanUnknown
		}
		return res
	}
	if current.State == "configured" {
		res.Revision = current.Revision
		if parentalPoliciesEqual(*current.Policy, *e.Policy) {
			res.Outcome, res.Detail = outEqual, "already configured identically; nothing to do"
		} else {
			res.Outcome, res.Detail = outConflict, "a different policy is already configured ("+describeParentalPolicy(*current.Policy)+"); the provider is the authority, so it was not overwritten"
		}
		return res
	}
	if dryRun {
		if e.Kind == planUnrestricted {
			res.Outcome, res.Detail = outOptInOnly, "unconfigured; would be set unrestricted only if the opt-in is ticked"
		} else {
			res.Outcome, res.Detail = outWouldCreate, "unconfigured; would be created ("+describeParentalPolicy(*e.Policy)+")"
		}
		return res
	}
	saved, err := h.putParentalPolicy(ctx, sess, e.UserID, 0, *e.Policy)
	if err != nil {
		res.Outcome, res.Detail = outcomeFromError(err, true)
		return res
	}
	res.Outcome, res.Revision, res.Detail = outCreated, saved.Revision, "created ("+describeParentalPolicy(*e.Policy)+")"
	return res
}

var migrationOutcomeLabels = map[string]string{
	outCreated:     "Created",
	outWouldCreate: "Would create",
	outOptInOnly:   "Would create unrestricted (opt-in only)",
	outEqual:       "Already equal, skipped",
	outConflict:    "Conflict, not overwritten",
	outStale:       "Stale, not written",
	outSkipped:     "Skipped",
	outNotSelected: "Not selected, untouched",
	outUnauth:      "Not authorized",
	outForbidden:   "Forbidden",
	outNotFound:    "Not found",
	outError:       "Error",
	outPlanUnknown: "Could not read current policy",
}

func policyMapSummary(e planEntry) string {
	if e.Policy == nil {
		return "no policy (stays unconfigured)"
	}
	return describeParentalPolicy(*e.Policy)
}

func (h *Handler) migrateForbidden(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusForbidden)
	h.render(w, r, templates.Layout("Forbidden", h.nav(r.URL.Path), templates.Forbidden()))
}

// ParentalMigratePage renders the migration entry page (admin only).
func (h *Handler) ParentalMigratePage(w http.ResponseWriter, r *http.Request) {
	if _, admin := parentalActor(r); !admin {
		h.migrateForbidden(w, r)
		return
	}
	h.render(w, r, templates.Layout("Parental migration", h.nav(r.URL.Path), templates.ParentalMigratePage()))
}

// ParentalMigrate runs the migration. phase=dry-run (default) is read-only.
// phase=apply writes only when digest matches the plan recomputed now.
func (h *Handler) ParentalMigrate(w http.ResponseWriter, r *http.Request) {
	sess, admin := parentalActor(r)
	if !admin {
		// The provider enforces admin on PUT too; refuse before any call.
		h.migrateForbidden(w, r)
		return
	}
	_ = r.ParseForm()
	w.Header().Set("Cache-Control", "no-store")
	fail := func(msg string) {
		h.render(w, r, templates.ParentalMigrateResult(templates.ParentalMigrateData{Error: msg}))
	}
	if strings.TrimSpace(sess.AuthLocalToken) == "" {
		fail("This session has no identity-provider bearer, so the policy service cannot be called. Sign in again.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	users, err := h.listUsersForMigration(ctx)
	if err != nil {
		fail(identityReadError("list accounts", err))
		return
	}
	src, err := readParentalLegacy()
	if err != nil {
		fail("The legacy parental.json could not be read, so nothing can be migrated: " + err.Error())
		return
	}
	plan, info, orphans := buildMigrationPlan(users, src.Entries)
	digest := migrationDigest(src.Raw, plan)

	data := templates.ParentalMigrateData{Digest: digest, Orphans: orphans}
	if !src.Found {
		data.SourceMissing, data.SourcePath = true, parentalFilePath()
	}
	for _, e := range plan {
		if e.Kind == planUnrestricted {
			data.OptInCount++
		}
	}

	apply := r.FormValue("phase") == "apply"
	if apply && strings.TrimSpace(r.FormValue("digest")) != digest {
		data.Error = "The legacy file or the account list changed since the dry run (or no dry-run digest was supplied), so nothing was written. Review the new dry run below and apply again."
		apply = false
	}

	if apply {
		// Refuse before any provider write if the run history cannot be kept.
		if _, err := readMigrationRecord(); err != nil {
			fail("Nothing was written: the existing migration record cannot be read, and it will not be overwritten (" + err.Error() + "). Repair or move parental-migration.json, then try again.")
			return
		}
		// A committed write must be recorded even if the browser goes away
		// mid-run, so the apply runs on its own bounded context.
		var applyCancel context.CancelFunc
		ctx, applyCancel = context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer applyCancel()
	}

	parentalMigrateMu.Lock()
	defer parentalMigrateMu.Unlock()

	optIn := apply && r.FormValue("optin_unrestricted") == "1"
	results := make([]migrationResult, 0, len(plan))
	for _, e := range plan {
		res := h.migrateAccount(ctx, sess, e, optIn, !apply)
		res.Username = info[e.UserID].Username
		results = append(results, res)
		data.Rows = append(data.Rows, migrationRow(res, e, info[e.UserID]))
	}

	if !apply {
		data.Phase = "dry-run"
		h.render(w, r, templates.ParentalMigrateResult(data))
		return
	}

	data.Phase = "applied"
	data.OptIn = optIn
	run := migrationRun{At: time.Now().UTC().Format(time.RFC3339), Actor: sess.UserID, Digest: digest, OptIn: optIn, Results: results}
	for _, res := range results {
		details := map[string]string{"outcome": res.Outcome, "kind": res.Kind, "digest": digest[:16]}
		if res.Mode != "" {
			details["mode"] = res.Mode
		}
		if res.Revision > 0 {
			details["revision"] = strconv.FormatInt(res.Revision, 10)
		}
		h.auditLog(r.Context(), sess.UserID, parentalMigrateAction, "user", res.UserID, details)
		if res.Outcome == outCreated {
			data.Created++
		}
	}
	if err := recordMigrationRun(run); err != nil {
		slog.Warn("parental migration record not written", "error", err)
		data.Error = "The policy writes above completed, but parental-migration.json could not be written: " + err.Error() + ". The audit log still holds every outcome."
	}
	h.render(w, r, templates.ParentalMigrateResult(data))
}

func migrationRow(res migrationResult, e planEntry, u planUser) templates.ParentalMigrateRow {
	row := templates.ParentalMigrateRow{
		UserID:       res.UserID,
		Username:     u.Username,
		Mapped:       policyMapSummary(e),
		Outcome:      res.Outcome,
		OutcomeLabel: migrationOutcomeLabels[res.Outcome],
		Detail:       res.Detail,
		Revision:     res.Revision,
	}
	if e.Kind == planUnrestricted {
		row.Reason = e.Reason // "no legacy entry" vs "legacy entry sets no restriction"
	}
	if e.Kind == planRestricted && (hasRole(u.Roles, "admin") || hasRole(u.Roles, "manager")) {
		row.Note = "Holds the admin or manager role: the restriction is applied but is not a security boundary."
	}
	return row
}

func (h *Handler) listUsersForMigration(ctx context.Context) ([]*authv1.UserInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, usersDialTimeout+usersReadTimeout)
	defer cancel()
	client, conn, err := h.authClient(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	resp, err := client.ListUsers(authContextWithToken(ctx), &authv1.ListUsersRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetUsers(), nil
}
