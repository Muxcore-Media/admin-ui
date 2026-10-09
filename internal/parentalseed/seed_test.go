package parentalseed

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/parental"

	"github.com/Muxcore-Media/admin-ui/internal/userdatahttp/userdatahttptest"
)

const (
	adminBearer  = "s3cr3t-admin-bearer-7f1c"
	memberBearer = "member-bearer-91aa"
	tenant       = "tenant-1"
)

// provider is a real-mTLS userdata-local stand-in for the policy resource.
type provider struct {
	t   *testing.T
	ca  *userdatahttptest.CA
	srv *httptest.Server

	mu        sync.Mutex
	docs      map[string]parental.Document
	puts      int
	gets      int
	cns       []string
	auths     []string
	deny      bool                                // module admission refuses everything
	beforePut func(p *provider, user string) bool // true: answer 409
	putDelay  time.Duration                       // hold the (applied) answer back
}

func newProvider(t *testing.T) *provider {
	t.Helper()
	ca := userdatahttptest.NewCA(t, "mesh")
	p := &provider{t: t, ca: ca, docs: map[string]parental.Document{}}
	p.srv = userdatahttptest.StartTLS(t, ca.Provider(), ca, userdatahttptest.Admit(http.HandlerFunc(p.serve)))
	return p
}

func (p *provider) set(user string, rev int64, pol *parental.Policy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	scope := parental.Scope{UserID: user, TenantID: tenant}
	if pol == nil {
		p.docs[user] = parental.Unconfigured(scope)
		return
	}
	p.docs[user] = parental.Document{Scope: scope, State: "configured", Revision: rev, Policy: pol}
}

func (p *provider) doc(user string) parental.Document {
	p.mu.Lock()
	defer p.mu.Unlock()
	if d, ok := p.docs[user]; ok {
		return d
	}
	return parental.Unconfigured(parental.Scope{UserID: user, TenantID: tenant})
}

func (p *provider) counts() (gets, puts int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gets, p.puts
}

func (p *provider) seenAuths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.auths...)
}

func writeCode(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"code":"` + code + `"}`))
}

func (p *provider) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	if p.deny {
		p.mu.Unlock()
		userdatahttptest.ModuleForbidden(w)
		return
	}
	p.cns = append(p.cns, userdatahttptest.VerifiedCN(r))
	p.auths = append(p.auths, r.Header.Values("Authorization")...)
	user := r.Header.Get("X-MuxCore-User-Id")
	w.Header().Set("Content-Type", "application/json")
	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+adminBearer && auth != "Bearer "+memberBearer {
		p.mu.Unlock()
		writeCode(w, http.StatusUnauthorized, "policy.unauthenticated")
		return
	}
	if user == "ghost" {
		p.mu.Unlock()
		writeCode(w, http.StatusNotFound, "policy.account_not_found")
		return
	}
	doc, ok := p.docs[user]
	if !ok {
		doc = parental.Unconfigured(parental.Scope{UserID: user, TenantID: tenant})
	}
	if r.Method == http.MethodGet {
		p.gets++
		p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(doc)
		return
	}
	p.puts++
	if auth != "Bearer "+adminBearer {
		p.mu.Unlock()
		writeCode(w, http.StatusForbidden, "policy.forbidden")
		return
	}
	if p.beforePut != nil {
		hook := p.beforePut
		p.mu.Unlock()
		conflict := hook(p, user)
		p.mu.Lock()
		doc = p.docs[user]
		if doc.State == "" {
			doc = parental.Unconfigured(parental.Scope{UserID: user, TenantID: tenant})
		}
		if conflict {
			p.mu.Unlock()
			writeCode(w, http.StatusConflict, "policy.revision_conflict")
			return
		}
	}
	update, err := parental.DecodeUpdate(body)
	if err != nil {
		p.mu.Unlock()
		writeCode(w, http.StatusBadRequest, "policy.invalid_body")
		return
	}
	if update.ExpectedRevision != doc.Revision {
		p.mu.Unlock()
		writeCode(w, http.StatusConflict, "policy.revision_conflict")
		return
	}
	pol := update.Policy
	out := parental.Document{Scope: doc.Scope, State: "configured", Revision: doc.Revision + 1, Policy: &pol}
	p.docs[user] = out
	delay := p.putDelay
	p.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}

type result struct {
	code           int
	stdout, stderr string
}

// seed runs the helper with admin-ui's identity against origin.
func seed(t *testing.T, ca *userdatahttptest.CA, leaf userdatahttptest.Leaf, origin, stdin string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	resolve := func(o string) (httpclient.Config, error) {
		return userdatahttptest.ClientConfig(o, "admin-ui", leaf, ca, 0), nil
	}
	all := append([]string{"--origin", origin}, args...)
	code := run(all, strings.NewReader(stdin), &out, &errb, func(string) string { return "" }, resolve)
	r := result{code: code, stdout: out.String(), stderr: errb.String()}
	for _, secret := range []string{adminBearer, memberBearer} {
		if strings.Contains(r.stdout+r.stderr, secret) {
			t.Fatalf("bearer leaked into output:\n%s\n%s", r.stdout, r.stderr)
		}
	}
	return r
}

func (p *provider) seed(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	return seed(t, p.ca, p.ca.Module("admin-ui"), p.srv.URL, stdin, args...)
}

var restricted = parental.Policy{Version: 1, Mode: "restricted", Rules: &parental.Rules{MaxRating: "PG", BlockedTags: []string{}, AllowedTags: []string{}}}

func TestSeedCreatesUnrestrictedOnceAndIsIdempotent(t *testing.T) {
	p := newProvider(t)
	r := p.seed(t, adminBearer+"\n", "--user", "kid", "--bearer-file", "-")
	if r.code != ExitOK || !strings.Contains(r.stdout, "set unrestricted") {
		t.Fatalf("seed: %+v", r)
	}
	d := p.doc("kid")
	if d.State != "configured" || d.Revision != 1 || d.Policy.Mode != "unrestricted" {
		t.Fatalf("doc = %+v", d)
	}
	r = p.seed(t, adminBearer, "--user", "kid", "--bearer-file", "-", "--tenant", tenant)
	if r.code != ExitOK || !strings.Contains(r.stdout, "unchanged") {
		t.Fatalf("re-run: %+v", r)
	}
	if _, puts := p.counts(); puts != 1 {
		t.Fatalf("PUTs = %d, want exactly one", puts)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, cn := range p.cns {
		if cn != "admin-ui" {
			t.Fatalf("provider saw client CN %q", cn)
		}
	}
	for _, a := range p.auths {
		if a != "Bearer "+adminBearer {
			t.Fatalf("Authorization = %q", a)
		}
	}
}

func TestSeedNeverOverwritesARestrictedPolicy(t *testing.T) {
	p := newProvider(t)
	pol := restricted
	p.set("kid", 4, &pol)
	r := p.seed(t, adminBearer, "--user", "kid", "--bearer-file", "-")
	if r.code != ExitRestricted || !strings.Contains(r.stderr, "restricted") {
		t.Fatalf("restricted: %+v", r)
	}
	if _, puts := p.counts(); puts != 0 {
		t.Fatal("restricted policy written")
	}
	if d := p.doc("kid"); d.Revision != 4 || d.Policy.Mode != "restricted" {
		t.Fatalf("doc changed: %+v", d)
	}
}

func TestSeedRereadsOnConflict(t *testing.T) {
	t.Run("another writer set unrestricted", func(t *testing.T) {
		p := newProvider(t)
		var once sync.Once
		p.beforePut = func(p *provider, user string) (conflict bool) {
			once.Do(func() {
				u := parental.Policy{Version: 1, Mode: "unrestricted"}
				p.set(user, 1, &u)
				conflict = true
			})
			return conflict
		}
		r := p.seed(t, adminBearer, "--user", "kid", "--bearer-file", "-")
		if r.code != ExitOK || !strings.Contains(r.stdout, "revision conflict") || !strings.Contains(r.stdout, "unchanged") {
			t.Fatalf("%+v", r)
		}
		if gets, puts := p.counts(); gets != 2 || puts != 1 {
			t.Fatalf("gets=%d puts=%d", gets, puts)
		}
	})
	t.Run("another writer set restricted", func(t *testing.T) {
		p := newProvider(t)
		var once sync.Once
		p.beforePut = func(p *provider, user string) (conflict bool) {
			once.Do(func() {
				pol := restricted
				p.set(user, 1, &pol)
				conflict = true
			})
			return conflict
		}
		r := p.seed(t, adminBearer, "--user", "kid", "--bearer-file", "-")
		if r.code != ExitRestricted {
			t.Fatalf("%+v", r)
		}
		if d := p.doc("kid"); d.Policy.Mode != "restricted" {
			t.Fatal("restricted policy overwritten")
		}
	})
	t.Run("conflicts on every attempt", func(t *testing.T) {
		p := newProvider(t)
		p.beforePut = func(*provider, string) bool { return true }
		r := p.seed(t, adminBearer, "--user", "kid", "--bearer-file", "-", "--attempts", "2")
		if r.code != ExitStillConflict {
			t.Fatalf("%+v", r)
		}
		if _, puts := p.counts(); puts != 2 {
			t.Fatalf("puts = %d", puts)
		}
	})
}

func TestSeedUncertainWriteIsReadBack(t *testing.T) {
	p := newProvider(t)
	p.putDelay = 3 * time.Second
	r := p.seed(t, adminBearer, "--user", "kid", "--bearer-file", "-", "--timeout", "1s")
	if r.code != ExitOK || !strings.Contains(r.stdout, "no definite answer") || !strings.Contains(r.stdout, "after an uncertain write") {
		t.Fatalf("%+v", r)
	}
	if _, puts := p.counts(); puts != 1 {
		t.Fatalf("uncertain write retried blindly: %d PUTs", puts)
	}

	// The provider goes away after an uncertain write: exit 7, never success.
	p2 := newProvider(t)
	p2.putDelay = 3 * time.Second
	p2.beforePut = func(p *provider, _ string) bool {
		p.mu.Lock()
		p.deny = true // every later request is refused at admission
		p.mu.Unlock()
		return false
	}
	r = p2.seed(t, adminBearer, "--user", "kid", "--bearer-file", "-", "--timeout", "1s")
	if r.code != ExitUncertain || !strings.Contains(r.stderr, "may or may not have been applied") {
		t.Fatalf("%+v", r)
	}
}

func TestSeedErrorClasses(t *testing.T) {
	t.Run("module admission refusal", func(t *testing.T) {
		p := newProvider(t)
		p.deny = true
		r := p.seed(t, adminBearer, "--user", "kid", "--bearer-file", "-")
		if r.code != ExitNotPermitted || !strings.Contains(r.stderr, "not permitted for this service") || strings.Contains(r.stderr, "admin in the account") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("member bearer", func(t *testing.T) {
		p := newProvider(t)
		r := p.seed(t, memberBearer, "--user", "kid", "--bearer-file", "-")
		if r.code != ExitDenied || !strings.Contains(r.stderr, "policy.forbidden") {
			t.Fatalf("%+v", r)
		}
		if d := p.doc("kid"); d.State != "unconfigured" {
			t.Fatal("member write applied")
		}
	})
	t.Run("rejected bearer", func(t *testing.T) {
		p := newProvider(t)
		r := p.seed(t, "stale-token", "--user", "kid", "--bearer-file", "-")
		if r.code != ExitDenied || !strings.Contains(r.stderr, "rejected the bearer") || strings.Contains(r.stderr, "stale-token") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("unknown account", func(t *testing.T) {
		p := newProvider(t)
		if r := p.seed(t, adminBearer, "--user", "ghost", "--bearer-file", "-"); r.code != ExitDenied {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("tenant mismatch", func(t *testing.T) {
		p := newProvider(t)
		r := p.seed(t, adminBearer, "--user", "kid", "--bearer-file", "-", "--tenant", "other")
		if r.code != ExitUnavailable {
			t.Fatalf("%+v", r)
		}
		if _, puts := p.counts(); puts != 0 {
			t.Fatal("wrote after a scope mismatch")
		}
	})
}

// The helper presents the bearer only to the verified userdata-local.
func TestSeedRefusesImpostorsAndPlaintextWithoutSendingTheBearer(t *testing.T) {
	ca := userdatahttptest.NewCA(t, "mesh")
	leaf := ca.Module("admin-ui")
	for name, srvLeaf := range map[string]userdatahttptest.Leaf{
		"wrong CN with right SAN": ca.Issue(userdatahttptest.LeafOptions{CN: "media-ui", DNS: []string{"userdata-local"}}),
		"same-CA impostor":        ca.Module("jellyfin"),
		"wrong CA":                userdatahttptest.NewCA(t, "other").Provider(),
	} {
		t.Run(name, func(t *testing.T) {
			var rec userdatahttptest.Recorder
			srv := userdatahttptest.StartServerOnlyTLS(t, srvLeaf, rec.Wrap(nil))
			r := seed(t, ca, leaf, srv.URL, adminBearer, "--user", "kid", "--bearer-file", "-")
			if r.code != ExitUnavailable || rec.Count() != 0 {
				t.Fatalf("code %d, impostor requests %d: %s", r.code, rec.Count(), r.stderr)
			}
		})
	}
	t.Run("http origin", func(t *testing.T) {
		var rec userdatahttptest.Recorder
		plain := httptest.NewServer(rec.Wrap(nil))
		t.Cleanup(plain.Close)
		r := seed(t, ca, leaf, plain.URL, adminBearer, "--user", "kid", "--bearer-file", "-")
		if r.code != ExitUsage || rec.Count() != 0 {
			t.Fatalf("code %d, plaintext requests %d: %s", r.code, rec.Count(), r.stderr)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		var rec userdatahttptest.Recorder
		target := userdatahttptest.StartTLS(t, ca.Provider(), ca, rec.Wrap(nil))
		redirector := userdatahttptest.StartTLS(t, ca.Provider(), ca, http.RedirectHandler(target.URL+"/api/parental-policy", http.StatusTemporaryRedirect))
		r := seed(t, ca, leaf, redirector.URL, adminBearer, "--user", "kid", "--bearer-file", "-")
		if r.code != ExitUnavailable || rec.Count() != 0 {
			t.Fatalf("code %d, redirect target requests %d", r.code, rec.Count())
		}
	})
}

func TestSeedBearerInput(t *testing.T) {
	p := newProvider(t)
	file := filepath.Join(t.TempDir(), "bearer")
	if err := os.WriteFile(file, []byte("Bearer "+adminBearer+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := p.seed(t, "", "--user", "kid", "--bearer-file", file); r.code != ExitOK {
		t.Fatalf("bearer file: %+v", r)
	}
	for name, tc := range map[string]struct {
		stdin string
		args  []string
	}{
		"empty stdin":           {"", []string{"--bearer-file", "-"}},
		"two tokens":            {adminBearer + " other", []string{"--bearer-file", "-"}},
		"oversized":             {strings.Repeat("a", maxBearerBytes+1), []string{"--bearer-file", "-"}},
		"no bearer input":       {adminBearer, nil},
		"bearer as argument":    {"", []string{"--bearer", adminBearer}},
		"missing file":          {"", []string{"--bearer-file", filepath.Join(t.TempDir(), "nope")}},
		"no user":               {adminBearer, []string{"--bearer-file", "-", "--user", ""}},
		"extra args":            {adminBearer, []string{"--bearer-file", "-", "extra"}},
		"timeout above 30s":     {adminBearer, []string{"--bearer-file", "-", "--timeout", "31s"}},
		"attempts out of range": {adminBearer, []string{"--bearer-file", "-", "--attempts", "0"}},
	} {
		t.Run(name, func(t *testing.T) {
			gets, puts := p.counts()
			args := append([]string{"--user", "u2"}, tc.args...)
			if r := p.seed(t, tc.stdin, args...); r.code != ExitUsage {
				t.Fatalf("%+v", r)
			}
			if g, pu := p.counts(); g != gets || pu != puts {
				t.Fatal("a usage error reached the provider")
			}
		})
	}
	for _, a := range p.seenAuths() {
		if a != "Bearer "+adminBearer {
			t.Fatalf("unexpected Authorization %q", a)
		}
	}
}

func TestSeedRedactsTheBearerFromEveryMessage(t *testing.T) {
	var out, errb bytes.Buffer
	o := &output{stdout: &out, stderr: &errb, redact: adminBearer}
	o.okf("token %s", adminBearer)
	o.failf(ExitUnavailable, "cause: Bearer %s", adminBearer)
	if strings.Contains(out.String()+errb.String(), adminBearer) || !strings.Contains(errb.String(), "[REDACTED]") {
		t.Fatalf("not redacted: %q %q", out.String(), errb.String())
	}
}

// Existing identity only: with the identity directory empty the helper fails
// before any request and creates nothing (no enrollment, no generated CA),
// even when a bootstrap token is present.
func TestSeedUsesOnlyExistingIdentityFromEnvironment(t *testing.T) {
	p := newProvider(t)
	dir := t.TempDir()
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "")
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "")
	t.Setenv("MUXCORE_MODULE_ID", "")
	t.Setenv("MUXCORE_TLS_CERT", "")
	t.Setenv("MUXCORE_TLS_KEY", "")
	t.Setenv("MUXCORE_TLS_CA", "")
	t.Setenv("MUXCORE_CA_EXPORT_DIR", "")
	t.Setenv("MUXCORE_PROFILE", "household")
	t.Setenv("MUXCORE_BOOTSTRAP_TOKEN", "would-enroll")
	t.Setenv("MUXCORE_TLS_DIR", dir)
	env := func(k string) string {
		if k == "ADMIN_UI_USERDATA_URL" {
			return p.srv.URL
		}
		return ""
	}
	var out, errb bytes.Buffer
	code := Main([]string{"--user", "kid", "--bearer-file", "-"}, strings.NewReader(adminBearer), &out, &errb, env)
	if code != ExitUsage {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("helper created identity material: %v", entries)
	}
	if gets, puts := p.counts(); gets+puts != 0 {
		t.Fatal("request sent without an identity")
	}

	// The same environment with the enrolled files present succeeds.
	leaf := p.ca.Module("admin-ui")
	for src, dst := range map[string]string{leaf.CertFile: "module.crt", leaf.KeyFile: "module.key", p.ca.File: "ca.crt"} {
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, dst), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	errb.Reset()
	if code := Main([]string{"--user", "kid", "--bearer-file", "-"}, strings.NewReader(adminBearer), &out, &errb, env); code != ExitOK {
		t.Fatalf("code %d: %s", code, errb.String())
	}
}

// The helper's code cannot enroll, reach core or touch admin-ui storage: none
// of those packages is in its dependency closure.
func TestSeedDependencyClosure(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, forbidden := range []string{
		"github.com/Muxcore-Media/core/sdk/go/module/meshid",
		"github.com/Muxcore-Media/core/sdk/go/client",
		"github.com/Muxcore-Media/admin-ui/handler",
		"github.com/Muxcore-Media/admin-ui/session",
		"google.golang.org/grpc",
	} {
		for _, line := range strings.Split(string(out), "\n") {
			if line == forbidden {
				t.Errorf("seed helper depends on %s", forbidden)
			}
		}
	}
}
