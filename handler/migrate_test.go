package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

type migrateMoviesStub struct{ n int }

func (m *migrateMoviesStub) ImportMovie(_ context.Context, _ string, _, _ int, _, _ string, _ bool) (string, error) {
	m.n++
	return "mv_x", nil
}

func TestMigratePageRenders(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/migrate")
	w := httptest.NewRecorder()
	h.MigratePage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `data-testid="migrate-page"`) {
		t.Fatal("expected migrate page")
	}
}

func TestMigrateDryRunFixture(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/qualityprofile", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "name": "HD"}})
	})
	mux.HandleFunc("/api/v3/movie", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "title": "Fight Club", "year": 1999, "tmdbId": 550, "monitored": true, "qualityProfileId": 1, "rootFolderPath": "/movies"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	form := "service=radarr&base_url=" + srv.URL + "&api_key=k&dry_run=1"
	r := httptest.NewRequest(http.MethodPost, "/migrate", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.MigratePost(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="migrate-result"`) {
		t.Fatalf("expected result: %s", truncate(body, 500))
	}
	if !strings.Contains(body, "Fight Club") || !strings.Contains(body, "Dry run") {
		t.Fatalf("expected dry-run preview: %s", truncate(body, 600))
	}
}

func TestMigrateImportFixture(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/qualityprofile", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "name": "HD"}})
	})
	mux.HandleFunc("/api/v3/movie", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "title": "Fight Club", "year": 1999, "tmdbId": 550, "monitored": true, "qualityProfileId": 1, "rootFolderPath": "/movies"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	stub := &migrateMoviesStub{}
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.MigrateMovies = stub
	h.ResolveProfileID = func(context.Context, string) string { return "qp_hd" }

	form := "service=radarr&base_url=" + srv.URL + "&api_key=k"
	r := httptest.NewRequest(http.MethodPost, "/migrate", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.MigratePost(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if stub.n != 1 {
		t.Fatalf("expected 1 import, got %d", stub.n)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Imported 1") {
		t.Fatalf("expected import summary: %s", truncate(body, 500))
	}
	if !strings.Contains(body, "Library scan skipped") {
		t.Fatalf("expected scan skip when scanner is down: %s", truncate(body, 500))
	}
}

func TestMigrateImportTriggersLibraryScan(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/qualityprofile", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "name": "HD"}})
	})
	mux.HandleFunc("/api/v3/movie", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "title": "Fight Club", "year": 1999, "tmdbId": 550, "monitored": true, "qualityProfileId": 1, "rootFolderPath": "/movies"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	movies := &migrateMoviesStub{}
	scan := &scannerStub{}
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.MigrateMovies = movies
	h.MigrateScanner = scan
	h.ResolveProfileID = func(context.Context, string) string { return "qp_hd" }

	form := "service=radarr&base_url=" + srv.URL + "&api_key=k"
	r := httptest.NewRequest(http.MethodPost, "/migrate", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	h.MigratePost(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if movies.n != 1 || scan.rootsCalls != 1 {
		t.Fatalf("imports=%d scans=%d", movies.n, scan.rootsCalls)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="migrate-scan"`) || !strings.Contains(body, "found=1 imported=1") {
		t.Fatalf("expected scan note: %s", truncate(body, 600))
	}
}

func TestMigrateRouteAndNav(t *testing.T) {
	found := false
	for _, l := range staticNavLinks {
		if l.Path == "/migrate" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected Migrate nav link")
	}
	for _, l := range staticNavLinks {
		if l.Path == "/migrate" && l.Group != "Library" {
			t.Fatalf("expected Migrate in Library nav, got %q", l.Group)
		}
	}
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	r := mustRequest("GET", "/migrate")
	_, pattern := mux.Handler(r)
	if pattern == "" {
		t.Fatal("expected /migrate registered")
	}
}
