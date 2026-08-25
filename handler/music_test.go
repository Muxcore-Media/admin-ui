package handler

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

func TestMusicPageSoftWhenCoreMissing(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/music")
	w := httptest.NewRecorder()
	h.MusicListPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft-empty page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="music-page"`) {
		t.Fatal("expected music page markup")
	}
	if !strings.Contains(body, `data-testid="music-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, "core unavailable") {
		t.Fatalf("expected soft error about missing core, got: %s", truncate(body, 400))
	}
}

type musicMeshStub struct {
	meshv1.UnimplementedModuleMeshServer
	artists []map[string]any
}

func (s musicMeshStub) Call(_ context.Context, req *meshv1.CallRequest) (*meshv1.CallResponse, error) {
	switch req.GetMethod() {
	case "ListArtists":
		raw, _ := json.Marshal(s.artists)
		return &meshv1.CallResponse{Payload: raw}, nil
	case "GetArtist":
		var body struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(req.GetPayload(), &body)
		for _, a := range s.artists {
			if a["id"] == body.ID {
				raw, _ := json.Marshal(map[string]any{
					"artist": a,
					"albums": []map[string]any{
						{"id": "al_1", "artist_id": body.ID, "title": "Fixture Album", "year": 2024, "monitored": true},
					},
				})
				return &meshv1.CallResponse{Payload: raw}, nil
			}
		}
		return &meshv1.CallResponse{Error: "not found"}, nil
	default:
		return &meshv1.CallResponse{Error: "unknown method"}, nil
	}
}

func TestMusicPageListsViaMesh(t *testing.T) {
	musicLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	musicSrv := grpc.NewServer()
	meshv1.RegisterModuleMeshServer(musicSrv, musicMeshStub{
		artists: []map[string]any{
			{"id": "ar_fix", "name": "Fixture Artist", "monitored": true, "path": "/lib/Fixture Artist"},
		},
	})
	go func() { _ = musicSrv.Serve(musicLis) }()
	t.Cleanup(func() {
		musicSrv.Stop()
		_ = musicLis.Close()
	})

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, fixedDiscovery{
		mod: &discoveryv1.ModuleInfoProto{
			Id:       "media-music",
			Name:     "Music Manager",
			HttpAddr: musicLis.Addr().String(),
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
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/music")
	w := httptest.NewRecorder()
	h.MusicListPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="music-artist-table"`) {
		t.Fatalf("expected artist table, got: %s", truncate(body, 500))
	}
	if !strings.Contains(body, "Fixture Artist") {
		t.Fatalf("expected fixture artist name, got: %s", truncate(body, 500))
	}

	r2 := mustRequest("GET", "/music/ar_fix")
	r2.SetPathValue("id", "ar_fix")
	w2 := httptest.NewRecorder()
	h.MusicDetailPage(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 detail, got %d", w2.Code)
	}
	detail := w2.Body.String()
	if !strings.Contains(detail, `data-testid="music-detail-page"`) {
		t.Fatal("expected detail page")
	}
	if !strings.Contains(detail, "Fixture Album") {
		t.Fatalf("expected album on detail, got: %s", truncate(detail, 500))
	}
}

func TestMusicHTTPBaseFromGRPC(t *testing.T) {
	if got := musicHTTPBaseFromGRPC("127.0.0.1:9640"); got != "http://127.0.0.1:9641" {
		t.Fatalf("got %q", got)
	}
}

func TestMusicFetchArtistsHTTPFallback(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != musicHTTPPathList {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]musicArtistJSON{{ID: "http_a", Name: "HTTP Artist", Monitored: true}})
	}))
	defer httpSrv.Close()

	host, portStr, err := net.SplitHostPort(httpSrv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	grpcAddr := net.JoinHostPort(host, strconv.Itoa(port-1))

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	rows, err := h.musicFetchArtists(context.Background(), "media-music", grpcAddr, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "HTTP Artist" {
		t.Fatalf("rows=%v", rows)
	}
}
