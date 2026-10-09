package handler

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Clearing the operator classification is confirmed by the operator value being
// gone, not by an empty classification: media-movies and media-tvshows v0.1.24
// fall back to a lower-precedence TMDB rating, so a successful clear can
// legitimately read back as ("R", "tmdb"). A set still has to read back exactly.
func TestContentRatingClearConfirmationIsAPredicate(t *testing.T) {
	for _, module := range []string{"media-movies", "media-tvshows"} {
		for _, tc := range []struct {
			name, body string
			fixture    *ratingFixture
			code       int
			// page text that must appear; absent text that must not.
			present, absent []string
			outcome, reason string
			// observed readback recorded in the audit; empty means no readback.
			observedRating, observedSource, effective string
		}{
			{
				name: "clear with no TMDB value", body: "classification=clear",
				fixture: &ratingFixture{rating: "PG", source: "operator"}, code: 200,
				present: []string{"Operator classification cleared; no classification is available now."},
				absent:  []string{"from TMDB", "could not be confirmed"},
				outcome: "confirmed", reason: "readback_matched",
				effective: "none",
			},
			{
				name: "clear falling back to TMDB rating", body: "classification=clear",
				fixture: &ratingFixture{rating: "PG", source: "operator", tmdb: "R"}, code: 200,
				present: []string{"Operator classification cleared; the effective rating is now R from TMDB.", "Rated by TMDB (no operator classification)"},
				absent:  []string{"could not be confirmed", "Rated by an operator"},
				outcome: "confirmed", reason: "readback_matched",
				observedRating: "R", observedSource: "tmdb", effective: "tmdb R",
			},
			{
				name: "clear falling back to TMDB NR", body: "classification=clear",
				fixture: &ratingFixture{rating: "G", source: "operator", tmdb: "NR"}, code: 200,
				present: []string{"the effective rating is now Not rated (NR) from TMDB."},
				absent:  []string{"could not be confirmed"},
				outcome: "confirmed", reason: "readback_matched",
				observedRating: "NR", observedSource: "tmdb", effective: "tmdb NR",
			},
			{
				name: "clear but the operator value is still there", body: "classification=clear",
				fixture: &ratingFixture{rating: "PG", source: "operator", tmdb: "R", ignoreWrite: true}, code: 502,
				present: []string{"could not be confirmed"},
				absent:  []string{"Operator classification cleared", "saved and checked"},
				outcome: "uncertain", reason: "readback_mismatch",
				observedRating: "PG", observedSource: "operator",
			},
			{
				name: "clear but the readback fails", body: "classification=clear",
				fixture: &ratingFixture{rating: "PG", source: "operator", readErr: codes.Unavailable}, code: 502,
				present: []string{"could not be confirmed"},
				absent:  []string{"Operator classification cleared", "saved and checked"},
				outcome: "uncertain", reason: "readback_unavailable",
			},
			{
				name: "clear reads back an unusable TMDB value", body: "classification=clear",
				fixture: &ratingFixture{rating: "PG", source: "operator", tmdb: "15"}, code: 502,
				present: []string{"could not be confirmed"},
				absent:  []string{"Operator classification cleared"},
				outcome: "uncertain", reason: "readback_mismatch",
				observedRating: "15", observedSource: "tmdb",
			},
			{
				name: "set PG while a TMDB rating exists", body: "classification=PG",
				fixture: &ratingFixture{rating: "R", source: "tmdb", tmdb: "R"}, code: 200,
				present: []string{"saved and checked", "Operator classification"},
				absent:  []string{"could not be confirmed", "Operator classification cleared"},
				outcome: "confirmed", reason: "readback_matched",
				observedRating: "PG", observedSource: "operator", effective: "operator PG",
			},
			{
				name: "set whose readback is the TMDB value", body: "classification=PG",
				fixture: &ratingFixture{rating: "PG", source: "tmdb", ignoreWrite: true}, code: 502,
				present: []string{"could not be confirmed"},
				absent:  []string{"saved and checked"},
				outcome: "uncertain", reason: "readback_mismatch",
				observedRating: "PG", observedSource: "tmdb",
			},
			{
				name: "set with a different operator value", body: "classification=PG",
				fixture: &ratingFixture{rating: "G", source: "operator", ignoreWrite: true}, code: 502,
				present: []string{"could not be confirmed"},
				absent:  []string{"saved and checked"},
				outcome: "uncertain", reason: "readback_mismatch",
				observedRating: "G", observedSource: "operator",
			},
		} {
			t.Run(module+"/"+tc.name, func(t *testing.T) {
				h := newRatingHandler(t, tc.fixture)
				var audits []map[string]string
				h.AuditHook = func(_, _, _, _ string, details map[string]string) { audits = append(audits, details) }
				w := httptest.NewRecorder()
				h.ContentRatingSave(w, ratingRequest("POST", module, tc.body, []string{"admin"}, "bearer"))
				body := w.Body.String()
				if w.Code != tc.code {
					t.Fatalf("status = %d, want %d: %s", w.Code, tc.code, body)
				}
				for _, s := range tc.present {
					if !strings.Contains(body, s) {
						t.Errorf("page lacks %q: %s", s, body)
					}
				}
				for _, s := range tc.absent {
					if strings.Contains(body, s) {
						t.Errorf("page has %q: %s", s, body)
					}
				}
				// One write and one readback, never a retry.
				if tc.fixture.sets != 1 || tc.fixture.gets != 1 {
					t.Errorf("sets=%d gets=%d, want 1 and 1", tc.fixture.sets, tc.fixture.gets)
				}
				if len(audits) != 1 {
					t.Fatalf("audits = %d, want 1", len(audits))
				}
				d := audits[0]
				wantMode := "set"
				if strings.HasSuffix(tc.body, "=clear") {
					wantMode = "clear"
				}
				if d["outcome"] != tc.outcome || d["reason"] != tc.reason || d["requested_mode"] != wantMode {
					t.Errorf("audit = %v", d)
				}
				if d["observed_rating"] != tc.observedRating || d["observed_source"] != tc.observedSource {
					t.Errorf("audit observed = %q/%q, want %q/%q: %v", d["observed_rating"], d["observed_source"], tc.observedRating, tc.observedSource, d)
				}
				if d["effective"] != tc.effective {
					t.Errorf("audit effective = %q, want %q: %v", d["effective"], tc.effective, d)
				}
				// Stored-value keys appear only when the readback confirmed it.
				if _, ok := d["content_rating"]; ok != (tc.outcome == "confirmed") {
					t.Errorf("content_rating present=%v for %s: %v", ok, tc.outcome, d)
				}
			})
		}
	}
}

// The item page names the source of the effective rating in text and carries
// the precedence sentence.
func TestContentRatingPageShowsRatingSource(t *testing.T) {
	for _, tc := range []struct {
		name            string
		fixture         *ratingFixture
		want, notWanted string
	}{
		{"operator", &ratingFixture{rating: "PG", source: "operator"}, "Operator classification", "TMDB (applies"},
		{"tmdb", &ratingFixture{rating: "R", source: "tmdb"}, "TMDB (applies only because no operator classification exists)", "Rated by an operator"},
		{"none", &ratingFixture{}, "None (no classification available)", "TMDB (applies"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRatingHandler(t, tc.fixture)
			w := httptest.NewRecorder()
			h.ContentRatingPage(w, ratingRequest("GET", "media-movies", "", []string{"admin"}, "bearer"))
			body := w.Body.String()
			if w.Code != http.StatusOK || !strings.Contains(body, `data-testid="content-rating-source"`) || !strings.Contains(body, tc.want) || strings.Contains(body, tc.notWanted) {
				t.Fatalf("status %d: %s", w.Code, body)
			}
			const sentence = "A TMDB rating applies only where no operator classification exists; setting one here overrides it; clearing returns to TMDB."
			if !strings.Contains(body, sentence) {
				t.Errorf("precedence sentence missing: %s", body)
			}
			// A TMDB value is not the operator's: the form must not offer it as a
			// preselected operator rating.
			if tc.name == "tmdb" && strings.Contains(body, `<option value="R" selected`) {
				t.Errorf("TMDB rating preselected as an operator classification: %s", body)
			}
		})
	}
}

// confirmedBy is the whole predicate; pin it without a server.
func TestContentRatingChangeConfirmedBy(t *testing.T) {
	clear := contentRatingChange{Mode: "clear"}
	set := contentRatingChange{Rating: "PG", Mode: "set"}
	nr := contentRatingChange{ExplicitUnrated: true, Mode: "unrated"}
	for _, tc := range []struct {
		name           string
		ch             contentRatingChange
		rating, source string
		want           bool
	}{
		{"clear none", clear, "", "", true},
		{"clear tmdb ladder", clear, "R", "tmdb", true},
		{"clear tmdb NR", clear, "NR", "tmdb", true},
		{"clear still operator", clear, "R", "operator", false},
		{"clear operator empty", clear, "", "operator", false},
		{"clear tmdb empty", clear, "", "tmdb", false},
		{"clear tmdb unknown token", clear, "15", "tmdb", false},
		{"clear tmdb lower case", clear, "r", "tmdb", false},
		{"clear rating without source", clear, "R", "", false},
		{"clear unknown source", clear, "R", "imdb", false},
		{"set exact", set, "PG", "operator", true},
		{"set from tmdb", set, "PG", "tmdb", false},
		{"set no source", set, "PG", "", false},
		{"set other rating", set, "G", "operator", false},
		{"set nothing", set, "", "", false},
		{"nr exact", nr, "NR", "operator", true},
		{"nr from tmdb", nr, "NR", "tmdb", false},
		{"nr empty operator", nr, "", "operator", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ch.confirmedBy(tc.rating, tc.source); got != tc.want {
				t.Errorf("confirmedBy(%q, %q) = %v, want %v", tc.rating, tc.source, got, tc.want)
			}
		})
	}
}

// A bulk clear over a mix of titles reports each one on its own: operator
// values replaced by TMDB ratings, operator values replaced by nothing, and a
// stale readback that still names the operator. The summary counts match.
func TestContentRatingBulkClearMixedOutcomes(t *testing.T) {
	for _, kind := range []string{"movies", "tv"} {
		t.Run(kind, func(t *testing.T) {
			e := newRatingEnv(t, true, true)
			st := e.movies
			if kind == "tv" {
				st = e.tv
			}
			st.titles = []ratingTitle{
				{ID: "t1", Title: "Falls back R", Rating: "PG", Source: "operator", TMDB: "R"},
				{ID: "t2", Title: "Falls back NR", Rating: "G", Source: "operator", TMDB: "NR"},
				{ID: "t3", Title: "Nothing left", Rating: "PG-13", Source: "operator"},
				{ID: "t4", Title: "Stale readback", Rating: "PG", Source: "operator", TMDB: "R"},
				{ID: "t5", Title: "Readback fails", Rating: "PG", Source: "operator"},
			}
			st.getErr = map[string]error{"t5": status.Error(codes.Unavailable, "down")}
			// t4: the module keeps serving the operator value after the clear.
			st.readAs = map[string][2]string{"t4": {"PG", "operator"}}

			w := e.post(ratingSession("admin"), ratingForm(kind, "CLEAR", "t1", "t2", "t3", "t4", "t5"))
			body := w.Body.String()
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %.400s", w.Code, body)
			}
			for id, outcome := range map[string]string{"t1": "ok", "t2": "ok", "t3": "ok", "t4": "uncertain", "t5": "uncertain"} {
				if !strings.Contains(body, `data-id="`+id+`" data-outcome="`+outcome+`"`) {
					t.Errorf("%s should be %s: %.1200s", id, outcome, body)
				}
			}
			sum := ratingSummary(t, body)
			if !strings.HasPrefix(sum, "3 changed, 2 not confirmed.") ||
				!strings.Contains(sum, "a TMDB rating now applies to 2 and no rating to 1.") {
				t.Errorf("summary = %q", sum)
			}
			for _, want := range []string{
				"Operator classification cleared; the effective rating is now R from TMDB.",
				"Operator classification cleared; the effective rating is now Not rated (NR) from TMDB.",
				"Operator classification cleared; no classification is available now.",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("result lacks %q", want)
				}
			}
			// The refreshed list names the source of each effective rating.
			for id, src := range map[string]string{"t1": "tmdb", "t2": "tmdb", "t3": "", "t4": "tmdb", "t5": ""} {
				row := regexp.MustCompile(`(?s)<tr[^>]*data-id="` + id + `".*?</tr>`).FindString(body)
				if !strings.Contains(row, `data-source="`+src+`"`) {
					t.Errorf("%s row source != %q: %s", id, src, row)
				}
			}
			// One set and one readback per title, in order: never a retried write.
			wantOrder := []string{"set:t1", "get:t1", "set:t2", "get:t2", "set:t3", "get:t3", "set:t4", "get:t4", "set:t5", "get:t5"}
			if got := st.order[:len(wantOrder)]; strings.Join(got, ",") != strings.Join(wantOrder, ",") {
				t.Errorf("rpc order = %v", got)
			}
			// The audit carries the requested mode and the observed readback.
			type obs struct{ outcome, rating, source, effective string }
			want := map[string]obs{
				"t1": {"confirmed", "R", "tmdb", "tmdb R"},
				"t2": {"confirmed", "NR", "tmdb", "tmdb NR"},
				"t3": {"confirmed", "", "", "none"},
				"t4": {"uncertain", "PG", "operator", ""},
				"t5": {"uncertain", "", "", ""},
			}
			recs := e.auditRecords()
			if len(recs) != len(want) {
				t.Fatalf("audit entries = %d, want %d", len(recs), len(want))
			}
			for _, rec := range recs {
				w := want[rec.ResourceID]
				d := rec.Details
				if d["requested_mode"] != "clear" || d["requested_rating"] != "" || d["requested_source"] != "" || d["outcome"] != w.outcome || d["effective"] != w.effective {
					t.Errorf("%s audit = %v", rec.ResourceID, d)
				}
				if rec.ResourceID != "t5" && (d["observed_rating"] != w.rating || d["observed_source"] != w.source) {
					t.Errorf("%s observed = %q/%q, want %q/%q", rec.ResourceID, d["observed_rating"], d["observed_source"], w.rating, w.source)
				}
				if _, ok := d["observed_rating"]; rec.ResourceID == "t5" && ok {
					t.Errorf("t5 recorded an observation although the readback failed: %v", d)
				}
			}
		})
	}
}

// The list names the source of every row in words, and the page carries the
// precedence sentence; an operator row preselects its rating, a TMDB row none.
func TestContentRatingBulkListShowsRatingSource(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = []ratingTitle{
		{ID: "a", Title: "Operator one", Rating: "PG", Source: "operator"},
		{ID: "b", Title: "Tmdb one", Rating: "R", Source: "tmdb"},
		{ID: "c", Title: "None one"},
	}
	w := e.get(ratingSession("admin"), "/content-ratings?kind=movies")
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(body, "A TMDB rating applies only where no operator classification exists; setting one here overrides it; clearing returns to TMDB.") {
		t.Error("precedence sentence missing")
	}
	for id, want := range map[string]string{"a": "Operator", "b": "TMDB", "c": "None"} {
		row := regexp.MustCompile(`(?s)<tr[^>]*data-id="` + id + `".*?</tr>`).FindString(body)
		cell := regexp.MustCompile(`(?s)data-testid="content-rating-source"[^>]*>(.*?)</td>`).FindStringSubmatch(row)
		if cell == nil || !strings.Contains(strings.Join(strings.Fields(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(cell[1], " ")), " "), want) {
			t.Errorf("row %s source cell != %q: %s", id, want, row)
		}
	}
	rowB := regexp.MustCompile(`(?s)<tr[^>]*data-id="b".*?</tr>`).FindString(body)
	if !strings.Contains(rowB, "No operator classification") || strings.Contains(rowB, `<option value="R" selected`) {
		t.Errorf("TMDB row misleading: %s", rowB)
	}
	rowA := regexp.MustCompile(`(?s)<tr[^>]*data-id="a".*?</tr>`).FindString(body)
	if !strings.Contains(rowA, `<option value="PG" selected`) {
		t.Errorf("operator row lost its preselection: %s", rowA)
	}
}
