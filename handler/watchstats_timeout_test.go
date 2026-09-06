package handler

import (
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

func TestWatchStatsPageTimeoutsBound(t *testing.T) {
	budget := playbackMonitorDialTimeout + watchStatsReads*playbackMonitorReadTimeout + time.Second
	if budget < playbackMonitorDialTimeout+playbackMonitorReadTimeout {
		t.Fatal("watchstats page budget must cover at least dial + one read")
	}
}

func TestWatchStatsPageFailsFastOnBlockedDiscovery(t *testing.T) {
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

	r := mustRequest("GET", "/watchstats")
	w := httptest.NewRecorder()

	start := time.Now()
	h.WatchStatsPage(w, r)
	elapsed := time.Since(start)

	limit := playbackMonitorDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("WatchStatsPage took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < playbackMonitorDialTimeout-500*time.Millisecond {
		t.Fatalf("WatchStatsPage returned too quickly (%v); expected dial timeout ~%v", elapsed, playbackMonitorDialTimeout)
	}
	if w.Code != 200 {
		t.Fatalf("expected 200 soft error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="watchstats-page"`) {
		t.Fatalf("expected watchstats page markup after timeout, got: %s", truncate(body, 400))
	}
}
