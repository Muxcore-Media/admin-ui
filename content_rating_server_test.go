package adminui

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/handler"
	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

// slowMovies is a movie module whose SetContentRating takes a fixed time, the
// way a loaded module does.
type slowMovies struct {
	mgmntv1.UnimplementedMovieManagementServiceServer
	delay time.Duration
	mu    sync.Mutex
	sets  []string
}

func (m *slowMovies) ListMovies(context.Context, *mgmntv1.ListMoviesRequest) (*mgmntv1.ListMoviesResponse, error) {
	return &mgmntv1.ListMoviesResponse{Total: 3, Movies: []*mgmntv1.MovieItem{
		{Id: "m1", Title: "One"}, {Id: "m2", Title: "Two"}, {Id: "m3", Title: "Three"},
	}}, nil
}

func (m *slowMovies) SetContentRating(_ context.Context, req *mgmntv1.SetContentRatingRequest) (*mgmntv1.SetContentRatingResponse, error) {
	time.Sleep(m.delay)
	m.mu.Lock()
	m.sets = append(m.sets, req.GetMovieId())
	m.mu.Unlock()
	return &mgmntv1.SetContentRatingResponse{}, nil
}

func (m *slowMovies) setCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sets)
}

type allowAll struct {
	authv1.UnimplementedAuthServiceServer
}

func (allowAll) Can(context.Context, *authv1.CanRequest) (*authv1.CanResponse, error) {
	return &authv1.CanResponse{Allowed: true}, nil
}

type fixedDiscovery struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	addrs map[string]string
}

func (d fixedDiscovery) FindByCapability(_ context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	addr := d.addrs[req.GetCapability()]
	if addr == "" {
		return &discoveryv1.FindByCapabilityResponse{}, nil
	}
	return &discoveryv1.FindByCapabilityResponse{Modules: []*discoveryv1.ModuleInfoProto{{Id: "module", HttpAddr: addr}}}, nil
}

func serveTestGRPC(t *testing.T, register func(*grpc.Server)) string {
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

// ratingServer is the real route behind the real middleware chain on a real
// HTTP server. A recorder cannot show write-deadline behaviour: only a net/http
// connection has one.
type ratingServer struct {
	srv    *httptest.Server
	movies *slowMovies
	cookie string
	mu     sync.Mutex
	audits int
}

// startRatingServer serves withMiddleware(mux) with the given WriteTimeout. wrap,
// if set, puts an extra layer around the middleware chain.
func startRatingServer(t *testing.T, role string, writeTimeout time.Duration, wrap func(http.Handler) http.Handler) *ratingServer {
	t.Helper()
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")

	movies := &slowMovies{delay: 600 * time.Millisecond}
	moviesAddr := serveTestGRPC(t, func(s *grpc.Server) { mgmntv1.RegisterMovieManagementServiceServer(s, movies) })
	authAddr := serveTestGRPC(t, func(s *grpc.Server) { authv1.RegisterAuthServiceServer(s, allowAll{}) })
	discAddr := serveTestGRPC(t, func(s *grpc.Server) {
		discoveryv1.RegisterDiscoveryServiceServer(s, fixedDiscovery{addrs: map[string]string{
			"media.library.movies": moviesAddr,
			"authorizer":           authAddr,
		}})
	})
	core, err := client.Dial(discAddr, client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	store := session.NewStore(time.Hour)
	token, err := store.Create("u-"+role, role, []string{role}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := handler.New(nil, store, false, "test", nil, true, "", nil, nil)
	h.Core = core
	rs := &ratingServer{movies: movies, cookie: token}
	h.AuditHook = func(_, _, _, _ string, _ map[string]string) {
		rs.mu.Lock()
		rs.audits++
		rs.mu.Unlock()
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	var root http.Handler = withMiddleware(mux, newRateLimiter(), parseTrustedProxies(nil), "")
	if wrap != nil {
		root = wrap(root)
	}
	rs.srv = httptest.NewUnstartedServer(root)
	rs.srv.Config.ReadTimeout = 15 * time.Second
	rs.srv.Config.WriteTimeout = writeTimeout
	rs.srv.Start()
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *ratingServer) auditCount() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.audits
}

// apply posts a three-title apply with a valid session and CSRF pair.
func (rs *ratingServer) apply(t *testing.T) (status int, body string, elapsed time.Duration, err error) {
	t.Helper()
	form := url.Values{"kind": {"movies"}, "rating": {"PG"}, "ids": {"m1", "m2", "m3"}}
	req, err := http.NewRequest(http.MethodPost, rs.srv.URL+"/content-ratings", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", "csrf")
	req.AddCookie(&http.Cookie{Name: "session", Value: rs.cookie})
	req.AddCookie(&http.Cookie{Name: "csrf-token", Value: "csrf"})
	start := time.Now()
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, "", time.Since(start), err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), time.Since(start), err
}

// All three writes take 3 x 600ms against a 1s WriteTimeout; the browser must
// still receive the per-title results.
func TestContentRatingApplyOutlivesServerWriteTimeout(t *testing.T) {
	rs := startRatingServer(t, "admin", time.Second, nil)
	code, text, elapsed, err := rs.apply(t)
	if err != nil {
		t.Fatalf("browser got no complete response after %v although %d writes were made: %v", elapsed, rs.movies.setCount(), err)
	}
	if elapsed < 1800*time.Millisecond {
		t.Fatalf("apply finished in %v; the test no longer outlasts the 1s WriteTimeout", elapsed)
	}
	if code != http.StatusOK {
		t.Fatalf("status = %d: %.300s", code, text)
	}
	for _, id := range []string{"m1", "m2", "m3"} {
		if !strings.Contains(text, `data-id="`+id+`" data-outcome="ok"`) {
			t.Errorf("no result for %s: %.800s", id, text)
		}
	}
	if rs.movies.setCount() != 3 || rs.auditCount() != 3 {
		t.Errorf("writes = %d, audit records = %d, want 3 and 3", rs.movies.setCount(), rs.auditCount())
	}
}

// hideConnection wraps a handler so the response writer exposes neither
// SetWriteDeadline nor Unwrap, like a wrapper that does not forward them.
func hideConnection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(struct{ http.ResponseWriter }{w}, r)
	})
}

// If the deadline cannot be extended the apply is refused up front: no module is
// written, nothing is audited, and the browser gets a complete, swappable error.
func TestContentRatingRefusedBeforeWritesWhenConnectionIsHidden(t *testing.T) {
	rs := startRatingServer(t, "admin", time.Second, hideConnection)
	code, text, _, err := rs.apply(t)
	if err != nil {
		t.Fatalf("no response: %v", err)
	}
	if code != http.StatusFailedDependency || !strings.Contains(text, "Nothing was changed") {
		t.Fatalf("status = %d: %.500s", code, text)
	}
	if rs.movies.setCount() != 0 || rs.auditCount() != 0 {
		t.Errorf("writes = %d, audit records = %d, want none", rs.movies.setCount(), rs.auditCount())
	}
}

// Non-admins are refused before any module is reached and get no longer
// deadline; the 1s WriteTimeout is not touched for them.
func TestContentRatingNonAdminOverRealServerGetsForbidden(t *testing.T) {
	rs := startRatingServer(t, "manager", time.Second, nil)
	code, _, _, err := rs.apply(t)
	if err != nil {
		t.Fatalf("no response: %v", err)
	}
	if code != http.StatusForbidden || rs.movies.setCount() != 0 || rs.auditCount() != 0 {
		t.Errorf("status = %d, writes = %d, audit records = %d", code, rs.movies.setCount(), rs.auditCount())
	}
}
