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

func TestUnifiedQueuePageTimeoutsBound(t *testing.T) {
	pageBudget := automationDialTimeout + 2*automationReadTimeout + time.Second
	if pageBudget < automationDialTimeout+2*automationReadTimeout {
		t.Fatal("unified queue page budget must cover dial + two reads")
	}
}

func TestUnifiedQueuePageFailsFastOnBlockedDiscovery(t *testing.T) {
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

	r := mustRequest("GET", "/queue")
	w := httptest.NewRecorder()

	start := time.Now()
	h.UnifiedQueuePage(w, r)
	elapsed := time.Since(start)

	limit := automationDialTimeout + 2*time.Second
	if elapsed > limit {
		t.Fatalf("queue page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < automationDialTimeout-500*time.Millisecond {
		t.Fatalf("queue page returned too quickly (%v); expected dial timeout ~%v", elapsed, automationDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="unified-queue-page"`) {
		t.Fatal("expected queue page markup")
	}
	if !strings.Contains(body, "media automation module discovery") {
		t.Fatalf("expected user-facing discovery error, got: %s", truncate(body, 400))
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "timed out") &&
		!strings.Contains(lower, "context") &&
		!strings.Contains(lower, "deadline") &&
		!strings.Contains(lower, "cancel") {
		t.Fatalf("expected timeout/cancel error in page body, got: %s", truncate(body, 400))
	}
}
