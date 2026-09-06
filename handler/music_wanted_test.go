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

type fixtureMusicWantedAdmin struct {
	mediaadminv1.UnimplementedMediaAdminServiceServer
	missing []*mediaadminv1.MissingItem
}

func (f fixtureMusicWantedAdmin) GetMediaTypeInfo(context.Context, *mediaadminv1.GetMediaTypeInfoRequest) (*mediaadminv1.GetMediaTypeInfoResponse, error) {
	return &mediaadminv1.GetMediaTypeInfoResponse{
		DisplayName: "Music Manager",
		Features: []mediaadminv1.Feature{
			mediaadminv1.Feature_FEATURE_MISSING,
			mediaadminv1.Feature_FEATURE_TAGS,
		},
	}, nil
}

func (f fixtureMusicWantedAdmin) ListMissing(context.Context, *mediaadminv1.ListMissingRequest) (*mediaadminv1.ListMissingResponse, error) {
	return &mediaadminv1.ListMissingResponse{
		Items:    f.missing,
		Total:    int32(len(f.missing)),
		Page:     1,
		PageSize: 50,
	}, nil
}

type musicCapDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	addr     string
	moduleID string
	name     string
}

func (d musicCapDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() != capMediaMusic {
		return &discoveryv1.FindByCapabilityResponse{}, nil
	}
	return &discoveryv1.FindByCapabilityResponse{
		Modules: []*discoveryv1.ModuleInfoProto{{
			Id: d.moduleID, Name: d.name, HttpAddr: d.addr,
		}},
	}, nil
}

func TestMusicWantedPageSoftWhenCoreMissing(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest(http.MethodGet, "/music/wanted")
	w := httptest.NewRecorder()
	h.MusicWantedPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft-empty page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="music-wanted-page"`) {
		t.Fatal("expected music wanted page markup")
	}
	if !strings.Contains(body, `data-testid="music-wanted-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, "core unavailable") {
		t.Fatalf("expected soft error about missing core, got: %s", truncate(body, 400))
	}
}

func TestMusicWantedPageEmptyWhenHealthy(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(srv, fixtureMusicWantedAdmin{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, musicCapDiscovery{
		addr: lis.Addr().String(), moduleID: "media-music", name: "Music Manager",
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

	r := mustRequest(http.MethodGet, "/music/wanted")
	w := httptest.NewRecorder()
	h.MusicWantedPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `data-testid="music-wanted-soft-empty"`) {
		t.Fatal("did not expect soft-empty when module is available")
	}
	if !strings.Contains(body, `data-testid="music-wanted-empty"`) {
		t.Fatalf("expected healthy empty state, got: %s", truncate(body, 600))
	}
}

func TestMusicWantedPageListsMissingAlbums(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(srv, fixtureMusicWantedAdmin{
		missing: []*mediaadminv1.MissingItem{{
			Id: "al_1", ParentId: "ar_1", Title: "Missing Album", Year: 2024,
			Metadata: map[string]string{"artist_name": "Fixture Artist", "musicbrainz_id": "mbid-1"},
		}},
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, musicCapDiscovery{
		addr: lis.Addr().String(), moduleID: "media-music", name: "Music Manager",
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

	r := mustRequest(http.MethodGet, "/music/wanted")
	w := httptest.NewRecorder()
	h.MusicWantedPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="music-wanted-table"`) {
		t.Fatalf("expected wanted table, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "Fixture Artist") || !strings.Contains(body, "Missing Album") {
		t.Fatalf("expected fixture rows, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `href="/music/ar_1"`) {
		t.Fatalf("expected artist detail link, got: %s", truncate(body, 400))
	}
}
