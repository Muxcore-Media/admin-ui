package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/session"
	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

// TestActivityPageSoftEmpty verifies the page renders with a soft state when
// no Core connection is available (no modules, no automation).
func TestActivityPageSoftEmpty(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/activity")
	w := httptest.NewRecorder()
	h.ActivityPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="activity-page"`) {
		t.Fatal("expected activity page testid")
	}
	if !strings.Contains(body, `data-testid="activity-empty"`) {
		t.Fatalf("expected empty state, got: %s", truncate(body, 500))
	}
}

// fixtureActivityAutomation is a stub automation server with two history
// records: one import_failed (stuck) and one completed (not shown in triage).
type fixtureActivityAutomation struct {
	automationv1.UnimplementedAutomationServiceServer
}

func (f fixtureActivityAutomation) GetHistory(_ context.Context, _ *automationv1.GetHistoryRequest) (*automationv1.GetHistoryResponse, error) {
	return &automationv1.GetHistoryResponse{
		Records: []*automationv1.DownloadRecord{
			{
				Id: "af1", WantedItemId: "w1", Guid: "g-1", Title: "Stuck Movie",
				Status: "import_failed", Indexer: "fixture", CreatedAt: "2026-09-01T00:00:00Z",
			},
			{
				Id: "af2", Title: "OK Movie", Status: "completed", CreatedAt: "2026-09-02T00:00:00Z",
			},
		},
		Total: 2, Page: 1, PageSize: 50,
	}, nil
}

func (f fixtureActivityAutomation) RetryImport(_ context.Context, _ *automationv1.RetryImportRequest) (*automationv1.RetryImportResponse, error) {
	return &automationv1.RetryImportResponse{Attempted: 1, Message: "retry_ok"}, nil
}

func (f fixtureActivityAutomation) BlocklistRelease(_ context.Context, _ *automationv1.BlocklistReleaseRequest) (*automationv1.BlocklistReleaseResponse, error) {
	return &automationv1.BlocklistReleaseResponse{Success: true}, nil
}

// fixtureActivityMedia is a stub media-admin server returning a history record.
type fixtureActivityMedia struct {
	mediaadminv1.UnimplementedMediaAdminServiceServer
}

func (f fixtureActivityMedia) GetMediaTypeInfo(_ context.Context, _ *mediaadminv1.GetMediaTypeInfoRequest) (*mediaadminv1.GetMediaTypeInfoResponse, error) {
	return &mediaadminv1.GetMediaTypeInfoResponse{DisplayName: "Movies"}, nil
}

func (f fixtureActivityMedia) ListHistory(_ context.Context, _ *mediaadminv1.ListHistoryRequest) (*mediaadminv1.ListHistoryResponse, error) {
	return &mediaadminv1.ListHistoryResponse{
		Records: []*mediaadminv1.HistoryRecord{{
			Id: "r1", EventType: "import", ItemId: "m1", Title: "Fight Club",
			SourceTitle: "Fight.Club.1999.mkv", Quality: "1080p", Indexer: "fixture",
			CreatedAt: "2026-09-01T12:00:00Z",
		}},
		Total: 1, Page: 1, PageSize: 50,
	}, nil
}

// activityCapDiscovery responds to both media.library and media.automation
// capability queries, routing each to the right fixture server.
type activityCapDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	mediaAddr string
	autoAddr  string
}

func (d activityCapDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	switch req.GetCapability() {
	case capMediaLibrary:
		return &discoveryv1.FindByCapabilityResponse{
			Modules: []*discoveryv1.ModuleInfoProto{{
				Id: "media-movies", Name: "Movies", HttpAddr: d.mediaAddr,
			}},
		}, nil
	case capMediaAutomation:
		return &discoveryv1.FindByCapabilityResponse{
			Modules: []*discoveryv1.ModuleInfoProto{{
				Id: "media-automation", Name: "Automation", HttpAddr: d.autoAddr,
			}},
		}, nil
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func (d activityCapDiscovery) Resolve(_ context.Context, req *discoveryv1.ResolveRequest) (*discoveryv1.ResolveResponse, error) {
	return &discoveryv1.ResolveResponse{
		Found: true,
		Module: &discoveryv1.ModuleInfoProto{
			Id: req.GetModuleId(), Name: req.GetModuleId(), HttpAddr: d.mediaAddr,
		},
	}, nil
}

func startActivityFixture(t *testing.T) (core *client.Client, cleanup func()) {
	t.Helper()

	// Start media-admin stub.
	mediaLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mediaSrv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(mediaSrv, fixtureActivityMedia{})
	go func() { _ = mediaSrv.Serve(mediaLis) }()

	// Start automation stub.
	autoLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		mediaSrv.Stop()
		_ = mediaLis.Close()
		t.Fatal(err)
	}
	autoSrv := grpc.NewServer()
	automationv1.RegisterAutomationServiceServer(autoSrv, fixtureActivityAutomation{})
	go func() { _ = autoSrv.Serve(autoLis) }()

	// Start discovery stub.
	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		autoSrv.Stop()
		_ = autoLis.Close()
		mediaSrv.Stop()
		_ = mediaLis.Close()
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, activityCapDiscovery{
		mediaAddr: mediaLis.Addr().String(),
		autoAddr:  autoLis.Addr().String(),
	})
	go func() { _ = discSrv.Serve(discLis) }()

	core, err = client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		discSrv.Stop()
		autoSrv.Stop()
		mediaSrv.Stop()
		t.Fatal(err)
	}
	cleanup = func() {
		_ = core.Close()
		discSrv.Stop()
		autoSrv.Stop()
		mediaSrv.Stop()
		_ = discLis.Close()
		_ = autoLis.Close()
		_ = mediaLis.Close()
	}
	return core, cleanup
}

// TestActivityPageFixture verifies that the activity page renders history rows
// and surfaces the import_failed record in the "Needs attention" panel.
func TestActivityPageFixture(t *testing.T) {
	core, cleanup := startActivityFixture(t)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/activity")
	w := httptest.NewRecorder()
	h.ActivityPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, `data-testid="activity-page"`) {
		t.Fatal("expected activity page testid")
	}
	// Failed imports panel.
	if !strings.Contains(body, `data-testid="activity-failures-section"`) {
		t.Fatalf("expected failures section, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "Stuck Movie") {
		t.Fatalf("expected stuck movie title, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="activity-stuck-badge"`) {
		t.Fatalf("expected stuck badge, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="activity-retry"`) {
		t.Fatalf("expected retry button, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="activity-dismiss"`) {
		t.Fatalf("expected dismiss button, got: %s", truncate(body, 600))
	}
	// OK records should NOT appear in the failures section.
	if strings.Contains(body, "OK Movie") {
		t.Fatalf("completed record should not appear in failures section")
	}
	// Media library history row.
	if !strings.Contains(body, "Fight Club") {
		t.Fatalf("expected media history row, got: %s", truncate(body, 600))
	}
	// Open-item link present when ModuleID + ItemID are set.
	if !strings.Contains(body, `data-testid="activity-open-item"`) {
		t.Fatalf("expected open-item link, got: %s", truncate(body, 600))
	}
}

// TestActivityRetry verifies the retry handler redirects with a success status.
func TestActivityRetry(t *testing.T) {
	core, cleanup := startActivityFixture(t)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	form := url.Values{"history_id": {"af1"}}
	r, err := http.NewRequest("POST", "/activity/retry", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:12345"

	w := httptest.NewRecorder()
	h.ActivityRetry(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/activity") {
		t.Fatalf("expected redirect to /activity, got %s", loc)
	}
	if !strings.Contains(loc, "status=") {
		t.Fatalf("expected status param in redirect, got %s", loc)
	}
}

// TestActivityPageNoAutomation verifies the page renders gracefully when the
// automation service is unavailable (media history still loads).
func TestActivityPageNoAutomation(t *testing.T) {
	// Start only media-admin stub (no automation stub).
	mediaLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mediaSrv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(mediaSrv, fixtureActivityMedia{})
	go func() { _ = mediaSrv.Serve(mediaLis) }()
	defer func() { mediaSrv.Stop(); _ = mediaLis.Close() }()

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, activityCapDiscovery{
		mediaAddr: mediaLis.Addr().String(),
		autoAddr:  "", // no automation
	})
	go func() { _ = discSrv.Serve(discLis) }()
	defer func() { discSrv.Stop(); _ = discLis.Close() }()

	core, err := client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = core.Close() }()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/activity")
	w := httptest.NewRecorder()
	h.ActivityPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	// Page renders without failures section (automation down, but not an error).
	if !strings.Contains(body, `data-testid="activity-page"`) {
		t.Fatal("expected activity page testid")
	}
	// Media history still loads.
	if !strings.Contains(body, "Fight Club") {
		t.Fatalf("expected media history row even when automation is down, got: %s", truncate(body, 500))
	}
}
