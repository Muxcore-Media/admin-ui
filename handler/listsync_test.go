package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	listsyncv1 "github.com/Muxcore-Media/media-list-sync/proto/listsyncv1"
	"google.golang.org/grpc"
)

type listSyncStub struct {
	listsyncv1.UnimplementedListSyncServiceServer
	sources []*listsyncv1.ListSource
}

func (s *listSyncStub) ListSources(context.Context, *listsyncv1.ListSourcesRequest) (*listsyncv1.ListSourcesResponse, error) {
	return &listsyncv1.ListSourcesResponse{Sources: s.sources}, nil
}

func (s *listSyncStub) AddSource(_ context.Context, req *listsyncv1.AddSourceRequest) (*listsyncv1.AddSourceResponse, error) {
	s.sources = append(s.sources, &listsyncv1.ListSource{
		Id:      "src_1",
		Name:    req.GetName(),
		Enabled: true,
	})
	return &listsyncv1.AddSourceResponse{Source: s.sources[len(s.sources)-1]}, nil
}

func (s *listSyncStub) UpdateSource(_ context.Context, req *listsyncv1.UpdateSourceRequest) (*listsyncv1.UpdateSourceResponse, error) {
	for i, src := range s.sources {
		if src.GetId() == req.GetId() {
			s.sources[i].Enabled = req.GetEnabled()
			return &listsyncv1.UpdateSourceResponse{Source: s.sources[i]}, nil
		}
	}
	return &listsyncv1.UpdateSourceResponse{}, nil
}

func (s *listSyncStub) RemoveSource(_ context.Context, req *listsyncv1.RemoveSourceRequest) (*listsyncv1.RemoveSourceResponse, error) {
	out := s.sources[:0]
	for _, src := range s.sources {
		if src.GetId() != req.GetId() {
			out = append(out, src)
		}
	}
	s.sources = out
	return &listsyncv1.RemoveSourceResponse{}, nil
}

func setupListSyncHandler(t *testing.T, stub *listSyncStub) *Handler {
	t.Helper()
	lsLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lsSrv := grpc.NewServer()
	listsyncv1.RegisterListSyncServiceServer(lsSrv, stub)
	go func() { _ = lsSrv.Serve(lsLis) }()
	t.Cleanup(func() {
		lsSrv.Stop()
		_ = lsLis.Close()
	})

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, capDiscovery{
		mods: map[string]*discoveryv1.ModuleInfoProto{
			capMediaListSync: {Id: "media-list-sync", HttpAddr: lsLis.Addr().String()},
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

func TestListSyncAddSource(t *testing.T) {
	stub := &listSyncStub{}
	h := setupListSyncHandler(t, stub)

	body := strings.NewReader("name=MyList&type=trakt&username=&client_id=&list_url=https://trakt&sync_interval_minutes=60")
	r := httptest.NewRequest(http.MethodPost, "/list-sync/sources", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ListSyncAddSource(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if len(stub.sources) != 1 || stub.sources[0].GetName() != "MyList" {
		t.Fatalf("sources=%+v", stub.sources)
	}
}

func TestListSyncToggleSource(t *testing.T) {
	stub := &listSyncStub{sources: []*listsyncv1.ListSource{{Id: "src_1", Name: "x", Enabled: true}}}
	h := setupListSyncHandler(t, stub)

	r := httptest.NewRequest(http.MethodPost, "/list-sync/sources/src_1/toggle", nil)
	r.SetPathValue("id", "src_1")
	w := httptest.NewRecorder()
	h.ListSyncToggleSource(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if stub.sources[0].GetEnabled() {
		t.Fatal("expected disabled after toggle")
	}
}

func TestListSyncRemoveSource(t *testing.T) {
	stub := &listSyncStub{sources: []*listsyncv1.ListSource{{Id: "src_1", Name: "x", Enabled: true}}}
	h := setupListSyncHandler(t, stub)

	r := httptest.NewRequest(http.MethodPost, "/list-sync/sources/src_1/delete", nil)
	r.SetPathValue("id", "src_1")
	w := httptest.NewRecorder()
	h.ListSyncRemoveSource(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if len(stub.sources) != 0 {
		t.Fatalf("expected empty sources, got %d", len(stub.sources))
	}
}
