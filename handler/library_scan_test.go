package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	"google.golang.org/grpc"
)

type scannerStub struct {
	scannerv1.UnimplementedScannerServiceServer
	stats       *scannerv1.GetStatsResponse
	scanCalls   int
	rootsCalls  int
	scanResp    *scannerv1.ScanResponse
	rootsResp   *scannerv1.ScanLibraryRootsResponse
	lastScanReq *scannerv1.ScanRequest
}

func (s *scannerStub) GetStats(context.Context, *scannerv1.GetStatsRequest) (*scannerv1.GetStatsResponse, error) {
	if s.stats != nil {
		return s.stats, nil
	}
	return &scannerv1.GetStatsResponse{
		TotalImported:  42,
		WatchDirs:      3,
		LastScanStatus: "completed",
		LastScanAt:     1_700_000_000,
	}, nil
}

func (s *scannerStub) Scan(_ context.Context, req *scannerv1.ScanRequest) (*scannerv1.ScanResponse, error) {
	s.scanCalls++
	s.lastScanReq = req
	if s.scanResp != nil {
		return s.scanResp, nil
	}
	return &scannerv1.ScanResponse{FilesFound: 5, FilesImported: 2, FilesSkipped: 3}, nil
}

func (s *scannerStub) ScanLibraryRoots(context.Context, *scannerv1.ScanLibraryRootsRequest) (*scannerv1.ScanLibraryRootsResponse, error) {
	s.rootsCalls++
	if s.rootsResp != nil {
		return s.rootsResp, nil
	}
	return &scannerv1.ScanLibraryRootsResponse{FilesFound: 1, FilesImported: 1, FilesSkipped: 0}, nil
}

func setupScannerHandler(t *testing.T, stub *scannerStub) *Handler {
	t.Helper()
	sLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sSrv := grpc.NewServer()
	scannerv1.RegisterScannerServiceServer(sSrv, stub)
	go func() { _ = sSrv.Serve(sLis) }()
	t.Cleanup(func() {
		sSrv.Stop()
		_ = sLis.Close()
	})

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, capDiscovery{
		mods: map[string]*discoveryv1.ModuleInfoProto{
			capMediaScanner: {Id: "media-scanner", HttpAddr: sLis.Addr().String()},
		},
	})
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
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	h.Core = core
	return h
}

func TestLibraryScanPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest(http.MethodGet, "/library-scan")
	w := httptest.NewRecorder()
	h.LibraryScanPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="library-scan-page"`) {
		t.Fatal("expected library scan page")
	}
	if !strings.Contains(body, `data-testid="library-scan-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}

func TestLibraryScanPageShowsStats(t *testing.T) {
	stub := &scannerStub{
		stats: &scannerv1.GetStatsResponse{
			TotalImported:  99,
			WatchDirs:      2,
			LastScanStatus: "running",
			LastScanAt:     1_700_000_000,
		},
	}
	h := setupScannerHandler(t, stub)

	r := mustRequest(http.MethodGet, "/library-scan")
	w := httptest.NewRecorder()
	h.LibraryScanPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `data-testid="library-scan-soft-empty"`) {
		t.Fatal("did not expect soft-empty when module is available")
	}
	if !strings.Contains(body, `data-testid="library-scan-status"`) || !strings.Contains(body, "running") {
		t.Fatalf("expected running status, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "99") || !strings.Contains(body, "Scan watch folders") {
		t.Fatalf("expected stats and actions, got: %s", truncate(body, 800))
	}
}

func TestLibraryScanPostWatchScan(t *testing.T) {
	stub := &scannerStub{}
	h := setupScannerHandler(t, stub)

	body := strings.NewReader("scan_type=watch")
	r := httptest.NewRequest(http.MethodPost, "/library-scan/scan", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.LibraryScanPost(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if stub.scanCalls != 1 {
		t.Fatalf("scanCalls=%d", stub.scanCalls)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect %q: %v", w.Header().Get("Location"), err)
	}
	if loc.Path != "/library-scan" || !strings.Contains(loc.Query().Get("ok"), "found=5") {
		t.Fatalf("unexpected redirect %q", w.Header().Get("Location"))
	}
}

func TestLibraryScanPostLibraryRoots(t *testing.T) {
	stub := &scannerStub{}
	h := setupScannerHandler(t, stub)

	body := strings.NewReader("scan_type=library_roots")
	r := httptest.NewRequest(http.MethodPost, "/library-scan/scan", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.LibraryScanPost(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if stub.rootsCalls != 1 {
		t.Fatalf("rootsCalls=%d", stub.rootsCalls)
	}
}
