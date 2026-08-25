// Package arrmigrate fetches Sonarr/Radarr library catalogs for MuxCore import.
// Production use is via admin-ui /migrate; tests use httptest fixtures only.
package arrmigrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Item is one movie, series, or artist row from an Arr API.
type Item struct {
	Source             string // "radarr", "sonarr", or "lidarr"
	ArrID              int
	Title              string
	Year               int
	TMDBID             int
	TVDBID             int
	MusicBrainzID      string
	Monitored          bool
	QualityProfileName string
	RootFolderPath     string
}

// Result summarizes a migrate run.
type Result struct {
	DryRun   bool
	Fetched  int
	Imported int
	Skipped  int
	Errors   []string
	Items    []Item // dry-run preview (capped) or all fetched
}

// MovieImporter adds a movie into media-movies.
type MovieImporter interface {
	ImportMovie(ctx context.Context, title string, year, tmdbID int, qualityProfileID, rootFolder string, monitored bool) (id string, err error)
}

// TVImporter adds a series into media-tvshows.
type TVImporter interface {
	ImportSeries(ctx context.Context, title string, year, tmdbID int, qualityProfileID, rootFolder string, monitored bool) (id string, err error)
}

// MusicImporter adds an artist into media-music.
type MusicImporter interface {
	ImportArtist(ctx context.Context, name, musicbrainzID, qualityProfileID, rootFolder string, monitored bool) (id string, err error)
}

// Client talks to Radarr/Sonarr HTTP APIs.
type Client struct {
	HTTP *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// FetchRadarr loads /api/v3/movie (+ quality profiles for names).
func (c *Client) FetchRadarr(ctx context.Context, baseURL, apiKey string) ([]Item, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("radarr base URL and API key are required")
	}
	profiles, err := c.fetchQualityProfiles(ctx, baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, baseURL+"/api/v3/movie", apiKey)
	if err != nil {
		return nil, err
	}
	return parseRadarrMovies(body, profiles)
}

// FetchSonarr loads /api/v3/series (+ quality profiles for names).
func (c *Client) FetchSonarr(ctx context.Context, baseURL, apiKey string) ([]Item, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("sonarr base URL and API key are required")
	}
	profiles, err := c.fetchQualityProfiles(ctx, baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, baseURL+"/api/v3/series", apiKey)
	if err != nil {
		return nil, err
	}
	return parseSonarrSeries(body, profiles)
}

// FetchLidarr loads /api/v1/artist (+ quality profiles for names).
func (c *Client) FetchLidarr(ctx context.Context, baseURL, apiKey string) ([]Item, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("lidarr base URL and API key are required")
	}
	profiles, err := c.fetchLidarrQualityProfiles(ctx, baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, baseURL+"/api/v1/artist", apiKey)
	if err != nil {
		return nil, err
	}
	return parseLidarrArtists(body, profiles)
}

func (c *Client) fetchLidarrQualityProfiles(ctx context.Context, baseURL, apiKey string) (map[int]string, error) {
	body, err := c.get(ctx, baseURL+"/api/v1/qualityprofile", apiKey)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("parse lidarr quality profiles: %w", err)
	}
	out := make(map[int]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

func parseLidarrArtists(body []byte, profiles map[int]string) ([]Item, error) {
	var artists []struct {
		ID               int    `json:"id"`
		ArtistName       string `json:"artistName"`
		ForeignArtistID  string `json:"foreignArtistId"`
		Monitored        bool   `json:"monitored"`
		QualityProfileID int    `json:"qualityProfileId"`
		RootFolderPath   string `json:"rootFolderPath"`
		Path             string `json:"path"`
	}
	if err := json.Unmarshal(body, &artists); err != nil {
		return nil, fmt.Errorf("parse lidarr artists: %w", err)
	}
	out := make([]Item, 0, len(artists))
	for _, ar := range artists {
		root := strings.TrimSpace(ar.RootFolderPath)
		if root == "" {
			root = parentDir(ar.Path)
		}
		out = append(out, Item{
			Source:             "lidarr",
			ArrID:              ar.ID,
			Title:              ar.ArtistName,
			MusicBrainzID:      ar.ForeignArtistID,
			Monitored:          ar.Monitored,
			QualityProfileName: profiles[ar.QualityProfileID],
			RootFolderPath:     root,
		})
	}
	return out, nil
}

func (c *Client) fetchQualityProfiles(ctx context.Context, baseURL, apiKey string) (map[int]string, error) {
	body, err := c.get(ctx, baseURL+"/api/v3/qualityprofile", apiKey)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("parse quality profiles: %w", err)
	}
	out := make(map[int]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, url, apiKey string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		snip := string(body)
		if len(snip) > 200 {
			snip = snip[:200]
		}
		return nil, fmt.Errorf("arr API %s: HTTP %d: %s", url, resp.StatusCode, snip)
	}
	return body, nil
}

func parseRadarrMovies(body []byte, profiles map[int]string) ([]Item, error) {
	var movies []struct {
		ID               int    `json:"id"`
		Title            string `json:"title"`
		Year             int    `json:"year"`
		TmdbID           int    `json:"tmdbId"`
		Monitored        bool   `json:"monitored"`
		QualityProfileID int    `json:"qualityProfileId"`
		RootFolderPath   string `json:"rootFolderPath"`
		Path             string `json:"path"`
	}
	if err := json.Unmarshal(body, &movies); err != nil {
		return nil, fmt.Errorf("parse radarr movies: %w", err)
	}
	out := make([]Item, 0, len(movies))
	for _, mv := range movies {
		root := strings.TrimSpace(mv.RootFolderPath)
		if root == "" {
			root = parentDir(mv.Path)
		}
		out = append(out, Item{
			Source:             "radarr",
			ArrID:              mv.ID,
			Title:              mv.Title,
			Year:               mv.Year,
			TMDBID:             mv.TmdbID,
			Monitored:          mv.Monitored,
			QualityProfileName: profiles[mv.QualityProfileID],
			RootFolderPath:     root,
		})
	}
	return out, nil
}

func parseSonarrSeries(body []byte, profiles map[int]string) ([]Item, error) {
	var series []struct {
		ID               int    `json:"id"`
		Title            string `json:"title"`
		Year             int    `json:"year"`
		TmdbID           int    `json:"tmdbId"`
		TvdbID           int    `json:"tvdbId"`
		Monitored        bool   `json:"monitored"`
		QualityProfileID int    `json:"qualityProfileId"`
		RootFolderPath   string `json:"rootFolderPath"`
		Path             string `json:"path"`
	}
	if err := json.Unmarshal(body, &series); err != nil {
		return nil, fmt.Errorf("parse sonarr series: %w", err)
	}
	out := make([]Item, 0, len(series))
	for _, s := range series {
		root := strings.TrimSpace(s.RootFolderPath)
		if root == "" {
			root = parentDir(s.Path)
		}
		out = append(out, Item{
			Source:             "sonarr",
			ArrID:              s.ID,
			Title:              s.Title,
			Year:               s.Year,
			TMDBID:             s.TmdbID,
			TVDBID:             s.TvdbID,
			Monitored:          s.Monitored,
			QualityProfileName: profiles[s.QualityProfileID],
			RootFolderPath:     root,
		})
	}
	return out, nil
}

func parentDir(path string) string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return ""
	}
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return ""
	}
	return path[:i]
}

// Run imports items into MuxCore libraries (or dry-runs).
func Run(ctx context.Context, items []Item, dryRun bool, movies MovieImporter, tv TVImporter, music MusicImporter, resolveProfile func(ctx context.Context, name string) string) Result {
	res := Result{DryRun: dryRun, Fetched: len(items), Items: items}
	if dryRun {
		return res
	}
	for _, it := range items {
		profileID := ""
		if resolveProfile != nil && it.QualityProfileName != "" {
			profileID = resolveProfile(ctx, it.QualityProfileName)
		}
		var err error
		switch it.Source {
		case "radarr":
			if it.TMDBID <= 0 {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("%s %q: missing tmdb id (tvdb=%d)", it.Source, it.Title, it.TVDBID))
				continue
			}
			if movies == nil {
				err = fmt.Errorf("movies module unavailable")
			} else {
				_, err = movies.ImportMovie(ctx, it.Title, it.Year, it.TMDBID, profileID, it.RootFolderPath, it.Monitored)
			}
		case "sonarr":
			if it.TMDBID <= 0 {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("%s %q: missing tmdb id (tvdb=%d)", it.Source, it.Title, it.TVDBID))
				continue
			}
			if tv == nil {
				err = fmt.Errorf("tvshows module unavailable")
			} else {
				_, err = tv.ImportSeries(ctx, it.Title, it.Year, it.TMDBID, profileID, it.RootFolderPath, it.Monitored)
			}
		case "lidarr":
			if strings.TrimSpace(it.MusicBrainzID) == "" {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("%s %q: missing musicbrainz id", it.Source, it.Title))
				continue
			}
			if music == nil {
				err = fmt.Errorf("music module unavailable")
			} else {
				_, err = music.ImportArtist(ctx, it.Title, it.MusicBrainzID, profileID, it.RootFolderPath, it.Monitored)
			}
		default:
			err = fmt.Errorf("unknown source %q", it.Source)
		}
		if err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, fmt.Sprintf("%s %q: %v", it.Source, it.Title, err))
			continue
		}
		res.Imported++
	}
	return res
}
