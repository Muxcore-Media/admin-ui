package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

func TestPlaybackTimeoutsBound(t *testing.T) {
	if playbackDialTimeout != 3*time.Second {
		t.Fatalf("playbackDialTimeout: got %v, want 3s", playbackDialTimeout)
	}
	if playbackReadTimeout != 5*time.Second {
		t.Fatalf("playbackReadTimeout: got %v, want 5s", playbackReadTimeout)
	}
	pageBudget := playbackDialTimeout + playbackReadTimeout + time.Second
	if playbackPageTimeout != pageBudget {
		t.Fatalf("playbackPageTimeout: got %v, want %v", playbackPageTimeout, pageBudget)
	}
	if playbackActionTimeout != pageBudget {
		t.Fatalf("playbackActionTimeout: got %v, want %v", playbackActionTimeout, pageBudget)
	}
}

type playbackBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (playbackBlockingDiscovery) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	switch req.GetCapability() {
	case capMediaTranscode, capTranscoder:
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestPlaybackAdminPageFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, playbackBlockingDiscovery{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	core, err := client.Dial(lis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/playback")
	w := httptest.NewRecorder()

	start := time.Now()
	h.PlaybackAdminPage(w, r)
	elapsed := time.Since(start)

	limit := playbackDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("playback page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < playbackDialTimeout-500*time.Millisecond {
		t.Fatalf("playback page returned too quickly (%v); expected dial timeout ~%v", elapsed, playbackDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 playback page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="playback-admin-page"`) {
		t.Fatalf("expected playback page markup, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, `data-testid="playback-transcoder-soft-empty"`) {
		t.Fatalf("expected soft-empty transcoder note on blocked discovery, got: %s", truncate(body, 400))
	}
}

func TestPlaybackAdminSaveFailsFastOnBlockedDiscovery(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_PLAYBACK_FILE", dir+"/playback.json")

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, playbackBlockingDiscovery{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	core, err := client.Dial(lis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	form := "enable_resume=1&ffmpeg_bin=/usr/bin/ffmpeg"
	r := httptest.NewRequest(http.MethodPost, "/playback", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()

	start := time.Now()
	h.PlaybackAdminSave(w, r)
	elapsed := time.Since(start)

	limit := playbackDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("playback save took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < playbackDialTimeout-500*time.Millisecond {
		t.Fatalf("playback save returned too quickly (%v); expected dial timeout ~%v", elapsed, playbackDialTimeout)
	}
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "saved=1") || !strings.Contains(loc, "notice=") {
		t.Fatalf("expected saved redirect with notice, got %s", loc)
	}
}
