package arrmigrate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchRadarrFixture(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/qualityprofile", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "name": "HD-1080p"},
		})
	})
	mux.HandleFunc("/api/v3/movie", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id": 10, "title": "Fight Club", "year": 1999, "tmdbId": 550,
				"monitored": true, "qualityProfileId": 1,
				"rootFolderPath": "/movies", "path": "/movies/Fight Club (1999)",
			},
			{
				"id": 11, "title": "Unmonitored", "year": 2000, "tmdbId": 1,
				"monitored": false, "qualityProfileId": 1, "path": "/movies/X",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{HTTP: srv.Client()}
	items, err := c.FetchRadarr(context.Background(), srv.URL, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items", len(items))
	}
	if items[0].Title != "Fight Club" || items[0].QualityProfileName != "HD-1080p" {
		t.Fatalf("unexpected first item: %+v", items[0])
	}
	if items[0].RootFolderPath != "/movies" {
		t.Fatalf("root: %q", items[0].RootFolderPath)
	}
	if items[1].RootFolderPath != "/movies" {
		t.Fatalf("parent root: %q", items[1].RootFolderPath)
	}
}

func TestFetchSonarrFixture(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/qualityprofile", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 2, "name": "Any"}})
	})
	mux.HandleFunc("/api/v3/series", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id": 7, "title": "Breaking Bad", "year": 2008, "tmdbId": 1396, "tvdbId": 81189,
				"monitored": true, "qualityProfileId": 2, "rootFolderPath": "/tv",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{HTTP: srv.Client()}
	items, err := c.FetchSonarr(context.Background(), srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].TVDBID != 81189 || items[0].Source != "sonarr" {
		t.Fatalf("%+v", items)
	}
}

type stubMovies struct {
	n int
}

func (s *stubMovies) ImportMovie(_ context.Context, _ string, _, _ int, _, _ string, _ bool) (string, error) {
	s.n++
	return "mv_1", nil
}

type stubTV struct {
	n int
}

func (s *stubTV) ImportSeries(_ context.Context, _ string, _, _ int, _, _ string, _ bool) (string, error) {
	s.n++
	return "tv_1", nil
}

func TestRunDryAndImport(t *testing.T) {
	items := []Item{
		{Source: "radarr", Title: "A", TMDBID: 1, Monitored: true},
		{Source: "sonarr", Title: "B", TMDBID: 2},
		{Source: "sonarr", Title: "NoTMDB", TVDBID: 9},
	}
	dry := Run(context.Background(), items, true, nil, nil, nil, nil)
	if dry.Fetched != 3 || dry.Imported != 0 {
		t.Fatalf("%+v", dry)
	}
	m, tv := &stubMovies{}, &stubTV{}
	res := Run(context.Background(), items, false, m, tv, nil, func(context.Context, string) string { return "qp1" })
	if res.Imported != 2 || res.Skipped != 1 || m.n != 1 || tv.n != 1 {
		t.Fatalf("imported=%d skipped=%d movies=%d tv=%d errors=%v", res.Imported, res.Skipped, m.n, tv.n, res.Errors)
	}
}

func TestFetchRequiresCredentials(t *testing.T) {
	c := &Client{}
	if _, err := c.FetchRadarr(context.Background(), "", ""); err == nil {
		t.Fatal("expected error")
	}
}
