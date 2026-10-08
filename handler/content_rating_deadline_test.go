package handler

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	movies "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
	tv "github.com/Muxcore-Media/media-tvshows/proto/tvmgmtv1"
	"google.golang.org/grpc"
)

type ratingRouteDiscovery struct{ mediaResolveDiscovery }

func (d ratingRouteDiscovery) FindByCapability(context.Context, *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	return &discoveryv1.FindByCapabilityResponse{Modules: []*discoveryv1.ModuleInfoProto{{Id: "fixture", HttpAddr: d.addr}}}, nil
}

func newRatingRouteHandler(t *testing.T, f *ratingFixture, onAuth func(context.Context, string)) (*Handler, string) {
	t.Helper()
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		switch req.(type) {
		case *authv1.ValidateRequest:
			if onAuth != nil {
				onAuth(ctx, "Validate")
			}
			return &authv1.ValidateResponse{Valid: true, UserId: "operator", Roles: []string{"admin"}}, nil
		case *authv1.CanRequest:
			if onAuth != nil {
				onAuth(ctx, "Can")
			}
			return &authv1.CanResponse{Allowed: true}, nil
		}
		return f.intercept(ctx, req, info, next)
	}))
	authv1.RegisterAuthServiceServer(srv, &authv1.UnimplementedAuthServiceServer{})
	movies.RegisterMovieManagementServiceServer(srv, &movies.UnimplementedMovieManagementServiceServer{})
	tv.RegisterTvManagementServiceServer(srv, &tv.UnimplementedTvManagementServiceServer{})
	discoveryv1.RegisterDiscoveryServiceServer(srv, ratingRouteDiscovery{mediaResolveDiscovery{addr: lis.Addr().String()}})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	core, err := client.Dial(lis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	h := New(nil, session.NewStore(time.Hour), false, "test", nil, true, "", nil, nil)
	h.Core = core
	cookie, _ := h.Sessions.Create("operator", "Operator", []string{"admin"}, nil)
	h.Sessions.BindAuthLocalToken(cookie, "current-bearer")
	return h, cookie
}

func TestContentRatingRouteDeadlineReturnsUsableResponse(t *testing.T) {
	for _, module := range []string{"media-movies", "media-tvshows"} {
		for _, phase := range []string{"Validate", "Can", "read", "write", "readback"} {
			t.Run(module+"/"+phase, func(t *testing.T) {
				var validates, authorizations, reads, writes atomic.Int32
				f := &ratingFixture{beforeCall: func(ctx context.Context, write bool) {
					if write {
						writes.Add(1)
					} else {
						reads.Add(1)
					}
					if (write && phase == "write") || (!write && (phase == "read" || phase == "readback")) {
						<-ctx.Done()
					}
				}}
				h, cookie := newRatingRouteHandler(t, f, func(ctx context.Context, method string) {
					if method == "Validate" {
						validates.Add(1)
					} else {
						authorizations.Add(1)
					}
					if method == phase {
						<-ctx.Done()
					} else {
						// Earlier identity phases consume the same total budget.
						timer := time.NewTimer(15 * time.Millisecond)
						defer timer.Stop()
						select {
						case <-timer.C:
						case <-ctx.Done():
						}
					}
				})
				audits := make(chan map[string]string, 2)
				h.AuditHook = func(_, _, _, _ string, details map[string]string) { audits <- details }
				mux := http.NewServeMux()
				h.registerContentRatingRoutes(mux, 150*time.Millisecond)
				srv := httptest.NewUnstartedServer(mux)
				srv.Config.WriteTimeout = 600 * time.Millisecond
				srv.Start()
				defer srv.Close()
				method, body := "GET", ""
				if phase == "write" || phase == "readback" {
					method, body = "POST", "classification=PG"
				}
				req, _ := http.NewRequest(method, srv.URL+"/media/"+module+"/item/fixture/content-rating", strings.NewReader(body))
				req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				started := time.Now()
				httpClient := &http.Client{Timeout: time.Second}
				resp, err := httpClient.Do(req)
				if err != nil {
					t.Fatalf("deadline failed to return usable HTTP response: %v", err)
				}
				defer func() { _ = resp.Body.Close() }()
				result, err := io.ReadAll(resp.Body)
				if err != nil || len(result) == 0 || time.Since(started) > 500*time.Millisecond || resp.StatusCode < 500 {
					t.Fatalf("late/empty response: status=%d elapsed=%s body=%q error=%v", resp.StatusCode, time.Since(started), result, err)
				}
				if validates.Load() != 1 || (phase == "Validate" && authorizations.Load() != 0) || (phase != "Validate" && authorizations.Load() != 1) {
					t.Fatalf("wrong identity calls: validate=%d can=%d", validates.Load(), authorizations.Load())
				}
				wantWrites, wantReads := int32(0), int32(0)
				if method == "POST" {
					wantWrites = 1
					select {
					case audit := <-audits:
						if audit["outcome"] != "uncertain" {
							t.Errorf("deadline audit=%v", audit)
						}
					case <-time.After(time.Second):
						t.Fatal("missing uncertain audit")
					}
				}
				if phase == "read" || phase == "readback" {
					wantReads = 1
				}
				if writes.Load() != wantWrites || reads.Load() != wantReads || len(audits) != 0 {
					t.Fatalf("late/retried operations: writes=%d reads=%d audits=%d", writes.Load(), reads.Load(), len(audits))
				}
				if method == "POST" && (!strings.Contains(string(result), "Reload and check") || strings.Contains(string(result), "saved and checked")) {
					t.Fatalf("deadline lost uncertainty: %s", result)
				}
			})
		}
	}
}

type ratingDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *ratingDeadlineRecorder) SetReadDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		w.deadline = deadline
	}
	return nil
}

func TestContentRatingProductionRoutesStartBudgetBeforeAuth(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		h, _ := newRatingRouteHandler(t, &ratingFixture{}, nil)
		mux := http.NewServeMux()
		h.RegisterRoutes(mux)
		w := &ratingDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		started := time.Now()
		mux.ServeHTTP(w, httptest.NewRequest(method, "/media/media-movies/item/fixture/content-rating", strings.NewReader("classification=PG")))
		remaining := w.deadline.Sub(started)
		if w.Code != http.StatusSeeOther || remaining < 11*time.Second || remaining > 12*time.Second+time.Second/2 {
			t.Fatalf("%s missing pre-auth budget: status=%d deadline=%s", method, w.Code, remaining)
		}
	}
}

func TestContentRatingSlowBodyUsesRequestBudget(t *testing.T) {
	f := &ratingFixture{}
	h, cookie := newRatingRouteHandler(t, f, nil)
	var auditCount atomic.Int32
	h.AuditHook = func(_, _, _, _ string, _ map[string]string) { auditCount.Add(1) }
	mux := http.NewServeMux()
	h.registerContentRatingRoutes(mux, 150*time.Millisecond)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.WriteTimeout = 600 * time.Millisecond
	srv.Start()
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, err = io.WriteString(conn, "POST /media/media-movies/item/fixture/content-rating HTTP/1.1\r\nHost: fixture\r\nCookie: session="+cookie+"\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 17\r\n\r\nclassification=")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil || resp.StatusCode != 503 || !strings.Contains(string(body), "timed out before saving") || !strings.Contains(string(body), "</html>") || auditCount.Load() != 0 || f.gets != 0 || f.sets != 0 {
		t.Fatalf("slow-body response status=%d audit=%d body=%q err=%v", resp.StatusCode, auditCount.Load(), body, err)
	}
}

func TestContentRatingBudgetHonorsEarlierCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	w := &ratingDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	r := httptest.NewRequest("POST", "/", strings.NewReader("classification=PG")).WithContext(ctx)
	contentRatingRequest(func(_ http.ResponseWriter, r *http.Request) {
		want, _ := ctx.Deadline()
		got, _ := r.Context().Deadline()
		if !got.Equal(want) || !w.deadline.Equal(want) {
			t.Errorf("caller deadline extended: operation=%s read=%s caller=%s", got, w.deadline, want)
		}
	}, time.Second)(w, r)
}

func TestContentRatingEarlyRefusalDoesNotDrainSlowBody(t *testing.T) {
	for _, tc := range []struct {
		name, contentType string
		signIn            bool
		code              int
	}{
		{"missing-cookie", "application/x-www-form-urlencoded", false, 303},
		{"unsupported-form-type", "text/plain", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &ratingFixture{}
			var authCalls atomic.Int32
			h, cookie := newRatingRouteHandler(t, f, func(context.Context, string) { authCalls.Add(1) })
			mux := http.NewServeMux()
			h.registerContentRatingRoutes(mux, 150*time.Millisecond)
			srv := httptest.NewUnstartedServer(mux)
			srv.Config.WriteTimeout = 600 * time.Millisecond
			srv.Start()
			defer srv.Close()
			conn, err := net.Dial("tcp", srv.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			headers := "POST /media/media-movies/item/fixture/content-rating HTTP/1.1\r\nHost: fixture\r\nContent-Type: " + tc.contentType + "\r\nContent-Length: 17\r\n"
			if tc.signIn {
				headers += "Cookie: session=" + cookie + "\r\n"
			}
			if _, err := io.WriteString(conn, headers+"\r\nclassification="); err != nil {
				t.Fatal(err)
			}
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if _, err := io.ReadAll(resp.Body); err != nil || resp.StatusCode != tc.code || !resp.Close {
				t.Fatalf("early refusal failed: status=%d close=%v err=%v", resp.StatusCode, resp.Close, err)
			}
			wantAuthCalls := int32(0)
			if tc.signIn {
				wantAuthCalls = 2
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if authCalls.Load() != wantAuthCalls || f.gets != 0 || f.sets != 0 {
				t.Fatalf("unexpected calls: auth=%d reads=%d writes=%d", authCalls.Load(), f.gets, f.sets)
			}
		})
	}
}

func TestContentRatingRenderKeepsCallerCancellation(t *testing.T) {
	h := newRatingHandler(t, &ratingFixture{})
	ctx, cancel := context.WithCancel(context.Background())
	r := ratingRequest("GET", "media-movies", "", []string{"admin"}, "bearer").WithContext(ctx)
	w := httptest.NewRecorder()
	contentRatingRequest(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		h.renderContentRating(w, r, contentRatingData(r), 503)
	}, time.Second)(w, r)
	if w.Body.Len() != 0 {
		t.Fatalf("render ignored caller cancellation: %s", w.Body.String())
	}
}

func TestContentRatingCallerCancellationStopsProviderWork(t *testing.T) {
	for _, module := range []string{"media-movies", "media-tvshows"} {
		t.Run(module, func(t *testing.T) {
			writeStarted, providerCanceled := make(chan struct{}), make(chan struct{})
			var writes, reads atomic.Int32
			f := &ratingFixture{beforeCall: func(ctx context.Context, write bool) {
				if !write {
					reads.Add(1)
					return
				}
				if writes.Add(1) == 1 {
					close(writeStarted)
				}
				<-ctx.Done()
				close(providerCanceled)
			}}
			h, cookie := newRatingRouteHandler(t, f, nil)
			audits := make(chan map[string]string, 2)
			h.AuditHook = func(_, _, _, _ string, details map[string]string) { audits <- details }
			mux := http.NewServeMux()
			h.RegisterRoutes(mux)
			srv := httptest.NewServer(mux)
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/media/"+module+"/item/fixture/content-rating", strings.NewReader("classification=PG"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
			requestDone := make(chan error, 1)
			go func() {
				resp, err := srv.Client().Do(req)
				if resp != nil {
					_ = resp.Body.Close()
				}
				requestDone <- err
			}()
			select {
			case <-writeStarted:
			case <-time.After(time.Second):
				t.Fatal("write did not start")
			}
			cancel()
			select {
			case <-providerCanceled:
			case <-time.After(time.Second):
				t.Fatal("provider work outlived caller cancellation")
			}
			select {
			case audit := <-audits:
				if audit["outcome"] != "uncertain" || audit["reason"] != "Canceled" {
					t.Errorf("wrong canceled outcome: %v", audit)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled attempt lacked audit")
			}
			if err := <-requestDone; err == nil || writes.Load() != 1 || reads.Load() != 0 || len(audits) != 0 {
				t.Fatalf("cancellation/retry regression: error=%v writes=%d reads=%d", err, writes.Load(), reads.Load())
			}
		})
	}
}
