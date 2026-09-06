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
	"github.com/Muxcore-Media/core/sdk/go/client"
	"google.golang.org/grpc"
)

type acquisitionHealthStub struct {
	healthv1.UnimplementedHealthServiceServer
	status map[string]healthv1.HealthCheckResponse_Status
	load   map[string]map[string]float64
}

func (s acquisitionHealthStub) Check(_ context.Context, req *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	status := healthv1.HealthCheckResponse_STATUS_HEALTHY
	if s.status != nil {
		if st, ok := s.status[req.GetModuleId()]; ok {
			status = st
		}
	}
	load := map[string]float64{}
	if s.load != nil {
		if l, ok := s.load[req.GetModuleId()]; ok {
			load = l
		}
	}
	return &healthv1.HealthCheckResponse{
		ModuleId: req.GetModuleId(),
		Status:   status,
		Load:     load,
	}, nil
}

func setupAcquisitionHealthHandler(t *testing.T, peers map[string][]*discoveryv1.ModuleInfoProto, health acquisitionHealthStub) *Handler {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(srv, multiCapDiscovery{byCap: peers})
	healthv1.RegisterHealthServiceServer(srv, health)
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

func TestAcquisitionHealthPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest(http.MethodGet, "/acquisition-health")
	w := httptest.NewRecorder()
	h.AcquisitionHealthPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="acquisition-health-page"`) {
		t.Fatal("expected acquisition health page")
	}
	if !strings.Contains(body, `data-testid="acquisition-health-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}

func TestAcquisitionHealthPageSoftEmptyWithoutModule(t *testing.T) {
	h := setupAcquisitionHealthHandler(t, map[string][]*discoveryv1.ModuleInfoProto{}, acquisitionHealthStub{})
	r := mustRequest(http.MethodGet, "/acquisition-health")
	w := httptest.NewRecorder()
	h.AcquisitionHealthPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="acquisition-health-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}

func TestAcquisitionHealthPageShowsPeers(t *testing.T) {
	h := setupAcquisitionHealthHandler(t, map[string][]*discoveryv1.ModuleInfoProto{
		capIndexer: {{
			Id:           "indexer-piratebay",
			Name:         "Pirate Bay",
			Capabilities: []string{"indexer", "indexer.torrent"},
		}},
		capDownloader: {{
			Id:           "downloader-native-torrent",
			Name:         "Native Torrent",
			Capabilities: []string{"downloader", "torrent"},
		}},
	}, acquisitionHealthStub{
		load: map[string]map[string]float64{
			"indexer-piratebay": {"last_ok_at": 1_700_000_000},
		},
	})

	r := mustRequest(http.MethodGet, "/acquisition-health")
	w := httptest.NewRecorder()
	h.AcquisitionHealthPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `data-testid="acquisition-health-soft-empty"`) {
		t.Fatal("did not expect soft-empty when peers are available")
	}
	for _, want := range []string{
		`data-testid="acquisition-health-cards"`,
		`data-testid="acquisition-indexers-list"`,
		`data-testid="acquisition-downloaders-list"`,
		`data-testid="acquisition-peer-indexer-piratebay"`,
		`data-testid="acquisition-peer-downloader-native-torrent"`,
		`data-testid="peer-health-ok"`,
		"Pirate Bay",
		"Native Torrent",
		`data-testid="acquisition-path-status"`,
		"ready",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in page, got: %s", want, truncate(body, 900))
		}
	}
}

func TestAcquisitionHealthPageShowsUnhealthyPeer(t *testing.T) {
	h := setupAcquisitionHealthHandler(t, map[string][]*discoveryv1.ModuleInfoProto{
		capIndexer: {{
			Id:          "indexer-broken",
			Name:        "Broken Indexer",
			HealthError: "connection refused",
		}},
	}, acquisitionHealthStub{})

	r := mustRequest(http.MethodGet, "/acquisition-health")
	w := httptest.NewRecorder()
	h.AcquisitionHealthPage(w, r)
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="peer-health-down"`) {
		t.Fatalf("expected unhealthy peer, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "connection refused") {
		t.Fatalf("expected health error detail, got: %s", truncate(body, 600))
	}
}

func TestAcquisitionHealthPageFailsFastOnBlockedDiscovery(t *testing.T) {
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

	r := mustRequest(http.MethodGet, "/acquisition-health")
	w := httptest.NewRecorder()
	start := time.Now()
	h.AcquisitionHealthPage(w, r)
	elapsed := time.Since(start)

	limit := acquisitionDialTimeout + acquisitionDialTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("AcquisitionHealthPage took %v, want ≤ %v", elapsed, limit)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="acquisition-health-page"`) {
		t.Fatal("expected acquisition health page markup")
	}
	if !strings.Contains(body, `data-testid="acquisition-health-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}

func TestAcquisitionHealthTimeoutsBound(t *testing.T) {
	if acquisitionDialTimeout != 3*time.Second {
		t.Fatalf("acquisitionDialTimeout: got %v, want 3s", acquisitionDialTimeout)
	}
	if acquisitionReadTimeout != 5*time.Second {
		t.Fatalf("acquisitionReadTimeout: got %v, want 5s", acquisitionReadTimeout)
	}
	pageBudget := acquisitionDialTimeout + 2*acquisitionDialTimeout + 2*acquisitionReadTimeout + time.Second
	if acquisitionPageTimeout != pageBudget {
		t.Fatalf("acquisitionPageTimeout: got %v, want %v", acquisitionPageTimeout, pageBudget)
	}
}
