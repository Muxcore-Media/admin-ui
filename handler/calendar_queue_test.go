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

type fixtureCalendarAdmin struct {
	mediaadminv1.UnimplementedMediaAdminServiceServer
	items []*mediaadminv1.CalendarItem
}

func (f fixtureCalendarAdmin) GetCalendar(context.Context, *mediaadminv1.GetCalendarRequest) (*mediaadminv1.GetCalendarResponse, error) {
	return &mediaadminv1.GetCalendarResponse{Items: f.items}, nil
}

func (f fixtureCalendarAdmin) GetMediaTypeInfo(context.Context, *mediaadminv1.GetMediaTypeInfoRequest) (*mediaadminv1.GetMediaTypeInfoResponse, error) {
	return &mediaadminv1.GetMediaTypeInfoResponse{DisplayName: "TV", Features: []string{"calendar"}}, nil
}

type calendarCapDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	addr string
}

func (d calendarCapDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() != capMediaLibrary {
		return &discoveryv1.FindByCapabilityResponse{}, nil
	}
	return &discoveryv1.FindByCapabilityResponse{
		Modules: []*discoveryv1.ModuleInfoProto{{
			Id: "media-tvshows", Name: "TV Shows", HttpAddr: d.addr,
		}},
	}, nil
}

func (d calendarCapDiscovery) Resolve(_ context.Context, req *discoveryv1.ResolveRequest) (*discoveryv1.ResolveResponse, error) {
	return &discoveryv1.ResolveResponse{
		Found: true,
		Module: &discoveryv1.ModuleInfoProto{
			Id: req.GetModuleId(), Name: req.GetModuleId(), HttpAddr: d.addr,
		},
	}, nil
}

func TestUnifiedCalendarPageSoftEmpty(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/calendar")
	w := httptest.NewRecorder()
	h.UnifiedCalendarPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="unified-calendar-page"`) {
		t.Fatal("expected calendar page")
	}
}

func TestUnifiedCalendarPageFixture(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(srv, fixtureCalendarAdmin{
		items: []*mediaadminv1.CalendarItem{{
			Id: "e1", ParentId: "s1", Title: "Show", Subtitle: "Pilot",
			Date: "2026-08-21", Monitored: true, HasFile: false,
			Metadata: map[string]string{"season_number": "1", "episode_number": "1"},
		}},
	})
	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, calendarCapDiscovery{addr: lis.Addr().String()})
	go discSrv.Serve(discLis)
	t.Cleanup(func() { discSrv.Stop(); _ = discLis.Close() })

	core, err := client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/calendar")
	w := httptest.NewRecorder()
	h.UnifiedCalendarPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="calendar-table"`) || !strings.Contains(body, "Show") {
		t.Fatalf("expected calendar rows, got: %s", truncate(body, 600))
	}
}

type fixtureAutomationQueue struct {
	automationv1.UnimplementedAutomationServiceServer
}

func (f fixtureAutomationQueue) GetQueue(context.Context, *automationv1.GetQueueRequest) (*automationv1.GetQueueResponse, error) {
	return &automationv1.GetQueueResponse{
		Items: []*automationv1.QueueItem{{
			Id: "q1", ItemId: "m1", ItemType: "movie", Title: "Fight Club", Year: 1999, Missing: true, Monitored: true,
		}},
		Total: 1, Page: 1, PageSize: 50,
	}, nil
}

func (f fixtureAutomationQueue) GetHistory(context.Context, *automationv1.GetHistoryRequest) (*automationv1.GetHistoryResponse, error) {
	return &automationv1.GetHistoryResponse{
		Records: []*automationv1.DownloadRecord{{
			Id: "h1", WantedItemId: "w1", Guid: "guid-1", Title: "Fight Club.Fixture",
			Status: "import_failed", Indexer: "fixture", CreatedAt: "2026-08-20T00:00:00Z",
		}},
		Total: 1, Page: 1, PageSize: 50,
	}, nil
}

func (f fixtureAutomationQueue) RetryImport(context.Context, *automationv1.RetryImportRequest) (*automationv1.RetryImportResponse, error) {
	return &automationv1.RetryImportResponse{Attempted: 1, Message: "ok"}, nil
}

func (f fixtureAutomationQueue) BlocklistRelease(context.Context, *automationv1.BlocklistReleaseRequest) (*automationv1.BlocklistReleaseResponse, error) {
	return &automationv1.BlocklistReleaseResponse{Success: true}, nil
}

func (f fixtureAutomationQueue) RemoveFromQueue(context.Context, *automationv1.RemoveFromQueueRequest) (*automationv1.RemoveFromQueueResponse, error) {
	return &automationv1.RemoveFromQueueResponse{}, nil
}

type autoCapDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	addr string
}

func (d autoCapDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() != capMediaAutomation {
		return &discoveryv1.FindByCapabilityResponse{}, nil
	}
	return &discoveryv1.FindByCapabilityResponse{
		Modules: []*discoveryv1.ModuleInfoProto{{
			Id: "media-automation", Name: "Automation", HttpAddr: d.addr,
		}},
	}, nil
}

func startAutomationFixture(t *testing.T) (core *client.Client, cleanup func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	automationv1.RegisterAutomationServiceServer(srv, fixtureAutomationQueue{})
	go srv.Serve(lis)

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		srv.Stop()
		_ = lis.Close()
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, autoCapDiscovery{addr: lis.Addr().String()})
	go discSrv.Serve(discLis)

	core, err = client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		discSrv.Stop()
		srv.Stop()
		t.Fatal(err)
	}
	cleanup = func() {
		_ = core.Close()
		discSrv.Stop()
		srv.Stop()
		_ = discLis.Close()
		_ = lis.Close()
	}
	return core, cleanup
}

func TestUnifiedQueuePageFixture(t *testing.T) {
	core, cleanup := startAutomationFixture(t)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/queue")
	w := httptest.NewRecorder()
	h.UnifiedQueuePage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="unified-queue-page"`) {
		t.Fatal("expected queue page")
	}
	if !strings.Contains(body, "Fight Club") {
		t.Fatalf("expected wanted title, got: %s", truncate(body, 500))
	}
	if !strings.Contains(body, `data-testid="queue-failures-section"`) {
		t.Fatalf("expected failures section, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, `data-testid="stuck-badge"`) || !strings.Contains(body, "import_failed") {
		t.Fatalf("expected stuck import_failed badge, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, `data-testid="retry-import"`) || !strings.Contains(body, `data-testid="blocklist-release"`) {
		t.Fatalf("expected stuck actions, got: %s", truncate(body, 800))
	}
}

func TestUnifiedQueuePageSoftError(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/queue")
	w := httptest.NewRecorder()
	h.UnifiedQueuePage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="unified-queue-page"`) {
		t.Fatal("expected queue page")
	}
	if !strings.Contains(body, `data-testid="queue-error"`) {
		t.Fatalf("expected soft error, got: %s", truncate(body, 400))
	}
}
