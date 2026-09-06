package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"net"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

func TestMaintainerTimeoutsBound(t *testing.T) {
	if maintainerDialTimeout != 3*time.Second {
		t.Fatalf("maintainerDialTimeout: got %v, want 3s", maintainerDialTimeout)
	}
	if maintainerReadTimeout != 5*time.Second {
		t.Fatalf("maintainerReadTimeout: got %v, want 5s", maintainerReadTimeout)
	}
	pageBudget := maintainerDialTimeout + 6*maintainerReadTimeout + time.Second
	if maintainerPageTimeout != pageBudget {
		t.Fatalf("maintainerPageTimeout: got %v, want %v", maintainerPageTimeout, pageBudget)
	}
	if maintainerPageTimeout < maintainerDialTimeout+maintainerReadTimeout {
		t.Fatal("maintainer page budget must cover dial + at least one read")
	}
	actionBudget := maintainerDialTimeout + maintainerReadTimeout + time.Second
	if maintainerActionTimeout != actionBudget {
		t.Fatalf("maintainerActionTimeout: got %v, want %v", maintainerActionTimeout, actionBudget)
	}
}

func TestMaintainerPageFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, blockingDiscovery{})
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

	r := mustRequest(http.MethodGet, "/maintainer")
	w := httptest.NewRecorder()

	start := time.Now()
	h.MaintainerPage(w, r)
	elapsed := time.Since(start)

	limit := maintainerDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("MaintainerPage took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < maintainerDialTimeout-500*time.Millisecond {
		t.Fatalf("MaintainerPage returned too quickly (%v); expected dial timeout ~%v", elapsed, maintainerDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="maintainer-page"`) {
		t.Fatal("expected maintainer page markup")
	}
	if !strings.Contains(body, `data-testid="maintainer-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}
