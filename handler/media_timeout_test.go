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

func TestMediaTimeoutsBound(t *testing.T) {
	if mediaDialTimeout != 3*time.Second {
		t.Fatalf("mediaDialTimeout: got %v, want 3s", mediaDialTimeout)
	}
	if mediaReadTimeout != 5*time.Second {
		t.Fatalf("mediaReadTimeout: got %v, want 5s", mediaReadTimeout)
	}
	pageBudget := mediaDialTimeout + 2*mediaReadTimeout + time.Second
	if pageBudget < mediaDialTimeout+mediaReadTimeout {
		t.Fatal("media page budget must cover dial + at least one read")
	}
}

// mediaBlockingDiscovery blocks Resolve until ctx cancel.
type mediaBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (mediaBlockingDiscovery) Resolve(ctx context.Context, _ *discoveryv1.ResolveRequest) (*discoveryv1.ResolveResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestMediaLibraryListFailsFastOnBlockedResolve(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, mediaBlockingDiscovery{})
	go srv.Serve(lis)
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

	r := mustRequest("GET", "/media/media-movies")
	r.SetPathValue("moduleID", "media-movies")
	w := httptest.NewRecorder()

	start := time.Now()
	h.MediaLibraryList(w, r)
	elapsed := time.Since(start)

	limit := mediaDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("media list took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < mediaDialTimeout-500*time.Millisecond {
		t.Fatalf("media list returned too quickly (%v); expected dial timeout ~%v", elapsed, mediaDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 media list page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="media-list-page"`) {
		t.Fatalf("expected media list page markup, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, `data-testid="media-soft-empty"`) {
		t.Fatalf("expected soft-empty state on blocked resolve, got: %s", truncate(body, 400))
	}
}
