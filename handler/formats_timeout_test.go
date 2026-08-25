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

func TestFormatsTimeoutsBound(t *testing.T) {
	if formatsDialTimeout != 3*time.Second {
		t.Fatalf("formatsDialTimeout: got %v, want 3s", formatsDialTimeout)
	}
	if formatsReadTimeout != 5*time.Second {
		t.Fatalf("formatsReadTimeout: got %v, want 5s", formatsReadTimeout)
	}
	if formatsPageTimeout < formatsDialTimeout+formatsReadTimeout {
		t.Fatal("formats page budget must cover dial + at least one read")
	}
}

type formatsBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (formatsBlockingDiscovery) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() == capMediaFormats {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestFormatsListFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, formatsBlockingDiscovery{})
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

	r := mustRequest("GET", "/formats")
	w := httptest.NewRecorder()

	start := time.Now()
	h.FormatsList(w, r)
	elapsed := time.Since(start)

	limit := formatsDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("formats list took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < formatsDialTimeout-500*time.Millisecond {
		t.Fatalf("formats list returned too quickly (%v); expected dial timeout ~%v", elapsed, formatsDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 formats page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="formats-list-page"`) {
		t.Fatalf("expected formats page markup, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, "Formats module unavailable") {
		t.Fatalf("expected formats unavailable error, got: %s", truncate(body, 400))
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "timed out") &&
		!strings.Contains(lower, "context") &&
		!strings.Contains(lower, "deadline") &&
		!strings.Contains(lower, "cancel") {
		t.Fatalf("expected timeout/cancel error in page body, got: %s", truncate(body, 400))
	}
}

func TestProfilesListFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, formatsBlockingDiscovery{})
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

	r := mustRequest("GET", "/formats/profiles")
	w := httptest.NewRecorder()

	start := time.Now()
	h.ProfilesList(w, r)
	elapsed := time.Since(start)

	limit := formatsDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("profiles list took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < formatsDialTimeout-500*time.Millisecond {
		t.Fatalf("profiles list returned too quickly (%v); expected dial timeout ~%v", elapsed, formatsDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 profiles page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Quality Profiles") {
		t.Fatalf("expected profiles page markup, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, "Formats module unavailable") {
		t.Fatalf("expected formats unavailable error, got: %s", truncate(body, 400))
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "timed out") &&
		!strings.Contains(lower, "context") &&
		!strings.Contains(lower, "deadline") &&
		!strings.Contains(lower, "cancel") {
		t.Fatalf("expected timeout/cancel error in page body, got: %s", truncate(body, 400))
	}
}
