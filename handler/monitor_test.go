package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

func TestMonitorSummaryUnconfigured(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/dashboard/monitor")
	w := httptest.NewRecorder()
	h.MonitorSummary(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="monitor-unconfigured"`) {
		t.Fatalf("expected unconfigured empty-state, got: %s", truncate(body, 400))
	}
}

func TestMonitorSummaryFixtureGolden(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":               "healthy",
			"module_count":         2,
			"stale_count":          0,
			"degraded_transitions": 0,
			"events_published":     3,
			"modules": []map[string]any{
				{"module_id": "api-rest", "state": "running", "stale": false},
				{"module_id": "media-movies", "state": "running", "stale": false},
			},
			"recent_events": []map[string]any{
				{"event_type": "module.healthy", "module_id": "api-rest", "message": "ok"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.HealthMonitorURL = srv.URL

	r := mustRequest("GET", "/dashboard/monitor")
	w := httptest.NewRecorder()
	h.MonitorSummary(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="monitor-summary"`) {
		t.Fatalf("expected monitor summary, got: %s", truncate(body, 500))
	}
	if !strings.Contains(body, "api-rest") || !strings.Contains(body, "media-movies") {
		t.Fatalf("expected fixture module ids, got: %s", truncate(body, 500))
	}
}

func TestMonitorSummaryEmptyModules(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":       "unknown",
			"module_count": 0,
			"modules":      []any{},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.HealthMonitorURL = srv.URL

	r := mustRequest("GET", "/dashboard/monitor")
	w := httptest.NewRecorder()
	h.MonitorSummary(w, r)
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="monitor-empty-modules"`) {
		t.Fatalf("expected empty modules state, got: %s", truncate(body, 400))
	}
}
