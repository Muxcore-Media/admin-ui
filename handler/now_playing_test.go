package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"

	"github.com/Muxcore-Media/admin-ui/session"
)

func TestNowPlayingPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/now-playing")
	w := httptest.NewRecorder()
	h.NowPlayingPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="now-playing-page"`) {
		t.Fatal("expected now playing page")
	}
	if !strings.Contains(body, `data-testid="now-playing-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="now-playing-empty"`) {
		t.Fatal("expected empty active streams message")
	}
}

func TestNowPlayingDashboardSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/dashboard/now-playing")
	w := httptest.NewRecorder()
	h.NowPlayingDashboard(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="dashboard-now-playing"`) {
		t.Fatal("expected dashboard now playing panel")
	}
	if !strings.Contains(body, `data-testid="now-playing-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 400))
	}
}

func TestSessionRowFromProtoAssembly(t *testing.T) {
	started := time.Date(2026, 3, 15, 14, 30, 0, 0, time.UTC).Unix()
	row := sessionRowFromProto(&monitorv1.SessionRecord{
		Id:              "sess-1",
		UserName:        "alice",
		Title:           "Inception",
		State:           monitorv1.SessionState_SESSION_STATE_PLAYING,
		PositionSeconds: 125,
		DurationSeconds: 3600,
		Platform:        "Android TV",
		Player:          "Jellyfin",
		IpAddress:       "192.168.1.10",
		StartedAtUnix:   started,
	})
	if row.ID != "sess-1" {
		t.Fatalf("id: got %q", row.ID)
	}
	if row.User != "alice" || row.Title != "Inception" {
		t.Fatalf("user/title: %+v", row)
	}
	if row.State != "playing" {
		t.Fatalf("state: got %q", row.State)
	}
	if row.Position != "2:05 / 1:00:00" {
		t.Fatalf("position: got %q", row.Position)
	}
	if row.Platform != "Android TV" || row.Player != "Jellyfin" {
		t.Fatalf("device: %+v", row)
	}
	if row.IP != "192.168.1.10" {
		t.Fatalf("ip: got %q", row.IP)
	}
	if row.Started != "2026-03-15 14:30" {
		t.Fatalf("started: got %q", row.Started)
	}
}

func TestSessionRowFromProtoFallsBackToUserID(t *testing.T) {
	row := sessionRowFromProto(&monitorv1.SessionRecord{
		UserId: "user-42",
		Title:  "Episode 1",
		State:  monitorv1.SessionState_SESSION_STATE_PAUSED,
	})
	if row.User != "user-42" {
		t.Fatalf("expected user id fallback, got %q", row.User)
	}
	if row.State != "paused" {
		t.Fatalf("state: got %q", row.State)
	}
}

func TestSessionRowFromProtoNil(t *testing.T) {
	row := sessionRowFromProto(nil)
	if row.User != "" || row.Title != "" {
		t.Fatalf("expected empty row, got %+v", row)
	}
}

func TestFetchActiveSessionsSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	rows, softNote, errMsg := h.fetchActiveSessions(context.Background(), 10)
	if !softNote {
		t.Fatal("expected soft note")
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows, got %d", len(rows))
	}
	if errMsg != "" {
		t.Fatalf("expected no error message without core, got %q", errMsg)
	}
}
