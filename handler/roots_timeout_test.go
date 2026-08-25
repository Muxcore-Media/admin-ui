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

func TestRootsTimeoutsBound(t *testing.T) {
	if rootsDialTimeout != 3*time.Second {
		t.Fatalf("rootsDialTimeout: got %v, want 3s", rootsDialTimeout)
	}
	if rootsReadTimeout != 5*time.Second {
		t.Fatalf("rootsReadTimeout: got %v, want 5s", rootsReadTimeout)
	}
	if rootsListPageTimeout < rootsDialTimeout+rootsReadTimeout {
		t.Fatal("roots list page budget must cover dial + at least one read")
	}
	if rootsEditPageTimeout < rootsDialTimeout+2*rootsReadTimeout {
		t.Fatal("roots edit page budget must cover dial + list + browse")
	}
	if rootsActionTimeout < rootsDialTimeout+rootsReadTimeout {
		t.Fatal("roots action budget must cover dial + at least one read")
	}
}

type rootsBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (rootsBlockingDiscovery) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() == capMediaRoots {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestRootsListFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, rootsBlockingDiscovery{})
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

	r := mustRequest("GET", "/roots")
	w := httptest.NewRecorder()

	start := time.Now()
	h.RootsList(w, r)
	elapsed := time.Since(start)

	limit := rootsDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("roots list took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < rootsDialTimeout-500*time.Millisecond {
		t.Fatalf("roots list returned too quickly (%v); expected dial timeout ~%v", elapsed, rootsDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 roots page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Root Folders") {
		t.Fatalf("expected roots page markup, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, "Roots module unavailable") {
		t.Fatalf("expected roots unavailable error, got: %s", truncate(body, 400))
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "timed out") &&
		!strings.Contains(lower, "context") &&
		!strings.Contains(lower, "deadline") &&
		!strings.Contains(lower, "cancel") {
		t.Fatalf("expected timeout/cancel error in page body, got: %s", truncate(body, 400))
	}
}
