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

func TestTranscodeTimeoutsBound(t *testing.T) {
	if transcodeDialTimeout != 3*time.Second {
		t.Fatalf("transcodeDialTimeout: got %v, want 3s", transcodeDialTimeout)
	}
	if transcodeReadTimeout != 5*time.Second {
		t.Fatalf("transcodeReadTimeout: got %v, want 5s", transcodeReadTimeout)
	}
	if transcodePageTimeout < transcodeDialTimeout+transcodeReadTimeout {
		t.Fatal("transcode page budget must cover dial + at least one read")
	}
	if transcodeEditPageTimeout < transcodeDialTimeout+transcodeReadTimeout {
		t.Fatal("transcode edit page budget must cover dial + read")
	}
}

// transcodeBlockingDiscovery blocks FindByCapability for transcoder until ctx cancel.
type transcodeBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (transcodeBlockingDiscovery) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	switch req.GetCapability() {
	case capMediaTranscoderAdmin, capTranscoder:
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestTranscodePageFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, transcodeBlockingDiscovery{})
	go srv.Serve(lis)
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

	r := mustRequest("GET", "/transcode")
	w := httptest.NewRecorder()

	start := time.Now()
	h.TranscodePage(w, r)
	elapsed := time.Since(start)

	limit := transcodeDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("transcode page took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < transcodeDialTimeout-500*time.Millisecond {
		t.Fatalf("transcode page returned too quickly (%v); expected dial timeout ~%v", elapsed, transcodeDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 transcode page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="transcode-page"`) {
		t.Fatalf("expected transcode page markup, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, `data-testid="transcode-soft-empty"`) {
		t.Fatalf("expected soft-empty state on blocked discovery, got: %s", truncate(body, 400))
	}
}
