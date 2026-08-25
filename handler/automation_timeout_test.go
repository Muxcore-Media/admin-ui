package handler

import (
	"context"
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

func TestAutomationTimeoutsBound(t *testing.T) {
	if automationDialTimeout != 3*time.Second {
		t.Fatalf("automationDialTimeout: got %v, want 3s", automationDialTimeout)
	}
	if automationReadTimeout != 4*time.Second {
		t.Fatalf("automationReadTimeout: got %v, want 4s", automationReadTimeout)
	}
	if automationDispatchTO < 20*time.Second {
		t.Fatalf("automationDispatchTO too short: %v", automationDispatchTO)
	}
	if automationPeerTimeout < time.Second {
		t.Fatalf("automationPeerTimeout too short: %v", automationPeerTimeout)
	}
	pageBudget := automationDialTimeout + 2*automationReadTimeout + time.Second
	if pageBudget < automationDialTimeout+automationPeerTimeout {
		t.Fatal("page budget must cover dial + peer discovery")
	}
}

// blockingDiscovery hangs on FindByCapability until the caller's context is cancelled.
type blockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (blockingDiscovery) FindByCapability(ctx context.Context, _ *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestAutomationQueuePageFailsFastOnBlockedDiscovery(t *testing.T) {
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

	r := mustRequest("GET", "/automation")
	w := httptest.NewRecorder()

	start := time.Now()
	h.AutomationQueuePage(w, r)
	elapsed := time.Since(start)

	// Dial resolve uses automationDialTimeout; peer discovery may add up to automationPeerTimeout.
	limit := automationDialTimeout + automationPeerTimeout + 2*time.Second
	if elapsed > limit {
		t.Fatalf("automation page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < automationDialTimeout-500*time.Millisecond {
		t.Fatalf("automation page returned too quickly (%v); expected dial timeout ~%v", elapsed, automationDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="automation-page"`) {
		t.Fatal("expected automation page markup")
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "timed out") &&
		!strings.Contains(lower, "context") &&
		!strings.Contains(lower, "deadline") &&
		!strings.Contains(lower, "cancel") {
		t.Fatalf("expected timeout/cancel error in page body, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, "media automation module discovery") {
		t.Fatalf("expected user-facing discovery error, got: %s", truncate(body, 400))
	}
}

func TestAutomationQueuePageEmptyQueueFast(t *testing.T) {
	core, cleanup := startAutomationHealthyFixture(t)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/automation")
	w := httptest.NewRecorder()

	start := time.Now()
	h.AutomationQueuePage(w, r)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("automation page took %v with healthy fixture; want fast empty queue path", elapsed)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="automation-page"`) {
		t.Fatal("expected automation page markup")
	}
	if !strings.Contains(body, "Queue is empty.") {
		t.Fatalf("expected empty queue message, got: %s", truncate(body, 600))
	}
	if strings.Contains(body, "media automation module discovery") {
		t.Fatalf("did not expect discovery error on fast empty queue, got: %s", truncate(body, 400))
	}
	if strings.Contains(body, "queue unavailable:") {
		t.Fatalf("did not expect queue error on healthy fixture, got: %s", truncate(body, 400))
	}
}

func TestAutomationQueuePageFixtureHistoryFailures(t *testing.T) {
	core, cleanup := startAutomationFixture(t)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/automation")
	w := httptest.NewRecorder()
	h.AutomationQueuePage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="automation-page"`) {
		t.Fatal("expected automation page markup")
	}
	if !strings.Contains(body, "Fight Club") {
		t.Fatalf("expected wanted queue item, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "import_failed") || !strings.Contains(body, "failed") {
		t.Fatalf("expected failed history statuses in recent history, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, "Fight Club.Fixture") || !strings.Contains(body, "Bad Release") {
		t.Fatalf("expected fixture history titles, got: %s", truncate(body, 800))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
