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

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

func requestBudgetFixture(t *testing.T, budget, writeTimeout time.Duration, onAuth func(context.Context, string), upstream http.Handler) (*http.Client, string, string) {
	t.Helper()
	provider := httptest.NewServer(upstream)
	t.Cleanup(provider.Close)
	h := setupIdentityHandler(t, &identityRPCStub{allowAdmin: true, onCall: onAuth, validateResp: &authv1.ValidateResponse{Valid: true, UserId: "u1", Roles: []string{"admin"}}})
	h.RequestMediaURL = provider.URL
	cookie := boundSession(t, h, "u1", "fixture", "", "fixture-bearer", []string{"admin"})
	mux := http.NewServeMux()
	if budget == requestMutationTimeout {
		h.RegisterRoutes(mux) // Production registration and its actual 12s budget.
	} else {
		h.registerRequestMutationRoutes(mux, budget)
	}
	server := httptest.NewUnstartedServer(mux)
	server.Config.WriteTimeout = writeTimeout
	server.Start()
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = writeTimeout + time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, server.URL, cookie
}

func TestRequestMutationCombinedDelayBeforeProductionWriteTimeout(t *testing.T) {
	var authCalls, writes atomic.Int32
	client, base, cookie := requestBudgetFixture(t, requestMutationTimeout, 15*time.Second,
		func(ctx context.Context, _ string) {
			authCalls.Add(1)
			timer := time.NewTimer(5500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
			}
		}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writes.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
	started := time.Now()
	resp, body := requestAdapterPost(t, client, base, cookie, "/request", "tmdb_id=1&type=movie")
	assertRequestAdapterError(t, resp, body, http.StatusServiceUnavailable)
	if authCalls.Load() != 2 || writes.Load() != 1 || time.Since(started) > 13*time.Second || !strings.Contains(body, requestMutationUnknown) {
		t.Fatalf("combined route budget: auth=%d writes=%d elapsed=%v", authCalls.Load(), writes.Load(), time.Since(started))
	}
}

func TestRequestMutationRouteBudgetCoversEveryPhase(t *testing.T) {
	for _, mutation := range requestAdapterMutations {
		for _, phase := range []string{"Validate", "Can", "provider"} {
			t.Run(mutation.name+"/"+phase, func(t *testing.T) {
				var validates, authorizations, writes atomic.Int32
				client, base, cookie := requestBudgetFixture(t, 150*time.Millisecond, 600*time.Millisecond,
					func(ctx context.Context, method string) {
						if method == "Validate" {
							validates.Add(1)
						} else {
							authorizations.Add(1)
						}
						if method == phase {
							<-ctx.Done()
						} else {
							timer := time.NewTimer(20 * time.Millisecond)
							defer timer.Stop()
							select {
							case <-timer.C:
							case <-ctx.Done():
							}
						}
					}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						writes.Add(1)
						_, _ = io.Copy(io.Discard, r.Body)
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
						<-r.Context().Done()
					}))
				started := time.Now()
				resp, body := requestAdapterPost(t, client, base, cookie, mutation.path, mutation.form)
				if resp.StatusCode != http.StatusServiceUnavailable || len(body) == 0 || time.Since(started) > 500*time.Millisecond {
					t.Fatalf("missing bounded response: status=%d elapsed=%v body=%q", resp.StatusCode, time.Since(started), body)
				}
				wantWrites, wantCan := int32(0), int32(1)
				if phase == "provider" {
					wantWrites = 1
					assertRequestAdapterError(t, resp, body, http.StatusServiceUnavailable)
				} else {
					// Authorization errors keep the existing plaintext guard response.
					if !strings.Contains(body, sessionUnavailableMessage) {
						t.Error("changed existing auth failure response")
					}
				}
				if phase == "Validate" {
					wantCan = 0
				}
				if validates.Load() != 1 || authorizations.Load() != wantCan || writes.Load() != wantWrites {
					t.Fatalf("unexpected phase calls: validate=%d can=%d writes=%d", validates.Load(), authorizations.Load(), writes.Load())
				}
			})
		}
	}
}

func TestRequestMutationProductionBudgetStartsBeforeAuth(t *testing.T) {
	h := setupIdentityHandler(t, &identityRPCStub{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	for _, mutation := range requestAdapterMutations {
		w := &ratingDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		started := time.Now()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, mutation.path, strings.NewReader("fixture=body")))
		if remaining := w.deadline.Sub(started); w.Code != http.StatusSeeOther || remaining < 11*time.Second || remaining > 12500*time.Millisecond {
			t.Errorf("%s missing pre-auth budget: status=%d deadline=%v", mutation.name, w.Code, remaining)
		}
	}
}

func TestRequestMutationSlowFormAndEarlyRefusal(t *testing.T) {
	for _, path := range []string{"/request", "/approvals/fixture/deny"} {
		for _, signIn := range []bool{false, true} {
			t.Run(path+"/signed-in="+map[bool]string{false: "false", true: "true"}[signIn], func(t *testing.T) {
				var writes atomic.Int32
				_, base, cookie := requestBudgetFixture(t, 150*time.Millisecond, 600*time.Millisecond, nil,
					http.HandlerFunc(func(http.ResponseWriter, *http.Request) { writes.Add(1) }))
				conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				headers := "POST " + path + " HTTP/1.1\r\nHost: fixture\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 99\r\n"
				if signIn {
					headers += "Cookie: session=" + cookie + "\r\n"
				}
				if _, err := io.WriteString(conn, headers+"\r\nreason=partial"); err != nil {
					t.Fatal(err)
				}
				resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				body, err := io.ReadAll(resp.Body)
				want := http.StatusSeeOther
				if signIn {
					want = http.StatusServiceUnavailable
					if !strings.Contains(string(body), "No request was sent.") || !strings.Contains(string(body), "</html>") || resp.Header.Get(swapErrorHeader) != "1" {
						t.Errorf("slow form did not return complete refusal: %s", body)
					}
				}
				if err != nil || resp.StatusCode != want || !resp.Close || writes.Load() != 0 {
					t.Fatalf("slow/early refusal status=%d close=%t writes=%d err=%v", resp.StatusCode, resp.Close, writes.Load(), err)
				}
			})
		}
	}
}

func TestRequestMutationBudgetKeepsCallerDeadlineAndCancellation(t *testing.T) {
	h := setupIdentityHandler(t, &identityRPCStub{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	w := &ratingDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	r := httptest.NewRequest(http.MethodPost, "/request", strings.NewReader("type=movie")).WithContext(ctx)
	h.requestMutationRequest(func(w http.ResponseWriter, r *http.Request) {
		want, _ := ctx.Deadline()
		got, _ := r.Context().Deadline()
		if !got.Equal(want) || !w.(*ratingDeadlineRecorder).deadline.Equal(want) {
			t.Error("wrapper extended the caller deadline")
		}
		cancel()
		h.renderRequestMutationError(w, r, http.StatusServiceUnavailable, requestMutationUnknown, "/request")
	}, "/request", time.Second)(w, r)
	if w.Body.Len() != 0 {
		t.Error("render-only context ignored actual caller cancellation")
	}
}

func TestRequestMutationCallerCancellationStopsProvider(t *testing.T) {
	for _, mutation := range requestAdapterMutations {
		t.Run(mutation.name, func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			var writes atomic.Int32
			client, base, cookie := requestBudgetFixture(t, time.Second, 2*time.Second, nil,
				http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if writes.Add(1) == 1 {
						close(started)
					}
					<-r.Context().Done()
					close(stopped)
				}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+mutation.path, strings.NewReader(mutation.form))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
			done := make(chan error, 1)
			go func() {
				resp, err := client.Do(req)
				if resp != nil {
					_ = resp.Body.Close()
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("provider mutation did not start")
			}
			cancel()
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("provider work outlived caller cancellation")
			}
			select {
			case err := <-done:
				if err == nil {
					t.Error("canceled caller received a success")
				}
			case <-time.After(time.Second):
				t.Fatal("canceled caller remained blocked")
			}
			if writes.Load() != 1 {
				t.Errorf("mutation retried after cancellation: %d", writes.Load())
			}
		})
	}
}
