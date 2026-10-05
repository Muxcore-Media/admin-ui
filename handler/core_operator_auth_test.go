package handler

import (
	"context"
	"net"
	"net/http/httptest"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	spoolv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/spool/v1"
)

type mdSpoolServer struct {
	spoolv1.UnimplementedSpoolServiceServer
	mu sync.Mutex
	md []metadata.MD
}

func (s *mdSpoolServer) rec(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	s.md = append(s.md, md)
	s.mu.Unlock()
}
func (s *mdSpoolServer) ListSpools(ctx context.Context, _ *spoolv1.ListSpoolsRequest) (*spoolv1.ListSpoolsResponse, error) {
	s.rec(ctx)
	return &spoolv1.ListSpoolsResponse{}, nil
}
func (s *mdSpoolServer) ListTags(ctx context.Context, _ *spoolv1.ListTagsRequest) (*spoolv1.ListTagsResponse, error) {
	s.rec(ctx)
	return &spoolv1.ListTagsResponse{}, nil
}
func (s *mdSpoolServer) FetchTag(ctx context.Context, _ *spoolv1.FetchTagRequest) (*spoolv1.FetchTagResponse, error) {
	s.rec(ctx)
	return &spoolv1.FetchTagResponse{}, nil
}
func (s *mdSpoolServer) DeployTag(ctx context.Context, _ *spoolv1.DeployTagRequest) (*spoolv1.DeployTagResponse, error) {
	s.rec(ctx)
	return &spoolv1.DeployTagResponse{}, nil
}

func TestSpoolGRPCCarriesUserBearer(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &mdSpoolServer{}
	gs := grpc.NewServer()
	spoolv1.RegisterSpoolServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	defer gs.Stop()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	api := &grpcSpoolAPI{client: spoolv1.NewSpoolServiceClient(conn)}

	ctx := context.WithValue(context.Background(), ctxSessionKey, idSession())
	_, _ = api.ListSpools(ctx)
	_, _ = api.ListTags(ctx, "u")
	_, _ = api.FetchTag(ctx, "u", "t")
	_, _ = api.DeployTag(ctx, "u", "t")
	// without a session: no authorization metadata
	_, _ = api.ListSpools(context.Background())

	if len(srv.md) != 5 {
		t.Fatalf("got %d calls", len(srv.md))
	}
	for i := 0; i < 4; i++ {
		if got := srv.md[i].Get("authorization"); len(got) != 1 || got[0] != "Bearer al-secret" {
			t.Errorf("call %d authorization = %v", i, got)
		}
	}
	if got := srv.md[4].Get("authorization"); len(got) != 0 {
		t.Errorf("unexpected authorization without session: %v", got)
	}
}

func TestHTTPSpoolCarriesUserBearer(t *testing.T) {
	var seen []seenReq
	srv := fakeUpstream(t, &seen)
	api := &httpSpoolAPI{base: srv.URL}
	ctx := context.WithValue(context.Background(), ctxSessionKey, idSession())
	_, _ = api.ListSpools(ctx)
	_, _ = api.DeployTag(ctx, "u", "t")
	if len(seen) != 2 {
		t.Fatalf("got %d calls", len(seen))
	}
	for i, s := range seen {
		if got := s.hdr.Get("Authorization"); got != "Bearer al-secret" {
			t.Errorf("call %d Authorization = %q", i, got)
		}
	}
}

func TestAuditQueryCarriesUserBearer(t *testing.T) {
	srv := &recordingAuditServer{}
	core, cleanup := startRecordingAudit(t, srv)
	defer cleanup()
	h := testHandler(core)

	r := requestWithSession("GET", "/audit", idSession())
	h.AuditPage(httptest.NewRecorder(), r)
	h.AuditPage(httptest.NewRecorder(), mustRequest("GET", "/audit"))

	if len(srv.queryMD) != 2 {
		t.Fatalf("got %d queries", len(srv.queryMD))
	}
	if got := srv.queryMD[0].Get("authorization"); len(got) != 1 || got[0] != "Bearer al-secret" {
		t.Errorf("authorization = %v", got)
	}
	if got := srv.queryMD[1].Get("authorization"); len(got) != 0 {
		t.Errorf("unexpected authorization without session: %v", got)
	}
}
