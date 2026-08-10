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
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	jellyfinv1 "github.com/Muxcore-Media/jellyfin/proto/jellyfinv1"
)

func TestJellyfinPageSoftWhenCoreMissing(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/jellyfin")
	w := httptest.NewRecorder()
	h.JellyfinStatusPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft-unconfigured page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="jellyfin-page"`) {
		t.Fatal("expected jellyfin page markup")
	}
	if !strings.Contains(body, "core unavailable") {
		t.Fatalf("expected soft error about missing core, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, `data-testid="jellyfin-refresh-library"`) || !strings.Contains(body, `data-testid="jellyfin-sync"`) {
		t.Fatal("expected refresh/sync actions present even when unconfigured")
	}
}

type softJellyfinBridge struct {
	jellyfinv1.UnimplementedJellyfinBridgeServer
}

func (softJellyfinBridge) Status(context.Context, *jellyfinv1.StatusRequest) (*jellyfinv1.StatusResponse, error) {
	return &jellyfinv1.StatusResponse{
		Configured:   false,
		ConflictMode: "skip",
		ItemLinks:    0,
	}, nil
}

func (softJellyfinBridge) ListItemLinks(context.Context, *jellyfinv1.ListItemLinksRequest) (*jellyfinv1.ListItemLinksResponse, error) {
	return &jellyfinv1.ListItemLinksResponse{}, nil
}

func (softJellyfinBridge) SyncLibrary(context.Context, *jellyfinv1.SyncLibraryRequest) (*jellyfinv1.SyncLibraryResponse, error) {
	return &jellyfinv1.SyncLibraryResponse{Scanned: 0, Upserted: 0, Errors: []string{"jellyfin not configured"}}, nil
}

func (softJellyfinBridge) RefreshLibrary(context.Context, *jellyfinv1.RefreshLibraryRequest) (*jellyfinv1.RefreshLibraryResponse, error) {
	return &jellyfinv1.RefreshLibraryResponse{}, nil
}

type fixedDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	mod *discoveryv1.ModuleInfoProto
}

func (d fixedDiscovery) FindByCapability(_ context.Context, _ *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	return &discoveryv1.FindByCapabilityResponse{Modules: []*discoveryv1.ModuleInfoProto{d.mod}}, nil
}

func TestJellyfinPageSoftUnconfiguredStatus(t *testing.T) {
	jfLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	jfSrv := grpc.NewServer()
	jellyfinv1.RegisterJellyfinBridgeServer(jfSrv, softJellyfinBridge{})
	go jfSrv.Serve(jfLis)
	t.Cleanup(func() {
		jfSrv.Stop()
		_ = jfLis.Close()
	})

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, fixedDiscovery{
		mod: &discoveryv1.ModuleInfoProto{
			Id:       "jellyfin",
			Name:     "Jellyfin",
			HttpAddr: jfLis.Addr().String(),
		},
	})
	go discSrv.Serve(discLis)
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

	r := mustRequest("GET", "/jellyfin")
	w := httptest.NewRecorder()
	h.JellyfinStatusPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="jellyfin-page"`) {
		t.Fatal("expected jellyfin page markup")
	}
	if !strings.Contains(body, "Soft OK without live Jellyfin server") {
		t.Fatalf("expected soft-unconfigured note, got: %s", truncate(body, 500))
	}
	if !strings.Contains(body, `<span class="text-yellow-400">no</span>`) {
		t.Fatal("expected Configured=no for soft-unconfigured status")
	}
}

func TestJellyfinSyncSoftUnconfigured(t *testing.T) {
	jfLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	jfSrv := grpc.NewServer()
	jellyfinv1.RegisterJellyfinBridgeServer(jfSrv, softJellyfinBridge{})
	go jfSrv.Serve(jfLis)
	t.Cleanup(func() {
		jfSrv.Stop()
		_ = jfLis.Close()
	})

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, fixedDiscovery{
		mod: &discoveryv1.ModuleInfoProto{
			Id:       "jellyfin",
			HttpAddr: jfLis.Addr().String(),
		},
	})
	go discSrv.Serve(discLis)
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

	r := mustRequest("POST", "/jellyfin/sync")
	w := httptest.NewRecorder()
	h.JellyfinSync(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/jellyfin?synced=") {
		t.Fatalf("expected soft sync flash redirect, got %q", loc)
	}
}
