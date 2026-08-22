package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/session"
	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	automationv1 "github.com/Muxcore-Media/media-automation/proto/automationv1"
)

type dashDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	autoAddr string
	tvAddr   string
}

func (d dashDiscovery) Members(context.Context, *discoveryv1.MembersRequest) (*discoveryv1.MembersResponse, error) {
	return &discoveryv1.MembersResponse{
		Members: []*discoveryv1.NodeInfo{{
			Id: "muxcore-local", GrpcAddr: ":9090", HttpAddr: ":8080",
			Modules: []string{"media-automation", "media-tvshows"},
		}},
		LeaderId: "muxcore-local",
	}, nil
}

func (d dashDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	switch req.GetCapability() {
	case capMediaAutomation:
		return &discoveryv1.FindByCapabilityResponse{
			Modules: []*discoveryv1.ModuleInfoProto{{
				Id: "media-automation", Name: "Automation", HttpAddr: d.autoAddr,
			}},
		}, nil
	case capMediaLibrary:
		return &discoveryv1.FindByCapabilityResponse{
			Modules: []*discoveryv1.ModuleInfoProto{{
				Id: "media-tvshows", Name: "TV Shows", HttpAddr: d.tvAddr,
			}},
		}, nil
	default:
		return &discoveryv1.FindByCapabilityResponse{}, nil
	}
}

func (d dashDiscovery) Resolve(_ context.Context, req *discoveryv1.ResolveRequest) (*discoveryv1.ResolveResponse, error) {
	addr := d.autoAddr
	if req.GetModuleId() == "media-tvshows" {
		addr = d.tvAddr
	}
	return &discoveryv1.ResolveResponse{
		Found: true,
		Module: &discoveryv1.ModuleInfoProto{
			Id: req.GetModuleId(), Name: req.GetModuleId(), HttpAddr: addr,
		},
	}, nil
}

func startDashboardFixture(t *testing.T) (core *client.Client, cleanup func()) {
	t.Helper()

	autoLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	autoSrv := grpc.NewServer()
	automationv1.RegisterAutomationServiceServer(autoSrv, fixtureAutomationQueue{})
	go autoSrv.Serve(autoLis)

	tvLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		autoSrv.Stop()
		t.Fatal(err)
	}
	tvSrv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(tvSrv, fixtureCalendarAdmin{
		items: []*mediaadminv1.CalendarItem{{
			Id: "ep1", ParentId: "show1", Title: "Pilot", Date: "2026-08-25",
		}},
	})
	go tvSrv.Serve(tvLis)

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		autoSrv.Stop()
		tvSrv.Stop()
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, dashDiscovery{
		autoAddr: autoLis.Addr().String(),
		tvAddr:   tvLis.Addr().String(),
	})
	go discSrv.Serve(discLis)

	core, err = client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		discSrv.Stop()
		autoSrv.Stop()
		tvSrv.Stop()
		t.Fatal(err)
	}
	cleanup = func() {
		_ = core.Close()
		discSrv.Stop()
		autoSrv.Stop()
		tvSrv.Stop()
		_ = discLis.Close()
		_ = autoLis.Close()
		_ = tvLis.Close()
	}
	return core, cleanup
}

func TestDashboardDailyDriverPanel(t *testing.T) {
	core, cleanup := startDashboardFixture(t)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/")
	w := httptest.NewRecorder()
	h.Dashboard(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="dashboard-daily-driver"`) {
		t.Fatal("expected daily driver panel")
	}
	if !strings.Contains(body, "Wanted items") || !strings.Contains(body, "Upcoming releases") {
		t.Fatalf("expected queue and calendar summaries, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "Pilot") {
		t.Fatalf("expected upcoming calendar item, got: %s", truncate(body, 800))
	}
}
