package arrmigrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Muxcore-Media/core/sdk/go/module/netguard"
)

func TestDefaultClientArrIntegrations(t *testing.T) {
	client := &Client{}
	for _, tc := range []struct {
		name, catalog, payload string
		fetch                  func(context.Context, string, string) ([]Item, error)
	}{
		{"radarr", "/api/v3/movie", `[{"id":1,"title":"Fixture","tmdbId":550,"qualityProfileId":1}]`, client.FetchRadarr},
		{"sonarr", "/api/v3/series", `[{"id":1,"title":"Fixture","tmdbId":1396,"tvdbId":81189,"qualityProfileId":1}]`, client.FetchSonarr},
		{"lidarr", "/api/v1/artist", `[{"id":1,"artistName":"Fixture","foreignArtistId":"fixture-mbid","qualityProfileId":1}]`, client.FetchLidarr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const prefix = "/reverse-proxy/arr"
			profile := tc.catalog[:strings.LastIndex(tc.catalog, "/")] + "/qualityprofile"
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.Header.Get("X-Api-Key") != "fixture-key" || r.Header.Get("Accept") != "application/json" {
					t.Error("missing Arr request headers")
				}
				switch r.URL.Path {
				case prefix + profile:
					_, _ = io.WriteString(w, `[{"id":1,"name":"Fixture profile"}]`)
				case prefix + tc.catalog:
					_, _ = io.WriteString(w, tc.payload)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			items, err := tc.fetch(context.Background(), server.URL+prefix+"/", "fixture-key")
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].Title != "Fixture" || items[0].Source != tc.name || items[0].QualityProfileName != "Fixture profile" {
				t.Fatalf("unexpected catalog: %+v", items)
			}
			if want := []string{prefix + profile, prefix + tc.catalog}; !reflect.DeepEqual(paths, want) {
				t.Fatalf("paths=%v, want %v", paths, want)
			}
		})
	}
}

func TestDefaultClientBlocksUnsafeDestinations(t *testing.T) {
	for _, target := range []string{
		"http://169.254.169.254/", "http://[::ffff:169.254.169.254]/",
		"http://100.100.100.200/", "http://[fd00:ec2::254]/",
		"http://metadata.google.internal/", "http://[fe80::1]/",
		"http://0.0.0.0/", "http://224.0.0.1/", "file:///etc/passwd",
	} {
		t.Run(target, func(t *testing.T) {
			// Validation happens before dialing. Cancellation also prevents an
			// unguarded regression from contacting a real destination.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := (&Client{}).get(ctx, target, "fixture-key")
			if !errors.Is(err, netguard.ErrBlocked) {
				t.Fatalf("want blocked destination, got %v", err)
			}
		})
	}
}

func TestDefaultClientRejectsArrRedirects(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, phase := range []string{"qualityprofile", "movie"} {
			for _, sameOrigin := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%s/same-origin-%t", code, phase, sameOrigin), func(t *testing.T) {
					var redirected atomic.Int32
					sink := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						redirected.Add(1)
						_, _ = io.WriteString(w, `[]`)
					})
					destination := httptest.NewServer(sink)
					defer destination.Close()
					origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/redirected" {
							sink.ServeHTTP(w, r)
							return
						}
						if r.Header.Get("X-Api-Key") != "fixture-key" {
							t.Error("origin did not receive the Arr API key")
						}
						if r.URL.Path == "/api/v3/"+phase {
							location := destination.URL
							if sameOrigin {
								location = "/redirected"
							}
							http.Redirect(w, r, location, code)
							return
						}
						_, _ = io.WriteString(w, `[]`)
					}))
					defer origin.Close()
					_, err := (&Client{}).FetchRadarr(context.Background(), origin.URL, "fixture-key")
					if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", code)) {
						t.Errorf("want rejected redirect status, got %v", err)
					}
					if redirected.Load() != 0 {
						t.Errorf("redirect target received %d requests", redirected.Load())
					}
				})
			}
		}
	}
}

func TestDefaultClientVerifiesArrTLS(t *testing.T) {
	var received atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		_, _ = io.WriteString(w, `[]`)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	_, err := (&Client{}).FetchRadarr(context.Background(), server.URL, "fixture-key")
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("want untrusted certificate rejection, got %v", err)
	}
	if received.Load() != 0 {
		t.Fatalf("untrusted TLS server received %d requests", received.Load())
	}
}
