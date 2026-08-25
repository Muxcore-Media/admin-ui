package handler

import (
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

func TestJellyfinPageTimeoutsBound(t *testing.T) {
	if jellyfinDialTimeout != 3*time.Second {
		t.Fatalf("jellyfinDialTimeout: got %v, want 3s", jellyfinDialTimeout)
	}
	if jellyfinReadTimeout != 5*time.Second {
		t.Fatalf("jellyfinReadTimeout: got %v, want 5s", jellyfinReadTimeout)
	}
	pageBudget := jellyfinDialTimeout + 2*jellyfinReadTimeout + time.Second
	if jellyfinPageTimeout != pageBudget {
		t.Fatalf("jellyfinPageTimeout: got %v, want %v", jellyfinPageTimeout, pageBudget)
	}
	actionBudget := jellyfinDialTimeout + jellyfinReadTimeout + time.Second
	if jellyfinActionTimeout != actionBudget {
		t.Fatalf("jellyfinActionTimeout: got %v, want %v", jellyfinActionTimeout, actionBudget)
	}
}

func TestJellyfinStatusPageFailsFastOnBlockedDiscovery(t *testing.T) {
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

	r := mustRequest("GET", "/jellyfin")
	w := httptest.NewRecorder()

	start := time.Now()
	h.JellyfinStatusPage(w, r)
	elapsed := time.Since(start)

	limit := jellyfinDialTimeout + 2*time.Second
	if elapsed > limit {
		t.Fatalf("jellyfin page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < jellyfinDialTimeout-500*time.Millisecond {
		t.Fatalf("jellyfin page returned too quickly (%v); expected dial timeout ~%v", elapsed, jellyfinDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="jellyfin-page"`) {
		t.Fatal("expected jellyfin page markup")
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "timed out") &&
		!strings.Contains(lower, "context") &&
		!strings.Contains(lower, "deadline") &&
		!strings.Contains(lower, "cancel") {
		t.Fatalf("expected timeout/cancel error in page body, got: %s", truncate(body, 400))
	}
}

func TestJellyfinSyncFailsFastOnBlockedDiscovery(t *testing.T) {
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

	r := mustRequest("POST", "/jellyfin/sync")
	w := httptest.NewRecorder()

	start := time.Now()
	h.JellyfinSync(w, r)
	elapsed := time.Since(start)

	limit := jellyfinDialTimeout + 2*time.Second
	if elapsed > limit {
		t.Fatalf("jellyfin sync took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < jellyfinDialTimeout-500*time.Millisecond {
		t.Fatalf("jellyfin sync returned too quickly (%v); expected dial timeout ~%v", elapsed, jellyfinDialTimeout)
	}
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	lower := strings.ToLower(loc)
	if !strings.Contains(lower, "error=") {
		t.Fatalf("expected error redirect, got %q", loc)
	}
	if !strings.Contains(lower, "timed out") &&
		!strings.Contains(lower, "context") &&
		!strings.Contains(lower, "deadline") &&
		!strings.Contains(lower, "cancel") {
		t.Fatalf("expected timeout/cancel error in redirect, got %q", loc)
	}
}
