package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

func mustRequest(method, path string) *http.Request {
	r, err := http.NewRequest(method, path, nil)
	if err != nil {
		panic(err)
	}
	r.RemoteAddr = "127.0.0.1:12345"
	return r
}

func TestNewHandler(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test-version", nil, false, "", nil, nil)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.version != "test-version" {
		t.Fatalf("expected test-version, got %s", h.version)
	}
}

func TestHandlerDisconnectedMode(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	// requireAuth should show disconnected page instead of crashing
	handler := h.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach authenticated handler")
	})

	r := mustRequest("GET", "/")
	w := httptest.NewRecorder()
	handler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (disconnected page), got %d", w.Code)
	}
}

func TestNavLinks(t *testing.T) {
	if len(staticNavLinks) == 0 {
		t.Fatal("expected non-empty navLinks")
	}
	foundDash, foundRequest := false, false
	for _, l := range staticNavLinks {
		if l.Path == "/" {
			foundDash = true
		}
		if l.Path == "/request" {
			foundRequest = true
		}
	}
	if !foundDash {
		t.Fatal("expected Dashboard link in nav")
	}
	if !foundRequest {
		t.Fatal("expected Request link in nav")
	}
}

func TestRequestRoutesRegistered(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	for _, path := range []string{"/request", "/media/x/item/y/refresh", "/media/x/item/y/delete"} {
		r := mustRequest("GET", path)
		if path != "/request" {
			r = mustRequest("POST", path)
		}
		_, pattern := mux.Handler(r)
		if pattern == "" {
			t.Fatalf("expected route registered for %s", path)
		}
	}
}

func TestAuthStatus(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/auth/status")
	w := httptest.NewRecorder()
	h.AuthStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != `{"available":false}` {
		t.Fatalf("expected available=false without core, got %q", w.Body.String())
	}
}

func TestLoginMetricsInterface(t *testing.T) {
	lm := &mockLoginMetrics{}
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", lm, false, "", nil, nil)

	// Verify the handler stores the metrics
	if h.loginMetrics != lm {
		t.Fatal("expected loginMetrics to be stored")
	}
}

type mockLoginMetrics struct {
	success int64
	failure int64
}

func (m *mockLoginMetrics) IncSuccess() { m.success++ }
func (m *mockLoginMetrics) IncFailure() { m.failure++ }

func TestForbiddenOnAuthorizedRoutes(t *testing.T) {
	ss := session.NewStore(0)
	token, _ := ss.Create("user1", "testuser", []string{"admin"}, []string{"admin.access"})
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	handler := h.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	r := mustRequest("GET", "/")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	handler(w, r)
	// Without core, authorizer check is skipped, so this should pass
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 without authorizer, got %d", w.Code)
	}
}
