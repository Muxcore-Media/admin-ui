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
	automationv1 "github.com/Muxcore-Media/contracts-automation/muxcore/automation/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

// TestReleaseSearchPageEmpty verifies the empty-query path renders the form
// without requiring any upstream connectivity.
func TestReleaseSearchPageEmpty(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/release-search")
	w := httptest.NewRecorder()
	h.ReleaseSearchPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="release-search-page"`) {
		t.Fatal("expected release-search-page test id")
	}
	// Results section must not appear when no query is given.
	if strings.Contains(body, `data-testid="release-search-results"`) {
		t.Fatal("results section must be absent without a query")
	}
}

// TestReleaseSearchPageSoftEmptyNoAutomation verifies that a query with no
// reachable automation module renders a soft-empty error page rather than a
// hard failure.
func TestReleaseSearchPageSoftEmptyNoAutomation(t *testing.T) {
	// Discovery returns nothing for media.automation.
	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, multiCapDiscovery{byCap: map[string][]*discoveryv1.ModuleInfoProto{}})
	go func() { _ = discSrv.Serve(discLis) }()
	t.Cleanup(func() {
		discSrv.Stop()
		_ = discLis.Close()
	})

	core, err := client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/release-search?q=Fight+Club")
	w := httptest.NewRecorder()
	h.ReleaseSearchPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (soft error), got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="release-search-page"`) {
		t.Fatal("expected release-search-page markup")
	}
	if !strings.Contains(body, `data-testid="release-search-error"`) {
		t.Fatalf("expected error section when automation unavailable, got: %s", truncate(body, 600))
	}
}

// fixtureSearchAutomation is a minimal automation gRPC server that returns
// canned SearchItem hits so the release-search page can be tested end-to-end.
type fixtureSearchAutomation struct {
	automationv1.UnimplementedAutomationServiceServer
}

func (fixtureSearchAutomation) SearchItem(_ context.Context, req *automationv1.SearchItemRequest) (*automationv1.SearchItemResponse, error) {
	if req.GetQuery() == "" {
		return &automationv1.SearchItemResponse{}, nil
	}
	return &automationv1.SearchItemResponse{
		Matches: []*automationv1.ReleaseMatch{
			{
				Guid:             "guid-1",
				Title:            "Fight.Club.1999.1080p.BluRay.x264",
				IndexerName:      "BTN",
				DownloadProtocol: "torrent",
				Size:             int64(8) << 30,
				Score:            185,
				DownloadUrl:      "magnet:?xt=urn:btih:AABBCCDDEE",
			},
			{
				Guid:             "guid-2",
				Title:            "Fight.Club.1999.720p.WEB-DL",
				IndexerName:      "NZBGeek",
				DownloadProtocol: "usenet",
				Size:             int64(4) << 30,
				Score:            140,
				DownloadUrl:      "https://example.com/nzb/fc.nzb",
			},
		},
	}, nil
}

func (fixtureSearchAutomation) Dispatch(_ context.Context, req *automationv1.DispatchRequest) (*automationv1.DispatchResponse, error) {
	return &automationv1.DispatchResponse{
		DownloadId: "dl-fixture-1",
		Status:     "queued",
	}, nil
}

// startSearchFixture stands up a fake discovery + automation gRPC pair and
// returns a connected core client.
func startSearchFixture(t *testing.T) (core *client.Client, cleanup func()) {
	t.Helper()

	autoLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	autoSrv := grpc.NewServer()
	automationv1.RegisterAutomationServiceServer(autoSrv, fixtureSearchAutomation{})
	go func() { _ = autoSrv.Serve(autoLis) }()

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		autoSrv.Stop()
		_ = autoLis.Close()
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, autoCapDiscovery{addr: autoLis.Addr().String()})
	go func() { _ = discSrv.Serve(discLis) }()

	core, err = client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		discSrv.Stop()
		autoSrv.Stop()
		t.Fatal(err)
	}

	cleanup = func() {
		_ = core.Close()
		discSrv.Stop()
		autoSrv.Stop()
		_ = discLis.Close()
		_ = autoLis.Close()
	}
	return core, cleanup
}

// TestReleaseSearchPageResults checks that search results are rendered when
// the automation module returns matches.
func TestReleaseSearchPageResults(t *testing.T) {
	core, cleanup := startSearchFixture(t)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/release-search?q=Fight+Club&type=movie")
	w := httptest.NewRecorder()
	h.ReleaseSearchPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="release-search-rows"`) {
		t.Fatalf("expected result rows, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, "Fight.Club.1999.1080p.BluRay.x264") {
		t.Fatalf("expected first result title, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, "BTN") {
		t.Fatalf("expected indexer name BTN, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="grab-button"`) {
		t.Fatalf("expected grab button, got: %s", truncate(body, 600))
	}
	// Size formatting: 8 GiB → "8.0 GB"
	if !strings.Contains(body, "8.0 GB") {
		t.Fatalf("expected formatted size, got: %s", truncate(body, 600))
	}
	if strings.Contains(body, `data-testid="release-search-error"`) {
		t.Fatal("did not expect error section on successful search")
	}
}

// TestReleaseSearchGrabRedirects verifies that a valid grab POST dispatches
// and redirects back to the search page with a flash message.
func TestReleaseSearchGrabRedirects(t *testing.T) {
	core, cleanup := startSearchFixture(t)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r, err := http.NewRequest("POST", "/release-search/grab", strings.NewReader(
		"guid=guid-1"+
			"&release_title=Fight.Club.1999.1080p.BluRay.x264"+
			"&download_url=magnet%3A%3Fxt%3Durn%3Abtih%3AAABBCCDDEE"+
			"&download_protocol=torrent"+
			"&size=8589934592"+
			"&score=185"+
			"&indexer=BTN"+
			"&item_type=movie"+
			"&item_id="+
			"&title=Fight+Club"+
			"&query=Fight+Club"+
			"&search_type=movie",
	))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:12345"

	w := httptest.NewRecorder()
	h.ReleaseSearchGrab(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d (body: %s)", w.Code, truncate(w.Body.String(), 400))
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/release-search?") {
		t.Fatalf("expected redirect to /release-search?, got %s", loc)
	}
	if !strings.Contains(loc, "dispatched=") {
		t.Fatalf("expected dispatched param in redirect, got %s", loc)
	}
}

// TestReleaseSearchGrabSoftErrorNoAutomation verifies the grab endpoint
// redirects with an error param rather than crashing when automation is down.
func TestReleaseSearchGrabSoftErrorNoAutomation(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r, err := http.NewRequest("POST", "/release-search/grab", strings.NewReader(
		"guid=guid-x&release_title=Test&download_url=magnet:?x&download_protocol=torrent&size=0&score=0&indexer=x&item_type=movie&query=test&search_type=movie",
	))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:12345"

	w := httptest.NewRecorder()
	h.ReleaseSearchGrab(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Fatalf("expected error param in redirect, got %s", loc)
	}
}

// TestReleaseSearchRoutesRegistered confirms both routes are wired up.
func TestReleaseSearchRoutesRegistered(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/release-search"},
		{"POST", "/release-search/grab"},
	} {
		r := mustRequest(tc.method, tc.path)
		_, pattern := mux.Handler(r)
		if pattern == "" {
			t.Fatalf("expected route registered for %s %s", tc.method, tc.path)
		}
	}
}
