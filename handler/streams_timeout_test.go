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

func TestStreamsPageTimeoutsBound(t *testing.T) {
	mainBudget := playbackMonitorDialTimeout + 3*playbackMonitorReadTimeout + time.Second
	if mainBudget < playbackMonitorDialTimeout+playbackMonitorReadTimeout {
		t.Fatal("streams main page budget must cover dial + at least one read")
	}
	historyBudget := playbackMonitorDialTimeout + playbackMonitorReadTimeout
	if historyBudget < playbackMonitorDialTimeout {
		t.Fatal("streams history page budget must cover dial")
	}
	const statsReads = 10
	statsBudget := playbackMonitorDialTimeout + statsReads*playbackMonitorReadTimeout + time.Second
	if statsBudget < playbackMonitorDialTimeout+playbackMonitorReadTimeout {
		t.Fatal("streams stats page budget must cover dial + at least one read")
	}
}

func testStreamsPageFailsFastOnBlockedDiscovery(t *testing.T, handler func(*Handler, http.ResponseWriter, *http.Request), testID string) {
	t.Helper()

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

	r := mustRequest("GET", "/streams")
	w := httptest.NewRecorder()

	start := time.Now()
	handler(h, w, r)
	elapsed := time.Since(start)

	limit := playbackMonitorDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("%s took %v, want ≤ %v (deadline fail-fast)", testID, elapsed, limit)
	}
	if elapsed < playbackMonitorDialTimeout-500*time.Millisecond {
		t.Fatalf("%s returned too quickly (%v); expected dial timeout ~%v", testID, elapsed, playbackMonitorDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("%s: expected 200 soft error page, got %d", testID, w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, testID) {
		t.Fatalf("%s: expected page markup with %q", testID, testID)
	}
}

func TestStreamsPageFailsFastOnBlockedDiscovery(t *testing.T) {
	testStreamsPageFailsFastOnBlockedDiscovery(t, func(h *Handler, w http.ResponseWriter, r *http.Request) {
		h.StreamsPage(w, r)
	}, `data-testid="streams-page"`)
}

func TestStreamsHistoryPageFailsFastOnBlockedDiscovery(t *testing.T) {
	testStreamsPageFailsFastOnBlockedDiscovery(t, func(h *Handler, w http.ResponseWriter, r *http.Request) {
		h.StreamsHistoryPage(w, r)
	}, `data-testid="streams-history-page"`)
}

func TestNowPlayingPageFailsFastOnBlockedDiscovery(t *testing.T) {
	testStreamsPageFailsFastOnBlockedDiscovery(t, func(h *Handler, w http.ResponseWriter, r *http.Request) {
		h.NowPlayingPage(w, r)
	}, `data-testid="now-playing-page"`)
}
