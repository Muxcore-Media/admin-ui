package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	"github.com/Muxcore-Media/userdata-local/parental"
)

const pinHashKid = "deadbeef-pin-hash-kid-0123456789abcdef"

func migrationFixture(t *testing.T) *parentalEnv {
	t.Helper()
	e := newParentalEnv(t,
		user("kid", "Kid", "viewer"),
		user("teen", "Teen", "viewer"),
		user("bad", "Bad rating", "viewer"),
		user("open", "Open default", "viewer"),
		user("none", "No entry", "viewer"),
		user("adm", "Admin person", "admin"),
	)
	if err := saveParentalMap(map[string]parentalSettings{
		"kid":   {MaxParentalRating: " pg-13 ", BlockedTags: "Gore, violence", KidsMode: true, PINHash: pinHashKid},
		"teen":  {MaxParentalRating: "TV-14", AllowedTags: "family", AllowUnrated: true},
		"bad":   {MaxParentalRating: "15", BlockedTags: "gore"},
		"open":  {PINHash: "deadbeef-pin-hash-open"},
		"adm":   {MaxParentalRating: "R"},
		"ghost": {MaxParentalRating: "G", PINHash: "deadbeef-pin-hash-ghost"},
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

var digestRe = regexp.MustCompile(`name="digest" value="([0-9a-f]{64})"`)

func (e *parentalEnv) dryRun(t *testing.T) (string, string) {
	t.Helper()
	w := e.migrate(form("phase", "dry-run"), e.sess)
	if w.Code != http.StatusOK {
		t.Fatalf("dry run status %d: %s", w.Code, w.Body.String())
	}
	m := digestRe.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("dry run offered no digest:\n%s", w.Body.String())
	}
	return m[1], w.Body.String()
}

func (e *parentalEnv) apply(digest string, optIn bool) *httpResult {
	f := form("phase", "apply", "digest", digest)
	if optIn {
		f.Set("optin_unrestricted", "1")
	}
	w := e.migrate(f, e.sess)
	return &httpResult{Code: w.Code, Body: w.Body.String()}
}

type httpResult struct {
	Code int
	Body string
}

func rowOutcome(t *testing.T, body, userID string) string {
	t.Helper()
	re := regexp.MustCompile(`data-user="` + userID + `" data-outcome="([a-z-]*)"`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no row for %s in:\n%s", userID, body)
	}
	return m[1]
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// --- Mapping (ADR-0031 §5.3) ---

func TestMapLegacyParentalMapping(t *testing.T) {
	unrestricted := parental.Policy{Version: 1, Mode: "unrestricted"}
	for _, tc := range []struct {
		name  string
		entry *parentalSettings
		kind  string
		want  *parental.Policy
	}{
		{"no entry", nil, planUnrestricted, &unrestricted},
		{"all default", &parentalSettings{PINHash: "x"}, planUnrestricted, &unrestricted},
		{"allow_unrated alone sets no restriction", &parentalSettings{AllowUnrated: true}, planUnrestricted, &unrestricted},
		{"rating trimmed and upper-cased", &parentalSettings{MaxParentalRating: "  tv-14 "}, planRestricted,
			ptr(restrictedPolicy(parental.Rules{MaxRating: "TV-14"}))},
		{"tags split on commas then normalized", &parentalSettings{BlockedTags: " Gore,,violence , gore", AllowedTags: "Family"}, planRestricted,
			ptr(restrictedPolicy(parental.Rules{BlockedTags: []string{"gore", "violence"}, AllowedTags: []string{"family"}}))},
		{"kids mode alone is a restriction", &parentalSettings{KidsMode: true}, planRestricted,
			ptr(restrictedPolicy(parental.Rules{KidsMode: true}))},
		{"allow_unrated maps directly", &parentalSettings{MaxParentalRating: "PG", AllowUnrated: true}, planRestricted,
			ptr(restrictedPolicy(parental.Rules{MaxRating: "PG", AllowUnrated: true}))},
		{"unsupported rating skipped", &parentalSettings{MaxParentalRating: "15", KidsMode: true}, planSkip, nil},
		{"NR is not a ceiling", &parentalSettings{MaxParentalRating: "NR"}, planSkip, nil},
		{"invalid tag skipped", &parentalSettings{BlockedTags: strings.Repeat("x", 200)}, planSkip, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var entry parentalSettings
			if tc.entry != nil {
				entry = *tc.entry
			}
			got := mapLegacyParental("u", entry, tc.entry != nil)
			if got.Kind != tc.kind {
				t.Fatalf("kind = %s want %s (%s)", got.Kind, tc.kind, got.Reason)
			}
			if !reflect.DeepEqual(got.Policy, tc.want) {
				t.Fatalf("policy = %+v want %+v", got.Policy, tc.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestMigrationDigestBindsFileAndPlan(t *testing.T) {
	plan := []planEntry{mapLegacyParental("a", parentalSettings{MaxParentalRating: "PG"}, true)}
	base := migrationDigest([]byte(`{"a":{}}`), plan)
	if base != migrationDigest([]byte(`{"a":{}}`), plan) {
		t.Fatal("digest is not deterministic")
	}
	if base == migrationDigest([]byte(`{"a":{} }`), plan) {
		t.Fatal("a changed file must change the digest")
	}
	other := []planEntry{mapLegacyParental("a", parentalSettings{MaxParentalRating: "R"}, true)}
	if base == migrationDigest([]byte(`{"a":{}}`), other) {
		t.Fatal("a changed plan must change the digest")
	}
}

// --- Dry run ---

func TestParentalMigrateDryRunWritesNothing(t *testing.T) {
	e := migrationFixture(t)
	legacyBefore := mustReadFile(t, parentalFilePath())

	_, body := e.dryRun(t)

	if n := e.prov.count(http.MethodPut, ""); n != 0 {
		t.Fatalf("dry run sent %d PUTs", n)
	}
	if _, puts := e.prov.blobState(); puts != 0 {
		t.Fatal("dry run wrote a blob")
	}
	for _, u := range []string{"kid", "teen", "bad", "open", "none", "adm"} {
		if d := e.prov.doc(u); d.State != "unconfigured" {
			t.Errorf("%s changed: %+v", u, d)
		}
	}
	if _, err := os.Stat(parentalMigrationFilePath()); !os.IsNotExist(err) {
		t.Fatalf("dry run created parental-migration.json (err=%v)", err)
	}
	if !reflect.DeepEqual(legacyBefore, mustReadFile(t, parentalFilePath())) {
		t.Fatal("dry run modified parental.json")
	}
	if got := e.auditsFor(parentalMigrateAction); len(got) != 0 {
		t.Fatalf("dry run audited %d outcomes as migrations", len(got))
	}
	// It lists every listed account with its mapped policy and outcome.
	for _, want := range []string{
		"restricted; kids mode; max rating PG-13; blocked tags gore, violence",
		"restricted; max rating TV-14; allowed tags family; allow unrated",
		"no policy (stays unconfigured)", "unsupported maximum rating",
		"Would create unrestricted (opt-in only)",
		"Set unrestricted for 2 listed accounts", // open + none
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dry run missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "deadbeef") {
		t.Error("a PIN hash was rendered")
	}
	if strings.Contains(body, `name="optin_unrestricted" value="1" checked`) {
		t.Error("the unrestricted opt-in must be unchecked by default")
	}
	if !strings.Contains(body, `data-testid="parental-migrate-orphans"`) || !strings.Contains(body, "ghost") {
		t.Error("orphan legacy entry not reported")
	}
	if !strings.Contains(body, "Holds the admin or manager role") {
		t.Error("restricted admin account not flagged as non-boundary")
	}
	if rowOutcome(t, body, "kid") != outWouldCreate || rowOutcome(t, body, "bad") != outSkipped || rowOutcome(t, body, "open") != outOptInOnly {
		t.Error("dry-run outcomes wrong")
	}
	e.prov.assertCleanRequests(t)
}

// --- Apply ---

func TestParentalMigrateApplyCreatesThenRerunIsNoop(t *testing.T) {
	e := migrationFixture(t)
	legacyBefore := mustReadFile(t, parentalFilePath())

	digest, _ := e.dryRun(t)
	res := e.apply(digest, false)
	if res.Code != http.StatusOK {
		t.Fatalf("apply status %d", res.Code)
	}
	for u, want := range map[string]string{
		"kid": outCreated, "teen": outCreated, "adm": outCreated,
		"bad": outSkipped, "open": outNotSelected, "none": outNotSelected,
	} {
		if got := rowOutcome(t, res.Body, u); got != want {
			t.Errorf("%s outcome = %s want %s", u, got, want)
		}
	}
	kid := e.prov.doc("kid")
	wantKid := restrictedPolicy(parental.Rules{KidsMode: true, MaxRating: "PG-13", BlockedTags: []string{"gore", "violence"}})
	if kid.State != "configured" || kid.Revision != 1 || !reflect.DeepEqual(*kid.Policy, wantKid) {
		t.Fatalf("kid = %+v / %+v", kid, kid.Policy)
	}
	puts := e.prov.count(http.MethodPut, "")
	if puts != 3 {
		t.Fatalf("PUTs = %d, want 3", puts)
	}
	for _, r := range e.prov.requests() {
		if r.Method == http.MethodPut {
			update, err := parental.DecodeUpdate(r.Body)
			if err != nil || update.ExpectedRevision != 0 {
				t.Fatalf("PUT must be a revision-0 create: %v %+v", err, update)
			}
		}
	}
	// Unconfigured stays unconfigured and untouched without the opt-in.
	for _, u := range []string{"bad", "open", "none"} {
		if d := e.prov.doc(u); d.State != "unconfigured" {
			t.Errorf("%s was written: %+v", u, d)
		}
		if e.prov.count(http.MethodPut, u) != 0 {
			t.Errorf("%s: PUT without the opt-in or a mapping", u)
		}
	}
	if e.prov.count("", "bad") != 0 {
		t.Error("a skipped account must never reach the provider")
	}

	// Audit trail, one entry per account, and the migration record.
	audits := e.auditsFor(parentalMigrateAction)
	if len(audits) != 6 {
		t.Fatalf("audits = %d, want one per listed account", len(audits))
	}
	byUser := map[string]string{}
	for _, a := range audits {
		if a.Actor != "admin1" || a.Resource != "user" {
			t.Errorf("audit %+v", a)
		}
		byUser[a.ResourceID] = a.Details["outcome"]
	}
	if byUser["kid"] != outCreated || byUser["bad"] != outSkipped || byUser["open"] != outNotSelected {
		t.Errorf("audit outcomes = %v", byUser)
	}
	info, err := os.Stat(parentalMigrationFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("parental-migration.json mode = %v", info.Mode().Perm())
	}
	var rec migrationFile
	if err := json.Unmarshal(mustReadFile(t, parentalMigrationFilePath()), &rec); err != nil || len(rec.Runs) != 1 || len(rec.Runs[0].Results) != 6 || rec.Runs[0].Digest != digest {
		t.Fatalf("record = %+v err=%v", rec, err)
	}
	if strings.Contains(string(mustReadFile(t, parentalMigrationFilePath())), "deadbeef-pin-hash") {
		t.Fatal("PIN hash written to the migration record")
	}
	if !reflect.DeepEqual(legacyBefore, mustReadFile(t, parentalFilePath())) {
		t.Fatal("parental.json must be retained unchanged")
	}

	// Re-run: a fresh dry run, then apply again, must write nothing.
	putsBefore := e.prov.count(http.MethodPut, "")
	digest2, dry2 := e.dryRun(t)
	if rowOutcome(t, dry2, "kid") != outEqual {
		t.Fatalf("re-run dry outcome = %s", rowOutcome(t, dry2, "kid"))
	}
	res2 := e.apply(digest2, false)
	if e.prov.count(http.MethodPut, "") != putsBefore {
		t.Fatalf("re-run wrote: %d PUTs", e.prov.count(http.MethodPut, "")-putsBefore)
	}
	for _, u := range []string{"kid", "teen", "adm"} {
		if got := rowOutcome(t, res2.Body, u); got != outEqual {
			t.Errorf("re-run %s = %s", u, got)
		}
	}
	if e.prov.doc("kid").Revision != 1 {
		t.Fatal("re-run bumped a revision")
	}
	e.prov.assertCleanRequests(t)
}

func TestParentalMigrateDifferingConfiguredPolicyIsReportedNotOverwritten(t *testing.T) {
	e := migrationFixture(t)
	existing := restrictedPolicy(parental.Rules{MaxRating: "G"})
	e.prov.seed("kid", 7, existing)

	digest, dry := e.dryRun(t)
	if rowOutcome(t, dry, "kid") != outConflict {
		t.Fatalf("dry outcome = %s", rowOutcome(t, dry, "kid"))
	}
	res := e.apply(digest, true)
	if rowOutcome(t, res.Body, "kid") != outConflict {
		t.Fatalf("apply outcome = %s", rowOutcome(t, res.Body, "kid"))
	}
	if !strings.Contains(res.Body, "was not overwritten") {
		t.Error("conflict not explained")
	}
	got := e.prov.doc("kid")
	if got.Revision != 7 || !reflect.DeepEqual(*got.Policy, existing) {
		t.Fatalf("configured policy was overwritten: %+v", got)
	}
	if e.prov.count(http.MethodPut, "kid") != 0 {
		t.Fatal("a PUT was sent for the conflicting account")
	}
}

func TestParentalMigrateBadRatingIsSkippedAndStaysUnconfigured(t *testing.T) {
	e := migrationFixture(t)
	digest, _ := e.dryRun(t)
	res := e.apply(digest, true)
	if rowOutcome(t, res.Body, "bad") != outSkipped || !strings.Contains(res.Body, "unsupported maximum rating") {
		t.Fatalf("bad rating not reported as skipped:\n%s", res.Body)
	}
	if d := e.prov.doc("bad"); d.State != "unconfigured" {
		t.Fatalf("bad-rating account was configured: %+v", d)
	}
	if e.prov.count("", "bad") != 0 {
		t.Fatal("nothing may be sent for a skipped account")
	}
}

func TestParentalMigrateUnlistedAndUnselectedAccountsUntouchedWithoutOptIn(t *testing.T) {
	e := migrationFixture(t)
	digest, _ := e.dryRun(t)
	e.apply(digest, false)
	for _, u := range []string{"open", "none", "ghost"} {
		if e.prov.count(http.MethodPut, u) != 0 || e.prov.doc(u).State != "unconfigured" {
			t.Errorf("%s was written without the opt-in", u)
		}
	}
	if e.prov.count("", "ghost") != 0 {
		t.Fatal("a legacy entry for an unlisted account was sent to the provider")
	}

	// With the explicit opt-in the default/absent accounts become unrestricted.
	digest, _ = e.dryRun(t)
	res := e.apply(digest, true)
	for _, u := range []string{"open", "none"} {
		if rowOutcome(t, res.Body, u) != outCreated {
			t.Errorf("%s = %s", u, rowOutcome(t, res.Body, u))
		}
		d := e.prov.doc(u)
		if d.State != "configured" || d.Policy.Mode != "unrestricted" || d.Policy.Rules != nil {
			t.Errorf("%s = %+v", u, d)
		}
	}
	if e.prov.count("", "ghost") != 0 {
		t.Fatal("orphan sent")
	}
}

func TestParentalMigrateNonAdminCausesZeroWrites(t *testing.T) {
	e := migrationFixture(t)
	legacyBefore := mustReadFile(t, parentalFilePath())
	digest, _ := e.dryRun(t)
	before := len(e.prov.requests())

	for _, roles := range [][]string{{"manager"}, {"viewer"}, nil} {
		sess := &session.Session{UserID: "mgr", Roles: roles, AuthLocalToken: testAdminBearer, TenantID: testTenant}
		for _, f := range []map[string][]string{
			form("phase", "dry-run"), form("phase", "apply", "digest", digest, "optin_unrestricted", "1"),
		} {
			w := e.migrate(f, sess)
			if w.Code != http.StatusForbidden {
				t.Errorf("roles %v: status %d, want 403", roles, w.Code)
			}
		}
		w := httpGetMigratePage(e, sess)
		if w != http.StatusForbidden {
			t.Errorf("roles %v: page status %d, want 403", roles, w)
		}
	}
	if after := len(e.prov.requests()); after != before {
		t.Fatalf("non-admin caused %d provider calls", after-before)
	}
	if e.prov.count(http.MethodPut, "") != 0 {
		t.Fatal("non-admin wrote policy")
	}
	if _, err := os.Stat(parentalMigrationFilePath()); !os.IsNotExist(err) {
		t.Fatal("non-admin created a migration record")
	}
	if len(e.auditsFor(parentalMigrateAction)) != 0 {
		t.Fatal("non-admin produced migration audits")
	}
	if !reflect.DeepEqual(legacyBefore, mustReadFile(t, parentalFilePath())) {
		t.Fatal("parental.json changed")
	}
}

func httpGetMigratePage(e *parentalEnv, sess *session.Session) int {
	w := httptest.NewRecorder()
	e.h.ParentalMigratePage(w, e.request(http.MethodGet, "/users/parental/migrate", nil, sess, ""))
	return w.Code
}

func TestParentalMigrateSurfacesStale409(t *testing.T) {
	e := migrationFixture(t)
	digest, _ := e.dryRun(t)
	e.prov.raceOnce["kid"] = true // another writer configures kid between GET and PUT
	res := e.apply(digest, false)
	if rowOutcome(t, res.Body, "kid") != outStale {
		t.Fatalf("kid = %s\n%s", rowOutcome(t, res.Body, "kid"), res.Body)
	}
	if !strings.Contains(res.Body, "Stale, not written") {
		t.Error("stale outcome not labelled")
	}
	if d := e.prov.doc("kid"); d.Policy.Mode != "unrestricted" || d.Revision != 1 {
		t.Fatalf("our write must not have landed: %+v", d)
	}
	if rowOutcome(t, res.Body, "teen") != outCreated {
		t.Error("a stale account must not abort the others")
	}
}

func TestParentalMigrateReportsProviderRefusals(t *testing.T) {
	for status, want := range map[int]string{401: outUnauth, 403: outForbidden, 404: outNotFound, 500: outError, 503: outError} {
		e := migrationFixture(t)
		digest, _ := e.dryRun(t)
		e.prov.force["PUT kid"] = status
		res := e.apply(digest, false)
		if got := rowOutcome(t, res.Body, "kid"); got != want {
			t.Errorf("PUT %d: outcome %s want %s", status, got, want)
		}
		if d := e.prov.doc("kid"); d.State != "unconfigured" {
			t.Errorf("PUT %d: kid written", status)
		}
	}
	for status, want := range map[int]string{403: outForbidden, 404: outNotFound, 503: outPlanUnknown} {
		e := migrationFixture(t)
		e.prov.force["GET kid"] = status
		_, dry := e.dryRun(t)
		if got := rowOutcome(t, dry, "kid"); got != want {
			t.Errorf("dry-run GET %d: %s want %s", status, got, want)
		}
	}
}

func TestParentalMigrateNeverTransmitsPINHash(t *testing.T) {
	e := migrationFixture(t)
	digest, _ := e.dryRun(t)
	e.apply(digest, true)
	reqs := e.prov.requests()
	if e.prov.count(http.MethodPut, "") == 0 {
		t.Fatal("expected writes")
	}
	for _, r := range reqs {
		all := string(r.Body)
		for k, v := range r.Header {
			all += k + strings.Join(v, ",")
		}
		for _, secret := range []string{"pin", "deadbeef", pinHashKid} {
			if strings.Contains(strings.ToLower(all), secret) {
				t.Errorf("%s %v transmitted %q: %s", r.Method, r.UserIDs, secret, all)
			}
		}
	}
	if _, puts := e.prov.blobState(); puts != 0 {
		t.Fatal("migration wrote a userdata blob")
	}
}

func TestParentalMigrateApplyRequiresCurrentDigest(t *testing.T) {
	e := migrationFixture(t)
	digest, _ := e.dryRun(t)

	for name, f := range map[string]map[string][]string{
		"missing digest": form("phase", "apply"),
		"wrong digest":   form("phase", "apply", "digest", strings.Repeat("0", 64), "optin_unrestricted", "1"),
	} {
		res := e.migrate(f, e.sess)
		if e.prov.count(http.MethodPut, "") != 0 {
			t.Fatalf("%s: wrote", name)
		}
		if !strings.Contains(res.Body.String(), "nothing was written") {
			t.Errorf("%s: no explanation", name)
		}
	}

	// The legacy file changes after the dry run: the old digest is void.
	if err := saveParentalMap(map[string]parentalSettings{"kid": {MaxParentalRating: "G"}}); err != nil {
		t.Fatal(err)
	}
	res := e.apply(digest, true)
	if e.prov.count(http.MethodPut, "") != 0 {
		t.Fatal("stale digest wrote")
	}
	if !strings.Contains(res.Body, "changed since the dry run") {
		t.Fatalf("stale digest not explained:\n%s", res.Body)
	}
	if strings.Contains(res.Body, `data-phase="applied"`) {
		t.Fatal("stale digest rendered as applied")
	}
	if _, err := os.Stat(parentalMigrationFilePath()); !os.IsNotExist(err) {
		t.Fatal("record written for a refused run")
	}
}

func TestParentalMigrateRefusesUnreadableLegacyFile(t *testing.T) {
	e := migrationFixture(t)
	if err := os.WriteFile(parentalFilePath(), []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []map[string][]string{form("phase", "dry-run"), form("phase", "apply", "digest", "x", "optin_unrestricted", "1")} {
		body := e.migrate(f, e.sess).Body.String()
		if !strings.Contains(body, `data-testid="parental-migrate-error"`) || strings.Contains(body, "Would create") {
			t.Fatalf("unreadable file must stop the run:\n%s", body)
		}
	}
	if len(e.prov.requests()) != 0 {
		t.Fatal("provider contacted with an unreadable source")
	}
}

func TestParentalMigrateMissingFileIsAnEmptySourceAndNeedsOptIn(t *testing.T) {
	e := newParentalEnv(t, user("a", "A"))
	digest, dry := e.dryRun(t)
	if rowOutcome(t, dry, "a") != outOptInOnly {
		t.Fatalf("outcome %s", rowOutcome(t, dry, "a"))
	}
	e.apply(digest, false)
	if e.prov.count(http.MethodPut, "") != 0 {
		t.Fatal("no opt-in, no write")
	}
}

func TestParentalMigrateListUsersFailureStopsTheRun(t *testing.T) {
	e := migrationFixture(t)
	e.h.Core = nil // authClient cannot resolve the identity provider
	body := e.migrate(form("phase", "dry-run"), e.sess).Body.String()
	if !strings.Contains(body, `data-testid="parental-migrate-error"`) {
		t.Fatalf("no error:\n%s", body)
	}
	if len(e.prov.requests()) != 0 {
		t.Fatal("provider contacted")
	}
}

func TestParentalMigrateRequiresSessionBearer(t *testing.T) {
	e := migrationFixture(t)
	sess := *e.sess
	sess.AuthLocalToken = ""
	body := e.migrate(form("phase", "dry-run"), &sess).Body.String()
	if !strings.Contains(body, "no identity-provider bearer") || len(e.prov.requests()) != 0 {
		t.Fatalf("missing bearer not refused:\n%s", body)
	}
}

func TestParentalMigrateRecordAccumulatesRuns(t *testing.T) {
	e := migrationFixture(t)
	for i := 0; i < 2; i++ {
		digest, _ := e.dryRun(t)
		e.apply(digest, false)
	}
	var rec migrationFile
	if err := json.Unmarshal(mustReadFile(t, parentalMigrationFilePath()), &rec); err != nil || len(rec.Runs) != 2 {
		t.Fatalf("runs = %d err=%v", len(rec.Runs), err)
	}
}
