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

func TestRenameTimeoutsBound(t *testing.T) {
	if renameDialTimeout != 3*time.Second {
		t.Fatalf("renameDialTimeout: got %v, want 3s", renameDialTimeout)
	}
	if renameReadTimeout != 5*time.Second {
		t.Fatalf("renameReadTimeout: got %v, want 5s", renameReadTimeout)
	}
	if renamePageTimeout < renameDialTimeout+renameReadTimeout {
		t.Fatal("rename page budget must cover dial + at least one read")
	}
	if renameBatchTimeout < renameDialTimeout {
		t.Fatal("rename batch budget must cover dial")
	}
}

// renameBlockingDiscovery blocks FindByCapability for renamer until ctx cancel.
type renameBlockingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
}

func (renameBlockingDiscovery) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() == capMediaRenamer {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func TestNamingTemplatesListFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, renameBlockingDiscovery{})
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

	r := mustRequest("GET", "/rename/templates")
	w := httptest.NewRecorder()

	start := time.Now()
	h.NamingTemplatesList(w, r)
	elapsed := time.Since(start)

	limit := renameDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("naming templates list took %v, want ≤ %v (deadline fail-fast)", elapsed, limit)
	}
	if elapsed < renameDialTimeout-500*time.Millisecond {
		t.Fatalf("naming templates list returned too quickly (%v); expected dial timeout ~%v", elapsed, renameDialTimeout)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 naming templates page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Naming Templates") {
		t.Fatalf("expected naming templates page markup, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, "Rename module unavailable") {
		t.Fatalf("expected rename unavailable error, got: %s", truncate(body, 400))
	}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "timed out") &&
		!strings.Contains(lower, "context") &&
		!strings.Contains(lower, "deadline") &&
		!strings.Contains(lower, "cancel") {
		t.Fatalf("expected timeout/cancel error in page body, got: %s", truncate(body, 400))
	}
}
