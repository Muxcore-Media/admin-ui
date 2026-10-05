package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/Muxcore-Media/admin-ui/session"
	auditv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/audit/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

type recordingAuditServer struct {
	auditv1.UnimplementedAuditServiceServer

	mu       sync.Mutex
	queries  []*auditv1.AuditQueryRequest
	logs     []*auditv1.LogRequest
	entries  []*auditv1.AuditEntryProto
	queryErr error
	queryMD  []metadata.MD
	logCh    chan struct{}
}

func (s *recordingAuditServer) Query(ctx context.Context, req *auditv1.AuditQueryRequest) (*auditv1.AuditQueryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	md, _ := metadata.FromIncomingContext(ctx)
	s.queryMD = append(s.queryMD, md)
	s.queries = append(s.queries, req)
	if s.queryErr != nil {
		return nil, s.queryErr
	}
	return &auditv1.AuditQueryResponse{Entries: s.entries, Total: int32(len(s.entries))}, nil
}

func (s *recordingAuditServer) Log(_ context.Context, req *auditv1.LogRequest) (*auditv1.LogResponse, error) {
	s.mu.Lock()
	s.logs = append(s.logs, req)
	ch := s.logCh
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return &auditv1.LogResponse{Id: "entry-1"}, nil
}

func startRecordingAudit(t *testing.T, srv *recordingAuditServer) (*client.Client, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	auditv1.RegisterAuditServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()

	c, err := client.Dial(lis.Addr().String(), client.WithInsecure())
	if err != nil {
		gs.Stop()
		_ = lis.Close()
		t.Fatalf("dial: %v", err)
	}
	cleanup := func() {
		_ = c.Close()
		gs.Stop()
		_ = lis.Close()
	}
	return c, cleanup
}

func testHandler(core *client.Client) *Handler {
	return &Handler{
		Core:          core,
		Sessions:      session.NewStore(0),
		coreConnected: core != nil,
		events:        newEventRing(100),
		version:       "test",
	}
}

func TestAuditPageUsesCoreQuery(t *testing.T) {
	srv := &recordingAuditServer{
		entries: []*auditv1.AuditEntryProto{
			{
				Id:         "a1",
				Timestamp:  time.Now().Unix(),
				Actor:      "user-1",
				Action:     "admin.login",
				Resource:   "session",
				ResourceId: "",
				TraceId:    "t1",
				NodeId:     "n1",
			},
		},
	}
	core, cleanup := startRecordingAudit(t, srv)
	defer cleanup()

	h := testHandler(core)
	r := mustRequest("GET", "/audit?actor=user-1&action=admin.login")
	w := httptest.NewRecorder()
	h.AuditPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "admin.login") {
		t.Fatalf("expected action in body, got %q", body)
	}
	if !strings.Contains(body, "user-1") {
		t.Fatalf("expected actor in body, got %q", body)
	}
	if strings.Contains(body, "Audit log unavailable") {
		t.Fatal("unexpected unavailable fallback")
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.queries) != 1 {
		t.Fatalf("expected 1 Query call, got %d", len(srv.queries))
	}
	q := srv.queries[0]
	if q.GetActor() != "user-1" || q.GetAction() != "admin.login" {
		t.Fatalf("unexpected query filters: actor=%q action=%q", q.GetActor(), q.GetAction())
	}
}

func TestAuditPageUnavailableWithoutClient(t *testing.T) {
	h := testHandler(nil)
	r := mustRequest("GET", "/audit")
	w := httptest.NewRecorder()
	h.AuditPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Audit log unavailable") {
		t.Fatalf("expected unavailable message, got %q", w.Body.String())
	}
}

func TestAuditPageUnavailableOnQueryError(t *testing.T) {
	srv := &recordingAuditServer{
		queryErr: status.Error(codes.Unavailable, "audit down"),
	}
	core, cleanup := startRecordingAudit(t, srv)
	defer cleanup()

	h := testHandler(core)
	r := mustRequest("GET", "/audit")
	w := httptest.NewRecorder()
	h.AuditPage(w, r)

	if !strings.Contains(w.Body.String(), "Audit log unavailable") {
		t.Fatalf("expected unavailable message, got %q", w.Body.String())
	}
}

func TestAuditLogWritesViaSDK(t *testing.T) {
	srv := &recordingAuditServer{logCh: make(chan struct{}, 1)}
	core, cleanup := startRecordingAudit(t, srv)
	defer cleanup()

	h := testHandler(core)
	h.auditLog(context.Background(), "actor-1", "admin.format.create", "format", "fmt-9", map[string]string{
		"name": "HDR",
	})

	select {
	case <-srv.logCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for audit Log")
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.logs) != 1 {
		t.Fatalf("expected 1 Log call, got %d", len(srv.logs))
	}
	got := srv.logs[0]
	if got.GetActor() != "actor-1" || got.GetAction() != "admin.format.create" || got.GetResource() != "format" || got.GetResourceId() != "fmt-9" {
		t.Fatalf("unexpected log entry: %+v", got)
	}
	if got.GetDetails()["name"] != "HDR" {
		t.Fatalf("expected details name=HDR, got %v", got.GetDetails())
	}
}

func TestAuditLogNoopWithoutCore(t *testing.T) {
	h := testHandler(nil)
	h.auditLog(context.Background(), "a", "admin.test", "r", "id", nil)
}

func TestAuditLogSurvivesRequestContextCancel(t *testing.T) {
	srv := &recordingAuditServer{logCh: make(chan struct{}, 1)}
	core, cleanup := startRecordingAudit(t, srv)
	defer cleanup()

	h := testHandler(core)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), "trace_id", "trace-42")) //nolint:staticcheck // matches production key
	h.auditLog(ctx, "actor-1", "admin.login", "session", "", nil)
	// Simulates net/http cancelling the request context once the handler returns.
	cancel()

	select {
	case <-srv.logCh:
	case <-time.After(3 * time.Second):
		t.Fatal("audit entry not delivered after request context cancel")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.logs) != 1 || srv.logs[0].GetAction() != "admin.login" {
		t.Fatalf("unexpected logs: %+v", srv.logs)
	}
	if srv.logs[0].GetTraceId() != "trace-42" {
		t.Fatalf("trace id not preserved: %q", srv.logs[0].GetTraceId())
	}
}
