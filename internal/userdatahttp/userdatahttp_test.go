package userdatahttp_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/userdata-local/httpclient"
	"github.com/Muxcore-Media/userdata-local/parental"

	"github.com/Muxcore-Media/admin-ui/internal/userdatahttp"
	"github.com/Muxcore-Media/admin-ui/internal/userdatahttp/userdatahttptest"
)

func TestOriginFromAdvertised(t *testing.T) {
	for _, tc := range []struct {
		addr     string
		insecure bool
		want     string
		bad      bool
	}{
		{addr: "userdata-local:9672", want: "https://userdata-local:9672"},
		{addr: "127.0.0.1:9701", want: "https://127.0.0.1:9701"},
		{addr: "[::1]:9701", want: "https://[::1]:9701"},
		{addr: "userdata-local:9672", insecure: true, want: "http://userdata-local:9672"},
		// An advertised scheme is kept as is; the checked client then decides.
		{addr: "http://userdata-local:9672/", want: "http://userdata-local:9672"},
		{addr: "https://userdata-local:9672", insecure: true, want: "https://userdata-local:9672"},
		{addr: "0.0.0.0:9701", bad: true},
		{addr: "[::]:9701", bad: true},
		{addr: ":9701", bad: true},
		{addr: "userdata-local", bad: true},
		{addr: "", bad: true},
	} {
		got, err := userdatahttp.OriginFromAdvertised(tc.addr, tc.insecure)
		if tc.bad {
			if err == nil || !errors.Is(err, userdatahttp.ErrUnavailable) || !userdatahttp.NotApplied(err) {
				t.Errorf("%q: want unavailable/not-sent, got %q %v", tc.addr, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q insecure=%v: %q %v, want %q", tc.addr, tc.insecure, got, err, tc.want)
		}
	}
}

// fixture is a real mTLS provider plus a checked client with admin-ui's identity.
type fixture struct {
	ca     *userdatahttptest.CA
	leaf   userdatahttptest.Leaf
	srv    string
	client *httpclient.Client
}

func newFixture(t *testing.T, h http.Handler) *fixture {
	t.Helper()
	ca := userdatahttptest.NewCA(t, "mesh")
	f := &fixture{ca: ca, leaf: ca.Module("admin-ui")}
	f.srv = userdatahttptest.StartTLS(t, ca.Provider(), ca, userdatahttptest.Admit(h)).URL
	pool := &userdatahttp.Pool{Resolve: func(o string) (httpclient.Config, error) {
		return userdatahttptest.ClientConfig(o, "admin-ui", f.leaf, ca, time.Second), nil
	}}
	t.Cleanup(pool.Close)
	c, err := pool.Client(f.srv)
	if err != nil {
		t.Fatal(err)
	}
	f.client = c
	return f
}

func reply(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func TestPolicyErrorClasses(t *testing.T) {
	target := userdatahttp.Target{UserID: "u1", TenantID: "t1"}
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		appStatus  int
		notApplied bool
	}{
		{"application 403 policy.forbidden", 403, `{"code":"policy.forbidden"}`, 403, true},
		{"application 401", 401, `{"code":"policy.unauthenticated"}`, 401, true},
		{"application 409", 409, `{"code":"policy.revision_conflict"}`, 409, false},
		{"application 503", 503, `{"code":"policy.storage_unavailable"}`, 503, false},
		{"bare 400 (plaintext to TLS)", 400, "Client sent an HTTP request to an HTTPS server.\n", 0, false},
		{"403 without a policy code", 403, `{"code":"forbidden"}`, 0, false},
		{"502 from an intermediary", 502, `bad gateway`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, reply(tc.status, tc.body))
			_, err := userdatahttp.PutPolicy(context.Background(), f.client, "b", target, 0, parental.Policy{Version: 1, Mode: "unrestricted"})
			if got := userdatahttp.Status(err); got != tc.appStatus {
				t.Fatalf("status = %d want %d (%v)", got, tc.appStatus, err)
			}
			if tc.appStatus == 0 && !errors.Is(err, userdatahttp.ErrUnavailable) {
				t.Fatalf("unrecognised answer must be unavailability: %v", err)
			}
			if userdatahttp.NotApplied(err) != tc.notApplied {
				t.Fatalf("NotApplied = %v", !tc.notApplied)
			}
			if userdatahttp.ModuleForbidden(err) {
				t.Fatal("not an admission refusal")
			}
		})
	}
	t.Run("module admission refusal", func(t *testing.T) {
		f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { userdatahttptest.ModuleForbidden(w) }))
		_, err := userdatahttp.PutPolicy(context.Background(), f.client, "b", target, 0, parental.Policy{Version: 1, Mode: "unrestricted"})
		if !userdatahttp.ModuleForbidden(err) || !userdatahttp.NotApplied(err) || userdatahttp.Status(err) != 0 {
			t.Fatalf("admission refusal: forbidden=%v notApplied=%v status=%d", userdatahttp.ModuleForbidden(err), userdatahttp.NotApplied(err), userdatahttp.Status(err))
		}
		if !errors.Is(err, httpclient.ErrUnavailable) || !errors.Is(err, userdatahttp.ErrUnavailable) {
			t.Fatalf("admission refusal must be unavailability: %v", err)
		}
	})
}

func TestRequestHeadersAreOnlyTheSessionBearerAndTarget(t *testing.T) {
	var got http.Header
	f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"user_id":"u1","tenant_id":"t1","state":"unconfigured","revision":0,"policy":null}`))
	}))
	if _, err := userdatahttp.GetPolicy(context.Background(), f.client, " tok ", userdatahttp.Target{UserID: "u1", TenantID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if got.Get("Authorization") != "Bearer tok" || got.Get("X-MuxCore-User-Id") != "u1" || len(got.Values("Authorization")) != 1 {
		t.Fatalf("headers = %v", got)
	}
	for _, bad := range []struct{ bearer, user string }{{"", "u1"}, {"a b", "u1"}, {"a\r\nX: y", "u1"}, {"tok", ""}, {"tok", "u1\r\nX: y"}} {
		_, err := userdatahttp.GetPolicy(context.Background(), f.client, bad.bearer, userdatahttp.Target{UserID: bad.user})
		if err == nil || !userdatahttp.NotApplied(err) {
			t.Errorf("%q/%q: want refused before sending, got %v", bad.bearer, bad.user, err)
		}
	}
}

func TestTenantPinning(t *testing.T) {
	doc := []byte(`{"user_id":"u1","tenant_id":"t9","state":"unconfigured","revision":0,"policy":null}`)
	if _, err := userdatahttp.ParseDocument(doc, userdatahttp.Target{UserID: "u1", TenantID: "t1"}); err == nil {
		t.Fatal("other tenant accepted")
	}
	d, err := userdatahttp.ParseDocument(doc, userdatahttp.Target{UserID: "u1", AnyTenant: true})
	if err != nil || d.TenantID != "t9" {
		t.Fatalf("AnyTenant: %+v %v", d, err)
	}
	if _, err := userdatahttp.ParseDocument(doc, userdatahttp.Target{UserID: "u2", AnyTenant: true}); err == nil {
		t.Fatal("other user accepted")
	}
}

// The pool rebuilds the client when the identity on disk changes (renewal),
// and refuses to build one without usable identity material.
func TestPoolRebuildsOnIdentityChangeAndRefusesMissingIdentity(t *testing.T) {
	ca := userdatahttptest.NewCA(t, "mesh")
	leaf := ca.Module("admin-ui")
	pool := &userdatahttp.Pool{Resolve: func(o string) (httpclient.Config, error) {
		return userdatahttptest.ClientConfig(o, "admin-ui", leaf, ca, 0), nil
	}}
	defer pool.Close()
	a, err := pool.Client("https://userdata-local:9672")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := pool.Client("https://userdata-local:9672"); b != a {
		t.Fatal("client not reused")
	}
	renewed := ca.Module("admin-ui")
	raw, _ := os.ReadFile(renewed.CertFile)
	key, _ := os.ReadFile(renewed.KeyFile)
	later := time.Now().Add(time.Minute)
	if err := os.WriteFile(leaf.CertFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf.KeyFile, key, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(leaf.CertFile, later, later)
	if c, err := pool.Client("https://userdata-local:9672"); err != nil || c == a {
		t.Fatalf("client not rebuilt after renewal: %v", err)
	}

	for name, cfg := range map[string]httpclient.Config{
		"no identity":           {ModuleID: "admin-ui", Profile: "household"},
		"wrong module CN":       userdatahttptest.ClientConfig("", "admin-ui", ca.Module("media-ui"), ca, 0),
		"insecure in household": {ModuleID: "admin-ui", Profile: "household", Insecure: true},
	} {
		p := &userdatahttp.Pool{Resolve: func(o string) (httpclient.Config, error) { cfg.Origin = o; return cfg, nil }}
		_, err := p.Client("https://userdata-local:9672")
		if !userdatahttp.NotConfigured(err) || !userdatahttp.NotApplied(err) {
			t.Errorf("%s: want a not-configured error, got %v", name, err)
		}
	}
	p := &userdatahttp.Pool{Resolve: func(o string) (httpclient.Config, error) {
		return userdatahttptest.ClientConfig(o, "admin-ui", leaf, ca, 0), nil
	}}
	if _, err := p.Client("http://userdata-local:9672"); !userdatahttp.NotConfigured(err) {
		t.Fatalf("http origin in household accepted: %v", err)
	}
	if _, err := p.Client("https://userdata-local:9672/prefix"); !userdatahttp.NotConfigured(err) || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("origin with a path accepted: %v", err)
	}
}
