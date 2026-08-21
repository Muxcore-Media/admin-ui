package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

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
