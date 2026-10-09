package handler

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/parental"
	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/internal/meshdial"
	"github.com/Muxcore-Media/admin-ui/internal/userdatahttp/userdatahttptest"
	"github.com/Muxcore-Media/admin-ui/session"
)

// ADR-0033 / S9c: every admin-ui userdata request (form GET/PUT, migration
// dry run/apply/readback, PIN blob GET/PUT, discovery) goes over real mTLS
// through the published checked client. These tests use real TLS sockets.

const notPermittedText = "does not permit this service"

func assertNoAdminDenial(t *testing.T, body string) {
	t.Helper()
	for _, bad := range []string{"admin role in the account", "rejected the request as invalid", "Sign in again"} {
		if strings.Contains(body, bad) {
			t.Errorf("transport/admission failure rendered as an application answer (%q):\n%s", bad, body)
		}
	}
}

func TestUserdataEveryRequestUsesAdminIdentityOverMTLS(t *testing.T) {
	e := migrationFixture(t)
	if w := e.getForm("kid"); !strings.Contains(w.Body.String(), "<form") {
		t.Fatalf("form GET failed:\n%s", w.Body.String())
	}
	if w := e.postForm("kid", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG")); !strings.Contains(w.Body.String(), `data-testid="parental-saved"`) {
		t.Fatalf("form PUT failed:\n%s", w.Body.String())
	}
	if w := e.postPIN("teen", form("pin", "1234")); !strings.Contains(w.Body.String(), "PIN updated") {
		t.Fatalf("PIN sync failed:\n%s", w.Body.String())
	}
	digest, _ := e.dryRun(t)
	e.apply(digest, true)
	if got := len(e.prov.requests()); got < 8 {
		t.Fatalf("provider saw only %d policy requests", got)
	}
	e.prov.assertCleanRequests(t) // verified client CN admin-ui, one bearer, one target
	if _, puts := e.prov.blobState(); puts != 1 {
		t.Fatalf("blob PUTs = %d", puts)
	}
	if strings.HasPrefix(e.prov.srv.URL, "http://") {
		t.Fatal("fake provider is not TLS")
	}
}

// Module admission refusal (403 userdata.module_forbidden) is userdata
// unavailability for this service: never an admin-role denial, never an
// "invalid request", never unrestricted, and certain not to have written.
func TestUserdataModuleAdmissionIsNotAnAdminDenial(t *testing.T) {
	t.Run("form read", func(t *testing.T) {
		e := newParentalEnv(t, user("u1", "alice"))
		e.prov.denyModule["GET "+parentalPolicyPath] = true
		body := e.getForm("u1").Body.String()
		assertParentalErrorState(t, body)
		if !strings.Contains(body, notPermittedText) {
			t.Fatalf("no not-permitted message:\n%s", body)
		}
		assertNoAdminDenial(t, body)
	})
	t.Run("form write", func(t *testing.T) {
		e := newParentalEnv(t, user("u1", "alice"))
		e.prov.denyModule["PUT "+parentalPolicyPath] = true
		body := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG")).Body.String()
		if !strings.Contains(body, notPermittedText) || !strings.Contains(body, "Nothing was changed") {
			t.Fatalf("admission refusal must be a certain no-write, not-permitted message:\n%s", body)
		}
		assertNoAdminDenial(t, body)
		if d := e.prov.doc("u1"); d.State != "unconfigured" {
			t.Fatalf("refused write applied: %+v", d)
		}
	})
	t.Run("migration", func(t *testing.T) {
		e := migrationFixture(t)
		digest, _ := e.dryRun(t)
		e.prov.denyModule["PUT "+parentalPolicyPath] = true
		res := e.apply(digest, true)
		for _, u := range []string{"kid", "teen", "adm", "open", "none"} {
			if got := rowOutcome(t, res.Body, u); got != outNotPermitted {
				t.Errorf("%s: outcome %s want %s", u, got, outNotPermitted)
			}
		}
		if strings.Contains(res.Body, `data-outcome="forbidden"`) {
			t.Fatal("admission refusal reported as an admin/tenant denial")
		}
		e.prov.denyModule["GET "+parentalPolicyPath] = true
		_, dry := e.dryRun(t)
		if got := rowOutcome(t, dry, "kid"); got != outNotPermitted {
			t.Fatalf("dry run: %s", got)
		}
	})
	t.Run("pin sync", func(t *testing.T) {
		e := newParentalEnv(t, user("u1", "alice"))
		e.prov.denyModule["GET "+userdataHTTPPath] = true
		body := e.postPIN("u1", form("pin", "1234")).Body.String()
		if !strings.Contains(body, "userdata sync failed") || !strings.Contains(body, notPermittedText) {
			t.Fatalf("PIN admission refusal not explained:\n%s", body)
		}
		assertNoAdminDenial(t, body)
		if _, puts := e.prov.blobState(); puts != 0 {
			t.Fatal("blob written after admission refusal")
		}
	})
}

// The application's own 403 policy.forbidden keeps its meaning: a member
// bearer or a cross-tenant target is an authorization answer.
func TestUserdataApplicationForbiddenStaysAnAuthorizationAnswer(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"), user("member", "m"))
	e.sess.AuthLocalToken = testMemberBearer
	body := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG")).Body.String()
	if !strings.Contains(body, "admin role in the account") || strings.Contains(body, notPermittedText) {
		t.Fatalf("member write should be an admin-role denial:\n%s", body)
	}
	if !strings.Contains(body, "Nothing was changed") {
		t.Fatalf("a definite 403 is certain not to have written:\n%s", body)
	}
	if d := e.prov.doc("u1"); d.State != "unconfigured" {
		t.Fatal("member write applied")
	}

	e = newParentalEnv(t, user("u1", "alice"))
	e.prov.foreign["u1"] = true
	body = e.getForm("u1").Body.String()
	assertParentalErrorState(t, body)
	if !strings.Contains(body, "admin role in the account") || strings.Contains(body, notPermittedText) {
		t.Fatalf("cross-tenant read should be the provider's denial:\n%s", body)
	}
}

func TestUserdataValidAdminWriteAndStaleWriteOverMTLS(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	e.prov.seed("u1", 2, restrictedPolicy(parental.Rules{MaxRating: "PG"}))
	body := e.postForm("u1", form("expected_revision", "2", "mode", "restricted", "max_rating", "R")).Body.String()
	if d := e.prov.doc("u1"); d.Revision != 3 || d.Policy.Rules.MaxRating != "R" {
		t.Fatalf("admin write not applied: %+v\n%s", d, body)
	}
	// A stale revision is a 409: the conflict UI shows what is stored now.
	body = e.postForm("u1", form("expected_revision", "2", "mode", "unrestricted")).Body.String()
	if !strings.Contains(body, `data-testid="parental-conflict"`) {
		t.Fatalf("stale write not surfaced as a conflict:\n%s", body)
	}
	if d := e.prov.doc("u1"); d.Revision != 3 || d.Policy.Mode != "restricted" {
		t.Fatalf("stale write overwrote: %+v", d)
	}
	e.prov.assertCleanRequests(t)
}

// Every redirect is refused, including same-origin ones: the bearer is never
// re-sent to the Location and the result is unavailability, never unrestricted.
func TestUserdataRefusesEveryRedirect(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, sameOrigin := range []bool{true, false} {
			name := strconv.Itoa(code) + map[bool]string{true: "-same-origin", false: "-elsewhere"}[sameOrigin]
			t.Run(name, func(t *testing.T) {
				e := newParentalEnv(t, user("u1", "alice"))
				var target userdatahttptest.Recorder
				elsewhere := userdatahttptest.StartTLS(t, e.prov.pki.ca.Provider(), e.prov.pki.ca, target.Wrap(nil))
				var redirector *httptest.Server
				redirector = userdatahttptest.StartTLS(t, e.prov.pki.ca.Provider(), e.prov.pki.ca, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("followed") != "" {
						target.Wrap(nil).ServeHTTP(w, r)
						return
					}
					loc := elsewhere.URL + r.URL.Path
					if sameOrigin {
						loc = redirector.URL + r.URL.Path + "?followed=1"
					}
					http.Redirect(w, r, loc, code)
				}))
				e.prov.pki.attach(e.h, redirector.URL)
				body := e.getForm("u1").Body.String()
				assertParentalErrorState(t, body)
				body = e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG")).Body.String()
				if !strings.Contains(body, "may not have been saved") {
					t.Fatalf("redirected write must be uncertain:\n%s", body)
				}
				if target.Count() != 0 || len(target.Auths()) != 0 {
					t.Fatalf("redirect followed: %d requests, auth %v", target.Count(), target.Auths())
				}
			})
		}
	}
}

// A write whose answer never arrives within the deadline may have been applied.
func TestUserdataUncertainWriteAfterSend(t *testing.T) {
	e := newParentalEnv(t, user("u1", "alice"))
	e.prov.pki.timeout = time.Second
	e.prov.attach(e.h)
	e.prov.delay["PUT u1"] = 4 * time.Second
	body := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG")).Body.String()
	if strings.Contains(body, "Nothing was changed") || !strings.Contains(body, "may not have been saved") {
		t.Fatalf("timed-out write must be uncertain:\n%s", body)
	}
	if d := e.prov.doc("u1"); d.State != "configured" {
		t.Fatalf("fixture: the provider should have applied the write: %+v", d)
	}
	// Reloading shows the stored policy rather than guessing.
	delete(e.prov.delay, "PUT u1")
	if body := e.getForm("u1").Body.String(); !strings.Contains(body, `data-state="configured"`) {
		t.Fatalf("reload does not show the applied policy:\n%s", body)
	}
}

// Server identity: impostors are refused during the handshake, before any
// request (and therefore any bearer) is sent.
func TestUserdataRejectsImpostorServersBeforeSendingTheBearer(t *testing.T) {
	ca := userdatahttptest.NewCA(t, "mesh")
	otherCA := userdatahttptest.NewCA(t, "other")
	expired := time.Now().Add(-time.Minute)
	cases := map[string]userdatahttptest.Leaf{
		// Another enrolled module: same CA, shared loopback SANs only.
		"same-CA impostor with loopback SAN": ca.Module("jellyfin"),
		// Has the userdata-local SAN but is not userdata-local.
		"wrong CN with right SAN": ca.Issue(userdatahttptest.LeafOptions{CN: "media-ui", DNS: []string{"userdata-local", "localhost"}, IPs: userdatahttptest.Loopback.IPs}),
		"wrong CA":                otherCA.Provider(),
		"expired":                 ca.Issue(userdatahttptest.LeafOptions{CN: "userdata-local", DNS: []string{"userdata-local"}, NotAfter: expired}),
		"client-only EKU":         ca.Issue(userdatahttptest.LeafOptions{CN: "userdata-local", DNS: []string{"userdata-local"}, Usages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}),
		"missing service SAN":     ca.Issue(userdatahttptest.LeafOptions{CN: "userdata-local", DNS: []string{"localhost"}, IPs: userdatahttptest.Loopback.IPs}),
	}
	for name, leaf := range cases {
		t.Run(name, func(t *testing.T) {
			e := newParentalEnv(t, user("u1", "alice"))
			pki := &testPKI{ca: ca, client: ca.Module("admin-ui")}
			var rec userdatahttptest.Recorder
			srv := userdatahttptest.StartServerOnlyTLS(t, leaf, rec.Wrap(nil))
			pki.attach(e.h, srv.URL)
			assertParentalErrorState(t, e.getForm("u1").Body.String())
			body := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG")).Body.String()
			assertNoAdminDenial(t, body)
			ctx := context.WithValue(context.Background(), ctxSessionKey, e.sess)
			if err := e.h.syncParentalPINToUserdata(ctx, "u1", "h"); err == nil {
				t.Fatal("PIN sync succeeded against an impostor")
			}
			if rec.Count() != 0 || len(rec.Auths()) != 0 {
				t.Fatalf("impostor received %d requests, auth %v", rec.Count(), rec.Auths())
			}
		})
	}
}

// Plaintext never carries the bearer: an https origin pointed at a plaintext
// listener fails the handshake, an http origin is refused outside insecure
// dev, and a plaintext dev client against the TLS provider is "unavailable",
// not the misleading "rejected the request as invalid".
func TestUserdataPlaintext(t *testing.T) {
	t.Run("https origin to plaintext listener", func(t *testing.T) {
		e := newParentalEnv(t, user("u1", "alice"))
		var rec userdatahttptest.Recorder
		plain := httptest.NewServer(rec.Wrap(nil))
		t.Cleanup(plain.Close)
		e.prov.pki.attach(e.h, strings.Replace(plain.URL, "http://", "https://", 1))
		body := e.getForm("u1").Body.String()
		assertParentalErrorState(t, body)
		assertNoAdminDenial(t, body)
		if rec.Count() != 0 {
			t.Fatalf("plaintext listener handled %d requests", rec.Count())
		}
	})
	t.Run("http origin in household", func(t *testing.T) {
		e := newParentalEnv(t, user("u1", "alice"))
		var rec userdatahttptest.Recorder
		plain := httptest.NewServer(rec.Wrap(nil))
		t.Cleanup(plain.Close)
		e.prov.pki.attach(e.h, plain.URL)
		body := e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG")).Body.String()
		if !strings.Contains(body, "no verified connection") || !strings.Contains(body, "Nothing was changed") {
			t.Fatalf("http origin must be a configuration refusal with nothing sent:\n%s", body)
		}
		if rec.Count() != 0 {
			t.Fatalf("plaintext listener handled %d requests", rec.Count())
		}
	})
	t.Run("plaintext dev client to TLS provider", func(t *testing.T) {
		e := newParentalEnv(t, user("u1", "alice"))
		e.h.UserdataURL = strings.Replace(e.prov.srv.URL, "https://", "http://", 1)
		e.h.userdata.Resolve = func(o string) (httpclient.Config, error) {
			return httpclient.Config{Origin: o, ModuleID: "admin-ui", Profile: "dev", Insecure: true}, nil
		}
		for _, body := range []string{
			e.getForm("u1").Body.String(),
			e.postForm("u1", form("expected_revision", "0", "mode", "restricted", "max_rating", "PG")).Body.String(),
		} {
			if !strings.Contains(body, "unavailable or returned an unusable answer") {
				t.Fatalf("plaintext-to-TLS should read as unavailable:\n%s", body)
			}
			assertNoAdminDenial(t, body)
		}
		if n := len(e.prov.requests()); n != 0 {
			t.Fatalf("provider handled %d plaintext requests", n)
		}
	})
}

// newDiscoveryHandler is a handler whose core discovery advertises
// userdata-local at addr (and no configured origin).
func newDiscoveryHandler(t *testing.T, addr string) *Handler {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, capDiscovery{mods: map[string]*discoveryv1.ModuleInfoProto{
		capUserdataLocal: {Id: "userdata-local", HttpAddr: addr},
	}})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	core, err := client.Dial(lis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	h := New(nil, session.NewStore(time.Hour), false, "test", nil, true, "", nil, nil)
	h.Core = core
	return h
}

func TestUserdataDiscoveryUsesHTTPS(t *testing.T) {
	t.Setenv("ADMIN_UI_USERDATA_URL", "")
	sess := &session.Session{UserID: "admin1", Roles: []string{"admin"}, AuthLocalToken: testAdminBearer, TenantID: testTenant}
	ctx := context.WithValue(context.Background(), ctxSessionKey, sess)
	prov := newFakePolicyProvider(t, "u1")
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(prov.srv.URL, "https://"))

	for name, tc := range map[string]struct {
		addr, dialLocal string
	}{
		"explicit host":            {addr: "127.0.0.1:" + port},
		"bare port, host dial":     {addr: ":" + port, dialLocal: "true"},
		"wildcard bind, host dial": {addr: "0.0.0.0:" + port, dialLocal: "true"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("MUXCORE_MESH_DIAL_LOCAL", tc.dialLocal)
			h := newDiscoveryHandler(t, tc.addr)
			prov.pki.attach(h, "")
			origin, err := h.userdataOrigin(ctx)
			if err != nil || origin != "https://127.0.0.1:"+port {
				t.Fatalf("origin = %q, %v", origin, err)
			}
			before := len(prov.requests())
			if _, err := h.getParentalPolicy(ctx, sess, "u1"); err != nil {
				t.Fatalf("discovered provider not reachable over mTLS: %v", err)
			}
			if got := prov.requests(); len(got) != before+1 || got[len(got)-1].CN != "admin-ui" {
				t.Fatalf("provider did not see an admin-ui mTLS request: %+v", got)
			}
		})
	}

	t.Run("advertised http is never used in household", func(t *testing.T) {
		var rec userdatahttptest.Recorder
		plain := httptest.NewServer(rec.Wrap(nil))
		t.Cleanup(plain.Close)
		h := newDiscoveryHandler(t, plain.URL)
		prov.pki.attach(h, "")
		_, err := h.getParentalPolicy(ctx, sess, "u1")
		if err == nil || !strings.Contains(parentalErrorMessage(err, false), "no verified connection") {
			t.Fatalf("advertised http must be refused as configuration: %v", err)
		}
		if rec.Count() != 0 {
			t.Fatal("plaintext provider was dialled")
		}
	})
	t.Run("insecure dev keeps http for a bare address", func(t *testing.T) {
		h := newDiscoveryHandler(t, "127.0.0.1:"+port)
		h.userdata.Resolve = func(o string) (httpclient.Config, error) {
			return httpclient.Config{Origin: o, ModuleID: "admin-ui", Profile: "dev", Insecure: true}, nil
		}
		if origin, err := h.userdataOrigin(ctx); err != nil || origin != "http://127.0.0.1:"+port {
			t.Fatalf("dev origin = %q, %v", origin, err)
		}
	})
}

// The production path resolves admin-ui's identity from the environment the
// SDK exports after enrollment (MUXCORE_TLS_CERT/KEY/CA).
func TestUserdataClientUsesEnrolledIdentityFromEnvironment(t *testing.T) {
	_, _ = meshdial.DialOption() // pin the process-wide gRPC dial config before touching the env
	prov := newFakePolicyProvider(t, "u1")
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "")
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "")
	t.Setenv("MUXCORE_MODULE_ID", "")
	t.Setenv("MUXCORE_PROFILE", "household")
	t.Setenv("MUXCORE_TLS_CERT", prov.pki.client.CertFile)
	t.Setenv("MUXCORE_TLS_KEY", prov.pki.client.KeyFile)
	t.Setenv("MUXCORE_TLS_CA", prov.pki.ca.File)
	h := New(nil, session.NewStore(time.Hour), false, "test", nil, true, "", nil, nil)
	h.UserdataURL = prov.srv.URL
	sess := &session.Session{UserID: "admin1", Roles: []string{"admin"}, AuthLocalToken: testAdminBearer, TenantID: testTenant}
	if _, err := h.getParentalPolicy(context.Background(), sess, "u1"); err != nil {
		t.Fatalf("enrolled identity from env: %v", err)
	}
	// Without an identity there is no plaintext fallback.
	t.Setenv("MUXCORE_TLS_CERT", "")
	t.Setenv("MUXCORE_TLS_KEY", "")
	t.Setenv("MUXCORE_TLS_CA", "")
	t.Setenv("MUXCORE_TLS_DIR", t.TempDir())
	h = New(nil, session.NewStore(time.Hour), false, "test", nil, true, "", nil, nil)
	h.UserdataURL = prov.srv.URL
	_, err := h.getParentalPolicy(context.Background(), sess, "u1")
	if err == nil || !strings.Contains(parentalErrorMessage(err, false), "no verified connection") {
		t.Fatalf("missing identity must be a configuration error: %v", err)
	}
}

// Request-time session revalidation (PR #65) still runs before every
// provider-backed page, and a module-admission refusal neither signs the
// operator out nor reads as an authorization failure.
func TestUserdataAdmissionRefusalKeepsRevalidatedSession(t *testing.T) {
	stub := &identityRPCStub{
		allowAdmin: true,
		validateResp: &authv1.ValidateResponse{
			Valid: true, UserId: "admin1", Username: "admin", Roles: []string{"admin"}, TenantId: testTenant,
		},
		users: []*authv1.UserInfo{user("u1", "alice")},
	}
	h := setupIdentityHandler(t, stub)
	t.Setenv("ADMIN_UI_DATA_DIR", t.TempDir())
	prov := newFakePolicyProvider(t, "u1")
	prov.attach(h)
	prov.denyModule["GET "+parentalPolicyPath] = true
	cookie := boundSession(t, h, "admin1", "admin", testTenant, testAdminBearer, []string{"admin"})

	page := h.requireAuth(h.UsersParental)
	for i := 1; i <= 2; i++ {
		r := sessionRequest("/users/u1/parental", cookie)
		r.SetPathValue("id", "u1")
		w := httptest.NewRecorder()
		page(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), notPermittedText) {
			t.Fatalf("request %d: status %d\n%s", i, w.Code, w.Body.String())
		}
		assertSessionKept(t, w)
		if _, ok := h.Sessions.Snapshot(cookie); !ok {
			t.Fatalf("request %d: session revoked after an admission refusal", i)
		}
		stub.mu.Lock()
		validated := stub.calls["Validate"]
		stub.mu.Unlock()
		if validated != i {
			t.Fatalf("request %d: Validate ran %d times; revalidation must run per request", i, validated)
		}
	}
	if prov.refusalCount() != 2 {
		t.Fatalf("provider admission saw %d requests", prov.refusalCount())
	}

	// A bearer the provider now rejects (401 at the provider) still does not
	// sign the operator out from here: the per-request revalidation owns that.
	delete(prov.denyModule, "GET "+parentalPolicyPath)
	prov.force["GET u1"] = http.StatusUnauthorized
	r := sessionRequest("/users/u1/parental", cookie)
	r.SetPathValue("id", "u1")
	w := httptest.NewRecorder()
	page(w, r)
	if !strings.Contains(w.Body.String(), "Sign in again") {
		t.Fatalf("provider 401 keeps its existing message:\n%s", w.Body.String())
	}
}

// Dry runs never write, over the real transport, and a partially failed apply
// is recoverable: the next dry run shows what landed and the re-run writes
// only what is still missing, never overwriting.
func TestUserdataMigrationDryRunAndPartialRecovery(t *testing.T) {
	e := migrationFixture(t)
	e.prov.pki.timeout = time.Second
	e.prov.attach(e.h)
	for i := 0; i < 2; i++ {
		e.dryRun(t)
	}
	if puts := e.prov.count(http.MethodPut, ""); puts != 0 {
		t.Fatalf("dry runs wrote %d times", puts)
	}

	// Apply: kid's answer is lost after it was applied (uncertain), teen's
	// PUT is refused at admission (certain no-write), adm succeeds.
	digest, _ := e.dryRun(t)
	e.prov.delay["PUT kid"] = 4 * time.Second
	e.prov.mu.Lock()
	e.prov.denyModule["PUT "+parentalPolicyPath+" teen"] = true
	e.prov.mu.Unlock()
	res := e.apply(digest, false)
	if got := rowOutcome(t, res.Body, "kid"); got != outError || !strings.Contains(res.Body, "may or may not have been applied") {
		t.Fatalf("kid: %s\n%s", got, res.Body)
	}
	if got := rowOutcome(t, res.Body, "teen"); got != outNotPermitted || e.prov.refusalCount() != 1 {
		t.Fatalf("teen: %s (refusals %d)", got, e.prov.refusalCount())
	}
	if got := rowOutcome(t, res.Body, "adm"); got != outCreated {
		t.Fatalf("adm: %s", got)
	}

	// Recovery: the dry run reads back what is stored now.
	e.prov.mu.Lock()
	delete(e.prov.delay, "PUT kid")
	delete(e.prov.denyModule, "PUT "+parentalPolicyPath+" teen")
	e.prov.mu.Unlock()
	_, dry := e.dryRun(t)
	for u, want := range map[string]string{"kid": outEqual, "teen": outWouldCreate, "adm": outEqual} {
		if got := rowOutcome(t, dry, u); got != want {
			t.Errorf("recovery dry run %s: %s want %s", u, got, want)
		}
	}
	putsBefore := e.prov.count(http.MethodPut, "")
	res = e.apply(digest, false)
	if got := rowOutcome(t, res.Body, "teen"); got != outCreated {
		t.Fatalf("re-run teen: %s", got)
	}
	if got := e.prov.count(http.MethodPut, "") - putsBefore; got != 1 {
		t.Fatalf("re-run wrote %d policies, want only teen", got)
	}
	if d := e.prov.doc("kid"); d.Revision != 1 {
		t.Fatalf("kid rewritten: %+v", d)
	}
}
