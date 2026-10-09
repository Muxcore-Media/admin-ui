package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These are the five registered mutations, independent of handler dispatch.
var requestAdapterMutations = []struct {
	name, path, upstream, form string
}{
	{"create", "/request", "/api/request", "tmdb_id=1396&title=Fixture+TV&type=tv"},
	{"request-approve", "/request/fixture/approve", "/api/requests/fixture/approve", ""},
	{"request-deny", "/request/fixture/deny", "/api/requests/fixture/deny", ""},
	{"approvals-approve", "/approvals/fixture/approve", "/api/requests/fixture/approve", ""},
	{"approvals-deny", "/approvals/fixture/deny", "/api/requests/fixture/deny", "reason=Fixture+reason"},
}

func requestAdapterFixture(t *testing.T, upstream http.Handler, configure ...func(*Handler, *identityRPCStub)) (*http.Client, string, string) {
	t.Helper()
	provider := httptest.NewServer(upstream)
	t.Cleanup(provider.Close)
	stub := &identityRPCStub{allowAdmin: true, validateResp: &authv1.ValidateResponse{
		Valid: true, UserId: "u1", Username: "fixture", TenantId: "fixture-tenant", Roles: []string{"admin"},
	}}
	h := setupIdentityHandler(t, stub)
	h.RequestMediaURL = provider.URL
	cookie := boundSession(t, h, "u1", "fixture", "fixture-tenant", "fixture-bearer", []string{"admin"})
	for _, f := range configure {
		f(h, stub)
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	admin := httptest.NewServer(mux)
	t.Cleanup(admin.Close)
	client := admin.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, admin.URL, cookie
}

func requestAdapterPost(t *testing.T, client *http.Client, base, cookie, path, form string) (*http.Response, string) {
	t.Helper()
	return requestAdapterSend(t, client, base, cookie, http.MethodPost, path, form, true)
}

func requestAdapterSend(t *testing.T, client *http.Client, base, cookie, method, path, form string, htmx bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(form))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	// Browser identity hints must not compete with the bound provider bearer.
	req.Header.Set("Authorization", "Bearer browser-forged")
	req.Header.Set("X-Caller-Id", "forged-user")
	req.Header.Set("X-MuxCore-Roles", "forged-role")
	req.Header.Set("X-Tenant-ID", "forged-tenant")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

func TestRequestAdapterCreateCanonicalMediaType(t *testing.T) {
	seen := make(chan string, 3)
	client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// request-media 10483aa internal/module.go:639-685 decodes mediaType;
		// empty and unknown values enter its movie branch. This fixture models
		// that documented HTTP contract, without compiling the blocked provider.
		var payload struct {
			MediaType string `json:"mediaType"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		kind := "movie"
		if payload.MediaType == "tv" {
			kind = "tv"
		}
		seen <- kind
		_, _ = io.WriteString(w, `{"requestId":"fixture","status":"pending"}`)
	}))
	for _, kind := range []string{"tv", "movie", ""} {
		requestAdapterPost(t, client, base, cookie, "/request", "tmdb_id=1396&title=Fixture&type="+kind)
		want := kind
		if want == "" {
			want = "movie"
		}
		if got := <-seen; got != want {
			t.Fatalf("provider's canonical mediaType decoder selected %q for %q form", got, kind)
		}
	}
}

func TestRequestAdapterShowsProviderRejection(t *testing.T) {
	for _, mutation := range requestAdapterMutations {
		t.Run(mutation.name, func(t *testing.T) {
			var calls atomic.Int32
			var upstreamCode atomic.Int32
			var handler *Handler
			client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != mutation.upstream {
					t.Errorf("upstream path=%s", r.URL.Path)
				}
				http.Error(w, "fixture-private-provider-error", int(upstreamCode.Load()))
			}), func(h *Handler, _ *identityRPCStub) { handler = h })
			for _, code := range []int{400, 401, 403, 404, 409, 413, 429, 500, 503} {
				for _, htmx := range []bool{false, true} {
					t.Run(fmt.Sprintf("%d/htmx=%t", code, htmx), func(t *testing.T) {
						calls.Store(0)
						upstreamCode.Store(int32(code))
						resp, body := requestAdapterSend(t, client, base, cookie, http.MethodPost, mutation.path, mutation.form, htmx)
						want := code
						if code >= 500 {
							want = http.StatusBadGateway
						}
						assertRequestAdapterError(t, resp, body, want)
						if code >= 500 && !strings.Contains(body, requestMutationUnknown) {
							t.Error("indeterminate outcome lacks check-before-retry warning")
						}
						if got := calls.Load(); got != 1 {
							t.Errorf("mutation calls=%d, want 1", got)
						}
						if _, ok := handler.Sessions.Snapshot(cookie); !ok || resp.Header.Get("Set-Cookie") != "" {
							t.Error("provider mutation rejection changed the local sign-in")
						}
					})
				}
			}
		})
	}
}

func TestRequestAdapterDoesNotFollowMutationRedirect(t *testing.T) {
	for _, mutation := range requestAdapterMutations {
		t.Run(mutation.name, func(t *testing.T) {
			var calls, redirectCode atomic.Int32
			client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == mutation.upstream {
					http.Redirect(w, r, "/unexpected-mutation-or-login-page", int(redirectCode.Load()))
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			for _, code := range []int{301, 302, 303, 307, 308} {
				t.Run(fmt.Sprint(code), func(t *testing.T) {
					calls.Store(0)
					redirectCode.Store(int32(code))
					resp, body := requestAdapterPost(t, client, base, cookie, mutation.path, mutation.form)
					assertRequestAdapterError(t, resp, body, http.StatusBadGateway)
					if calls.Load() != 1 || !strings.Contains(body, requestMutationUnknown) {
						t.Fatalf("redirect attempts=%d, must stay single and report uncertain outcome", calls.Load())
					}
				})
			}
		})
	}
}

func assertRequestAdapterError(t *testing.T, resp *http.Response, body string, code int) {
	t.Helper()
	if resp.StatusCode != code || resp.Header.Get("Location") != "" || resp.Header.Get("X-Admin-Swap-Error") != "1" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("response=%d want=%d location=%q swap=%q cache=%q", resp.StatusCode, code, resp.Header.Get("Location"), resp.Header.Get("X-Admin-Swap-Error"), resp.Header.Get("Cache-Control"))
	}
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, `id="main-content"`) || !strings.Contains(body, `hx-select="#main-content > *"`) {
		t.Error("missing visible full-page/HTMX-selectable error")
	}
	for _, unsafe := range []string{"fixture-private-provider-error", "fixture-bearer", "browser-forged", "forged-user", "forged-tenant", `<form`, "Nothing was changed", "No request was sent"} {
		if strings.Contains(body, unsafe) {
			t.Errorf("response contains unsafe mutation-result content %q", unsafe)
		}
	}
}

func TestRequestAdapterDoesNotTreatUntypedMoviesAsTV(t *testing.T) {
	client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/search" {
			// Canonical request-media 10483aa searches movies and does not
			// attach a type field. The browser's selected type is not evidence.
			_, _ = io.WriteString(w, `{"results":[{"id":550,"title":"Fixture movie","year":1999}]}`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	req, err := http.NewRequest(http.MethodGet, base+"/request?q=Fixture&type=tv", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `action="/request"><input type="hidden"`) || strings.Contains(string(body), "Fixture movie") {
		t.Fatal("TV search presented untyped canonical movie as a requestable TV result")
	}
	if !strings.Contains(string(body), "TV results are unavailable") {
		t.Fatal("TV search omission lacks explicit unavailable feedback")
	}
}

func TestRequestAdapterSearchUsesResultType(t *testing.T) {
	client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/search" {
			_, _ = io.WriteString(w, `{"results":[{"id":1,"title":"Untyped movie"},{"id":2,"title":"Typed movie","type":"movie"},{"id":3,"title":"Typed TV","type":"tv"}]}`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	for _, kind := range []string{"movie", "tv"} {
		resp, body := requestAdapterSend(t, client, base, cookie, http.MethodGet, "/request?q=Fixture&type="+kind, "", false)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", resp.StatusCode)
		}
		for _, title := range []string{"Untyped movie", "Typed movie", "Typed TV"} {
			want := (kind == "tv") == (title == "Typed TV")
			if strings.Contains(body, title) != want {
				t.Errorf("%s search: visibility of %q, want %t", kind, title, want)
			}
		}
		if !strings.Contains(body, `name="type" value="`+kind+`"`) {
			t.Errorf("missing %s form field", kind)
		}
	}
}

func TestRequestAdapterSuccessKeepsIdentityAndNavigation(t *testing.T) {
	for _, mutation := range requestAdapterMutations {
		t.Run(mutation.name, func(t *testing.T) {
			var calls, successCode atomic.Int32
			client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != mutation.upstream {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				for key, want := range map[string]string{"Authorization": "Bearer fixture-bearer", "X-Caller-Id": "u1", "X-Tenant-ID": "fixture-tenant", "X-Auth-Claims-Tenant": "fixture-tenant", "X-MuxCore-Roles": "admin"} {
					if got := r.Header.Get(key); got != want {
						t.Errorf("%s=%q, want %q", key, got, want)
					}
				}
				if r.Header.Get("Cookie") != "" {
					t.Error("forwarded browser session cookie")
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if mutation.name == "create" && (payload["mediaType"] != "tv" || payload["tmdbId"] != float64(1396) || payload["title"] != "Fixture TV" || payload["type"] != nil) {
					t.Errorf("create payload=%v", payload)
				}
				if mutation.name == "approvals-deny" && payload["reason"] != "Fixture reason" {
					t.Errorf("deny reason=%v", payload["reason"])
				}
				w.WriteHeader(int(successCode.Load()))
				if successCode.Load() != http.StatusNoContent {
					_, _ = io.WriteString(w, `{"requestId":"fixture","status":"pending"}`)
				}
			}))
			for _, code := range []int{200, 201, 204} {
				calls.Store(0)
				successCode.Store(int32(code))
				resp, _ := requestAdapterPost(t, client, base, cookie, mutation.path, mutation.form)
				want := "/request"
				if strings.HasPrefix(mutation.path, "/approvals") {
					want = "/approvals"
				} else if mutation.name == "create" {
					want = "/request?type=tv&q=Fixture+TV"
				}
				if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != want || calls.Load() != 1 || resp.Header.Get("Cache-Control") != "no-store" {
					t.Errorf("%d success: response=%d location=%q calls=%d", code, resp.StatusCode, resp.Header.Get("Location"), calls.Load())
				}
			}
		})
	}
}

func TestRequestAdapterAuthDenialNeverReachesProvider(t *testing.T) {
	for _, mutation := range requestAdapterMutations {
		t.Run(mutation.name, func(t *testing.T) {
			var calls atomic.Int32
			client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				calls.Add(1)
			}), func(_ *Handler, stub *identityRPCStub) {
				stub.allowAdmin = false
				stub.validateResp.Roles = []string{"manager"}
			})
			resp, _ := requestAdapterPost(t, client, base, cookie, mutation.path, mutation.form)
			if resp.StatusCode != http.StatusForbidden || calls.Load() != 0 {
				t.Fatalf("manager response=%d downstream=%d", resp.StatusCode, calls.Load())
			}
		})
	}
}

func TestRequestAdapterTransportAndBodyFailuresAreUncertain(t *testing.T) {
	for _, mutation := range requestAdapterMutations {
		for _, failure := range []string{"connection", "truncated-body", "oversized-body"} {
			t.Run(mutation.name+"/"+failure, func(t *testing.T) {
				var calls atomic.Int32
				client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					switch failure {
					case "connection":
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
					case "truncated-body":
						w.Header().Set("Content-Length", "99")
						_, _ = io.WriteString(w, "short")
					case "oversized-body":
						_, _ = io.WriteString(w, strings.Repeat("x", (1<<20)+1))
					}
				}))
				resp, body := requestAdapterPost(t, client, base, cookie, mutation.path, mutation.form)
				assertRequestAdapterError(t, resp, body, http.StatusServiceUnavailable)
				if calls.Load() != 1 || !strings.Contains(body, requestMutationUnknown) {
					t.Errorf("calls=%d, expected one uncertain outcome", calls.Load())
				}
			})
		}
	}
}

func TestRequestAdapterSlowMutationShowsBoundedError(t *testing.T) {
	for _, mutation := range requestAdapterMutations {
		t.Run(mutation.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				// Stall after headers, exercising the response-body timeout too.
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			started := time.Now()
			resp, body := requestAdapterPost(t, client, base, cookie, mutation.path, mutation.form)
			assertRequestAdapterError(t, resp, body, http.StatusServiceUnavailable)
			if elapsed := time.Since(started); elapsed > requestReadTimeout+2*time.Second {
				t.Errorf("mutation exceeded transport budget: %v", elapsed)
			}
			if calls.Load() != 1 || !strings.Contains(body, requestMutationUnknown) {
				t.Error("timeout retried or failed to report uncertain outcome")
			}
		})
	}
}

func TestRequestAdapterPreDispatchFailures(t *testing.T) {
	for _, mutation := range requestAdapterMutations {
		t.Run(mutation.name, func(t *testing.T) {
			var calls atomic.Int32
			client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				calls.Add(1)
			}), func(h *Handler, stub *identityRPCStub) {
				h.RequestMediaURL = ""
				stub.onDiscovery = func(_ context.Context, capability string) error {
					if capability == capMediaRequest {
						return status.Error(codes.Unavailable, "fixture-private-provider-error")
					}
					return nil
				}
			})
			resp, body := requestAdapterPost(t, client, base, cookie, mutation.path, mutation.form)
			if resp.StatusCode != http.StatusServiceUnavailable || calls.Load() != 0 || !strings.Contains(body, "No request was sent.") || strings.Contains(body, "fixture-private-provider-error") {
				t.Errorf("predispatch result=%d downstream=%d body=%s", resp.StatusCode, calls.Load(), body)
			}
		})
	}
	for _, path := range []string{"/request", "/approvals/fixture/deny"} {
		t.Run("malformed-form"+path, func(t *testing.T) {
			var calls atomic.Int32
			client, base, cookie := requestAdapterFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			resp, body := requestAdapterPost(t, client, base, cookie, path, "reason=%zz")
			if resp.StatusCode != http.StatusBadRequest || calls.Load() != 0 || !strings.Contains(body, "No request was sent.") {
				t.Errorf("malformed form response=%d downstream=%d", resp.StatusCode, calls.Load())
			}
		})
	}
}

func TestRequestMutationClientHonorsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		<-r.Context().Done()
	}))
	defer server.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = requestMutationHTTPDo(req)
	if err == nil || calls.Load() != 1 || time.Since(started) > time.Second {
		t.Fatalf("caller cancellation: err=%v calls=%d elapsed=%v", err, calls.Load(), time.Since(started))
	}
}
