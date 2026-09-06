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

func TestLibraryScanTimeoutsBound(t *testing.T) {
	if scannerDialTimeout != 3*time.Second {
		t.Fatalf("scannerDialTimeout: got %v, want 3s", scannerDialTimeout)
	}
	if scannerReadTimeout != 5*time.Second {
		t.Fatalf("scannerReadTimeout: got %v, want 5s", scannerReadTimeout)
	}
	pageBudget := scannerDialTimeout + scannerReadTimeout + time.Second
	if scannerPageTimeout != pageBudget {
		t.Fatalf("scannerPageTimeout: got %v, want %v", scannerPageTimeout, pageBudget)
	}
	actionBudget := scannerDialTimeout + scannerScanTimeout + time.Second
	if scannerActionTimeout != actionBudget {
		t.Fatalf("scannerActionTimeout: got %v, want %v", scannerActionTimeout, actionBudget)
	}
}

func TestLibraryScanPageFailsFastOnBlockedDiscovery(t *testing.T) {
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

	r := mustRequest(http.MethodGet, "/library-scan")
	w := httptest.NewRecorder()

	start := time.Now()
	h.LibraryScanPage(w, r)
	elapsed := time.Since(start)

	limit := scannerDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("LibraryScanPage took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < scannerDialTimeout-500*time.Millisecond {
		t.Fatalf("LibraryScanPage returned too quickly (%v); expected dial timeout ~%v", elapsed, scannerDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="library-scan-page"`) {
		t.Fatal("expected library scan page markup")
	}
	if !strings.Contains(body, `data-testid="library-scan-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}
