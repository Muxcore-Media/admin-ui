package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Muxcore-Media/userdata-local/parental"
	"google.golang.org/grpc/codes"
)

// --- Provider contract guards ---

func TestParentalRatingTokensMatchProvider(t *testing.T) {
	for _, token := range parentalRatingTokens {
		if !parentalRatingSupported(token) {
			t.Errorf("form offers %q but the provider rejects it", token)
		}
	}
	for _, token := range []string{"NR", "UR", "15", "12A", "PG-14", "", "pg"} {
		if containsString(parentalRatingTokens, token) {
			t.Errorf("form must not offer %q", token)
		}
	}
	// Unsupported tokens are rejected by the provider package itself.
	for _, token := range []string{"NR", "UR", "15", "12A", "PG-14"} {
		if parentalRatingSupported(token) {
			t.Errorf("provider unexpectedly accepts %q", token)
		}
	}
}

// --- Form: distinct states ---

func TestParentalFormShowsDistinctStates(t *testing.T) {
	e := newParentalEnv(t, user("u-new", "new"), user("u-open", "open"), user("u-kid", "kid"))
	e.prov.seed("u-open", 4, parental.Policy{Version: 1, Mode: "unrestricted"})
	e.prov.seed("u-kid", 2, restrictedPolicy(parental.Rules{
		MaxRating: "PG-13", BlockedTags: []string{"Gore"}, AllowedTags: []string{"family"}, AllowUnrated: true, KidsMode: true,
	}))

	for _, tc := range []struct {
		user, state, mode, label string
		contains                 []string
	}{
		{"u-new", "unconfigured", "", "Not configured", []string{`name="expected_revision" value="0"`, "not the same as unrestricted"}},
		{"u-open", "configured", "unrestricted", "Configured: unrestricted", []string{`name="expected_revision" value="4"`, `<option value="unrestricted" selected>`}},
		{"u-kid", "configured", "restricted", "Configured: restricted", []string{
			`name="expected_revision" value="2"`, `<option value="PG-13" selected>`, `value="gore"`, `value="family"`,
			`name="kids_mode" value="1" checked`, `name="allow_unrated" value="1" checked`,
		}},
	} {
		t.Run(tc.user, func(t *testing.T) {
			w := e.getForm(tc.user)
			body := w.Body.String()
			if w.Code != http.StatusOK {
				t.Fatalf("status %d", w.Code)
			}
			for _, want := range append([]string{
				`data-state="` + tc.state + `"`, `data-mode="` + tc.mode + `"`, tc.label, `data-testid="parental-form"`,
			}, tc.contains...) {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q in:\n%s", want, body)
				}
			}
			for _, other := range []string{"Configured: unrestricted", "Configured: restricted", "Not configured"} {
				if other != tc.label && strings.Contains(body, other) {
					t.Errorf("state %q must not also read %q", tc.label, other)
				}
			}
		})
	}
	e.prov.assertCleanRequests(t)
	if _, puts := e.prov.blobState(); puts != 0 {
		t.Fatal("rendering must not write the blob")
	}
}

func TestParentalFormRequestShapeUsesSessionBearerOnly(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	e.getForm("u1")
	reqs := e.prov.requests()
	if len(reqs) != 1 {
		t.Fatalf("want one provider request, got %d", len(reqs))
	}
	if reqs[0].Method != http.MethodGet || reqs[0].Path != parentalPolicyPath {
		t.Fatalf("unexpected request %+v", reqs[0])
	}
	if reqs[0].UserIDs[0] != "u1" {
		t.Fatalf("target = %v", reqs[0].UserIDs)
	}
	e.prov.assertCleanRequests(t) // bearer is the session's, never the browser-forged one
}

func TestParentalFormMissingSessionBearerIsAnErrorState(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	e.sess.AuthLocalToken = ""
	body := e.getForm("u1").Body.String()
	assertParentalErrorState(t, body)
	if len(e.prov.requests()) != 0 {
		t.Fatal("no provider call may be made without the session's bearer")
	}
}

// assertParentalErrorState proves an error never reads as empty or
// unrestricted policy and offers no form to submit one.
func assertParentalErrorState(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, `data-testid="parental-load-error"`) {
		t.Fatalf("no error state:\n%s", body)
	}
	for _, bad := range []string{
		"<form", `data-state=`, "Not configured", "Configured", "Unrestricted", "unrestricted (", `name="mode"`, `name="expected_revision"`,
	} {
		if strings.Contains(body, bad) {
			t.Errorf("error state must not contain %q:\n%s", bad, body)
		}
	}
	if !strings.Contains(body, "Retry") {
		t.Error("error state should offer a retry")
	}
}

func TestParentalFormProviderFailureNeverRendersAsUnrestricted(t *testing.T) {
	cases := map[string]func(*parentalEnv){
		"401":         func(e *parentalEnv) { e.prov.force["GET u1"] = 401 },
		"403":         func(e *parentalEnv) { e.prov.force["GET u1"] = 403 },
		"404":         func(e *parentalEnv) { e.prov.force["GET u1"] = 404 },
		"500":         func(e *parentalEnv) { e.prov.force["GET u1"] = 500 },
		"503":         func(e *parentalEnv) { e.prov.force["GET u1"] = 503 },
		"409":         func(e *parentalEnv) { e.prov.force["GET u1"] = 409 },
		"redirect":    func(e *parentalEnv) { e.prov.force["GET u1"] = 302 },
		"unreachable": func(e *parentalEnv) { e.h.UserdataURL = "http://127.0.0.1:1" },
		"malformed":   func(e *parentalEnv) { e.prov.raw["GET u1"] = `{"user_id":` },
		"empty body":  func(e *parentalEnv) { e.prov.raw["GET u1"] = `` },
		"unknown field": func(e *parentalEnv) {
			e.prov.raw["GET u1"] = `{"user_id":"u1","tenant_id":"tenant-1","state":"unconfigured","revision":0,"policy":null,"extra":1}`
		},
		"other user": func(e *parentalEnv) {
			e.prov.raw["GET u1"] = `{"user_id":"u2","tenant_id":"tenant-1","state":"unconfigured","revision":0,"policy":null}`
		},
		"other tenant": func(e *parentalEnv) {
			e.prov.raw["GET u1"] = `{"user_id":"u1","tenant_id":"tenant-9","state":"unconfigured","revision":0,"policy":null}`
		},
		"unknown state": func(e *parentalEnv) {
			e.prov.raw["GET u1"] = `{"user_id":"u1","tenant_id":"tenant-1","state":"open","revision":0,"policy":null}`
		},
		"rev0 with policy": func(e *parentalEnv) {
			e.prov.raw["GET u1"] = `{"user_id":"u1","tenant_id":"tenant-1","state":"configured","revision":0,"policy":{"version":1,"mode":"unrestricted","rules":null}}`
		},
		"unconfigured with revision": func(e *parentalEnv) {
			e.prov.raw["GET u1"] = `{"user_id":"u1","tenant_id":"tenant-1","state":"unconfigured","revision":3,"policy":null}`
		},
		"configured null policy": func(e *parentalEnv) {
			e.prov.raw["GET u1"] = `{"user_id":"u1","tenant_id":"tenant-1","state":"configured","revision":1,"policy":null}`
		},
		"invalid stored policy": func(e *parentalEnv) {
			e.prov.raw["GET u1"] = `{"user_id":"u1","tenant_id":"tenant-1","state":"configured","revision":1,"policy":{"version":1,"mode":"restricted","rules":{"kids_mode":false,"max_rating":"15","blocked_tags":[],"allowed_tags":[],"allow_unrated":false}}}`
		},
		"two documents": func(e *parentalEnv) {
			e.prov.raw["GET u1"] = `{"user_id":"u1","tenant_id":"tenant-1","state":"unconfigured","revision":0,"policy":null}{}`
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newParentalEnv(t, user("u1", "alice"))
			setup(e)
			w := e.getForm("u1")
			assertParentalErrorState(t, w.Body.String())
			if _, puts := e.prov.blobState(); puts != 0 {
				t.Fatal("blob written")
			}
		})
	}
}

// --- Form: writes ---

func form(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

func TestParentalFormPutSendsExpectedRevisionAndNormalizedPolicy(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	e.prov.seed("u1", 3, parental.Policy{Version: 1, Mode: "unrestricted"})

	w := e.postForm("u1", form(
		"expected_revision", "3", "mode", "restricted", "max_rating", "PG-13", "kids_mode", "1",
		"blocked_tags", " Gore , violence,gore ", "allowed_tags", "Family", "allow_unrated", "1",
	))
	if !strings.Contains(w.Body.String(), `data-testid="parental-saved"`) {
		t.Fatalf("not saved:\n%s", w.Body.String())
	}
	var put *capturedRequest
	for _, r := range e.prov.requests() {
		if r.Method == http.MethodPut {
			r := r
			put = &r
		}
	}
	if put == nil {
		t.Fatal("no PUT sent")
	}
	update, err := parental.DecodeUpdate(put.Body) // the provider's strict decoder
	if err != nil {
		t.Fatalf("PUT body rejected by provider schema: %v\n%s", err, put.Body)
	}
	if update.ExpectedRevision != 3 {
		t.Fatalf("expected_revision = %d", update.ExpectedRevision)
	}
	want := restrictedPolicy(parental.Rules{
		KidsMode: true, MaxRating: "PG-13", BlockedTags: []string{"gore", "violence"}, AllowedTags: []string{"family"}, AllowUnrated: true,
	})
	if !reflect.DeepEqual(update.Policy, want) {
		t.Fatalf("policy = %+v want %+v", update.Policy, want)
	}
	if got := e.prov.doc("u1"); got.Revision != 4 || got.Policy.Mode != "restricted" {
		t.Fatalf("stored %+v", got)
	}
	if put.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("content type %q", put.Header.Get("Content-Type"))
	}
	e.prov.assertCleanRequests(t)
	if got := e.auditsFor("admin.parental.save"); len(got) != 1 || got[0].ResourceID != "u1" || got[0].Details["revision"] != "4" {
		t.Fatalf("audit = %+v", got)
	}
}

func TestParentalFormCreatesFromUnconfiguredWithRevisionZero(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	e.postForm("u1", form("expected_revision", "0", "mode", "unrestricted"))
	got := e.prov.doc("u1")
	if got.State != "configured" || got.Revision != 1 || got.Policy.Mode != "unrestricted" || got.Policy.Rules != nil {
		t.Fatalf("stored %+v", got)
	}
}

func TestParentalForm409ShowsConflictAndReloadsCurrentRevision(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	e.prov.seed("u1", 1, parental.Policy{Version: 1, Mode: "unrestricted"})
	e.prov.raceOnce["u1"] = true // another admin saves first

	w := e.postForm("u1", form("expected_revision", "1", "mode", "restricted", "max_rating", "PG"))
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="parental-conflict"`) {
		t.Fatalf("no conflict UI:\n%s", body)
	}
	if strings.Contains(body, "Parental policy saved") || strings.Contains(body, `data-testid="parental-saved"`) {
		t.Fatal("a conflict must never read as saved")
	}
	// The form is reloaded on the revision the other writer produced.
	if !strings.Contains(body, `name="expected_revision" value="2"`) {
		t.Fatalf("form not reloaded to the current revision:\n%s", body)
	}
	if !strings.Contains(body, "Not saved: restricted; max rating PG") {
		t.Fatalf("attempted change not echoed:\n%s", body)
	}
	if got := e.prov.doc("u1"); got.Revision != 2 || got.Policy.Mode != "unrestricted" {
		t.Fatalf("our write must not have been applied: %+v", got)
	}
	puts := e.prov.count(http.MethodPut, "u1")
	if puts != 1 {
		t.Fatalf("no silent retry: %d PUTs", puts)
	}
}

func TestParentalFormRatingMustComeFromProviderList(t *testing.T) {
	for _, rating := range []string{"15", "NR", "pg-13", "PG-13 ", "R; DROP", "TV-MA\x00"} {
		t.Run(rating, func(t *testing.T) {
			e := newParentalEnv(t, user("u1", "alice"))
			w := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", rating))
			if rating == "PG-13 " {
				// Surrounding whitespace is trimmed; the token itself is valid.
				if e.prov.count(http.MethodPut, "") != 1 {
					t.Fatalf("trimmed token should save:\n%s", w.Body.String())
				}
				return
			}
			if !strings.Contains(w.Body.String(), "Choose a maximum rating from the list") {
				t.Fatalf("free-text rating accepted:\n%s", w.Body.String())
			}
			if e.prov.count(http.MethodPut, "") != 0 {
				t.Fatal("invalid rating reached the provider")
			}
		})
	}
}

func TestParentalFormRequiresExplicitModeAndRevision(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	w := e.postForm("u1", form("expected_revision", "0", "max_rating", "PG"))
	if !strings.Contains(w.Body.String(), "Choose whether this account is unrestricted or restricted") {
		t.Fatalf("mode not required:\n%s", w.Body.String())
	}
	w = e.postForm("u1", form("mode", "unrestricted"))
	if !strings.Contains(w.Body.String(), "did not say which revision") {
		t.Fatalf("revision not required:\n%s", w.Body.String())
	}
	w = e.postForm("u1", form("mode", "unrestricted", "expected_revision", "-1"))
	if !strings.Contains(w.Body.String(), "did not say which revision") {
		t.Fatal("negative revision accepted")
	}
	if e.prov.count(http.MethodPut, "") != 0 {
		t.Fatal("an invalid submission reached the provider")
	}
}

func TestParentalFormRejectsInvalidTags(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	w := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "blocked_tags", strings.Repeat("x", 129)))
	if !strings.Contains(w.Body.String(), "tag lists are not valid") || e.prov.count(http.MethodPut, "") != 0 {
		t.Fatalf("invalid tag accepted:\n%s", w.Body.String())
	}
}

func TestParentalFormPutFailureIsNotSavedAndKeepsInput(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500, 503} {
		e := newParentalEnv(t, user("u1", "alice"))
		e.prov.force["PUT u1"] = status
		w := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG"))
		body := w.Body.String()
		if !strings.Contains(body, `data-testid="parental-error"`) || strings.Contains(body, `data-testid="parental-saved"`) {
			t.Fatalf("status %d: not an error state:\n%s", status, body)
		}
		if !strings.Contains(body, `<option value="PG" selected>`) {
			t.Fatalf("status %d: operator input lost", status)
		}
		if got := e.prov.doc("u1"); got.State != "unconfigured" {
			t.Fatalf("status %d: stored %+v", status, got)
		}
	}
}

func TestParentalFormPutAcknowledgementMustMatchTheRequest(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	// The provider claims success but stored something else.
	e.prov.raw["PUT u1"] = `{"user_id":"u1","tenant_id":"tenant-1","state":"configured","revision":1,"policy":{"version":1,"mode":"unrestricted","rules":null}}`
	w := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG"))
	if strings.Contains(w.Body.String(), `data-testid="parental-saved"`) {
		t.Fatal("mismatched acknowledgement shown as saved")
	}
}

// --- Roles warning ---

func TestParentalFormWarnsForAdminAndManagerTargets(t *testing.T) {
	e := newParentalEnv(t, user("adm", "root", "admin"), user("mgr", "boss", "manager"), user("kid", "kid", "viewer"))
	for id, want := range map[string]bool{"adm": true, "mgr": true, "kid": false} {
		body := e.getForm(id).Body.String()
		if got := strings.Contains(body, `data-testid="parental-privileged-warning"`); got != want {
			t.Errorf("%s: warning=%v want %v", id, got, want)
		}
		if want && !strings.Contains(body, "not a security boundary") {
			t.Errorf("%s: warning text missing", id)
		}
	}
}

func TestParentalFormWarnsWhenRolesCannotBeRead(t *testing.T) {
	stub := &identityRPCStub{fail: map[string]codes.Code{"ListUsers": codes.Unavailable}}
	h := setupIdentityHandler(t, stub)
	t.Setenv("ADMIN_UI_DATA_DIR", t.TempDir())
	prov := newFakePolicyProvider(t, "u1")
	h.UserdataURL = prov.srv.URL
	e := &parentalEnv{h: h, prov: prov}
	e.sess = newParentalEnv(t).sess
	body := e.getForm("u1").Body.String()
	if !strings.Contains(body, "could not be read") || !strings.Contains(body, "not a security boundary") {
		t.Fatalf("unknown roles should warn:\n%s", body)
	}
}

// --- No restriction fields in the writable blob; PIN behaviour unchanged ---

func TestParentalSaveWritesNoRestrictionFieldsToBlobOrLegacyFile(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	if err := saveParentalMap(map[string]parentalSettings{"u1": {MaxParentalRating: "G", PINHash: "keep"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(parentalFilePath())

	e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "R", "kids_mode", "1", "blocked_tags", "gore"))

	if blob, puts := e.prov.blobState(); puts != 0 {
		t.Fatalf("restriction save wrote the userdata blob: %s", blob)
	}
	after, _ := os.ReadFile(parentalFilePath())
	if string(before) != string(after) {
		t.Fatal("the retained legacy parental.json must not be rewritten by a policy save")
	}
}

func TestParentalPINSetWritesOnlyPINToBlobAndNothingToProvider(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	w := e.postPIN("u1", form("pin", "1234"))
	if !strings.Contains(w.Body.String(), "PIN updated") {
		t.Fatalf("PIN not saved:\n%s", w.Body.String())
	}
	m := loadParentalMap()
	if m["u1"].PINHash != hashParentalPIN("u1", "1234") {
		t.Fatalf("legacy PIN hash = %q", m["u1"].PINHash)
	}
	blobRaw, puts := e.prov.blobState()
	if puts != 1 {
		t.Fatalf("blob puts = %d", puts)
	}
	var blob userdataBlob
	if err := json.Unmarshal(blobRaw, &blob); err != nil {
		t.Fatal(err)
	}
	var prefs map[string]map[string]any
	if err := json.Unmarshal(blob.Prefs, &prefs); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prefs["parental"], map[string]any{"pin_hash": hashParentalPIN("u1", "1234")}) {
		t.Fatalf("blob prefs.parental = %v, want only pin_hash", prefs["parental"])
	}
	for _, r := range e.prov.requests() {
		if r.Method == http.MethodPut || strings.Contains(string(r.Body), "pin") {
			t.Fatalf("PIN reached the policy provider: %+v", r)
		}
	}
	if strings.Contains(w.Body.String(), hashParentalPIN("u1", "1234")) {
		t.Fatal("PIN hash rendered")
	}
}

func TestParentalPINClearAndValidation(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	e.postPIN("u1", form("pin", "5678"))
	if loadParentalMap()["u1"].PINHash == "" {
		t.Fatal("PIN not set")
	}
	w := e.postPIN("u1", form("clear_pin", "1"))
	if loadParentalMap()["u1"].PINHash != "" || !strings.Contains(w.Body.String(), "PIN updated") {
		t.Fatalf("PIN not cleared:\n%s", w.Body.String())
	}
	for pin, want := range map[string]string{"12": "4–6 digits", "1234567": "4–6 digits", "ab12": "digits only"} {
		body := e.postPIN("u1", form("pin", pin)).Body.String()
		if !strings.Contains(body, want) {
			t.Errorf("pin %q: want %q in:\n%s", pin, want, body)
		}
		if loadParentalMap()["u1"].PINHash != "" {
			t.Errorf("pin %q stored despite validation error", pin)
		}
	}
}

func TestParentalPINUserdataUnavailable(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	e.h.UserdataURL = "http://127.0.0.1:1"
	body := e.postPIN("u1", form("pin", "1234")).Body.String()
	if !strings.Contains(body, "userdata sync failed") || strings.Contains(body, "PIN updated") {
		t.Fatalf("sync failure not visible:\n%s", body)
	}
}

func TestParentalPINClearRemovesOnlyPINKeyFromBlob(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	// An existing blob keeps whatever else it carried; only pin_hash is managed.
	ctx := context.WithValue(context.Background(), ctxSessionKey, e.sess)
	if err := e.h.syncParentalPINToUserdata(ctx, "u1", "h1"); err != nil {
		t.Fatal(err)
	}
	if err := e.h.syncParentalPINToUserdata(ctx, "u1", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := e.prov.blobState()
	var blob userdataBlob
	_ = json.Unmarshal(raw, &blob)
	if strings.Contains(string(blob.Prefs), "pin_hash") {
		t.Fatalf("pin_hash not removed: %s", blob.Prefs)
	}
}

func TestParentalRoutesRegisterWithoutConflict(t *testing.T) {
	e := newParentalEnv(t)
	mux := http.NewServeMux()
	e.h.RegisterRoutes(mux) // panics on ambiguous patterns
	for _, probe := range []struct{ method, path string }{
		{"GET", "/users/parental/migrate"}, {"POST", "/users/parental/migrate"},
		{"GET", "/users/u1/parental"}, {"POST", "/users/u1/parental"}, {"POST", "/users/u1/parental/pin"},
	} {
		_, pattern := mux.Handler(mustRequest(probe.method, probe.path))
		if pattern == "" {
			t.Errorf("%s %s is not routed", probe.method, probe.path)
		}
	}
}

// --- hashParentalPIN / validateParentalPIN unit tests ---

func TestHashParentalPINDeterministicAndSalted(t *testing.T) {
	first, second := hashParentalPIN("user1", "1234"), hashParentalPIN("user1", "1234")
	if first != second {
		t.Fatal("hashParentalPIN must be deterministic")
	}
	if hashParentalPIN("user1", "1234") == hashParentalPIN("user2", "1234") {
		t.Fatal("same PIN for different users must produce different hashes")
	}
}

func TestValidateParentalPIN(t *testing.T) {
	for pin, ok := range map[string]bool{"1234": true, "12345": true, "123456": true, "123": false, "1234567": false, "abcd": false, "12a4": false} {
		if err := validateParentalPIN(pin); (err == nil) != ok {
			t.Errorf("pin %q: ok=%v err=%v", pin, ok, err)
		}
	}
}
