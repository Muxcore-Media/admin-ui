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

func TestDashboardTimeoutsBound(t *testing.T) {
	if dashboardDialTimeout != 3*time.Second {
		t.Fatalf("dashboardDialTimeout: got %v, want 3s", dashboardDialTimeout)
	}
	if dashboardReadTimeout != 5*time.Second {
		t.Fatalf("dashboardReadTimeout: got %v, want 5s", dashboardReadTimeout)
	}
	pageBudget := 3*dashboardDialTimeout + 3*dashboardReadTimeout + time.Second
	if pageBudget < dashboardDialTimeout+dashboardReadTimeout {
		t.Fatal("dashboard page budget must cover dial + at least one read")
	}
}

// dashboardBlockingDiscovery returns Members promptly but blocks FindByCapability until ctx cancel.
type dashboardBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (dashboardBlockingDiscovery) Members(context.Context, *discoveryv1.MembersRequest) (*discoveryv1.MembersResponse, error) {
	return &discoveryv1.MembersResponse{
		Members: []*discoveryv1.NodeInfo{{
			Id: "muxcore-local", GrpcAddr: ":9090", HttpAddr: ":8080",
			Modules: []string{"media-automation"},
		}},
		LeaderId: "muxcore-local",
	}, nil
}

func (dashboardBlockingDiscovery) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() == capMediaAutomation {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestDashboardFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, dashboardBlockingDiscovery{})
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
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/")
	w := httptest.NewRecorder()

	start := time.Now()
	h.Dashboard(w, r)
	elapsed := time.Since(start)

	limit := dashboardDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("dashboard took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < dashboardDialTimeout-500*time.Millisecond {
		t.Fatalf("dashboard returned too quickly (%v); expected dial timeout ~%v", elapsed, dashboardDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 dashboard page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="dashboard-daily-driver"`) {
		t.Fatalf("expected dashboard markup, got: %s", truncate(body, 400))
	}
}
