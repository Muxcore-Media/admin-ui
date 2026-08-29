package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

func TestPlaybackAdminPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/playback")
	w := httptest.NewRecorder()
	h.PlaybackAdminPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="playback-admin-page"`) {
		t.Fatal("expected playback page")
	}
	if !strings.Contains(body, `data-testid="playback-transcoder-soft-empty"`) {
		t.Fatalf("expected soft-empty transcoder note, got: %s", truncate(body, 600))
	}
}

func TestPlaybackAdminSaveWithoutTranscoder(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_PLAYBACK_FILE", dir+"/playback.json")

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	form := url.Values{}
	form.Set("enable_resume", "1")
	form.Set("prefer_direct", "1")
	form.Set("enable_transcode", "1")
	form.Set("ffmpeg_bin", "/usr/bin/ffmpeg")
	form.Set("max_bitrate", "40")
	r := httptest.NewRequest(http.MethodPost, "/playback", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.PlaybackAdminSave(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "saved=1") {
		t.Fatalf("expected saved redirect, got %s", loc)
	}
	if !strings.Contains(loc, "notice=") {
		t.Fatalf("expected soft-empty notice in redirect, got %s", loc)
	}
	p := loadPlayback()
	if p.FFmpegBin != "/usr/bin/ffmpeg" || p.MaxBitrateMbps != "40" {
		t.Fatalf("expected local save, got %+v", p)
	}
}
