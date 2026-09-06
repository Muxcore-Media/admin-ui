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

func TestWantedTimeoutsBound(t *testing.T) {
	if wantedDialTimeout != 3*time.Second {
		t.Fatalf("wantedDialTimeout: got %v, want 3s", wantedDialTimeout)
	}
	if wantedReadTimeout != 5*time.Second {
		t.Fatalf("wantedReadTimeout: got %v, want 5s", wantedReadTimeout)
	}
	pageBudget := wantedDialTimeout + 2*(wantedDialTimeout+2*wantedReadTimeout) + time.Second
	if wantedPageTimeout != pageBudget {
		t.Fatalf("wantedPageTimeout: got %v, want %v", wantedPageTimeout, pageBudget)
	}
}

type wantedBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (wantedBlockingDiscovery) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() == capMediaLibrary {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestUnifiedWantedPageFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, wantedBlockingDiscovery{})
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

	r := mustRequest(http.MethodGet, "/wanted")
	w := httptest.NewRecorder()

	start := time.Now()
	h.UnifiedWantedPage(w, r)
	elapsed := time.Since(start)

	limit := wantedDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("UnifiedWantedPage took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < wantedDialTimeout-500*time.Millisecond {
		t.Fatalf("UnifiedWantedPage returned too quickly (%v); expected dial timeout ~%v", elapsed, wantedDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="unified-wanted-page"`) {
		t.Fatal("expected wanted page markup")
	}
	if !strings.Contains(body, `data-testid="wanted-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}
