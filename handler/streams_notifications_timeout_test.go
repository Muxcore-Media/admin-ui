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

func TestPlaybackMonitorTimeoutsBound(t *testing.T) {
	if playbackMonitorDialTimeout != 3*time.Second {
		t.Fatalf("playbackMonitorDialTimeout: got %v, want 3s", playbackMonitorDialTimeout)
	}
	if playbackMonitorReadTimeout != 5*time.Second {
		t.Fatalf("playbackMonitorReadTimeout: got %v, want 5s", playbackMonitorReadTimeout)
	}
	pageBudget := playbackMonitorDialTimeout + 2*playbackMonitorReadTimeout + time.Second
	if pageBudget < playbackMonitorDialTimeout+playbackMonitorReadTimeout {
		t.Fatal("notifications page budget must cover dial + at least one read")
	}
}

func TestStreamsNotificationsPageFailsFastOnBlockedDiscovery(t *testing.T) {
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

	r := mustRequest("GET", "/streams/notifications")
	w := httptest.NewRecorder()

	start := time.Now()
	h.StreamsNotificationsPage(w, r)
	elapsed := time.Since(start)

	limit := playbackMonitorDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("notifications page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < playbackMonitorDialTimeout-500*time.Millisecond {
		t.Fatalf("notifications page returned too quickly (%v); expected dial timeout ~%v", elapsed, playbackMonitorDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="streams-notifications-page"`) {
		t.Fatal("expected notifications page markup")
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "context") && !strings.Contains(lower, "deadline") && !strings.Contains(lower, "cancel") {
		t.Fatalf("expected timeout/cancel error in page body, got: %s", truncate(body, 400))
	}
}
