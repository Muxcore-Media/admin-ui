package handler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

// An unguarded regression must fail locally, never reach a real metadata host.
type rejectUnguardedArrTransport struct{ calls int }

func (r *rejectUnguardedArrTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return nil, errors.New("unguarded Arr transport used")
}

func TestMigrateBlockedDestinationHasNoSideEffects(t *testing.T) {
	trap := &rejectUnguardedArrTransport{}
	original := http.DefaultTransport
	http.DefaultTransport = trap
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, service := range []string{"radarr", "sonarr", "lidarr"} {
		t.Run(service, func(t *testing.T) {
			h := New(nil, session.NewStore(0), false, "test", nil, false, "", nil, nil)
			movies, scan := &migrateMoviesStub{}, &scannerStub{}
			h.MigrateMovies, h.MigrateScanner = movies, scan
			form := url.Values{"service": {service}, "base_url": {"http://169.254.169.254"}, "api_key": {"fixture-key"}}
			r := httptest.NewRequest(http.MethodPost, "/migrate", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			h.MigratePost(w, r)
			body := w.Body.String()
			if !strings.Contains(body, `data-testid="migrate-error"`) || !strings.Contains(body, "netguard: blocked") {
				t.Fatalf("expected destination policy error: %s", truncate(body, 600))
			}
			if strings.Contains(body, `data-testid="migrate-result"`) || movies.n != 0 || scan.rootsCalls != 0 {
				t.Fatalf("blocked fetch produced result or side effects: imports=%d scans=%d", movies.n, scan.rootsCalls)
			}
		})
	}
	if trap.calls != 0 {
		t.Errorf("unguarded transport attempted %d requests", trap.calls)
	}
}

func TestMigrateRedirectHasNoSideEffects(t *testing.T) {
	for _, phase := range []string{"qualityprofile", "movie"} {
		t.Run(phase, func(t *testing.T) {
			var redirected atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				redirected.Add(1)
				_, _ = io.WriteString(w, `[]`)
			}))
			defer destination.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "fixture-key" {
					t.Error("missing Arr API key at configured origin")
				}
				if r.URL.Path == "/api/v3/"+phase {
					http.Redirect(w, r, destination.URL, http.StatusFound)
					return
				}
				_, _ = io.WriteString(w, `[]`)
			}))
			defer origin.Close()
			h := New(nil, session.NewStore(0), false, "test", nil, false, "", nil, nil)
			movies, scan := &migrateMoviesStub{}, &scannerStub{}
			h.MigrateMovies, h.MigrateScanner = movies, scan
			form := url.Values{"service": {"radarr"}, "base_url": {origin.URL}, "api_key": {"fixture-key"}}
			r := httptest.NewRequest(http.MethodPost, "/migrate", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			h.MigratePost(w, r)
			body := w.Body.String()
			if !strings.Contains(body, `data-testid="migrate-error"`) || !strings.Contains(body, "HTTP 302") {
				t.Errorf("expected redirect error: %s", truncate(body, 600))
			}
			if strings.Contains(body, `data-testid="migrate-result"`) || movies.n != 0 || scan.rootsCalls != 0 {
				t.Errorf("redirected fetch produced result or side effects: imports=%d scans=%d", movies.n, scan.rootsCalls)
			}
			if redirected.Load() != 0 {
				t.Errorf("redirect destination received %d requests", redirected.Load())
			}
		})
	}
}
