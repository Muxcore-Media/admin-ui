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

func TestUsersTimeoutsBound(t *testing.T) {
	if usersDialTimeout != 3*time.Second {
		t.Fatalf("usersDialTimeout: got %v, want 3s", usersDialTimeout)
	}
	if usersReadTimeout != 5*time.Second {
		t.Fatalf("usersReadTimeout: got %v, want 5s", usersReadTimeout)
	}
	pageBudget := usersDialTimeout + usersReadTimeout + time.Second
	if pageBudget < usersDialTimeout+usersReadTimeout {
		t.Fatal("users page budget must cover dial + read")
	}
}

// usersBlockingDiscovery blocks FindByCapability for auth until ctx cancel.
type usersBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (usersBlockingDiscovery) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() == "auth" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestUsersPageFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, usersBlockingDiscovery{})
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

	r := mustRequest("GET", "/users")
	w := httptest.NewRecorder()

	start := time.Now()
	h.UsersPage(w, r)
	elapsed := time.Since(start)

	limit := usersDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("users page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < usersDialTimeout-500*time.Millisecond {
		t.Fatalf("users page returned too quickly (%v); expected dial timeout ~%v", elapsed, usersDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 users page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="users-page"`) {
		t.Fatalf("expected users page markup, got: %s", truncate(body, 400))
	}
}
