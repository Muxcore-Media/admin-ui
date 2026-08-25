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
)

type multiCapDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	byCap map[string][]*discoveryv1.ModuleInfoProto
}

func (d multiCapDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	return &discoveryv1.FindByCapabilityResponse{Modules: d.byCap[req.GetCapability()]}, nil
}

func TestAutomationQueuePageShowsAcquisitionPeers(t *testing.T) {
	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, multiCapDiscovery{
		byCap: map[string][]*discoveryv1.ModuleInfoProto{
			capIndexer: {{
				Id:   "indexer-piratebay",
				Name: "Pirate Bay",
			}},
			capDownloader: {{
				Id:   "downloader-native-torrent",
				Name: "Native Torrent",
			}},
			// no media.automation → soft error + peers still rendered
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

	r := mustRequest("GET", "/automation")
	w := httptest.NewRecorder()
	h.AutomationQueuePage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="acquisition-peers"`) {
		t.Fatal("expected acquisition peers section")
	}
	if !strings.Contains(body, `data-testid="indexers-list"`) || !strings.Contains(body, "indexer-piratebay") {
		t.Fatalf("expected indexer peer, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, `data-testid="downloaders-list"`) || !strings.Contains(body, "downloader-native-torrent") {
		t.Fatalf("expected downloader peer, got: %s", truncate(body, 800))
	}
}
