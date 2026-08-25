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

func TestCalendarTimeoutsBound(t *testing.T) {
	if calendarDialTimeout != 3*time.Second {
		t.Fatalf("calendarDialTimeout: got %v, want 3s", calendarDialTimeout)
	}
	if calendarReadTimeout != 5*time.Second {
		t.Fatalf("calendarReadTimeout: got %v, want 5s", calendarReadTimeout)
	}
	pageBudget := calendarDialTimeout + 2*(calendarDialTimeout+calendarReadTimeout) + time.Second
	if pageBudget < calendarDialTimeout+calendarReadTimeout {
		t.Fatal("calendar page budget must cover discovery dial + at least one module read")
	}
}

type calendarBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (calendarBlockingDiscovery) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() == capMediaLibrary {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestUnifiedCalendarPageFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, calendarBlockingDiscovery{})
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

	r := mustRequest("GET", "/calendar")
	w := httptest.NewRecorder()

	start := time.Now()
	h.UnifiedCalendarPage(w, r)
	elapsed := time.Since(start)

	limit := calendarDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("calendar page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < calendarDialTimeout-500*time.Millisecond {
		t.Fatalf("calendar page returned too quickly (%v); expected dial timeout ~%v", elapsed, calendarDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 calendar page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="unified-calendar-page"`) {
		t.Fatalf("expected calendar page markup, got: %s", truncate(body, 400))
	}
}
