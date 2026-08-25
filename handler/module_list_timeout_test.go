package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

func TestModuleListTimeoutsBound(t *testing.T) {
	if moduleListDialTimeout != 3*time.Second {
		t.Fatalf("moduleListDialTimeout: got %v, want 3s", moduleListDialTimeout)
	}
	if moduleListReadTimeout != 5*time.Second {
		t.Fatalf("moduleListReadTimeout: got %v, want 5s", moduleListReadTimeout)
	}
	pageBudget := moduleListDialTimeout + moduleListReadTimeout + time.Second
	if moduleListPageTimeout != pageBudget {
		t.Fatalf("moduleListPageTimeout: got %v, want %v", moduleListPageTimeout, pageBudget)
	}
}

type moduleListBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (moduleListBlockingDiscovery) ListAll(ctx context.Context, _ *discoveryv1.ListAllRequest) (*discoveryv1.ListAllResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (moduleListBlockingDiscovery) Members(context.Context, *discoveryv1.MembersRequest) (*discoveryv1.MembersResponse, error) {
	return &discoveryv1.MembersResponse{}, nil
}

func TestPluginsPageFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, moduleListBlockingDiscovery{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	core, err := client.Dial(lis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/plugins")
	w := httptest.NewRecorder()

	start := time.Now()
	h.PluginsPage(w, r)
	elapsed := time.Since(start)

	limit := moduleListReadTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("plugins page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < moduleListReadTimeout-500*time.Millisecond {
		t.Fatalf("plugins page returned too quickly (%v); expected read timeout ~%v", elapsed, moduleListReadTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 plugins page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="plugins-page"`) {
		t.Fatalf("expected plugins page markup, got: %s", truncate(body, 400))
	}
}

func TestAuthSSOPageFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, moduleListBlockingDiscovery{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	core, err := client.Dial(lis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/auth")
	w := httptest.NewRecorder()

	start := time.Now()
	h.AuthSSOPage(w, r)
	elapsed := time.Since(start)

	limit := moduleListReadTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("auth SSO page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < moduleListReadTimeout-500*time.Millisecond {
		t.Fatalf("auth SSO page returned too quickly (%v); expected read timeout ~%v", elapsed, moduleListReadTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 auth SSO page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="auth-sso-page"`) {
		t.Fatalf("expected auth SSO page markup, got: %s", truncate(body, 400))
	}
}
