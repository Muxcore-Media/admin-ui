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

func TestMediaLibraryListSoftEmptyWhenUnresolved(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/media/media-movies")
	r.SetPathValue("moduleID", "media-movies")
	w := httptest.NewRecorder()
	h.MediaLibraryList(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft-empty page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="media-list-page"`) {
		t.Fatal("expected media list page markup")
	}
	if !strings.Contains(body, `data-testid="media-soft-empty"`) {
		t.Fatalf("expected soft-empty state, got: %s", truncate(body, 500))
	}
}

type fixtureMediaAdmin struct {
	mediaadminv1.UnimplementedMediaAdminServiceServer
	items []*mediaadminv1.MediaItem
}

func (f fixtureMediaAdmin) GetMediaTypeInfo(context.Context, *mediaadminv1.GetMediaTypeInfoRequest) (*mediaadminv1.GetMediaTypeInfoResponse, error) {
	return &mediaadminv1.GetMediaTypeInfoResponse{
		DisplayName: "Movies",
		Features: []mediaadminv1.Feature{
			mediaadminv1.Feature_FEATURE_MISSING,
			mediaadminv1.Feature_FEATURE_TAGS,
		},
	}, nil
}

func (f fixtureMediaAdmin) ListItems(context.Context, *mediaadminv1.ListItemsRequest) (*mediaadminv1.ListItemsResponse, error) {
	return &mediaadminv1.ListItemsResponse{
		Items:    f.items,
		Total:    int32(len(f.items)),
		Page:     1,
		PageSize: 50,
	}, nil
}

type mediaResolveDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	addr string
}

func (d mediaResolveDiscovery) Resolve(_ context.Context, req *discoveryv1.ResolveRequest) (*discoveryv1.ResolveResponse, error) {
	return &discoveryv1.ResolveResponse{
		Found: true,
		Module: &discoveryv1.ModuleInfoProto{
			Id:       req.GetModuleId(),
			Name:     req.GetModuleId(),
			HttpAddr: d.addr,
		},
	}, nil
}

func startMediaFixture(t *testing.T, items []*mediaadminv1.MediaItem) (mediaAddr string, core *client.Client, cleanup func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	mediaadminv1.RegisterMediaAdminServiceServer(srv, fixtureMediaAdmin{items: items})
	go func() { _ = srv.Serve(lis) }()

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		srv.Stop()
		_ = lis.Close()
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, mediaResolveDiscovery{addr: lis.Addr().String()})
	go func() { _ = discSrv.Serve(discLis) }()

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
	return lis.Addr().String(), core, cleanup
}

func TestMediaLibraryListEmptyGolden(t *testing.T) {
	_, core, cleanup := startMediaFixture(t, nil)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/media/media-movies")
	r.SetPathValue("moduleID", "media-movies")
	w := httptest.NewRecorder()
	h.MediaLibraryList(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("empty list: status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="media-empty"`) {
		t.Fatalf("expected empty library state, got: %s", truncate(body, 500))
	}
}

func TestMediaLibraryListFixtureGolden(t *testing.T) {
	_, core, cleanup := startMediaFixture(t, []*mediaadminv1.MediaItem{{
		Id:    "m1",
		Title: "Fight Club",
		Year:  1999,
	}})
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/media/media-tvshows")
	r.SetPathValue("moduleID", "media-tvshows")
	w := httptest.NewRecorder()
	h.MediaLibraryList(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("fixture list: status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="media-list-page"`) || !strings.Contains(body, "Fight Club") {
		t.Fatalf("expected fixture title in golden HTML, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="media-item-count"`) {
		t.Fatal("expected item count testid")
	}
}
