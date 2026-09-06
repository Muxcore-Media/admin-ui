package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func TestWatchStatsPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/watchstats")
	w := httptest.NewRecorder()
	h.WatchStatsPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="watchstats-page"`) {
		t.Fatalf("expected watchstats page markup, got: %s", truncate(body, 400))
	}
	if !strings.Contains(body, `data-testid="watchstats-soft-empty"`) {
		t.Fatalf("expected soft-empty note when core is nil, got: %s", truncate(body, 600))
	}
}

func TestWatchStatsPageSoftEmptyHidesContentSections(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/watchstats")
	w := httptest.NewRecorder()
	h.WatchStatsPage(w, r)
	body := w.Body.String()
	for _, testID := range []string{"watchstats-top7", "watchstats-top30", "watchstats-users", "watchstats-recent"} {
		if strings.Contains(body, `data-testid="`+testID+`"`) {
			t.Errorf("section %q should not render in soft-empty state", testID)
		}
	}
}

func TestWatchStatsPageNavLinkPresent(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/watchstats")
	w := httptest.NewRecorder()
	h.WatchStatsPage(w, r)
	if !strings.Contains(w.Body.String(), `href="/watchstats"`) {
		t.Fatal("expected /watchstats nav link in rendered page")
	}
}

func TestWatchStatsPageTitle(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/watchstats")
	w := httptest.NewRecorder()
	h.WatchStatsPage(w, r)
	if !strings.Contains(w.Body.String(), "Watch Stats") {
		t.Fatal("expected 'Watch Stats' text in page")
	}
}

func TestWatchStatsPageDirectRenderWithData(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/watchstats")
	w := httptest.NewRecorder()

	data := templates.WatchStatsPageData{
		TopTitles7d: []templates.WatchStatsTitleRow{
			{Title: "Inception", MediaType: "movie", PlayCount: 3, WatchMinutes: "4.1 h"},
		},
		TopTitles30d: []templates.WatchStatsTitleRow{
			{Title: "Dune", MediaType: "movie", PlayCount: 12, WatchMinutes: "16.5 h"},
		},
		UserTotals: []templates.WatchStatsUserRow{
			{Username: "alice", PlayCount: 42, WatchMinutes: "60.2 h"},
		},
		RecentPlays: []templates.StreamSessionRow{
			{Title: "Succession S03E01", User: "bob", State: "stopped", Started: "2026-09-06 09:00"},
		},
	}
	h.renderWatchStats(w, r, data)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`data-testid="watchstats-top7"`,
		`data-testid="watchstats-top30"`,
		`data-testid="watchstats-users"`,
		`data-testid="watchstats-recent"`,
		"Inception",
		"Dune",
		"alice",
		"Succession S03E01",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in rendered output", want)
		}
	}
}
