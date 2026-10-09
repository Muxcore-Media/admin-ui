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
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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
	// commitThenFail stores the change and then reports an error, as a module
	// does when its reply is lost or late after the write.
	commitThenFail map[string]error
	listErr        error
	// listErrAfterSet makes every list call fail once any SetContentRating
	// has succeeded, to model a refresh failure after a write.
	listErrAfterSet error
	// identities is the incoming metadata of every call the module received.
	identities []metadata.MD
	// getErr fails the readback (Get) of an id. readAs makes the readback of an
	// id report these {rating, source} instead of what is stored, as a module
	// serving a stale or different value would. gets records readback ids.
	getErr map[string]error
	readAs map[string][2]string
	gets   []string
	// order is every set/get/list the module saw, in order: "set:<id>",
	// "get:<id>", "list".
	order []string
	// afterSet runs after a set was recorded, before it replies.
	afterSet func(id string)
}

func (s *ratingModuleState) record(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identities = append(s.identities, md.Copy())
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
	s.order = append(s.order, "list")
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
	s.order = append(s.order, "set:"+id)
	if err := s.failures[id]; err != nil {
		return err
	}
	commitErr := s.commitThenFail[id]
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
	return commitErr
}

// get is the readback of one title: what is stored unless the test says
// otherwise.
func (s *ratingModuleState) get(id string) (ratingTitle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets = append(s.gets, id)
	s.order = append(s.order, "get:"+id)
	if err := s.getErr[id]; err != nil {
		return ratingTitle{}, err
	}
	for _, t := range s.titles {
		if t.ID != id {
			continue
		}
		if v, ok := s.readAs[id]; ok {
			t.Rating, t.Source = v[0], v[1]
		}
		return t, nil
	}
	return ratingTitle{}, status.Error(codes.NotFound, "no such title")
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

func (f fakeMovieRatings) ListMovies(ctx context.Context, req *mgmntv1.ListMoviesRequest) (*mgmntv1.ListMoviesResponse, error) {
	f.st.record(ctx)
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

func (f fakeMovieRatings) GetMovie(ctx context.Context, req *mgmntv1.GetMovieRequest) (*mgmntv1.GetMovieResponse, error) {
	f.st.record(ctx)
	t, err := f.st.get(req.GetMovieId())
	if err != nil {
		return nil, err
	}
	return &mgmntv1.GetMovieResponse{Movie: &mgmntv1.MovieItem{Id: t.ID, Title: t.Title, ContentRating: t.Rating, ContentRatingSource: t.Source}}, nil
}

func (f fakeMovieRatings) SetContentRating(ctx context.Context, req *mgmntv1.SetContentRatingRequest) (*mgmntv1.SetContentRatingResponse, error) {
	f.st.record(ctx)
	err := f.st.set(req.GetMovieId(), req.GetContentRating(), req.GetExplicitUnrated())
	if f.st.afterSet != nil {
		f.st.afterSet(req.GetMovieId())
	}
	if err != nil {
		return nil, err
	}
	return &mgmntv1.SetContentRatingResponse{}, nil
}

type fakeTVRatings struct {
	tvmgmtv1.UnimplementedTvManagementServiceServer
	st *ratingModuleState
}

func (f fakeTVRatings) ListTVShows(ctx context.Context, req *tvmgmtv1.ListTVShowsRequest) (*tvmgmtv1.ListTVShowsResponse, error) {
	f.st.record(ctx)
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

func (f fakeTVRatings) GetTVShow(ctx context.Context, req *tvmgmtv1.GetTVShowRequest) (*tvmgmtv1.GetTVShowResponse, error) {
	f.st.record(ctx)
	t, err := f.st.get(req.GetSeriesId())
	if err != nil {
		return nil, err
	}
	return &tvmgmtv1.GetTVShowResponse{Series: &tvmgmtv1.TVSeries{Id: t.ID, Name: t.Title, ContentRating: t.Rating, ContentRatingSource: t.Source}}, nil
}

func (f fakeTVRatings) SetContentRating(ctx context.Context, req *tvmgmtv1.SetContentRatingRequest) (*tvmgmtv1.SetContentRatingResponse, error) {
	f.st.record(ctx)
	if err := f.st.set(req.GetSeriesId(), req.GetContentRating(), req.GetExplicitUnrated()); err != nil {
		return nil, err
	}
	return &tvmgmtv1.SetContentRatingResponse{}, nil
}

// ratingDiscovery answers module look-ups by module ID and counts them, so a
// test can prove a refused request never even resolved a module.
type ratingDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	movies, tv string
	mu         sync.Mutex
	lookups    int
}

func (d *ratingDiscovery) Resolve(_ context.Context, req *discoveryv1.ResolveRequest) (*discoveryv1.ResolveResponse, error) {
	d.mu.Lock()
	d.lookups++
	d.mu.Unlock()
	addr := ""
	switch req.GetModuleId() {
	case contentRatingModuleMovies:
		addr = d.movies
	case contentRatingModuleTV:
		addr = d.tv
	}
	if addr == "" {
		return &discoveryv1.ResolveResponse{}, nil
	}
	return &discoveryv1.ResolveResponse{Found: true, Module: &discoveryv1.ModuleInfoProto{Id: req.GetModuleId(), HttpAddr: addr}}, nil
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
		movies: &ratingModuleState{failures: map[string]error{}, commitThenFail: map[string]error{}},
		tv:     &ratingModuleState{failures: map[string]error{}, commitThenFail: map[string]error{}},
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

// ratingBearer is the provider bearer requireAuth would have validated.
const ratingBearer = "bearer-fixture"

// staleIdentity puts credentials on the outgoing context that no request
// should forward: only the session's validated bearer may reach a module.
func staleIdentity(ctx context.Context) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"authorization", "Bearer stale", "x-auth-token", "stale",
		"x-muxcore-user-id", "forged", "x-muxcore-tenant-id", "forged"))
}

func ratingSession(roles ...string) *session.Session {
	return &session.Session{UserID: "u-" + strings.Join(roles, "-"), Username: "someone", Roles: roles, AuthLocalToken: ratingBearer}
}

func (e *ratingEnv) get(sess *session.Session, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if sess != nil {
		r = r.WithContext(staleIdentity(context.WithValue(r.Context(), ctxSessionKey, sess)))
	}
	w := httptest.NewRecorder()
	e.h.ContentRatingsBulkPage(w, r)
	return w
}

// deadlineRecorder is a response recorder that, like a real connection behind
// the middleware, accepts a write deadline and remembers it.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func newDeadlineRecorder() *deadlineRecorder {
	return &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return nil
}

// post submits form as sess to a writer that accepts a write deadline.
func (e *ratingEnv) post(sess *session.Session, form url.Values) *deadlineRecorder {
	w := newDeadlineRecorder()
	e.postTo(w, sess, form)
	return w
}

// postTo submits form as sess to the given writer.
func (e *ratingEnv) postTo(w http.ResponseWriter, sess *session.Session, form url.Values) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(http.MethodPost, "/content-ratings", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sess != nil {
		r = r.WithContext(staleIdentity(context.WithValue(r.Context(), ctxSessionKey, sess)))
	}
	e.h.ContentRatingsBulkApply(w, r)
}

// ratingSummary is the apply summary the page announces, as text.
func ratingSummary(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)data-testid="content-rating-summary"[^>]*>(.*?)</p>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no apply summary in %.400s", body)
	}
	text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(m[1], "")
	text = strings.NewReplacer(" ,", ",", " .", ".").Replace(strings.Join(strings.Fields(text), " "))
	return text
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
func TestContentRatingBulkTokensArePinnedToTheLadder(t *testing.T) {
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

func TestContentRatingBulkChoiceMapping(t *testing.T) {
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

func TestContentRatingBulkNonAdminGetsNoModuleContact(t *testing.T) {
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

func TestContentRatingBulkAdminListsStatesSourceAndTags(t *testing.T) {
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

func TestContentRatingBulkSetReplaceNRClearMapToRPCFields(t *testing.T) {
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
			// The per-item page's audit shape: requested values always, stored
			// values because each save was read back and matched.
			module := map[string]string{"movies": "media-movies", "tv": "media-tvshows"}[kind]
			wantRating := []string{"TV-14", "PG", "NR", "", "E10+"}
			wantSource := []string{"operator", "operator", "operator", "", "operator"}
			for i, rec := range recs {
				if rec.Action != "admin.media.content_rating" || rec.Resource != "media_item" || rec.ResourceID != "x1" || rec.Actor != admin.UserID {
					t.Errorf("audit %d = %+v", i, rec)
				}
				want := map[string]string{
					"module": module, "outcome": "confirmed", "reason": "readback_matched",
					"requested_rating": wantRating[i], "requested_source": wantSource[i],
					"content_rating": wantRating[i], "source": wantSource[i],
				}
				if !reflect.DeepEqual(rec.Details, want) {
					t.Errorf("audit %d details = %v, want %v", i, rec.Details, want)
				}
			}
		})
	}
}

func TestContentRatingBulkRowButtonChangesOnlyThatRow(t *testing.T) {
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
	for _, want := range []string{"2 changed", "2 failed", "Not every title was confirmed as changed", "invalid argument", "unknown rating token"} {
		if !strings.Contains(body, want) {
			t.Errorf("result missing %q", want)
		}
	}
	if strings.Contains(body, "internal-detail-xyz") {
		t.Error("a NotFound message from the module leaked into the page")
	}
	// Every attempt is audited once: confirmed for the two that changed, refused
	// for the two the module rejected.
	got := map[string]string{}
	for _, rec := range e.auditRecords() {
		got[rec.ResourceID] = rec.Details["outcome"]
	}
	want := map[string]string{"m001": "confirmed", "m002": "refused", "m003": "refused", "m004": "confirmed"}
	if !reflect.DeepEqual(got, want) || len(e.auditRecords()) != 4 {
		t.Errorf("audit outcomes = %v (%d records), want %v", got, len(e.auditRecords()), want)
	}
}

func TestContentRatingBulkInvalidChoiceIsRejectedBeforeAnyRPC(t *testing.T) {
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

func TestContentRatingBulkMalformedSubmissionsAreRejectedBeforeAnyRPC(t *testing.T) {
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

func TestContentRatingBulkModuleErrorsAreRendered(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
		// uncertain: the module may have committed before the error, so the
		// attempt is audited as outcome=uncertain (never as a success).
		uncertain bool
		code      string
		stop      int // batch-ending status, 0 for a per-title failure
	}{
		{"invalid argument", status.Error(codes.InvalidArgument, "unsupported token"), "The module rejected this rating (invalid argument: unsupported token)", false, "InvalidArgument", 0},
		{"not found", status.Error(codes.NotFound, "gone"), "no longer has this title", false, "NotFound", 0},
		{"unavailable", status.Error(codes.Unavailable, "down"), "did not answer in time", true, "Unavailable", 0},
		{"unimplemented", status.Error(codes.Unimplemented, "old"), "does not support content ratings", false, "Unimplemented", 0},
		{"internal", status.Error(codes.Internal, "secret stack trace"), "reported an error (Internal)", true, "Internal", 0},
		{"unauthenticated", status.Error(codes.Unauthenticated, "token revoked"), "no longer authorized", false, "Unauthenticated", http.StatusUnauthorized},
		{"permission denied", status.Error(codes.PermissionDenied, "role revoked"), "refused this operation", false, "PermissionDenied", http.StatusForbidden},
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
			wantOutcome, wantAudit := "failed", "refused"
			if c.uncertain {
				wantOutcome, wantAudit = "uncertain", "uncertain"
			}
			if !strings.Contains(body, `data-outcome="`+wantOutcome+`"`) {
				t.Errorf("want outcome %s: %.600s", wantOutcome, body)
			}
			wantStatus := http.StatusOK
			if c.stop != 0 {
				wantStatus = c.stop
			}
			if w.Code != wantStatus {
				t.Errorf("status = %d, want %d", w.Code, wantStatus)
			}
			recs := e.auditRecords()
			// The attempt is audited either way (never silently dropped), with
			// the requested value and the gRPC code as the reason.
			want := map[string]string{
				"module": "media-movies", "outcome": wantAudit, "reason": c.code,
				"requested_rating": "R", "requested_source": "operator",
			}
			if len(recs) != 1 || !reflect.DeepEqual(recs[0].Details, want) {
				t.Errorf("audit = %+v, want details %v", recs, want)
			}
		})
	}
}

func TestContentRatingBulkModuleUnavailable(t *testing.T) {
	e := newRatingEnv(t, false, false)
	w := e.post(ratingSession("admin"), ratingForm("movies", "G", "m001"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST status = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Nothing was changed") || w.Header().Get(swapErrorHeader) != "1" {
		t.Errorf("unclear error: %.300s", w.Body.String())
	}
	g := e.get(ratingSession("admin"), "/content-ratings?kind=tv")
	if g.Code != http.StatusOK || !strings.Contains(g.Body.String(), "module is unavailable") {
		t.Errorf("GET: %d %.300s", g.Code, g.Body.String())
	}
}

func TestContentRatingBulkListErrorIsShownNotEmpty(t *testing.T) {
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

func TestContentRatingBulkPaginationAndSearch(t *testing.T) {
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
	if u := templates.ContentRatingBulkPageURL("tv", "a b&c", 3); u != "/content-ratings?kind=tv&page=3&q=a+b%26c" {
		t.Errorf("page URL = %q", u)
	}
}

func TestContentRatingBulkApplyKeepsSearchAndPageAndRefreshes(t *testing.T) {
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

func TestContentRatingBulkTVUsesTheSeriesModule(t *testing.T) {
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

func TestContentRatingBulkRoutesAreRegisteredBehindAuth(t *testing.T) {
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
func TestContentRatingBulkListFailureAfterSuccessfulSetKeepsResultsAuthoritative(t *testing.T) {
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

func TestContentRatingBulkPageNumberIsBoundedToInt32(t *testing.T) {
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

// A refused submission must not look like an empty library, and must say how
// to get the list back. The submissions here are validated before any module
// contact, so the page cannot reload the list itself.
func TestContentRatingBulkFormErrorsDoNotRenderAnEmptyLibrary(t *testing.T) {
	cases := map[string]url.Values{
		"no ids":            {"kind": {"movies"}, "rating": {"G"}},
		"default Choose":    ratingForm("movies", "", "m001"),
		"row Set no choice": {"kind": {"movies"}, "only": {"m001"}, "rating_m001": {""}},
		"bad kind":          {"kind": {"music"}, "rating": {"G"}, "ids": {"m001"}},
	}
	for name, form := range cases {
		t.Run(name, func(t *testing.T) {
			e := newRatingEnv(t, true, false)
			e.movies.titles = movieTitles(3)
			w := e.post(ratingSession("admin"), form)
			body := w.Body.String()
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			for _, bad := range []string{"content-rating-empty", "in this library yet", "No movies"} {
				if strings.Contains(body, bad) {
					t.Errorf("form error rendered as an empty library (%q): %.400s", bad, body)
				}
			}
			if !strings.Contains(body, "content-rating-form-error") {
				t.Error("the validation error is missing")
			}
			if !strings.Contains(body, "content-rating-not-loaded") || !strings.Contains(body, `href="/content-ratings?kind=movies"`) {
				t.Errorf("no retry path to reload the list: %.600s", body)
			}
			if e.disc.count() != 0 || e.movies.callCount() != 0 {
				t.Error("a refused submission contacted discovery or the module")
			}
		})
	}
}

func TestContentRatingBulkModuleUnavailableDoesNotRenderAnEmptyLibrary(t *testing.T) {
	e := newRatingEnv(t, false, false)
	w := e.post(ratingSession("admin"), ratingForm("movies", "G", "m001"))
	body := w.Body.String()
	if w.Code != http.StatusServiceUnavailable || w.Header().Get(swapErrorHeader) != "1" {
		t.Fatalf("status = %d, swap header %q", w.Code, w.Header().Get(swapErrorHeader))
	}
	for _, bad := range []string{"content-rating-empty", "in this library yet"} {
		if strings.Contains(body, bad) {
			t.Errorf("outage rendered as an empty library (%q)", bad)
		}
	}
	if !strings.Contains(body, "content-rating-list-error") || !strings.Contains(body, "module is unavailable") || !strings.Contains(body, "Nothing was changed") {
		t.Errorf("outage not explained: %.600s", body)
	}
}

// A list that was queried and is truly empty still says so.
func TestContentRatingBulkTrulyEmptyLibraryStillSaysSo(t *testing.T) {
	e := newRatingEnv(t, true, false)
	w := e.get(ratingSession("admin"), "/content-ratings")
	body := w.Body.String()
	if !strings.Contains(body, "content-rating-empty") || !strings.Contains(body, "in this library yet") {
		t.Errorf("empty library not reported: %.400s", body)
	}
	if strings.Contains(body, "content-rating-not-loaded") {
		t.Error("a loaded list was shown as not loaded")
	}
}

// A call that ends in an error other than a definite refusal may have been
// committed by the module, so it is audited as uncertain, never as a success.
func TestContentRatingBulkUncertainWritesAreAudited(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(5)
	e.movies.commitThenFail["m001"] = status.Error(codes.DeadlineExceeded, "late")
	e.movies.commitThenFail["m002"] = status.Error(codes.Unavailable, "dropped")
	e.movies.commitThenFail["m003"] = status.Error(codes.Unknown, "who knows")
	e.movies.failures["m004"] = status.Error(codes.InvalidArgument, "refused")
	w := e.post(ratingSession("admin"), ratingForm("movies", "R", "m001", "m002", "m003", "m004", "m005"))
	body := w.Body.String()
	if !strings.Contains(body, "may not have been changed") || strings.Contains(body, `data-id="m001" data-outcome="ok"`) {
		t.Errorf("uncertain outcome misreported: %.600s", body)
	}
	// A failed set is never read back: only the acknowledged m005 is.
	if !reflect.DeepEqual(e.movies.gets, []string{"m005"}) {
		t.Errorf("readbacks = %v, want only the acknowledged write", e.movies.gets)
	}
	got := map[string]auditRecord{}
	for _, rec := range e.auditRecords() {
		got[rec.ResourceID] = rec
	}
	if len(got) != 5 {
		t.Errorf("audit records = %d, want one per attempt (5)", len(got))
	}
	for id, want := range map[string]struct{ outcome, reason string }{
		"m001": {"uncertain", "DeadlineExceeded"}, "m002": {"uncertain", "Unavailable"}, "m003": {"uncertain", "Unknown"},
		"m004": {"refused", "InvalidArgument"}, "m005": {"confirmed", "readback_matched"},
	} {
		rec := got[id]
		details := map[string]string{
			"module": "media-movies", "outcome": want.outcome, "reason": want.reason,
			"requested_rating": "R", "requested_source": "operator",
		}
		if want.outcome == "confirmed" {
			details["content_rating"], details["source"] = "R", "operator"
		}
		if rec.Actor != "u-admin" || rec.Action != contentRatingAuditAction || rec.Resource != "media_item" || !reflect.DeepEqual(rec.Details, details) {
			t.Errorf("%s: audit = %+v, want details %v", id, rec, details)
		}
		// Stored values may appear only for a confirmed outcome.
		if _, ok := rec.Details["content_rating"]; ok != (want.outcome == "confirmed") {
			t.Errorf("%s: content_rating present=%v for outcome %s", id, ok, want.outcome)
		}
	}
}

// An acknowledgement is empty and proves nothing: the title is read back, and
// anything but the requested classification is "not confirmed", with requested
// (never stored) values in the audit entry.
func TestContentRatingBulkAcknowledgedWriteNeedsMatchingReadback(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(st *ratingModuleState)
		reason string
		msg    string
	}{
		{"readback error", func(st *ratingModuleState) { st.getErr["m001"] = status.Error(codes.Unavailable, "down") }, "readback_unavailable", ""},
		{"stale value", func(st *ratingModuleState) { st.readAs["m001"] = [2]string{"G", "operator"} }, "readback_mismatch", ""},
		{"wrong source", func(st *ratingModuleState) { st.readAs["m001"] = [2]string{"R", "tmdb"} }, "readback_mismatch", ""},
		{"nothing stored", func(st *ratingModuleState) { st.readAs["m001"] = [2]string{"", ""} }, "readback_mismatch", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newRatingEnv(t, true, false)
			e.movies.titles = movieTitles(2)
			e.movies.getErr, e.movies.readAs = map[string]error{}, map[string][2]string{}
			c.setup(e.movies)
			w := e.post(ratingSession("admin"), ratingForm("movies", "R", "m001", "m002"))
			body := w.Body.String()
			if w.Code != http.StatusOK {
				t.Fatalf("status %d", w.Code)
			}
			if !strings.Contains(body, `data-id="m001" data-outcome="uncertain"`) || strings.Contains(body, `data-id="m001" data-outcome="ok"`) {
				t.Errorf("an unconfirmed save was reported as changed: %.800s", body)
			}
			// The unconfirmed title does not stop the batch; its neighbour is confirmed.
			if !strings.Contains(body, `data-id="m002" data-outcome="ok"`) || !strings.HasPrefix(ratingSummary(t, body), "1 changed, 1 not confirmed.") {
				t.Errorf("summary/neighbour wrong: %.800s", body)
			}
			if !strings.Contains(body, "acknowledged, but this title&#39;s current rating could not be confirmed") {
				t.Error("unconfirmed message missing")
			}
			// Exactly one set and one readback per title: never a retried write.
			if !reflect.DeepEqual(e.movies.order[:4], []string{"set:m001", "get:m001", "set:m002", "get:m002"}) {
				t.Errorf("rpc order = %v", e.movies.order)
			}
			recs := e.auditRecords()
			if len(recs) != 2 {
				t.Fatalf("audit records = %d, want 2", len(recs))
			}
			want := map[string]string{
				"module": "media-movies", "outcome": "uncertain", "reason": c.reason,
				"requested_rating": "R", "requested_source": "operator",
			}
			if recs[0].ResourceID != "m001" || !reflect.DeepEqual(recs[0].Details, want) {
				t.Errorf("m001 audit = %+v, want details %v", recs[0], want)
			}
			// No stored classification may be claimed by an unconfirmed audit.
			for _, k := range []string{"content_rating", "source"} {
				if _, ok := recs[0].Details[k]; ok {
					t.Errorf("unconfirmed audit carries %s", k)
				}
			}
			if recs[1].ResourceID != "m002" || recs[1].Details["outcome"] != "confirmed" {
				t.Errorf("m002 audit = %+v", recs[1])
			}
		})
	}
}

// NR and Clear are confirmed against what the module reports for them, not
// against the literal request: NR stores "NR"/operator, Clear stores nothing.
func TestContentRatingBulkReadbackExpectationPerChoice(t *testing.T) {
	for _, tc := range []struct {
		choice, readRating, readSource string
		wantOutcome                    string
	}{
		{"NR", "NR", "operator", "ok"},
		{"NR", "", "operator", "uncertain"},
		{"CLEAR", "", "", "ok"},
		{"CLEAR", "", "operator", "uncertain"},
		{"CLEAR", "R", "operator", "uncertain"},
		{"PG-13", "PG-13", "operator", "ok"},
		{"PG-13", "pg-13", "operator", "uncertain"}, // exact, as on the per-item page
	} {
		t.Run(tc.choice+"/"+tc.readRating+"/"+tc.readSource, func(t *testing.T) {
			e := newRatingEnv(t, false, true)
			e.tv.titles = []ratingTitle{{ID: "s1", Title: "Show"}}
			e.tv.getErr, e.tv.readAs = map[string]error{}, map[string][2]string{"s1": {tc.readRating, tc.readSource}}
			w := e.post(ratingSession("admin"), ratingForm("tv", tc.choice, "s1"))
			if !strings.Contains(w.Body.String(), `data-id="s1" data-outcome="`+tc.wantOutcome+`"`) {
				t.Errorf("outcome != %s: %.600s", tc.wantOutcome, w.Body.String())
			}
		})
	}
}

// A revoked bearer or a refusing provider ends the batch: the offending title is
// audited as refused, every earlier attempt keeps its audit entry, the rest are
// reported not attempted, no further RPC of any kind is made (no readback, no
// list refresh) and the response is the matching auth status with the partial
// results still rendered.
func TestContentRatingBulkAuthFailureStopsTheBatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		code codes.Code
		want int
	}{
		{"unauthenticated", codes.Unauthenticated, http.StatusUnauthorized},
		{"permission denied", codes.PermissionDenied, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRatingEnv(t, true, false)
			e.movies.titles = movieTitles(4)
			e.movies.failures["m002"] = status.Error(tc.code, "secret provider text")
			w := e.post(ratingSession("admin"), ratingForm("movies", "R", "m001", "m002", "m003", "m004"))
			body := w.Body.String()
			if w.Code != tc.want || w.Header().Get(swapErrorHeader) != "1" {
				t.Fatalf("status %d swap %q, want %d with swap header", w.Code, w.Header().Get(swapErrorHeader), tc.want)
			}
			if strings.Contains(body, "secret provider text") {
				t.Error("provider text leaked")
			}
			for id, outcome := range map[string]string{"m001": "ok", "m002": "failed", "m003": "not-attempted", "m004": "not-attempted"} {
				if !strings.Contains(body, `data-id="`+id+`" data-outcome="`+outcome+`"`) {
					t.Errorf("%s should be %s: %.900s", id, outcome, body)
				}
			}
			if got := ratingSummary(t, body); !strings.HasPrefix(got, "1 changed, 1 failed, 2 not attempted.") {
				t.Errorf("partial summary = %q", got)
			}
			if strings.Contains(body, "in this library yet") {
				t.Error("stopped batch rendered as an empty library")
			}
			// The only RPCs: set+readback of m001, then the refused set of m002.
			if want := []string{"set:m001", "get:m001", "set:m002"}; !reflect.DeepEqual(e.movies.order, want) {
				t.Errorf("rpc order = %v, want %v", e.movies.order, want)
			}
			recs := e.auditRecords()
			if len(recs) != 2 || recs[0].ResourceID != "m001" || recs[0].Details["outcome"] != "confirmed" ||
				recs[1].ResourceID != "m002" || recs[1].Details["outcome"] != "refused" || recs[1].Details["reason"] != tc.code.String() {
				t.Errorf("audits = %+v", recs)
			}
		})
	}
}

// The credential can also be revoked between an acknowledged write and its
// readback. The write may have landed, so it is uncertain and audited, and the
// batch ends there.
func TestContentRatingBulkAuthFailureOnReadbackStopsTheBatch(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(3)
	e.movies.getErr, e.movies.readAs = map[string]error{"m001": status.Error(codes.Unauthenticated, "revoked")}, nil
	w := e.post(ratingSession("admin"), ratingForm("movies", "R", "m001", "m002", "m003"))
	body := w.Body.String()
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	for id, outcome := range map[string]string{"m001": "uncertain", "m002": "not-attempted", "m003": "not-attempted"} {
		if !strings.Contains(body, `data-id="`+id+`" data-outcome="`+outcome+`"`) {
			t.Errorf("%s should be %s", id, outcome)
		}
	}
	if want := []string{"set:m001", "get:m001"}; !reflect.DeepEqual(e.movies.order, want) {
		t.Errorf("rpc order = %v, want %v", e.movies.order, want)
	}
	recs := e.auditRecords()
	if len(recs) != 1 || recs[0].Details["outcome"] != "uncertain" || recs[0].Details["reason"] != "readback_unavailable" {
		t.Errorf("audits = %+v", recs)
	}
}

// A cancelled request stops the batch, audits what was attempted and does not
// lose the entry for the write that was in flight.
func TestContentRatingBulkCancelledRequestKeepsAttemptedAudits(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(4)
	ctx, cancel := context.WithCancel(context.Background())
	e.movies.afterSet = func(id string) {
		if id == "m002" {
			cancel()
			// Let the client observe the cancellation before this reply.
			time.Sleep(100 * time.Millisecond)
		}
	}
	form := ratingForm("movies", "R", "m001", "m002", "m003", "m004")
	r := httptest.NewRequest(http.MethodPost, "/content-ratings", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(staleIdentity(context.WithValue(ctx, ctxSessionKey, ratingSession("admin"))))
	w := newDeadlineRecorder()
	e.h.ContentRatingsBulkApply(w, r)
	_ = w // the caller is gone, so the page itself cannot be delivered
	for _, op := range e.movies.order {
		if op == "set:m003" || op == "set:m004" {
			t.Errorf("RPC %s after cancellation", op)
		}
	}
	if want := []string{"set:m001", "get:m001", "set:m002"}; !reflect.DeepEqual(e.movies.order[:3], want) {
		t.Errorf("rpc order = %v, want prefix %v", e.movies.order, want)
	}
	got := map[string]string{}
	for _, rec := range e.auditRecords() {
		got[rec.ResourceID] = rec.Details["outcome"]
	}
	if want := (map[string]string{"m001": "confirmed", "m002": "uncertain"}); !reflect.DeepEqual(got, want) {
		t.Errorf("audits = %v, want %v (one per attempt, none for unattempted)", got, want)
	}
}

// The full apply budget needs a longer write deadline, granted once, after the
// admin gate and validation, and covering the apply, the refresh and the render
// margin.
func TestContentRatingBulkApplyExtendsTheWriteDeadlineOnlyWhenItWorks(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(1)
	before := time.Now()
	w := e.post(ratingSession("admin"), ratingForm("movies", "G", "m001"))
	if len(w.deadlines) != 1 {
		t.Fatalf("write deadline set %d times, want 1", len(w.deadlines))
	}
	want := contentRatingApplyTimeout + contentRatingReadTimeout + contentRatingRenderMargin
	if got := w.deadlines[0].Sub(before); got < want || got > want+5*time.Second {
		t.Errorf("deadline is %v ahead, want about %v", got, want)
	}
	if !strings.Contains(w.Body.String(), `data-id="m001" data-outcome="ok"`) {
		t.Error("the apply did not run")
	}

	// Refused submissions get no extension.
	for name, form := range map[string]url.Values{
		"bad choice": ratingForm("movies", "nope", "m001"),
		"no ids":     {"kind": {"movies"}, "rating": {"G"}},
		"bad kind":   {"kind": {"x"}, "rating": {"G"}, "ids": {"m001"}},
	} {
		if w := e.post(ratingSession("admin"), form); len(w.deadlines) != 0 {
			t.Errorf("%s: deadline extended for a refused submission", name)
		}
	}
}

// Only admins ever get the longer deadline: the gate runs first.
func TestContentRatingBulkNonAdminNeverExtendsTheWriteDeadline(t *testing.T) {
	for _, roles := range [][]string{{"manager"}, {"user"}, {"viewer"}, nil} {
		e := newRatingEnv(t, true, true)
		e.movies.titles = movieTitles(1)
		w := e.post(ratingSession(roles...), ratingForm("movies", "G", "m001"))
		if w.Code != http.StatusForbidden || len(w.deadlines) != 0 {
			t.Errorf("roles %v: status %d, deadlines %d", roles, w.Code, len(w.deadlines))
		}
		if e.disc.count() != 0 || e.movies.callCount() != 0 || len(e.auditRecords()) != 0 {
			t.Errorf("roles %v: reached a module", roles)
		}
	}
	e := newRatingEnv(t, true, false)
	w := e.postAnonymous()
	if w.Code != http.StatusForbidden || len(w.deadlines) != 0 {
		t.Errorf("no session: status %d, deadlines %d", w.Code, len(w.deadlines))
	}
}

func (e *ratingEnv) postAnonymous() *deadlineRecorder {
	return e.post(nil, ratingForm("movies", "G", "m001"))
}

// A writer that cannot take a deadline (a plain recorder, or a wrapper that
// hides the connection) leaves the time before the server's own deadline
// unknown. The apply is then refused before any dial or write, with an error
// the browser will show.
func TestContentRatingBulkRefusesBeforeAnyWriteWhenDeadlineCannotBeExtended(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(2)
	w := httptest.NewRecorder() // no SetWriteDeadline, no Unwrap
	e.postTo(w, ratingSession("admin"), ratingForm("movies", "G", "m001", "m002"))
	body := w.Body.String()
	if w.Code != http.StatusInternalServerError || w.Header().Get(swapErrorHeader) != "1" {
		t.Fatalf("status = %d, swap header %q", w.Code, w.Header().Get(swapErrorHeader))
	}
	if !strings.Contains(body, "could not extend") && !strings.Contains(body, "cannot extend") {
		t.Errorf("reason missing: %.500s", body)
	}
	if !strings.Contains(body, "Nothing was changed") || strings.Contains(body, "in this library yet") {
		t.Errorf("unclear or misleading page: %.500s", body)
	}
	if e.disc.count() != 0 || e.movies.callCount() != 0 || len(e.auditRecords()) != 0 {
		t.Error("a refused apply reached discovery, the module or the audit log")
	}
}

// A writer whose deadline call fails is refused the same way.
type failingDeadlineWriter struct{ *httptest.ResponseRecorder }

func (failingDeadlineWriter) SetWriteDeadline(time.Time) error { return fmt.Errorf("connection gone") }

func TestContentRatingBulkRefusesBeforeAnyWriteWhenSettingDeadlineFails(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(1)
	w := failingDeadlineWriter{httptest.NewRecorder()}
	e.postTo(w, ratingSession("admin"), ratingForm("movies", "G", "m001"))
	if w.Code != http.StatusInternalServerError || w.Header().Get(swapErrorHeader) != "1" {
		t.Fatalf("status = %d", w.Code)
	}
	if e.disc.count() != 0 || e.movies.callCount() != 0 || len(e.auditRecords()) != 0 {
		t.Error("a refused apply reached discovery, the module or the audit log")
	}
}

// The module performs no authorization, so what it receives is the only
// attribution it has: exactly the bearer requireAuth validated, on lists,
// writes and the refresh alike, and none of the inherited identity headers.
func TestContentRatingBulkForwardsOnlyTheValidatedBearer(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(2)
	admin := ratingSession("admin")
	if g := e.get(admin, "/content-ratings"); g.Code != http.StatusOK {
		t.Fatalf("GET status = %d", g.Code)
	}
	if w := e.post(admin, ratingForm("movies", "G", "m001")); w.Code != http.StatusOK {
		t.Fatalf("POST status = %d", w.Code)
	}
	e.movies.mu.Lock()
	defer e.movies.mu.Unlock()
	if len(e.movies.identities) != 4 { // GET list, POST set, POST readback, POST refresh list
		t.Fatalf("module calls = %d, want 4 (GET list, set, readback, refresh list)", len(e.movies.identities))
	}
	for i, md := range e.movies.identities {
		if got := md.Get("authorization"); !reflect.DeepEqual(got, []string{"Bearer " + ratingBearer}) {
			t.Errorf("call %d authorization = %v", i, got)
		}
		for _, key := range []string{"x-auth-token", "x-muxcore-user-id", "x-muxcore-tenant-id"} {
			if len(md.Get(key)) != 0 {
				t.Errorf("call %d forwarded inherited %s", i, key)
			}
		}
	}
}

// A session with no provider bearer (a local-only session) cannot be attributed
// to a module call. Like the per-item page, bulk refuses before it resolves a
// module, extends a deadline, writes or audits.
func TestContentRatingBulkRefusesAnAdminSessionWithoutABearer(t *testing.T) {
	e := newRatingEnv(t, true, false)
	e.movies.titles = movieTitles(1)
	local := ratingSession("admin")
	local.AuthLocalToken = ""

	g := e.get(local, "/content-ratings")
	if g.Code != http.StatusUnauthorized || g.Header().Get(swapErrorHeader) != "1" || !strings.Contains(g.Body.String(), "Sign in again") {
		t.Fatalf("GET = %d swap %q: %.300s", g.Code, g.Header().Get(swapErrorHeader), g.Body.String())
	}
	if strings.Contains(g.Body.String(), "in this library yet") {
		t.Error("a refused list rendered as an empty library")
	}
	w := e.post(local, ratingForm("movies", "G", "m001"))
	if w.Code != http.StatusUnauthorized || w.Header().Get(swapErrorHeader) != "1" ||
		!strings.Contains(w.Body.String(), "Sign in again") || !strings.Contains(w.Body.String(), "Nothing was changed") {
		t.Fatalf("POST = %d swap %q: %.300s", w.Code, w.Header().Get(swapErrorHeader), w.Body.String())
	}
	if len(w.deadlines) != 0 {
		t.Error("a refused apply extended the write deadline")
	}
	if e.disc.count() != 0 || e.movies.callCount() != 0 || len(e.auditRecords()) != 0 {
		t.Error("a refused request reached discovery, the module or the audit log")
	}
}

// Bulk and per-item changes of the same classification leave the same audit
// entry (action, resource and details), so one query finds both.
func TestContentRatingBulkAuditMatchesThePerItemPage(t *testing.T) {
	for _, tc := range []struct{ kind, module, bulkChoice, itemChoice string }{
		{"movies", "media-movies", "PG-13", "PG-13"},
		{"movies", "media-movies", "NR", "unrated"},
		{"tv", "media-tvshows", "CLEAR", "clear"},
		{"tv", "media-tvshows", "TV-MA", "TV-MA"},
	} {
		t.Run(tc.module+"/"+tc.bulkChoice, func(t *testing.T) {
			var perItem []auditRecord
			f := &ratingFixture{rating: "R", source: "operator"}
			h := newRatingHandler(t, f)
			h.AuditHook = func(actor, action, resource, id string, details map[string]string) {
				perItem = append(perItem, auditRecord{actor, action, resource, id, details})
			}
			w := httptest.NewRecorder()
			h.ContentRatingSave(w, ratingRequest("POST", tc.module, url.Values{"classification": {tc.itemChoice}}.Encode(), []string{"admin"}, "current-bearer"))
			if w.Code != http.StatusOK || len(perItem) != 1 {
				t.Fatalf("per-item save status=%d audits=%d", w.Code, len(perItem))
			}

			e := newRatingEnv(t, true, true)
			st := e.movies
			if tc.kind == "tv" {
				st = e.tv
			}
			st.titles = []ratingTitle{{ID: "fixture"}}
			if w := e.post(ratingSession("admin"), ratingForm(tc.kind, tc.bulkChoice, "fixture")); w.Code != http.StatusOK {
				t.Fatalf("bulk status = %d", w.Code)
			}
			bulk := e.auditRecords()
			if len(bulk) != 1 {
				t.Fatalf("bulk audits = %d", len(bulk))
			}
			a, b := perItem[0], bulk[0]
			if a.Action != b.Action || a.Resource != b.Resource || a.ResourceID != b.ResourceID || !reflect.DeepEqual(a.Details, b.Details) {
				t.Errorf("per-item audit %+v differs from bulk audit %+v", a, b)
			}
		})
	}
}
