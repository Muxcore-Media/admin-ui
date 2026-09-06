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
)

type fixtureWantedAdmin struct {
	mediaadminv1.UnimplementedMediaAdminServiceServer
	missing []*mediaadminv1.MissingItem
	kind    string
}

func (f fixtureWantedAdmin) GetMediaTypeInfo(context.Context, *mediaadminv1.GetMediaTypeInfoRequest) (*mediaadminv1.GetMediaTypeInfoResponse, error) {
	name := "Movies"
	if f.kind == "tv" {
		name = "TV Shows"
	}
	return &mediaadminv1.GetMediaTypeInfoResponse{
		DisplayName: name,
		Features:    []mediaadminv1.Feature{mediaadminv1.Feature_FEATURE_MISSING},
	}, nil
}

func (f fixtureWantedAdmin) ListMissing(context.Context, *mediaadminv1.ListMissingRequest) (*mediaadminv1.ListMissingResponse, error) {
	return &mediaadminv1.ListMissingResponse{
		Items:    f.missing,
		Total:    int32(len(f.missing)),
		Page:     1,
		PageSize: 100,
	}, nil
}

type wantedCapDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	addr     string
	moduleID string
	name     string
}

func (d wantedCapDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() != capMediaLibrary {
		return &discoveryv1.FindByCapabilityResponse{}, nil
	}
	return &discoveryv1.FindByCapabilityResponse{
		Modules: []*discoveryv1.ModuleInfoProto{{
			Id: d.moduleID, Name: d.name, HttpAddr: d.addr,
		}},
	}, nil
}

func TestUnifiedWantedPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest(http.MethodGet, "/wanted")
	w := httptest.NewRecorder()
	h.UnifiedWantedPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="unified-wanted-page"`) {
		t.Fatal("expected wanted page")
	}
	if !strings.Contains(body, `data-testid="wanted-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}

func TestUnifiedWantedPageSoftEmptyNoModules(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, wantedCapDiscovery{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	core, err := client.Dial(lis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest(http.MethodGet, "/wanted")
	w := httptest.NewRecorder()
	h.UnifiedWantedPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="wanted-soft-empty"`) {
		t.Fatalf("expected soft-empty when no modules, got: %s", truncate(body, 600))
	}
}

func TestUnifiedWantedPageFixture(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(srv, fixtureWantedAdmin{
		kind: "tv",
		missing: []*mediaadminv1.MissingItem{{
			Id: "e1", ParentId: "s1", Title: "Missing Pilot", Year: 2024,
			Metadata: map[string]string{"season_number": "1", "episode_number": "1", "air_date": "2026-01-15"},
		}},
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, wantedCapDiscovery{
		addr: lis.Addr().String(), moduleID: "media-tvshows", name: "TV Shows",
	})
	go func() { _ = discSrv.Serve(discLis) }()
	t.Cleanup(func() { discSrv.Stop(); _ = discLis.Close() })

	core, err := client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest(http.MethodGet, "/wanted")
	w := httptest.NewRecorder()
	h.UnifiedWantedPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `data-testid="wanted-soft-empty"`) {
		t.Fatal("did not expect soft-empty when module is available")
	}
	if !strings.Contains(body, `data-testid="wanted-table"`) || !strings.Contains(body, "Missing Pilot") {
		t.Fatalf("expected missing rows, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, `data-testid="wanted-total"`) || !strings.Contains(body, ">1<") {
		t.Fatalf("expected summary card with total 1, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, "S1E1") {
		t.Fatalf("expected season/episode in row, got: %s", truncate(body, 400))
	}
}

func TestUnifiedWantedPageMovieFilter(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(srv, fixtureWantedAdmin{
		kind: "movie",
		missing: []*mediaadminv1.MissingItem{{
			Id: "m1", ParentId: "m1", Title: "Dune Part Three", Year: 2026,
		}},
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, wantedCapDiscovery{
		addr: lis.Addr().String(), moduleID: "media-movies", name: "Movies",
	})
	go func() { _ = discSrv.Serve(discLis) }()
	t.Cleanup(func() { discSrv.Stop(); _ = discLis.Close() })

	core, err := client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest(http.MethodGet, "/wanted?kind=movie")
	w := httptest.NewRecorder()
	h.UnifiedWantedPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Dune Part Three") {
		t.Fatalf("expected movie in filtered list, got: %s", truncate(body, 600))
	}
}
