package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	"google.golang.org/grpc"
)

type storageDiscoveryStub struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	mods []*discoveryv1.ModuleInfoProto
}

func (d storageDiscoveryStub) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.GetCapability() != "storage" {
		return &discoveryv1.FindByCapabilityResponse{}, nil
	}
	return &discoveryv1.FindByCapabilityResponse{Modules: d.mods}, nil
}

type storageServiceStub struct {
	storagev1.UnimplementedStorageServiceServer
	caps []string
}

func (s storageServiceStub) Capabilities(context.Context, *storagev1.CapabilitiesRequest) (*storagev1.CapabilitiesResponse, error) {
	return &storagev1.CapabilitiesResponse{Capabilities: s.caps}, nil
}

type storageHealthStub struct {
	healthv1.UnimplementedHealthServiceServer
	load map[string]float64
}

func (s storageHealthStub) Check(_ context.Context, req *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	return &healthv1.HealthCheckResponse{
		ModuleId: req.GetModuleId(),
		Status:   healthv1.HealthCheckResponse_STATUS_HEALTHY,
		Load:     s.load,
	}, nil
}

func setupStorageHandler(t *testing.T, mods []*discoveryv1.ModuleInfoProto, healthLoad map[string]float64, caps []string) *Handler {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, storageDiscoveryStub{mods: mods})
	storagev1.RegisterStorageServiceServer(srv, storageServiceStub{caps: caps})
	healthv1.RegisterHealthServiceServer(srv, storageHealthStub{load: healthLoad})
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
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	h.Core = core
	return h
}

func TestStoragePageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest(http.MethodGet, "/storage")
	w := httptest.NewRecorder()
	h.StoragePage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="storage-page"`) {
		t.Fatal("expected storage page markup")
	}
	if !strings.Contains(body, `data-testid="storage-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}

func TestStoragePageSoftEmptyWithoutModule(t *testing.T) {
	h := setupStorageHandler(t, nil, nil, nil)
	r := mustRequest(http.MethodGet, "/storage")
	w := httptest.NewRecorder()
	h.StoragePage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="storage-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}

func TestStoragePageShowsProviderCapacity(t *testing.T) {
	const (
		used  = 750_000_000_000.0
		free  = 250_000_000_000.0
		total = 1_000_000_000_000.0
	)
	h := setupStorageHandler(t, []*discoveryv1.ModuleInfoProto{{
		Id:           "storage-local",
		Name:         "Local Storage",
		Capabilities: []string{"storage", "hardlinkable"},
	}}, map[string]float64{
		"used_bytes":  used,
		"free_bytes":  free,
		"total_bytes": total,
	}, []string{"hardlinkable", "streamable"})

	r := mustRequest(http.MethodGet, "/storage")
	w := httptest.NewRecorder()
	h.StoragePage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `data-testid="storage-soft-empty"`) {
		t.Fatal("did not expect soft-empty when module is available")
	}
	for _, want := range []string{
		"Local Storage",
		`data-testid="storage-provider-storage-local"`,
		`data-testid="storage-provider-capacity"`,
		"698.5 GiB",
		"232.8 GiB",
		"931.3 GiB",
		"hardlinkable",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in page, got: %s", want, truncate(body, 800))
		}
	}
}

func TestStoragePageFailsFastOnBlockedDiscovery(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, blockingDiscovery{})
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

	r := mustRequest(http.MethodGet, "/storage")
	w := httptest.NewRecorder()
	start := time.Now()
	h.StoragePage(w, r)
	elapsed := time.Since(start)

	limit := storageDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("StoragePage took %v, want ≤ %v", elapsed, limit)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="storage-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}

func TestStorageCapacityFromLoad(t *testing.T) {
	cap := storageCapacityFromLoad(map[string]float64{
		"used_bytes":  100,
		"free_bytes":  300,
		"total_bytes": 400,
	})
	if !cap.HasData || cap.UsedBytes != 100 || cap.FreeBytes != 300 || cap.TotalBytes != 400 {
		t.Fatalf("unexpected capacity: %+v", cap)
	}
}

func TestFormatStorageCapacityBytes(t *testing.T) {
	if got := formatStorageCapacityBytes(0); got != "—" {
		t.Fatalf("zero: got %q", got)
	}
	if got := formatStorageCapacityBytes(1536); got != "1.5 KiB" {
		t.Fatalf("1536: got %q", got)
	}
}
