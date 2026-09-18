package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	transcodev1 "github.com/Muxcore-Media/media-transcoder/proto/transcodev1"
	"google.golang.org/grpc"
)

type transcoderStub struct {
	transcodev1.UnimplementedTranscodeServiceServer
	profiles  []*transcodev1.TranscodeProfile
	jobs      []*transcodev1.TranscodeJob
	hardware  []*transcodev1.HardwareDevice
	cancelled []string
}

func (s *transcoderStub) ListProfiles(context.Context, *transcodev1.ListProfilesRequest) (*transcodev1.ListProfilesResponse, error) {
	return &transcodev1.ListProfilesResponse{Profiles: s.profiles}, nil
}

func (s *transcoderStub) ListJobs(context.Context, *transcodev1.ListJobsRequest) (*transcodev1.ListJobsResponse, error) {
	return &transcodev1.ListJobsResponse{Jobs: s.jobs, Total: int32(len(s.jobs)), Page: 1, PageSize: 50}, nil
}

func (s *transcoderStub) DetectHardware(context.Context, *transcodev1.DetectHardwareRequest) (*transcodev1.DetectHardwareResponse, error) {
	return &transcodev1.DetectHardwareResponse{Devices: s.hardware}, nil
}

func (s *transcoderStub) CancelJob(_ context.Context, req *transcodev1.CancelJobRequest) (*transcodev1.CancelJobResponse, error) {
	s.cancelled = append(s.cancelled, req.GetJobId())
	return &transcodev1.CancelJobResponse{}, nil
}

func (s *transcoderStub) ListSetups(context.Context, *transcodev1.ListSetupsRequest) (*transcodev1.ListSetupsResponse, error) {
	return &transcodev1.ListSetupsResponse{}, nil
}

func (s *transcoderStub) ListPipelineRuns(context.Context, *transcodev1.ListPipelineRunsRequest) (*transcodev1.ListPipelineRunsResponse, error) {
	return &transcodev1.ListPipelineRunsResponse{}, nil
}

func setupTranscoderHandler(t *testing.T, stub *transcoderStub) *Handler {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	transcodev1.RegisterTranscodeServiceServer(srv, stub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, capDiscovery{
		mods: map[string]*discoveryv1.ModuleInfoProto{
			capMediaTranscoderAdmin: {Id: "media-transcoder", HttpAddr: lis.Addr().String()},
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
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	h.Core = core
	return h
}

func TestTranscodePageSoftEmpty(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/transcode", nil)
	rec := httptest.NewRecorder()
	h.TranscodePage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="transcode-page"`) {
		t.Fatalf("expected transcode page, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="transcode-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
	if strings.Contains(body, `data-testid="transcode-jobs"`) {
		t.Fatal("soft-empty should hide jobs table")
	}
}

func TestTranscodePageRequiresAuthWhenConnected(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	r := mustRequest(http.MethodGet, "/transcode")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect to login, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Fatalf("expected Location /login, got %q", loc)
	}
}

func TestTranscodePageEmptyWhenModuleUp(t *testing.T) {
	h := setupTranscoderHandler(t, &transcoderStub{})
	req := httptest.NewRequest(http.MethodGet, "/transcode", nil)
	rec := httptest.NewRecorder()
	h.TranscodePage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `data-testid="transcode-soft-empty"`) {
		t.Fatal("did not expect soft-empty when module is registered")
	}
	if !strings.Contains(body, `data-testid="transcode-jobs-empty"`) {
		t.Fatalf("expected empty jobs note, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, `data-testid="transcode-hardware-empty"`) {
		t.Fatalf("expected empty hardware note, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, "No encode profiles.") {
		t.Fatalf("expected empty profiles copy, got: %s", truncate(body, 800))
	}
}

func TestTranscodePageShowsJobsProfilesHardware(t *testing.T) {
	stub := &transcoderStub{
		profiles: []*transcodev1.TranscodeProfile{{
			Id: "hevc_gpu", Name: "HEVC GPU", VideoCodec: "hevc", AudioCodec: "copy",
			Preset: "fast", UseGpu: true, Container: "mkv",
		}},
		jobs: []*transcodev1.TranscodeJob{
			{
				Id: "job_run", InputPath: "/data/media/show.mkv", ProfileName: "HEVC GPU",
				Status: "running", Progress: 0.42,
			},
			{
				Id: "job_done", InputPath: "/data/media/movie.mkv", ProfileName: "HEVC GPU",
				Status: "completed", Progress: 1,
			},
		},
		hardware: []*transcodev1.HardwareDevice{{
			Name: "NVIDIA", Type: "nvenc", Available: true, Encoder: "hevc_nvenc",
		}},
	}
	h := setupTranscoderHandler(t, stub)

	req := httptest.NewRequest(http.MethodGet, "/transcode", nil)
	rec := httptest.NewRecorder()
	h.TranscodePage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `data-testid="transcode-soft-empty"`) {
		t.Fatal("did not expect soft-empty when module is available")
	}
	if !strings.Contains(body, `data-testid="transcode-jobs"`) || !strings.Contains(body, "/data/media/show.mkv") {
		t.Fatalf("expected running job path, got: %s", truncate(body, 1200))
	}
	if !strings.Contains(body, "42%") {
		t.Fatalf("expected progress percent, got: %s", truncate(body, 1200))
	}
	if !strings.Contains(body, `/transcode/jobs/job_run/cancel`) {
		t.Fatalf("expected cancel action for running job, got: %s", truncate(body, 1200))
	}
	if strings.Contains(body, `/transcode/jobs/job_done/cancel`) {
		t.Fatal("completed job should not be cancellable")
	}
	if !strings.Contains(body, `data-testid="transcode-profiles"`) || !strings.Contains(body, "HEVC GPU") || !strings.Contains(body, "hevc") {
		t.Fatalf("expected profile row, got: %s", truncate(body, 1200))
	}
	if !strings.Contains(body, `data-testid="transcode-hardware"`) || !strings.Contains(body, "hevc_nvenc") {
		t.Fatalf("expected hardware row, got: %s", truncate(body, 1200))
	}
}

func TestTranscodeCancelJob(t *testing.T) {
	stub := &transcoderStub{
		jobs: []*transcodev1.TranscodeJob{{Id: "job_run", Status: "running"}},
	}
	h := setupTranscoderHandler(t, stub)

	r := httptest.NewRequest(http.MethodPost, "/transcode/jobs/job_run/cancel", nil)
	r.SetPathValue("id", "job_run")
	w := httptest.NewRecorder()
	h.TranscodeCancelJob(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "ok=cancelled") {
		t.Fatalf("unexpected redirect %q", loc)
	}
	if len(stub.cancelled) != 1 || stub.cancelled[0] != "job_run" {
		t.Fatalf("cancelled=%v", stub.cancelled)
	}
}

func TestTranscodeCancelRouteRegistered(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	r := mustRequest(http.MethodPost, "/transcode/jobs/job_run/cancel")
	_, pattern := mux.Handler(r)
	if pattern == "" {
		t.Fatal("expected POST /transcode/jobs/{id}/cancel to be registered")
	}
}

func TestTranscodeNavLabel(t *testing.T) {
	for _, l := range staticNavLinks {
		if l.Path == "/transcode" {
			if l.Label != "Transcoder" {
				t.Fatalf("nav label %q, want Transcoder", l.Label)
			}
			return
		}
	}
	t.Fatal("expected /transcode nav entry")
}

func TestTranscodeEditPageNew(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/transcode/edit", nil)
	rec := httptest.NewRecorder()
	h.TranscodeEditPage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="transcode-flow-builder"`) {
		t.Fatalf("expected flow builder, got: %s", truncate(body, 800))
	}
}
