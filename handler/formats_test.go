package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/session"
	formatsv1 "github.com/Muxcore-Media/media-custom-formats/proto/formatsv1"
)

type stubFormatsClient struct {
	listResp *formatsv1.ListFormatsResponse
	syncResp *formatsv1.SyncTrashGuidesResponse
	syncErr  error
	listErr  error
}

func (s *stubFormatsClient) ListFormats(context.Context, *formatsv1.ListFormatsRequest, ...grpc.CallOption) (*formatsv1.ListFormatsResponse, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.listResp != nil {
		return s.listResp, nil
	}
	return &formatsv1.ListFormatsResponse{}, nil
}

func (s *stubFormatsClient) SyncTrashGuides(context.Context, *formatsv1.SyncTrashGuidesRequest, ...grpc.CallOption) (*formatsv1.SyncTrashGuidesResponse, error) {
	if s.syncErr != nil {
		return nil, s.syncErr
	}
	if s.syncResp != nil {
		return s.syncResp, nil
	}
	return &formatsv1.SyncTrashGuidesResponse{}, nil
}

// formatsClientStub satisfies FormatServiceClient for the methods under test.
type formatsClientStub struct {
	formatsv1.FormatServiceClient
	inner *stubFormatsClient
}

func (f formatsClientStub) ListFormats(ctx context.Context, in *formatsv1.ListFormatsRequest, opts ...grpc.CallOption) (*formatsv1.ListFormatsResponse, error) {
	return f.inner.ListFormats(ctx, in, opts...)
}

func (f formatsClientStub) SyncTrashGuides(ctx context.Context, in *formatsv1.SyncTrashGuidesRequest, opts ...grpc.CallOption) (*formatsv1.SyncTrashGuidesResponse, error) {
	return f.inner.SyncTrashGuides(ctx, in, opts...)
}

func TestFormatsListUnavailable(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/formats")
	w := httptest.NewRecorder()
	h.FormatsList(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="formats-list-page"`) {
		t.Fatal("expected formats page")
	}
	if !strings.Contains(body, "Formats module unavailable") {
		t.Fatalf("expected unavailable message, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, `data-testid="formats-sync-trash-form"`) {
		t.Fatal("expected sync form even when unavailable")
	}
}

func TestFormatsSyncTrashUnavailable(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := httptest.NewRequest(http.MethodPost, "/formats/sync-trash", strings.NewReader("score_set=default&service_radarr=1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.FormatsSyncTrash(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="formats-sync-error"`) && !strings.Contains(body, "unavailable") {
		t.Fatalf("expected sync error, got: %s", truncate(body, 500))
	}
}

func TestFormatsSyncTrashAvailable(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.FormatsClient = formatsClientStub{inner: &stubFormatsClient{
		listResp: &formatsv1.ListFormatsResponse{
			Formats: []*formatsv1.CustomFormat{{Id: "cf1", Name: "Remux", DefaultScore: 100}},
		},
		syncResp: &formatsv1.SyncTrashGuidesResponse{
			FormatsUpserted:  12,
			FormatsSkipped:   3,
			ProfilesUpserted: 2,
			GuidesPath:       "/tmp/guides",
		},
	}}
	r := httptest.NewRequest(http.MethodPost, "/formats/sync-trash", strings.NewReader("score_set=default&service_radarr=1&service_sonarr=1&import_profiles=1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.FormatsSyncTrash(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="formats-sync-result"`) {
		t.Fatalf("expected sync result, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "imported/updated 12") || !strings.Contains(body, "skipped 3") {
		t.Fatalf("expected counts in body: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "Remux") {
		t.Fatal("expected listed format after sync")
	}
}

func TestFormatsSyncTrashRouteRegistered(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	r := mustRequest("POST", "/formats/sync-trash")
	_, pattern := mux.Handler(r)
	if pattern == "" {
		t.Fatal("expected /formats/sync-trash registered")
	}
}
