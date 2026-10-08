package handler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tvmgmtv1 "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
)

// ratingCall is one SetContentRating request as the module received it.
type ratingCall struct {
	ID              string
	Rating          string
	ExplicitUnrated bool
}

// ratingModuleState is shared by the movie and TV fakes: the stored titles,
// the recorded calls and the failures to inject.
type ratingModuleState struct {
	mu       sync.Mutex
	titles   []ratingTitle
	calls    []ratingCall
	lists    []string // search strings of ListMovies / ListTVShows calls
	failures map[string]error
	listErr  error
	// listErrAfterSet makes every list call fail once any SetContentRating
	// has succeeded, to model a refresh failure after a write.
	listErrAfterSet error
}

type ratingTitle struct {
	ID, Title, Rating, Source string
	Year                      int32
	Tags                      []string
}

func (s *ratingModuleState) list(page, size int32, search string) ([]ratingTitle, int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists = append(s.lists, search)
	if s.listErr != nil {
		return nil, 0, s.listErr
	}
	var all []ratingTitle
	for _, t := range s.titles {
		if search == "" || strings.Contains(strings.ToLower(t.Title), strings.ToLower(search)) {
			all = append(all, t)
		}
	}
	start := int((page - 1) * size)
	if start > len(all) {
		start = len(all)
	}
	end := start + int(size)
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], int32(len(all)), nil
}

func (s *ratingModuleState) set(id, rating string, explicit bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, ratingCall{id, rating, explicit})
	if err := s.failures[id]; err != nil {
		return err
	}
	if s.listErrAfterSet != nil {
		s.listErr = s.listErrAfterSet
	}
	for i := range s.titles {
		if s.titles[i].ID != id {
			continue
		}
		switch {
		case explicit:
			s.titles[i].Rating, s.titles[i].Source = "NR", "operator"
		case rating == "":
			s.titles[i].Rating, s.titles[i].Source = "", ""
		default:
			s.titles[i].Rating, s.titles[i].Source = rating, "operator"
		}
	}
	return nil
}

func (s *ratingModuleState) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls) + len(s.lists)
}

type fakeMovieRatings struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
	st *ratingModuleState
}

func (f fakeMovieRatings) ListMovies(_ context.Context, req *mgmntv1.ListMoviesRequest) (*mgmntv1.ListMoviesResponse, error) {
	items, total, err := f.st.list(req.GetPage(), req.GetPageSize(), req.GetSearch())
	if err != nil {
		return nil, err
	}
	out := &mgmntv1.ListMoviesResponse{Total: total}
	for _, t := range items {
		out.Movies = append(out.Movies, &mgmntv1.MovieItem{
			Id: t.ID, Title: t.Title, Year: t.Year,
			ContentRating: t.Rating, ContentRatingSource: t.Source, TagLabels: t.Tags,
		})
	}
	return out, nil
}

func (f fakeMovieRatings) SetContentRating(_ context.Context, req *mgmntv1.SetContentRatingRequest) (*mgmntv1.SetContentRatingResponse, error) {
	if err := f.st.set(req.GetMovieId(), req.GetContentRating(), req.GetExplicitUnrated()); err != nil {
		return nil, err
	}
	return &mgmntv1.SetContentRatingResponse{}, nil
}

type fakeTVRatings struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
	st *ratingModuleState
}

func (f fakeTVRatings) ListTVShows(_ context.Context, req *tvmgmtv1.ListTVShowsRequest) (*tvmgmtv1.ListTVShowsResponse, error) {
	items, total, err := f.st.list(req.GetPage(), req.GetPageSize(), req.GetSearch())
	if err != nil {
		return nil, err
	}
	out := &tvmgmtv1.ListTVShowsResponse{Total: total}
	for _, t := range items {
		out.Series = append(out.Series, &tvmgmtv1.TVSeries{
			Id: t.ID, Name: t.Title, Year: t.Year,
			ContentRating: t.Rating, ContentRatingSource: t.Source, TagLabels: t.Tags,
		})
	}
	return out, nil
}

func (f fakeTVRatings) SetContentRating(_ context.Context, req *tvmgmtv1.SetContentRatingRequest) (*tvmgmtv1.SetContentRatingResponse, error) {
	if err := f.st.set(req.GetSeriesId(), req.GetContentRating(), req.GetExplicitUnrated()); err != nil {
		return nil, err
	}
	return &tvmgmtv1.SetContentRatingResponse{}, nil
}

// ratingDiscovery answers capability look-ups and counts them, so a test can
// prove a refused request never even resolved a module.
type ratingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	movies, tv string
	mu         sync.Mutex
	lookups    int
}

func (d *ratingDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	d.mu.Lock()
	d.lookups++
	d.mu.Unlock()
	addr := ""
	switch req.GetCapability() {
	case capMediaLibraryMovies:
		addr = d.movies
	case capMediaLibraryTV:
		addr = d.tv
	}
	if addr == "" {
		return &discoveryv1.FindByCapabilityResponse{}, nil
	}
	return &discoveryv1.FindByCapabilityResponse{Modules: []*discoveryv1.ModuleInfoProto{{Id: "module", HttpAddr: addr}}}, nil
}

func (d *ratingDiscovery) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lookups
}

type ratingEnv struct {
	h      *Handler
	disc   *ratingDiscovery
	movies *ratingModuleState
	tv     *ratingModuleState
	mu     sync.Mutex
	audits []auditRecord
}

func serveGRPC(t *testing.T, register func(*grpc.Server)) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	return lis.Addr().String()
}

// newRatingEnv starts the movie and TV fakes and a discovery fake. Pass false
// for a module to leave it unregistered.
func newRatingEnv(t *testing.T, withMovies, withTV bool) *ratingEnv {
	t.Helper()
	e := &ratingEnv{
		movies: &ratingModuleState{failures: map[string]error{}},
		tv:     &ratingModuleState{failures: map[string]error{}},
		disc:   &ratingDiscovery{},
	}
	if withMovies {
		e.disc.movies = serveGRPC(t, func(s *grpc.Server) {
			mgmntv1.RegisterMovieManagementServiceServer(s, fakeMovieRatings{st: e.movies})
		})
	}
	if withTV {
		e.disc.tv = serveGRPC(t, func(s *grpc.Server) {
			tvmgmtv1.RegisterTvManagementServiceServer(s, fakeTVRatings{st: e.tv})
		})
	}
	discAddr := serveGRPC(t, func(s *grpc.Server) { discoveryv1.RegisterDiscoveryServiceServer(s, e.disc) })
	core, err := client.Dial(discAddr, client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	e.h = New(nil, session.NewStore(0), false, "test", nil, true, "", nil, nil)
	e.h.Core = core
	e.h.AuditHook = func(actor, action, resource, id string, details map[string]string) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.audits = append(e.audits, auditRecord{actor, action, resource, id, details})
	}
	return e
}

func (e *ratingEnv) auditRecords() []auditRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]auditRecord(nil), e.audits...)
}

func ratingSession(roles ...string) *session.Session {
	return &session.Session{UserID: "u-" + strings.Join(roles, "-"), Username: "someone", Roles: roles}
}

func (e *ratingEnv) get(sess *session.Session, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if sess != nil {
		r = r.WithContext(context.WithValue(r.Context(), ctxSessionKey, sess))
	}
	w := httptest.NewRecorder()
	e.h.ContentRatingsPage(w, r)
	return w
}

func (e *ratingEnv) post(sess *session.Session, form url.Values) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(http.MethodPost, "/content-ratings", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sess != nil {
		r = r.WithContext(context.WithValue(r.Context(), ctxSessionKey, sess))
	}
	w := httptest.NewRecorder()
	e.h.ContentRatingsApply(w, r)
	return w
}

func movieTitles(n int) []ratingTitle {
	out := make([]ratingTitle, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, ratingTitle{ID: fmt.Sprintf("m%03d", i), Title: fmt.Sprintf("Movie %03d", i), Year: 2000 + int32(i%20)})
	}
	return out
}

func ratingForm(kind, choice string, ids ...string) url.Values {
	return url.Values{"kind": {kind}, "rating": {choice}, "ids": ids}
}

// The ladder is the contract with the modules and userdata-local. This list is
// written out in full on purpose: changing the code list must fail here.
func TestContentRatingTokensArePinnedToTheLadder(t *testing.T) {
	want := []string{
		"G", "TV-Y", "TV-Y7", "TV-Y7-FV", "ALL", "E",
		"PG", "TV-G", "TV-PG", "E10+",
		"PG-13", "TV-14", "T",
		"R", "TV-MA", "M", "MA",
		"NC-17", "AO", "X",
	}
	if len(want) != 20 {
		t.Fatalf("test ladder has %d tokens, want 20", len(want))
	}
	if !reflect.DeepEqual(parentalRatingTokens, want) {
		t.Fatalf("rating tokens drifted from the ADR-0031 ladder:\n got %v\nwant %v", parentalRatingTokens, want)
	}
	seen := map[string]bool{}
	for _, tok := range want {
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
}

func TestContentRatingChoiceMapping(t *testing.T) {
	for _, tok := range parentalRatingTokens {
		got, ok := parseContentRatingChoice(tok)
		if !ok || got.Rating != tok || got.ExplicitUnrated || got.Mode != "set" {
			t.Errorf("%q: got %+v ok=%v", tok, got, ok)
		}
	}
	nr, ok := parseContentRatingChoice("NR")
	if !ok || nr.Rating != "" || !nr.ExplicitUnrated || nr.Mode != "unrated" {
		t.Errorf("NR must be explicit_unrated with an empty rating, got %+v ok=%v", nr, ok)
	}
	clr, ok := parseContentRatingChoice("CLEAR")
	if !ok || clr.Rating != "" || clr.ExplicitUnrated || clr.Mode != "clear" {
		t.Errorf("CLEAR must be an empty rating without explicit_unrated, got %+v ok=%v", clr, ok)
	}
	for _, bad := range []string{"", " ", "pg-13", "Pg", "PG-99", " G", "G ", "nr", "clear", "NR ", "TV-MA;", "free text", "<script>"} {
		if _, ok := parseContentRatingChoice(bad); ok {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestContentRatingNonAdminGetsNoModuleContact(t *testing.T) {
	cases := map[string]*session.Session{
		"manager":      ratingSession("manager"),
		"user":         ratingSession("user"),
		"viewer":       ratingSession("viewer"),
		"no roles":     ratingSession(),
		"no session":   nil,
		"similar role": ratingSession("administrator", "admins"),
	}
	for name, sess := range cases {
		t.Run(name, func(t *testing.T) {
			e := newRatingEnv(t, true, true)
			e.movies.titles = movieTitles(3)
			if w := e.get(sess, "/content-ratings?kind=movies"); w.Code != http.StatusForbidden {
				t.Errorf("GET status = %d, want 403", w.Code)
			}
			for _, kind := range []string{"movies", "tv"} {
				w := e.post(sess, ratingForm(kind, "G", "m001"))
				if w.Code != http.StatusForbidden {
					t.Errorf("POST %s status = %d, want 403", kind, w.Code)
				}
			}
			if n := e.disc.count(); n != 0 {
				t.Errorf("discovery was consulted %d times for a refused request", n)
			}
			if n := e.movies.callCount() + e.tv.callCount(); n != 0 {
				t.Errorf("module received %d calls for a refused request", n)
			}
			if len(e.auditRecords()) != 0 {
				t.Errorf("a refused request was audited as a change: %+v", e.auditRecords())
			}
		})
	}
}

func TestContentRatingAdminListsStatesSourceAndTags(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = []ratingTitle{
		{ID: "a", Title: "Rated One", Year: 1999, Rating: "PG-13", Source: "operator", Tags: []string{"anime", "classic"}},
		{ID: "b", Title: "Unrated One", Rating: "NR", Source: "operator"},
		{ID: "c", Title: "Nothing Yet"},
		{ID: "d", Title: "Odd One", Rating: "XYZ", Source: "operator"},
		{ID: "e", Title: "<script>alert(1)</script>"},
	}
	w := e.get(ratingSession("admin"), "/content-ratings")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %.300s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	body := w.Body.String()
	for id, state := range map[string]string{"a": "rated", "b": "unrated", "c": "unavailable", "d": "unavailable"} {
		if !strings.Contains(body, `data-id="`+id+`" data-state="`+state+`"`) {
			t.Errorf("row %s is not rendered as %s", id, state)
		}
	}
	for _, want := range []string{
		"Rated PG-13", "Not rated (NR)", "Unavailable", "Operator", "anime", "classic", "Rated One (1999)",
		"hidden from restricted accounts", "not taken from TMDB",
		"which is not a known rating",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)") {
		t.Error("a title was rendered without escaping")
	}
	// Every ladder token plus NR and Clear is selectable; nothing is free text.
	for _, tok := range parentalRatingTokens {
		if !strings.Contains(body, `<option value="`+tok+`"`) {
			t.Errorf("option %q missing", tok)
		}
	}
	for _, v := range []string{`value="NR"`, `value="CLEAR"`} {
		if !strings.Contains(body, v) {
			t.Errorf("option %s missing", v)
		}
	}
	if strings.Contains(body, `name="rating" type="text"`) || strings.Contains(body, `<input type="text" name="rating`) {
		t.Error("rating must not be free text")
	}
	if n := len(e.auditRecords()); n != 0 {
		t.Errorf("listing wrote %d audit entries", n)
	}
}

func TestContentRatingSetReplaceNRClearMapToRPCFields(t *testing.T) {
	admin := ratingSession("admin")
	for _, kind := range []string{"movies", "tv"} {
		t.Run(kind, func(t *testing.T) {
			e := newRatingEnv(t, true, true)
			st := e.movies
			if kind == "tv" {
				st = e.tv
			}
			st.titles = []ratingTitle{{ID: "x1", Title: "Title One"}}
			steps := []struct {
				choice string
				want   ratingCall
			}{
				{"TV-14", ratingCall{"x1", "TV-14", false}},
				{"PG", ratingCall{"x1", "PG", false}},  // replace
				{"NR", ratingCall{"x1", "", true}},     // explicit unrated
				{"CLEAR", ratingCall{"x1", "", false}}, // back to unavailable
				{"E10+", ratingCall{"x1", "E10+", false}},
			}
			for _, s := range steps {
				w := e.post(admin, ratingForm(kind, s.choice, "x1"))
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status %d: %.300s", s.choice, w.Code, w.Body.String())
				}
				st.mu.Lock()
				got := st.calls[len(st.calls)-1]
				st.mu.Unlock()
				if got != s.want {
					t.Errorf("%s: RPC = %+v, want %+v", s.choice, got, s.want)
				}
			}
			recs := e.auditRecords()
			if len(recs) != len(steps) {
				t.Fatalf("audit entries = %d, want %d", len(recs), len(steps))
			}
			wantModes := []string{"set", "set", "unrated", "clear", "set"}
			for i, rec := range recs {
				if rec.Action != "admin.content_rating.set" || rec.Resource != "media_item" || rec.ResourceID != "x1" || rec.Actor != admin.UserID {
					t.Errorf("audit %d = %+v", i, rec)
				}
				if rec.Details["kind"] != kind || rec.Details["mode"] != wantModes[i] {
					t.Errorf("audit %d details = %v", i, rec.Details)
				}
				if len(rec.Details) != 3 {
					t.Errorf("audit %d carries unexpected fields: %v", i, rec.Details)
				}
			}
		})
	}
}

func TestContentRatingRowButtonChangesOnlyThatRow(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(3)
	form := url.Values{
		"kind": {"movies"}, "only": {"m002"},
		"rating_m001": {"R"}, "rating_m002": {"PG"}, "rating_m003": {"X"},
		"ids": {"m001", "m003"}, "rating": {"G"},
	}
	w := e.post(ratingSession("admin"), form)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !reflect.DeepEqual(e.movies.calls, []ratingCall{{"m002", "PG", false}}) {
		t.Fatalf("calls = %+v", e.movies.calls)
	}
}

func TestContentRatingBulkPartialFailureIsReported(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(4)
	e.movies.failures["m002"] = status.Error(codes.InvalidArgument, "unknown rating token \"G\"")
	e.movies.failures["m003"] = status.Error(codes.NotFound, "movie m003 not found: internal-detail-xyz")
	w := e.post(ratingSession("admin"), ratingForm("movies", "G", "m001", "m002", "m003", "m004", "m001"))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if len(e.movies.calls) != 4 {
		t.Fatalf("expected 4 distinct RPCs (duplicate id dropped), got %+v", e.movies.calls)
	}
	for id, outcome := range map[string]string{"m001": "ok", "m002": "failed", "m003": "failed", "m004": "ok"} {
		if !strings.Contains(body, `data-id="`+id+`" data-outcome="`+outcome+`"`) {
			t.Errorf("%s should be reported as %s", id, outcome)
		}
	}
	for _, want := range []string{"2 changed", "2 failed", "Some titles were not changed", "invalid argument", "unknown rating token"} {
		if !strings.Contains(body, want) {
			t.Errorf("result missing %q", want)
		}
	}
	if strings.Contains(body, "internal-detail-xyz") {
		t.Error("a NotFound message from the module leaked into the page")
	}
	// Only the changes that happened are audited.
	var audited []string
	for _, rec := range e.auditRecords() {
		audited = append(audited, rec.ResourceID)
	}
	if !reflect.DeepEqual(audited, []string{"m001", "m004"}) {
		t.Errorf("audited ids = %v", audited)
	}
}

func TestContentRatingInvalidChoiceIsRejectedBeforeAnyRPC(t *testing.T) {
	for _, choice := range []string{"", "PG-99", "pg-13", "NR ", "nr", "anything", "<b>"} {
		t.Run(choice, func(t *testing.T) {
			e := newRatingEnv(t, true, true)
			e.movies.titles = movieTitles(2)
			w := e.post(ratingSession("admin"), ratingForm("movies", choice, "m001"))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if w.Header().Get(swapErrorHeader) != "1" {
				t.Error("error body is not marked for htmx swap")
			}
			if !strings.Contains(w.Body.String(), "Nothing was changed") {
				t.Errorf("no clear error: %.300s", w.Body.String())
			}
			if e.disc.count() != 0 || e.movies.callCount() != 0 || len(e.auditRecords()) != 0 {
				t.Error("an invalid choice reached discovery, the module or the audit log")
			}
		})
	}
}

func TestContentRatingMalformedSubmissionsAreRejectedBeforeAnyRPC(t *testing.T) {
	many := make([]string, 0, contentRatingMaxBatch+1)
	for i := 0; i <= contentRatingMaxBatch; i++ {
		many = append(many, fmt.Sprintf("m%d", i))
	}
	cases := map[string]url.Values{
		"no kind":       {"rating": {"G"}, "ids": {"m001"}},
		"bad kind":      {"kind": {"music"}, "rating": {"G"}, "ids": {"m001"}},
		"no selection":  {"kind": {"movies"}, "rating": {"G"}},
		"blank ids":     {"kind": {"movies"}, "rating": {"G"}, "ids": {" ", ""}},
		"too many":      {"kind": {"movies"}, "rating": {"G"}, "ids": many},
		"newline in id": {"kind": {"movies"}, "rating": {"G"}, "ids": {"m1\nm2"}},
	}
	for name, form := range cases {
		t.Run(name, func(t *testing.T) {
			e := newRatingEnv(t, true, true)
			w := e.post(ratingSession("admin"), form)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if e.disc.count() != 0 || e.movies.callCount() != 0 || len(e.auditRecords()) != 0 {
				t.Error("a malformed submission reached discovery, the module or the audit log")
			}
		})
	}
}

func TestContentRatingModuleErrorsAreRendered(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"invalid argument", status.Error(codes.InvalidArgument, "unsupported token"), "The module rejected this rating (invalid argument: unsupported token)"},
		{"not found", status.Error(codes.NotFound, "gone"), "no longer has this title"},
		{"unavailable", status.Error(codes.Unavailable, "down"), "did not answer in time"},
		{"unimplemented", status.Error(codes.Unimplemented, "old"), "does not support content ratings"},
		{"internal", status.Error(codes.Internal, "secret stack trace"), "reported an error (Internal)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newRatingEnv(t, true, false)
			e.movies.titles = movieTitles(1)
			e.movies.failures["m001"] = c.err
			w := e.post(ratingSession("admin"), ratingForm("movies", "R", "m001"))
			body := w.Body.String()
			if !strings.Contains(body, c.want) {
				t.Errorf("missing %q in %.600s", c.want, body)
			}
			if strings.Contains(body, "secret stack trace") {
				t.Error("module internals leaked")
			}
			if !strings.Contains(body, `data-outcome="failed"`) || len(e.auditRecords()) != 0 {
				t.Error("failure not reported, or audited as a change")
			}
		})
	}
}

func TestContentRatingModuleUnavailable(t *testing.T) {
	e := newRatingEnv(t, false, false)
	w := e.post(ratingSession("admin"), ratingForm("movies", "G", "m001"))
	if w.Code != http.StatusFailedDependency {
		t.Fatalf("POST status = %d, want 424", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Nothing was changed") || w.Header().Get(swapErrorHeader) != "1" {
		t.Errorf("unclear error: %.300s", w.Body.String())
	}
	g := e.get(ratingSession("admin"), "/content-ratings?kind=tv")
	if g.Code != http.StatusOK || !strings.Contains(g.Body.String(), "module is unavailable") {
		t.Errorf("GET: %d %.300s", g.Code, g.Body.String())
	}
}

func TestContentRatingListErrorIsShownNotEmpty(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.listErr = status.Error(codes.Unimplemented, "old")
	w := e.get(ratingSession("admin"), "/content-ratings")
	body := w.Body.String()
	if !strings.Contains(body, "content-rating-list-error") || !strings.Contains(body, "v0.1.23") {
		t.Errorf("list failure not explained: %.400s", body)
	}
	if strings.Contains(body, "content-rating-empty") {
		t.Error("a list failure was shown as an empty library")
	}
}

func TestContentRatingPaginationAndSearch(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(120)
	admin := ratingSession("admin")

	w := e.get(admin, "/content-ratings?kind=movies&page=2")
	body := w.Body.String()
	if !strings.Contains(body, "Page 2 of 3") || !strings.Contains(body, "120 movies") {
		t.Errorf("page status wrong: %.200s", body)
	}
	if !strings.Contains(body, `data-id="m051"`) || !strings.Contains(body, `data-id="m100"`) ||
		strings.Contains(body, `data-id="m050"`) || strings.Contains(body, `data-id="m101"`) {
		t.Error("page 2 does not hold exactly titles 51-100")
	}
	if strings.Count(body, `data-testid="content-rating-row"`) != 50 {
		t.Error("page size is not 50")
	}
	for _, link := range []string{`rel="prev"`, `rel="next"`} {
		if !strings.Contains(body, link) {
			t.Errorf("missing %s link", link)
		}
	}

	// Search is passed to the module and carried by the links and the form.
	w = e.get(admin, "/content-ratings?kind=movies&q=Movie+01")
	body = w.Body.String()
	if e.movies.lists[len(e.movies.lists)-1] != "Movie 01" {
		t.Errorf("search not forwarded: %v", e.movies.lists)
	}
	if !strings.Contains(body, "10 movies") || !strings.Contains(body, `value="Movie 01"`) {
		t.Errorf("search results/echo wrong: %.300s", body)
	}
	if w = e.get(admin, "/content-ratings?kind=movies&q=nomatch"); !strings.Contains(w.Body.String(), "match “nomatch”") {
		t.Error("empty search result not explained")
	}
	if w = e.get(admin, "/content-ratings?kind=movies&page=99"); !strings.Contains(w.Body.String(), "past the end") {
		t.Error("page past the end not explained")
	}
	for _, bad := range []string{"page=0", "page=-3", "page=abc", "kind=weird"} {
		if w = e.get(admin, "/content-ratings?"+bad); w.Code != http.StatusOK {
			t.Errorf("%s: status %d", bad, w.Code)
		}
	}
	if u := templates.ContentRatingPageURL("tv", "a b&c", 3); u != "/content-ratings?kind=tv&page=3&q=a+b%26c" {
		t.Errorf("page URL = %q", u)
	}
}

func TestContentRatingApplyKeepsSearchAndPageAndRefreshes(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(120)
	form := ratingForm("movies", "PG", "m051")
	form.Set("q", "Movie")
	form.Set("page", "2")
	w := e.post(ratingSession("admin"), form)
	body := w.Body.String()
	if !strings.Contains(body, "Page 2 of 3") {
		t.Errorf("page not kept: %.200s", body)
	}
	if !strings.Contains(body, `data-id="m051" data-state="rated"`) {
		t.Error("list was not refreshed after the write")
	}
	if !strings.Contains(body, "Movie 051") {
		t.Error("result does not name the title")
	}
}

func TestContentRatingTVUsesTheSeriesModule(t *testing.T) {
	e := newRatingEnv(t, true, true)
	e.movies.titles = []ratingTitle{{ID: "same", Title: "A movie"}}
	e.tv.titles = []ratingTitle{{ID: "same", Title: "A series", Rating: "TV-MA", Source: "operator"}}
	w := e.get(ratingSession("admin"), "/content-ratings?kind=tv")
	if !strings.Contains(w.Body.String(), "A series") || strings.Contains(w.Body.String(), "A movie") {
		t.Error("TV page is not backed by the TV module")
	}
	e.post(ratingSession("admin"), ratingForm("tv", "TV-Y", "same"))
	if len(e.movies.calls) != 0 || !reflect.DeepEqual(e.tv.calls, []ratingCall{{"same", "TV-Y", false}}) {
		t.Errorf("movies=%+v tv=%+v", e.movies.calls, e.tv.calls)
	}
}

func TestContentRatingRoutesAreRegisteredBehindAuth(t *testing.T) {
	e := newRatingEnv(t, true, true)
	mux := http.NewServeMux()
	e.h.RegisterRoutes(mux)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "/content-ratings", nil)
		_, pattern := mux.Handler(r)
		if pattern == "" || !strings.Contains(pattern, "/content-ratings") {
			t.Errorf("%s /content-ratings is not routed (pattern %q)", method, pattern)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			t.Errorf("%s without a session returned 200", method)
		}
	}
	if e.movies.callCount() != 0 || e.tv.callCount() != 0 {
		t.Error("an unauthenticated request reached a module")
	}
	found := false
	for _, l := range staticNavLinks {
		if l.Path == "/content-ratings" {
			found = true
		}
	}
	if !found {
		t.Error("no navigation link to /content-ratings")
	}
}

// A refresh failure after a successful write must not read as a failed write.
func TestContentRatingListFailureAfterSuccessfulSetKeepsResultsAuthoritative(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(2)
	e.movies.listErrAfterSet = status.Error(codes.Unavailable, "gone")
	w := e.post(ratingSession("admin"), ratingForm("movies", "PG", "m001", "m002"))
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	for _, id := range []string{"m001", "m002"} {
		if !strings.Contains(body, `data-id="`+id+`" data-outcome="ok"`) {
			t.Errorf("%s should be reported as changed", id)
		}
	}
	if !strings.Contains(body, "2 changed") {
		t.Error("summary does not report the successful changes")
	}
	if !strings.Contains(body, "content-rating-list-error") || !strings.Contains(body, "Unable to refresh") {
		t.Errorf("refresh failure not shown: %.400s", body)
	}
	if strings.Contains(body, "were not changed") {
		t.Error("refresh failure claims ratings were not changed")
	}
	if n := len(e.auditRecords()); n != 2 {
		t.Errorf("audit entries = %d, want 2", n)
	}
}

func TestContentRatingPageNumberIsBoundedToInt32(t *testing.T) {
	for _, in := range []string{"99999999999999999999", "9223372036854775807", "2147483648", "4294967297", "2147483647"} {
		got := contentRatingPageNumber(in)
		if got < 1 || got > contentRatingMaxPage {
			t.Errorf("%s -> %d, outside [1, %d]", in, got, contentRatingMaxPage)
		}
	}
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(3)
	w := e.get(ratingSession("admin"), "/content-ratings?page=4294967297")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "past the end") {
		t.Errorf("huge page: %d %.200s", w.Code, w.Body.String())
	}
}
