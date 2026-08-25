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

func TestSubtitlesTimeoutsBound(t *testing.T) {
	if subtitlesDialTimeout != 3*time.Second {
		t.Fatalf("subtitlesDialTimeout: got %v, want 3s", subtitlesDialTimeout)
	}
	if subtitlesReadTimeout != 5*time.Second {
		t.Fatalf("subtitlesReadTimeout: got %v, want 5s", subtitlesReadTimeout)
	}
	if subtitlesPageTimeout < subtitlesDialTimeout+subtitlesReadTimeout {
		t.Fatal("subtitles page budget must cover dial + at least one read")
	}
	if subtitlesActionTimeout < subtitlesDialTimeout+subtitlesReadTimeout {
		t.Fatal("subtitles action budget must cover dial + read")
	}
	if subtitlesBatchTimeout < subtitlesDialTimeout {
		t.Fatal("subtitles batch budget must cover dial")
	}
}

type subtitlesBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (subtitlesBlockingDiscovery) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() == capMediaSubtitles {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestSubtitlesPageFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, subtitlesBlockingDiscovery{})
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

	r := mustRequest("GET", "/subtitles")
	w := httptest.NewRecorder()

	start := time.Now()
	h.SubtitlesPage(w, r)
	elapsed := time.Since(start)

	limit := subtitlesDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("subtitles page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < subtitlesDialTimeout-500*time.Millisecond {
		t.Fatalf("subtitles page returned too quickly (%v); expected dial timeout ~%v", elapsed, subtitlesDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 subtitles page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Subtitles") {
		t.Fatalf("expected subtitles page markup, got: %s", truncate(body, 400))
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "timed out") &&
		!strings.Contains(lower, "context") &&
		!strings.Contains(lower, "deadline") &&
		!strings.Contains(lower, "cancel") {
		t.Fatalf("expected timeout/cancel error in page body, got: %s", truncate(body, 400))
	}
}
