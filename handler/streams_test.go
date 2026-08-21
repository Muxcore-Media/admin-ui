package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
	"github.com/Muxcore-Media/admin-ui/session"
)

func TestStreamsPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/streams")
	w := httptest.NewRecorder()
	h.StreamsPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="streams-page"`) {
		t.Fatal("expected streams page")
	}
	if !strings.Contains(body, `data-testid="streams-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}

func TestStreamsHistoryPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/streams/history")
	w := httptest.NewRecorder()
	h.StreamsHistoryPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `data-testid="streams-history-page"`) {
		t.Fatal("expected history page")
	}
}

func TestStreamsStatsPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/streams/stats")
	w := httptest.NewRecorder()
	h.StreamsStatsPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `data-testid="streams-stats-page"`) {
		t.Fatal("expected stats page")
	}
}

func TestStreamsUsersPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/streams/users")
	w := httptest.NewRecorder()
	h.StreamsUsersPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `data-testid="streams-users-page"`) {
		t.Fatal("expected users page")
	}
}

func TestStreamsNotificationsPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/streams/notifications")
	w := httptest.NewRecorder()
	h.StreamsNotificationsPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `data-testid="streams-notifications-page"`) {
		t.Fatal("expected notifications page")
	}
}

func TestStreamsGuardPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/streams/guard")
	w := httptest.NewRecorder()
	h.StreamsGuardPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `data-testid="streams-guard-page"`) {
		t.Fatal("expected guard page")
	}
}

func TestStreamsServersPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/streams/servers")
	w := httptest.NewRecorder()
	h.StreamsServersPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `data-testid="streams-servers-page"`) {
		t.Fatal("expected servers page")
	}
}

func TestStreamsMapPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/streams/map")
	w := httptest.NewRecorder()
	h.StreamsMapPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `data-testid="streams-map-page"`) {
		t.Fatal("expected map page")
	}
}

func TestMapPinsFromSessions(t *testing.T) {
	pins := mapPinsFromSessions([]*monitorv1.SessionRecord{
		nil,
		{GeoLat: 0, GeoLon: 0},
		{GeoCountry: "Local Network", GeoLat: 10, GeoLon: 10},
		{Id: "s1", UserName: "alice", Title: "Movie", GeoCountry: "US", GeoCity: "NYC", GeoLat: 40.7, GeoLon: -74, IpAddress: "1.2.3.4"},
	})
	if len(pins) != 1 {
		t.Fatalf("expected 1 pin, got %d", len(pins))
	}
	if pins[0].User != "alice" || pins[0].Lat != 40.7 {
		t.Fatalf("unexpected pin: %+v", pins[0])
	}
}

func TestFormatDurationSeconds(t *testing.T) {
	if formatDurationSeconds(125) != "2:05" {
		t.Fatalf("got %q", formatDurationSeconds(125))
	}
	if formatDurationSeconds(3725) != "1:02:05" {
		t.Fatalf("got %q", formatDurationSeconds(3725))
	}
}
