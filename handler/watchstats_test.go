package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
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

func TestWatchStatsTitleRowsFromTopContentMergesBucketsByPlayCount(t *testing.T) {
	resp := &monitorv1.ListTopContentResponse{
		Movies: []*monitorv1.TopContentRow{
			{Title: "Inception", MediaType: "movie", PlayCount: 4, WatchMinutes: 246},
			{Title: "Dune", MediaType: "movie", PlayCount: 12, WatchMinutes: 990},
		},
		Shows: []*monitorv1.TopContentRow{
			{Title: "Succession", MediaType: "show", PlayCount: 8, WatchMinutes: 480},
		},
		Other: []*monitorv1.TopContentRow{
			{Title: "Concert", MediaType: "other", PlayCount: 2, WatchMinutes: 90},
		},
	}

	rows := watchStatsTitleRowsFromTopContent(resp, watchStatsTopLimit)
	if len(rows) != 4 {
		t.Fatalf("expected 4 merged rows, got %d", len(rows))
	}
	wantOrder := []struct {
		title     string
		mediaType string
		plays     int
	}{
		{"Dune", "movie", 12},
		{"Succession", "show", 8},
		{"Inception", "movie", 4},
		{"Concert", "other", 2},
	}
	for i, want := range wantOrder {
		if rows[i].Title != want.title || rows[i].MediaType != want.mediaType || rows[i].PlayCount != want.plays {
			t.Errorf("row %d: got %+v, want title=%q type=%q plays=%d", i, rows[i], want.title, want.mediaType, want.plays)
		}
		if rows[i].WatchMinutes == "" {
			t.Errorf("row %d (%s): empty WatchMinutes", i, rows[i].Title)
		}
	}
}

func TestWatchStatsTitleRowsFromTopContentAppliesLimit(t *testing.T) {
	resp := &monitorv1.ListTopContentResponse{
		Movies: []*monitorv1.TopContentRow{
			{Title: "A", PlayCount: 1},
			{Title: "B", PlayCount: 5},
		},
		Shows: []*monitorv1.TopContentRow{
			{Title: "C", PlayCount: 3},
		},
		Other: []*monitorv1.TopContentRow{
			{Title: "D", PlayCount: 9},
		},
	}
	rows := watchStatsTitleRowsFromTopContent(resp, 2)
	if len(rows) != 2 {
		t.Fatalf("expected limit 2, got %d rows", len(rows))
	}
	if rows[0].Title != "D" || rows[1].Title != "B" {
		t.Fatalf("expected D then B by play count, got %q then %q", rows[0].Title, rows[1].Title)
	}
}

func TestWatchStatsTitleRowsFromTopContentEmptyAndNil(t *testing.T) {
	if rows := watchStatsTitleRowsFromTopContent(nil, 10); rows != nil {
		t.Fatalf("nil response should yield nil, got %+v", rows)
	}
	if rows := watchStatsTitleRowsFromTopContent(&monitorv1.ListTopContentResponse{}, 10); rows != nil {
		t.Fatalf("empty buckets should yield nil, got %+v", rows)
	}
	rows := watchStatsTitleRowsFromTopContent(&monitorv1.ListTopContentResponse{
		Movies: []*monitorv1.TopContentRow{nil},
		Shows:  []*monitorv1.TopContentRow{{Title: "Only Show", PlayCount: 1}},
		Other:  nil,
	}, 10)
	if len(rows) != 1 || rows[0].Title != "Only Show" || rows[0].MediaType != "show" {
		t.Fatalf("expected nil movie skipped and show fallback type, got %+v", rows)
	}
}

func TestWatchStatsTitleRowsFromTopContentRendersInTopTables(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/watchstats")
	w := httptest.NewRecorder()

	mapped := watchStatsTitleRowsFromTopContent(&monitorv1.ListTopContentResponse{
		Movies: []*monitorv1.TopContentRow{{Title: "Blade Runner", MediaType: "movie", PlayCount: 6, WatchMinutes: 700}},
		Shows:  []*monitorv1.TopContentRow{{Title: "The Bear", MediaType: "show", PlayCount: 11, WatchMinutes: 330}},
		Other:  []*monitorv1.TopContentRow{{Title: "Standup Special", MediaType: "other", PlayCount: 3, WatchMinutes: 70}},
	}, watchStatsTopLimit)
	h.renderWatchStats(w, r, templates.WatchStatsPageData{
		TopTitles7d:  mapped,
		TopTitles30d: mapped,
	})
	body := w.Body.String()
	for _, want := range []string{
		`data-testid="watchstats-top7"`,
		`data-testid="watchstats-top30"`,
		"Blade Runner",
		"The Bear",
		"Standup Special",
		"movie",
		"show",
		"other",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q after movies/shows/other mapping", want)
		}
	}
}
