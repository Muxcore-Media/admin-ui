package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	movies "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tv "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ratingFixture struct {
	mu                         sync.Mutex
	rating, source, responseID string
	// tmdb is the lower-precedence rating the module falls back to once the
	// operator value is cleared (media-movies and media-tvshows v0.1.24).
	tmdb               string
	readErr, writeErr  codes.Code
	ignoreWrite        bool
	gets, sets         int
	lastID, lastRating string
	lastUnrated        bool
	identities         []metadata.MD
	beforeCall         func(context.Context, bool)
}

func (f *ratingFixture) intercept(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
	var itemID, rating string
	var unrated, write bool
	switch v := req.(type) {
	case *movies.GetMovieRequest:
		itemID = v.GetMovieId()
	case *tv.GetTVShowRequest:
		itemID = v.GetSeriesId()
	case *movies.SetContentRatingRequest:
		itemID, rating, unrated, write = v.GetMovieId(), v.GetContentRating(), v.GetExplicitUnrated(), true
	case *tv.SetContentRatingRequest:
		itemID, rating, unrated, write = v.GetSeriesId(), v.GetContentRating(), v.GetExplicitUnrated(), true
	default:
		return next(ctx, req)
	}
	if f.beforeCall != nil {
		f.beforeCall(ctx, write)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	md, _ := metadata.FromIncomingContext(ctx)
	f.identities = append(f.identities, md.Copy())
	if write {
		f.sets++
		f.lastID, f.lastRating, f.lastUnrated = itemID, rating, unrated
		if f.writeErr != codes.OK {
			return nil, status.Error(f.writeErr, "SECRET upstream detail")
		}
		if !f.ignoreWrite {
			f.rating, f.source = rating, "operator"
			if unrated {
				f.rating = "NR"
			}
			if rating == "" && !unrated {
				f.source = ""
				if f.tmdb != "" {
					f.rating, f.source = f.tmdb, "tmdb"
				}
			}
		}
		if _, ok := req.(*movies.SetContentRatingRequest); ok {
			return &movies.SetContentRatingResponse{}, nil
		}
		return &tv.SetContentRatingResponse{}, nil
	}
	f.gets++
	if f.readErr != codes.OK {
		return nil, status.Error(f.readErr, "SECRET upstream detail")
	}
	id := itemID
	if f.responseID != "" {
		id = f.responseID
	}
	if _, ok := req.(*movies.GetMovieRequest); ok {
		return &movies.GetMovieResponse{Movie: &movies.MovieItem{Id: id, Title: "Fixture <movie>", ContentRating: f.rating, ContentRatingSource: f.source}}, nil
	}
	return &tv.GetTVShowResponse{Series: &tv.TVSeries{Id: id, Name: "Fixture <series>", ContentRating: f.rating, ContentRatingSource: f.source}}, nil
}

func newRatingHandler(t *testing.T, f *ratingFixture) *Handler {
	t.Helper()
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.UnaryInterceptor(f.intercept))
	movies.RegisterMovieManagementServiceServer(srv, &movies.UnimplementedMovieManagementServiceServer{})
	tv.RegisterTvManagementServiceServer(srv, &tv.UnimplementedTvManagementServiceServer{})
	discoveryv1.RegisterDiscoveryServiceServer(srv, mediaResolveDiscovery{addr: lis.Addr().String()})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	core, err := client.Dial(lis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	h := New(nil, session.NewStore(time.Hour), false, "test", nil, true, "", nil, nil)
	h.Core = core
	return h
}

func ratingRequest(method, module, body string, roles []string, bearer string) *http.Request {
	r := httptest.NewRequest(method, "/media/"+module+"/item/fixture/content-rating", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", "Bearer browser-forged")
	r.SetPathValue("moduleID", module)
	r.SetPathValue("id", "fixture")
	ctx := context.WithValue(r.Context(), ctxSessionKey, &session.Session{UserID: "operator", AuthLocalToken: bearer, Roles: roles})
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer stale", "x-auth-token", "stale", "x-muxcore-user-id", "forged", "x-muxcore-tenant-id", "forged"))
	return r.WithContext(ctx)
}

func TestContentRatingRoundTrip(t *testing.T) {
	for _, module := range []string{"media-movies", "media-tvshows"} {
		for _, choice := range []string{"PG-13", "TV-Y7-FV", "unrated", "clear"} {
			t.Run(module+"/"+choice, func(t *testing.T) {
				f := &ratingFixture{rating: "R", source: "operator"}
				h := newRatingHandler(t, f)
				w := httptest.NewRecorder()
				h.ContentRatingSave(w, ratingRequest("POST", module, url.Values{"classification": {choice}}.Encode(), []string{"admin"}, "current-bearer"))
				wantNotice := "saved and checked"
				if choice == "clear" {
					wantNotice = "Operator classification cleared; no classification is available now."
				}
				if w.Code != 200 || !strings.Contains(w.Body.String(), wantNotice) {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				if f.sets != 1 || f.gets != 1 || f.lastID != "fixture" {
					t.Fatalf("set=%d get=%d id=%q", f.sets, f.gets, f.lastID)
				}
				wantRating := choice
				if choice == "clear" || choice == "unrated" {
					wantRating = ""
				}
				if f.lastRating != wantRating || f.lastUnrated != (choice == "unrated") {
					t.Fatalf("rating=%q unrated=%v", f.lastRating, f.lastUnrated)
				}
				for _, md := range f.identities {
					if !reflect.DeepEqual(md.Get("authorization"), []string{"Bearer current-bearer"}) {
						t.Fatalf("authorization=%v", md.Get("authorization"))
					}
					for _, key := range []string{"x-auth-token", "x-muxcore-user-id", "x-muxcore-tenant-id"} {
						if len(md.Get(key)) != 0 {
							t.Errorf("inherited identity %s forwarded", key)
						}
					}
				}
				if module == "media-tvshows" && !strings.Contains(w.Body.String(), "seasons and episodes inherit") {
					t.Fatal("missing inheritance notice")
				}
				if w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "current-bearer") {
					t.Fatal("cache/secret regression")
				}
			})
		}
	}
}

func TestContentRatingStates(t *testing.T) {
	for _, tc := range []struct{ rating, source, want string }{
		{"", "", "Unavailable"}, {"NR", "operator", "Explicit unrated"}, {"UR", "operator", "Explicit unrated"},
		{"PG", "operator", "Rated by an operator"}, {"15", "operator", "Unavailable"}, {"PG", "tmdb", "Rated by TMDB"}, {"NR", "tmdb", "Not rated according to TMDB"}, {"15", "tmdb", "Unavailable"}, {"PG", "", "Unavailable"},
	} {
		t.Run(tc.rating+"/"+tc.source, func(t *testing.T) {
			f := &ratingFixture{rating: tc.rating, source: tc.source}
			h := newRatingHandler(t, f)
			w := httptest.NewRecorder()
			h.ContentRatingPage(w, ratingRequest("GET", "media-movies", "", []string{"manager"}, "bearer"))
			if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), `data-testid="content-rating-form"`) {
				t.Fatal("manager has write form")
			}
			if !strings.Contains(w.Body.String(), "not a security boundary") {
				t.Fatal("missing trust warning")
			}
		})
	}
}

func TestContentRatingRejectsInvalidInputsAndRoles(t *testing.T) {
	for _, tc := range []struct {
		name, body, bearer, module string
		roles                      []string
		code                       int
	}{
		{"manager", "classification=PG", "bearer", "media-movies", []string{"manager"}, 403},
		{"user", "classification=PG", "bearer", "media-tvshows", []string{"user"}, 403},
		{"missing-bearer", "classification=PG", "", "media-movies", []string{"admin"}, 401},
		{"unsupported-module", "classification=PG", "bearer", "media-music", []string{"admin"}, 404},
		{"missing", "", "bearer", "media-movies", []string{"admin"}, 400},
		{"unknown", "classification=15", "bearer", "media-movies", []string{"admin"}, 400},
		{"raw-NR", "classification=NR", "bearer", "media-tvshows", []string{"admin"}, 400},
		{"raw-UR", "classification=UR", "bearer", "media-tvshows", []string{"admin"}, 400},
		{"duplicate", "classification=PG&classification=R", "bearer", "media-movies", []string{"admin"}, 400},
		{"spoofed-source", "classification=PG&source=tmdb", "bearer", "media-movies", []string{"admin"}, 400},
		{"oversized", "classification=" + strings.Repeat("x", 5000), "bearer", "media-movies", []string{"admin"}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &ratingFixture{}
			h := newRatingHandler(t, f)
			w := httptest.NewRecorder()
			h.ContentRatingSave(w, ratingRequest("POST", tc.module, tc.body, tc.roles, tc.bearer))
			if w.Code != tc.code || f.gets+f.sets != 0 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, f.gets+f.sets, w.Body.String())
			}
		})
	}
}

func TestContentRatingErrorsNeverClaimSuccess(t *testing.T) {
	for _, module := range []string{"media-movies", "media-tvshows"} {
		for _, tc := range []struct {
			name     string
			rpcCode  codes.Code
			httpCode int
			definite bool
		}{
			{"authentication", codes.Unauthenticated, 401, true}, {"permission", codes.PermissionDenied, 403, true},
			{"missing", codes.NotFound, 404, true}, {"invalid", codes.InvalidArgument, 400, true},
			{"unsupported", codes.Unimplemented, 501, true}, {"timeout", codes.DeadlineExceeded, 503, false},
			{"unavailable", codes.Unavailable, 503, false}, {"internal", codes.Internal, 503, false},
		} {
			for _, method := range []string{"GET", "POST"} {
				t.Run(module+"/"+method+"/"+tc.name, func(t *testing.T) {
					f := &ratingFixture{}
					if method == "GET" {
						f.readErr = tc.rpcCode
					} else {
						f.writeErr = tc.rpcCode
					}
					h := newRatingHandler(t, f)
					w := httptest.NewRecorder()
					r := ratingRequest(method, module, "classification=PG", []string{"admin"}, "bearer")
					if method == "GET" {
						h.ContentRatingPage(w, r)
					} else {
						h.ContentRatingSave(w, r)
					}
					body := w.Body.String()
					if w.Code != tc.httpCode || strings.Contains(body, "saved and checked") || strings.Contains(body, "SECRET") || strings.Contains(body, `data-testid="content-rating-form"`) {
						t.Fatalf("%d %s", w.Code, body)
					}
					if w.Header().Get(swapErrorHeader) != "1" || w.Header().Get("Cache-Control") != "no-store" {
						t.Fatal("errors need visible no-store response")
					}
					if method == "POST" && (strings.Contains(body, "Nothing was changed") != tc.definite || f.sets != 1 || f.gets != 0) {
						t.Fatalf("incorrect certainty/retry: %s sets=%d gets=%d", body, f.sets, f.gets)
					}
				})
			}
		}
	}
}

func TestContentRatingReadbackMismatch(t *testing.T) {
	for _, f := range []*ratingFixture{{ignoreWrite: true}, {ignoreWrite: true, rating: "PG", source: "tmdb"}, {ignoreWrite: true, rating: "PG"}, {responseID: "other-item"}, {readErr: codes.Unavailable}} {
		h := newRatingHandler(t, f)
		w := httptest.NewRecorder()
		h.ContentRatingSave(w, ratingRequest("POST", "media-movies", "classification=PG", []string{"admin"}, "bearer"))
		if w.Code != 502 || f.sets != 1 || f.gets != 1 || !strings.Contains(w.Body.String(), "could not be confirmed") || strings.Contains(w.Body.String(), "saved and checked") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
}

func TestContentRatingRoutesRequireCurrentAdminAccess(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		for _, roles := range [][]string{{"admin"}, {"manager"}, {"user"}} {
			stub := &identityRPCStub{allowAdmin: true, validateResp: &authv1.ValidateResponse{Valid: true, UserId: "operator", Roles: roles}}
			h := setupIdentityHandler(t, stub)
			cookie, _ := h.Sessions.Create("operator", "Operator", []string{"admin"}, nil)
			h.Sessions.BindAuthLocalToken(cookie, "bearer")
			mux := http.NewServeMux()
			h.RegisterRoutes(mux)
			r := httptest.NewRequest(method, "/media/media-movies/item/fixture/content-rating", strings.NewReader("classification=PG"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if stub.calls["Validate"] != 1 || stub.calls["Can"] != 1 {
				t.Fatalf("route bypassed revalidation/authorization: %v", stub.calls)
			}
			if method == "POST" && roles[0] != "admin" && w.Code != 403 {
				t.Fatalf("downgraded admin status=%d", w.Code)
			}
		}
	}
}

func TestContentRatingRouteAuthorizationRefusal(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		for _, tc := range []struct {
			name                           string
			valid, allowed, cookie, bearer bool
			code                           int
		}{
			{"missing-cookie", true, true, false, false, 303},
			{"revoked-bearer", false, true, true, true, 303},
			{"admin-access-denied", true, false, true, true, 403},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				stub := &identityRPCStub{allowAdmin: tc.allowed, validateResp: &authv1.ValidateResponse{Valid: tc.valid, UserId: "operator", Roles: []string{"admin"}}}
				h := setupIdentityHandler(t, stub)
				mux := http.NewServeMux()
				h.RegisterRoutes(mux)
				r := httptest.NewRequest(method, "/media/media-movies/item/fixture/content-rating", strings.NewReader("classification=PG"))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if tc.cookie {
					cookie, _ := h.Sessions.Create("operator", "Operator", []string{"admin"}, nil)
					if tc.bearer {
						h.Sessions.BindAuthLocalToken(cookie, "bearer")
					}
					r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != tc.code {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				if tc.name == "admin-access-denied" && (stub.calls["Can"] != 1 || !strings.Contains(w.Body.String(), "permission to access this page")) {
					t.Fatalf("wrong refusal calls=%v body=%s", stub.calls, w.Body.String())
				}
			})
		}
	}
}

func TestContentRatingRegisteredRouteBoundsBody(t *testing.T) {
	stub := &identityRPCStub{allowAdmin: true, validateResp: &authv1.ValidateResponse{Valid: true, UserId: "operator", Roles: []string{"admin"}}}
	h := setupIdentityHandler(t, stub)
	cookie, _ := h.Sessions.Create("operator", "Operator", []string{"admin"}, nil)
	h.Sessions.BindAuthLocalToken(cookie, "bearer")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	r := httptest.NewRequest("POST", "/media/media-movies/item/fixture/content-rating", strings.NewReader("classification="+strings.Repeat("x", 5000)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 400 || stub.calls["Validate"] != 1 || stub.calls["Can"] != 1 || !strings.Contains(w.Body.String(), "Nothing was changed") {
		t.Fatalf("status=%d calls=%v body=%s", w.Code, stub.calls, w.Body.String())
	}
}
