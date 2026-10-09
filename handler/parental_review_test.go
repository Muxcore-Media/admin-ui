package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review findings on PR #66.

// Finding 1: the PIN route must use the same fail-closed reader as the
// migration. A damaged file is refused and left byte-for-byte as found.
func TestParentalPINRefusesToRewriteDamagedLegacyFile(t *testing.T) {
	e := newParentalEnv(t, user("kid", "Kid"), user("other", "Other"))
	if err := saveParentalMap(map[string]parentalSettings{"kid": {MaxParentalRating: "G", KidsMode: true}}); err != nil {
		t.Fatal(err)
	}
	good := mustReadFile(t, parentalFilePath())
	damaged := good[:len(good)-2] // truncated by two bytes
	if err := os.WriteFile(parentalFilePath(), damaged, 0o600); err != nil {
		t.Fatal(err)
	}

	w := e.postPIN("other", form("pin", "1234"))
	if !strings.Contains(w.Body.String(), "PIN not saved") || !strings.Contains(w.Body.String(), "nothing was written") {
		t.Fatalf("no clear refusal:\n%s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "PIN updated") {
		t.Fatal("reported success")
	}
	if got := mustReadFile(t, parentalFilePath()); string(got) != string(damaged) {
		t.Fatalf("damaged parental.json was rewritten:\n%s", got)
	}
	if _, puts := e.prov.blobState(); puts != 0 {
		t.Fatal("blob written after a refused PIN save")
	}
	// The migration still refuses; kid never becomes an unrestricted candidate.
	body := e.migrate(form("phase", "dry-run"), e.sess).Body.String()
	if strings.Contains(body, outOptInOnly) || !strings.Contains(body, `data-testid="parental-migrate-error"`) {
		t.Fatalf("dry run after refused PIN save:\n%s", body)
	}
	// Clearing a PIN is refused the same way.
	e.postPIN("other", form("clear_pin", "1"))
	if got := mustReadFile(t, parentalFilePath()); string(got) != string(damaged) {
		t.Fatal("clear_pin rewrote the damaged file")
	}
}

// Finding 2: a missing source is called out, and the wording follows it.
func TestParentalMigrateWarnsWhenSourceFileIsMissing(t *testing.T) {
	e := newParentalEnv(t, user("a", "A"))
	_, body := e.dryRun(t)
	for _, want := range []string{
		`data-testid="parental-migrate-source-missing"`, "parental.json not found at " + parentalFilePath(),
		"because parental.json was not found", "no legacy entry",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "These accounts have no legacy restriction") {
		t.Error("the 'no legacy restriction' wording is false when the file is missing")
	}

	e2 := migrationFixture(t)
	_, body = e2.dryRun(t)
	if strings.Contains(body, "parental.json not found") || strings.Contains(body, "because parental.json was not found") {
		t.Errorf("unexpected missing-file warning:\n%s", body)
	}
	for _, want := range []string{"These accounts have no legacy restriction", "legacy entry sets no restriction", "no legacy entry"} {
		if !strings.Contains(body, want) {
			t.Errorf("existing file: missing %q", want)
		}
	}
}

// Finding 3: unrestricted plus rules is a contradiction, not a silent drop.
func TestParentalFormRejectsUnrestrictedWithRuleFields(t *testing.T) {
	for _, extra := range [][]string{
		{"kids_mode", "1"}, {"max_rating", "PG"}, {"blocked_tags", "gore"}, {"allowed_tags", "family"}, {"allow_unrated", "1"},
	} {
		e := newParentalEnv(t, user("u1", "alice"))
		f := form("expected_revision", "0", "mode", "unrestricted")
		f.Set(extra[0], extra[1])
		w := e.postForm("u1", f)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%v: status %d, want 400", extra, w.Code)
		}
		if w.Header().Get(swapErrorHeader) != "1" {
			t.Errorf("%v: 400 body would not be shown by the page", extra)
		}
		body := w.Body.String()
		if !strings.Contains(body, "Unrestricted mode cannot be combined with rules") || strings.Contains(body, `data-testid="parental-saved"`) {
			t.Errorf("%v: not rejected clearly:\n%s", extra, body)
		}
		if e.prov.count(http.MethodPut, "") != 0 || e.prov.doc("u1").State != "unconfigured" {
			t.Errorf("%v: policy written", extra)
		}
	}
	e := newParentalEnv(t, user("u1", "alice"))
	if w := e.postForm("u1", form("expected_revision", "0", "mode", "unrestricted")); w.Code != http.StatusOK {
		t.Fatalf("plain unrestricted status %d", w.Code)
	}
	// Other input errors are 400 too.
	if w := e.postForm("u1", form("expected_revision", "1", "mode", "restricted", "max_rating", "15")); w.Code != http.StatusBadRequest {
		t.Fatalf("bad rating status %d", w.Code)
	}
}

// Finding 4: closing the browser must not abort a running apply.
func TestParentalMigrateApplyContinuesAfterRequestCancellation(t *testing.T) {
	e := migrationFixture(t)
	digest, _ := e.dryRun(t)

	ctx, cancel := context.WithCancel(context.Background())
	once := false
	e.prov.onPut = func() {
		if !once {
			once = true
			cancel() // the operator closes the tab during the first write
		}
	}
	r := e.request(http.MethodPost, "/users/parental/migrate", form("phase", "apply", "digest", digest), e.sess, "")
	r = r.WithContext(context.WithValue(ctx, ctxSessionKey, e.sess))
	w := httptest.NewRecorder()
	e.h.ParentalMigrate(w, r)

	if got := e.prov.count(http.MethodPut, ""); got != 3 {
		t.Fatalf("PUTs = %d, want all 3 despite the cancelled request", got)
	}
	for _, u := range []string{"kid", "teen", "adm"} {
		if e.prov.doc(u).State != "configured" {
			t.Errorf("%s not configured", u)
		}
	}
	rec, err := readMigrationRecord()
	if err != nil || len(rec.Runs) != 1 {
		t.Fatalf("record = %+v err=%v", rec, err)
	}
	for _, res := range rec.Runs[0].Results {
		if res.Outcome == outError {
			t.Errorf("%s recorded as error although the request was merely cancelled", res.UserID)
		}
	}
	if len(e.auditsFor(parentalMigrateAction)) != 6 {
		t.Fatalf("audits = %d", len(e.auditsFor(parentalMigrateAction)))
	}
}

// Finding 5: "nothing was changed" only where it is certain.
func TestParentalWriteErrorsClaimNothingChangedOnlyWhenCertain(t *testing.T) {
	certain := map[string]func(*parentalEnv){
		"401": func(e *parentalEnv) { e.prov.force["PUT u1"] = 401 },
		"403": func(e *parentalEnv) { e.prov.force["PUT u1"] = 403 },
		"404": func(e *parentalEnv) { e.prov.force["PUT u1"] = 404 },
	}
	uncertain := map[string]func(*parentalEnv){
		"500":         func(e *parentalEnv) { e.prov.force["PUT u1"] = 500 },
		"503":         func(e *parentalEnv) { e.prov.force["PUT u1"] = 503 },
		"unreachable": func(e *parentalEnv) { e.h.UserdataURL = "https://127.0.0.1:1" },
		"mismatched acknowledgement": func(e *parentalEnv) {
			e.prov.raw["PUT u1"] = `{"user_id":"u1","tenant_id":"tenant-1","state":"configured","revision":1,"policy":{"version":1,"mode":"unrestricted","rules":null}}`
		},
	}
	for name, setup := range certain {
		e := newParentalEnv(t, user("u1", "alice"))
		setup(e)
		body := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG")).Body.String()
		if !strings.Contains(body, "Nothing was changed") {
			t.Errorf("%s: should say nothing changed:\n%s", name, body)
		}
	}
	for name, setup := range uncertain {
		e := newParentalEnv(t, user("u1", "alice"))
		setup(e)
		body := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG")).Body.String()
		if strings.Contains(body, "Nothing was changed") || !strings.Contains(body, "may not have been saved") {
			t.Errorf("%s: wrong certainty:\n%s", name, body)
		}
	}
	// Migration outcomes follow the same rule.
	e := migrationFixture(t)
	digest, _ := e.dryRun(t)
	e.prov.force["PUT kid"] = 500
	body := e.apply(digest, false).Body
	if !strings.Contains(body, "may or may not have been applied") {
		t.Fatalf("uncertain migration write not flagged:\n%s", body)
	}
}

// Finding 6a: an unreadable run history is never replaced.
func TestParentalMigrateRefusesUnreadableRecord(t *testing.T) {
	e := migrationFixture(t)
	digest, _ := e.dryRun(t)
	garbage := []byte(`{"version":1,"runs":[{"at":`)
	if err := os.WriteFile(parentalMigrationFilePath(), garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	res := e.apply(digest, true)
	if !strings.Contains(res.Body, "Nothing was written") || !strings.Contains(res.Body, "will not be overwritten") {
		t.Fatalf("no clear refusal:\n%s", res.Body)
	}
	if e.prov.count(http.MethodPut, "") != 0 {
		t.Fatal("provider written although the run could not be recorded")
	}
	if got := mustReadFile(t, parentalMigrationFilePath()); string(got) != string(garbage) {
		t.Fatal("unreadable record was overwritten")
	}
	if err := recordMigrationRun(migrationRun{}); err == nil {
		t.Fatal("recordMigrationRun must refuse to replace an unreadable history")
	}
	// A dry run is read-only and unaffected.
	if body := e.migrate(form("phase", "dry-run"), e.sess).Body.String(); !strings.Contains(body, "Dry run only") {
		t.Fatalf("dry run blocked:\n%s", body)
	}
}

// Finding 6b: a stale tmp with wider permissions never leaks through.
func TestWriteFile0600IgnoresPreexistingWiderTempFile(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	for name, path := range map[string]string{"parental.json": parentalFilePath(), "parental-migration.json": parentalMigrationFilePath()} {
		tmp := path + ".tmp"
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tmp, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(tmp, 0o644); err != nil {
			t.Fatal(err)
		}
		var err error
		if name == "parental.json" {
			err = saveParentalMap(map[string]parentalSettings{"u1": {PINHash: "x"}})
		} else {
			err = recordMigrationRun(migrationRun{Actor: "admin1"})
		}
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", name, info.Mode().Perm())
		}
		if _, err := os.Stat(tmp); !os.IsNotExist(err) {
			t.Errorf("%s: stale tmp remains (%v)", name, err)
		}
		if strings.Contains(string(mustReadFile(t, path)), "stale") {
			t.Errorf("%s: stale content published", name)
		}
	}
	// And through the PIN route end to end.
	if err := os.WriteFile(parentalFilePath()+".tmp", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.postPIN("u1", form("pin", "1234"))
	if info, err := os.Stat(parentalFilePath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("PIN route left %v %v", info, err)
	}
}
