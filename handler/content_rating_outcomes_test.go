package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
)

func TestContentRatingAuditsDispatchedOutcome(t *testing.T) {
	for _, module := range []string{"media-movies", "media-tvshows"} {
		for _, tc := range []struct {
			name, outcome, reason string
			fixture               *ratingFixture
		}{
			{"confirmed", "confirmed", "readback_matched", &ratingFixture{}},
			{"refused", "refused", "PermissionDenied", &ratingFixture{writeErr: codes.PermissionDenied}},
			{"unauthenticated", "refused", "Unauthenticated", &ratingFixture{writeErr: codes.Unauthenticated}},
			{"missing", "refused", "NotFound", &ratingFixture{writeErr: codes.NotFound}},
			{"invalid", "refused", "InvalidArgument", &ratingFixture{writeErr: codes.InvalidArgument}},
			{"unsupported", "refused", "Unimplemented", &ratingFixture{writeErr: codes.Unimplemented}},
			{"timeout", "uncertain", "DeadlineExceeded", &ratingFixture{writeErr: codes.DeadlineExceeded}},
			{"unavailable", "uncertain", "Unavailable", &ratingFixture{writeErr: codes.Unavailable}},
			{"internal", "uncertain", "Internal", &ratingFixture{writeErr: codes.Internal}},
			{"mismatch", "uncertain", "readback_mismatch", &ratingFixture{ignoreWrite: true}},
			{"readback_error", "uncertain", "readback_unavailable", &ratingFixture{readErr: codes.Unavailable}},
		} {
			t.Run(module+"/"+tc.name, func(t *testing.T) {
				h := newRatingHandler(t, tc.fixture)
				var audits []map[string]string
				h.AuditHook = func(actor, action, resource, resourceID string, details map[string]string) {
					if actor != "operator" || action != "admin.media.content_rating" || resource != "media_item" || resourceID != "fixture" {
						t.Errorf("unexpected audit identity: %q %q %q %q", actor, action, resource, resourceID)
					}
					audits = append(audits, details)
				}
				w := httptest.NewRecorder()
				h.ContentRatingSave(w, ratingRequest("POST", module, "classification=PG", []string{"admin"}, "bearer"))
				if len(audits) != 1 {
					t.Fatalf("dispatched writes need one outcome audit; got %d", len(audits))
				}
				d := audits[0]
				if d["outcome"] != tc.outcome || d["reason"] != tc.reason || d["module"] != module || d["requested_rating"] != "PG" || d["requested_source"] != "operator" {
					t.Errorf("unexpected audit details: %v", d)
				}
				if (d["content_rating"] == "PG") != (tc.outcome == "confirmed") || (d["source"] == "operator") != (tc.outcome == "confirmed") {
					t.Errorf("unconfirmed audit claims saved classification: %v", d)
				}
				for _, v := range d {
					if strings.Contains(v, "SECRET") || strings.Contains(v, "bearer") {
						t.Errorf("audit exposes provider detail or bearer: %v", d)
					}
				}
				if tc.fixture.sets != 1 || (tc.outcome == "confirmed") != strings.Contains(w.Body.String(), "saved and checked") {
					t.Fatalf("incorrect retry/success: sets=%d status=%d", tc.fixture.sets, w.Code)
				}
			})
		}
	}
}

func TestContentRatingDoesNotAuditUndispatchedWrites(t *testing.T) {
	for _, tc := range []struct {
		name, body, bearer string
		roles              []string
		discoveryFails     bool
	}{
		{"invalid", "classification=invalid", "bearer", []string{"admin"}, false},
		{"role", "classification=PG", "bearer", []string{"manager"}, false},
		{"bearer", "classification=PG", "", []string{"admin"}, false},
		{"discovery", "classification=PG", "bearer", []string{"admin"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &ratingFixture{}
			h := newRatingHandler(t, f)
			if tc.discoveryFails {
				h.Core = nil
			}
			h.AuditHook = func(_, _, _, _ string, _ map[string]string) { t.Error("undispatched write audited") }
			h.ContentRatingSave(httptest.NewRecorder(), ratingRequest("POST", "media-movies", tc.body, tc.roles, tc.bearer))
			if f.sets != 0 {
				t.Fatalf("unexpected writes: %d", f.sets)
			}
		})
	}
}

func TestContentRatingReadStatesKeepFormHonest(t *testing.T) {
	for _, module := range []string{"media-movies", "media-tvshows"} {
		for _, tc := range []struct {
			name, message string
			fixture       *ratingFixture
			loaded        bool
		}{
			{"empty", "Unavailable", &ratingFixture{}, true},
			{"rated", "Rated by an operator", &ratingFixture{rating: "PG", source: "operator"}, true},
			{"unrated", "Explicit unrated", &ratingFixture{rating: "NR", source: "operator"}, true},
			{"unknown", "Unavailable", &ratingFixture{rating: "15", source: "operator"}, true},
			{"foreign-source", "Unavailable", &ratingFixture{rating: "PG", source: "tmdb"}, true},
			{"read-error", "unavailable", &ratingFixture{readErr: codes.Unavailable}, false},
			{"wrong-item", "unavailable", &ratingFixture{responseID: "other"}, false},
		} {
			t.Run(module+"/"+tc.name, func(t *testing.T) {
				h := newRatingHandler(t, tc.fixture)
				w := httptest.NewRecorder()
				h.ContentRatingPage(w, ratingRequest("GET", module, "", []string{"admin"}, "bearer"))
				body := w.Body.String()
				if !strings.Contains(body, tc.message) || strings.Contains(body, `data-testid="content-rating-form"`) != tc.loaded || strings.Contains(body, `id="content-rating-current"`) != tc.loaded {
					t.Fatalf("dishonest loaded/form state: %d %s", w.Code, body)
				}
				if !tc.loaded && (strings.Contains(body, "Fixture") || strings.Contains(body, "Recorded value:")) {
					t.Fatalf("failed response exposed item data: %s", body)
				}
			})
		}
	}
}
