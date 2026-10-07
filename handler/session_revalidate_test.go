package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc/codes"
)

func boundSession(t *testing.T, h *Handler, userID, username, tenant, bearer string, roles []string) string {
	t.Helper()
	cookie, err := h.Sessions.CreateWithTenant(userID, username, tenant, roles, []string{"keep"})
	if err != nil {
		t.Fatal(err)
	}
	h.Sessions.BindAuthLocalToken(cookie, bearer)
	return cookie
}

func revalidateHandler(h *Handler, seen chan<- *session.Session) http.HandlerFunc {
	return h.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		snap := SessionFromContext(r.Context())
		if seen != nil && snap != nil {
			copied := *snap
			copied.Roles = append([]string(nil), snap.Roles...)
			copied.Permissions = append([]string(nil), snap.Permissions...)
			seen <- &copied
		}
		if snap != nil {
			snap.Username = "handler-mutated"
			if len(snap.Roles) > 0 {
				snap.Roles[0] = "handler-mutated"
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func sessionRequest(path, cookie string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer browser-forged-token")
	r.Header.Set("X-Auth-Token", "browser-forged-token")
	r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	return r
}

func assertSessionCleared(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	raw := w.Header().Get("Set-Cookie")
	if !strings.Contains(raw, "session=") || !strings.Contains(raw, "Max-Age=0") {
		t.Fatalf("cookie not cleared: %q", raw)
	}
}

func assertSessionKept(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if raw := w.Header().Get("Set-Cookie"); raw != "" {
		t.Fatalf("outage cleared the cookie: %q", raw)
	}
}

func TestBoundSessionRevalidationAppliesCurrentClaims(t *testing.T) {
	const bearer = "provider-bearer-secret"
	stub := &identityRPCStub{
		allowAdmin: true,
		validateResp: &authv1.ValidateResponse{
			Valid: true, UserId: "u1", Username: "alice-current", Roles: []string{"admin"}, TenantId: "tenant-current",
		},
	}
	h := setupIdentityHandler(t, stub)
	cookie := boundSession(t, h, "u1", "alice-stale", "tenant-stale", bearer, []string{"viewer"})
	seen := make(chan *session.Session, 1)
	w := httptest.NewRecorder()
	revalidateHandler(h, seen)(w, sessionRequest("/users", cookie))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	req := stub.requests["Validate"].(*authv1.ValidateRequest)
	if req.GetToken() != bearer {
		t.Fatalf("validated token=%q", req.GetToken())
	}
	if len(stub.tokens["Validate"]) != 0 {
		t.Fatalf("Validate forwarded metadata=%v", stub.tokens["Validate"])
	}
	snap := <-seen
	if snap.UserID != "u1" || snap.Username != "alice-current" || snap.TenantID != "tenant-current" || len(snap.Roles) != 1 || snap.Roles[0] != "admin" || snap.Permissions[0] != "keep" || snap.AuthLocalToken != bearer {
		t.Fatalf("request snapshot=%+v", snap)
	}
	stored, ok := h.Sessions.Get(cookie)
	if !ok || stored.Username != "alice-current" || stored.Roles[0] != "admin" {
		t.Fatalf("handler mutation leaked into store: %+v ok=%v", stored, ok)
	}
	if strings.Contains(w.Body.String(), bearer) {
		t.Fatal("response exposed the provider bearer")
	}
}

func TestBoundSessionRevalidationLogsOutInvalidBearer(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp *authv1.ValidateResponse
		code codes.Code
	}{
		{name: "invalid", resp: &authv1.ValidateResponse{Valid: false, Error: "invalid or expired token"}},
		{name: "empty-user", resp: &authv1.ValidateResponse{Valid: true, Username: "alice"}},
		{name: "other-user", resp: &authv1.ValidateResponse{Valid: true, UserId: "u2", Username: "bob"}},
		{name: "unauthenticated", code: codes.Unauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &identityRPCStub{allowAdmin: true, validateResp: tc.resp}
			if tc.code != codes.OK {
				stub.fail = map[string]codes.Code{"Validate": tc.code}
			}
			h := setupIdentityHandler(t, stub)
			cookie := boundSession(t, h, "u1", "alice", "tenant", "bearer-secret", []string{"admin"})
			w := httptest.NewRecorder()
			revalidateHandler(h, nil)(w, sessionRequest("/", cookie))
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
				t.Fatalf("status=%d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
			}
			assertSessionCleared(t, w)
			if _, ok := h.Sessions.Get(cookie); ok || h.Sessions.Count() != 0 {
				t.Fatal("invalid bearer left a local session")
			}
			req := stub.requests["Validate"].(*authv1.ValidateRequest)
			if req.GetToken() != "bearer-secret" {
				t.Fatalf("validated token=%q", req.GetToken())
			}
		})
	}
}

func TestBoundSessionRevalidationKeepsCookieOnOutage(t *testing.T) {
	for _, code := range []codes.Code{codes.Internal, codes.Unavailable, codes.DeadlineExceeded, codes.PermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			stub := &identityRPCStub{allowAdmin: true, fail: map[string]codes.Code{"Validate": code}}
			h := setupIdentityHandler(t, stub)
			cookie := boundSession(t, h, "u1", "alice", "tenant", "bearer-secret", []string{"admin"})
			w := httptest.NewRecorder()
			revalidateHandler(h, nil)(w, sessionRequest("/devices", cookie))
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			assertSessionKept(t, w)
			stored, ok := h.Sessions.Get(cookie)
			if !ok || stored.Username != "alice" || stored.AuthLocalToken != "bearer-secret" {
				t.Fatalf("outage changed the session: %+v ok=%v", stored, ok)
			}
			if !strings.Contains(w.Body.String(), sessionUnavailableMessage) {
				t.Fatalf("body=%q", w.Body.String())
			}
		})
	}

	t.Run("discovery-down", func(t *testing.T) {
		stub := &identityRPCStub{discoveryFail: codes.Unavailable}
		h := setupIdentityHandler(t, stub)
		cookie := boundSession(t, h, "u1", "alice", "tenant", "bearer-secret", []string{"admin"})
		w := httptest.NewRecorder()
		revalidateHandler(h, nil)(w, sessionRequest("/", cookie))
		if w.Code != http.StatusServiceUnavailable || stub.calls["Validate"] != 0 {
			t.Fatalf("status=%d validate calls=%d", w.Code, stub.calls["Validate"])
		}
		assertSessionKept(t, w)
		if _, ok := h.Sessions.Get(cookie); !ok {
			t.Fatal("discovery outage dropped the session")
		}
	})

	t.Run("core-missing", func(t *testing.T) {
		h := New(nil, session.NewStore(time.Hour), false, "test", nil, true, "", nil, nil)
		cookie := boundSession(t, h, "u1", "alice", "tenant", "bearer-secret", []string{"admin"})
		w := httptest.NewRecorder()
		revalidateHandler(h, nil)(w, sessionRequest("/", cookie))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		assertSessionKept(t, w)
		if _, ok := h.Sessions.Get(cookie); !ok {
			t.Fatal("missing core dropped the session")
		}
	})
}

func TestBoundSessionRevalidationDoesNotResurrectRevokedSession(t *testing.T) {
	started := make(chan struct{})
	hold := make(chan struct{})
	stub := &identityRPCStub{
		allowAdmin:      true,
		validateStarted: started,
		validateHold:    hold,
		validateResp: &authv1.ValidateResponse{
			Valid: true, UserId: "u1", Username: "alice-current", Roles: []string{"admin"}, TenantId: "tenant",
		},
	}
	h := setupIdentityHandler(t, stub)
	cookie := boundSession(t, h, "u1", "alice", "tenant", "bearer-secret", []string{"viewer"})
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		revalidateHandler(h, nil)(w, sessionRequest("/", cookie))
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("validation did not start")
	}
	h.Sessions.Revoke(cookie)
	close(hold)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("status=%d location=%q body=%s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	assertSessionCleared(t, w)
	if h.Sessions.Count() != 0 {
		t.Fatal("in-flight validation recreated the revoked session")
	}
}

func TestBoundSessionRevalidationIgnoresReplacedBearer(t *testing.T) {
	started := make(chan struct{})
	hold := make(chan struct{})
	stub := &identityRPCStub{
		allowAdmin:      true,
		validateStarted: started,
		validateHold:    hold,
		validateResp: &authv1.ValidateResponse{
			Valid: true, UserId: "u1", Username: "alice-current", Roles: []string{"admin"}, TenantId: "tenant-new",
		},
	}
	h := setupIdentityHandler(t, stub)
	cookie := boundSession(t, h, "u1", "alice", "tenant-old", "bearer-old", []string{"viewer"})
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		revalidateHandler(h, nil)(w, sessionRequest("/", cookie))
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("validation did not start")
	}
	h.Sessions.BindAuthLocalToken(cookie, "bearer-new")
	close(hold)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	assertSessionKept(t, w)
	stored, ok := h.Sessions.Get(cookie)
	if !ok || stored.Username != "alice" || stored.AuthLocalToken != "bearer-new" || stored.Roles[0] != "viewer" {
		t.Fatalf("replaced bearer was overwritten: %+v ok=%v", stored, ok)
	}
	req := stub.requests["Validate"].(*authv1.ValidateRequest)
	if req.GetToken() != "bearer-old" {
		t.Fatalf("validated token=%q", req.GetToken())
	}
}

func TestLocalSessionSkipsProviderRevalidation(t *testing.T) {
	stub := &identityRPCStub{allowAdmin: true}
	h := setupIdentityHandler(t, stub)
	cookie, err := h.Sessions.CreateWithTenant("u1", "alice", "tenant", []string{"admin"}, []string{"keep"})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan *session.Session, 1)
	w := httptest.NewRecorder()
	revalidateHandler(h, seen)(w, sessionRequest("/", cookie))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if stub.calls["Validate"] != 0 {
		t.Fatalf("local session called Validate %d times", stub.calls["Validate"])
	}
	<-seen
	stored, ok := h.Sessions.Get(cookie)
	if !ok || stored.Username != "alice" {
		t.Fatalf("local session changed: %+v ok=%v", stored, ok)
	}
}

func TestHTMXInvalidSessionRedirectsWithoutFollow(t *testing.T) {
	stub := &identityRPCStub{validateResp: &authv1.ValidateResponse{Valid: false}}
	h := setupIdentityHandler(t, stub)
	cookie := boundSession(t, h, "u1", "alice", "tenant", "bearer-secret", []string{"admin"})
	r := sessionRequest("/", cookie)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	revalidateHandler(h, nil)(w, r)
	if w.Code != http.StatusOK || w.Header().Get("HX-Redirect") != "/login" {
		t.Fatalf("status=%d hx=%q", w.Code, w.Header().Get("HX-Redirect"))
	}
	assertSessionCleared(t, w)
}

func TestHTMXOutageDoesNotRedirect(t *testing.T) {
	stub := &identityRPCStub{fail: map[string]codes.Code{"Validate": codes.Unavailable}}
	h := setupIdentityHandler(t, stub)
	cookie := boundSession(t, h, "u1", "alice", "tenant", "bearer-secret", []string{"admin"})
	r := sessionRequest("/dashboard/health", cookie)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	revalidateHandler(h, nil)(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("HX-Redirect") != "" {
		t.Fatalf("status=%d hx=%q body=%s", w.Code, w.Header().Get("HX-Redirect"), w.Body.String())
	}
	assertSessionKept(t, w)
}
