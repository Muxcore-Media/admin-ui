package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
)

func TestLoginPageRedirectsToPublicAuth(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "https://auth.gringotts", nil, nil)
	h.AuthInternalAddr = "http://127.0.0.1:9401"
	h.PublicURL = "https://admin.gringotts"

	r := mustRequest("GET", "/login")
	r.Host = "admin.gringotts"
	w := httptest.NewRecorder()
	h.LoginPage(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://auth.gringotts/login?redirect=") {
		t.Fatalf("unexpected location: %s", loc)
	}
	if !strings.Contains(loc, "redirect=https%3A%2F%2Fadmin.gringotts%2Fauth%2Fcallback") {
		t.Fatalf("callback missing/incorrect in %s", loc)
	}
}

func TestAuthExchangeBasePrefersInternal(t *testing.T) {
	h := &Handler{AuthAddr: "https://auth.gringotts", AuthInternalAddr: "http://127.0.0.1:9401"}
	if got := h.authExchangeBase(); got != "http://127.0.0.1:9401" {
		t.Fatalf("got %q", got)
	}
}

func TestAuthExchangeTimeoutBound(t *testing.T) {
	if authExchangeTimeout != 5*time.Second {
		t.Fatalf("authExchangeTimeout: got %v, want 5s", authExchangeTimeout)
	}
}

func TestPublicOriginUntrustedForwardedHeadersIgnored(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "https://auth.gringotts", nil, parseTrustedProxies(nil))
	h.PublicURL = ""

	r := mustRequest("GET", "/login")
	r.RemoteAddr = "203.0.113.50:12345"
	r.Host = "admin.example.com"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "evil.example")

	if origin := h.publicOrigin(r); origin != "http://admin.example.com" {
		t.Fatalf("expected untrusted forwarded headers to be ignored, got %q", origin)
	}
}

func TestPublicOriginTrustedForwardedHeadersHonored(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "https://auth.gringotts", nil, parseTrustedProxies(nil))
	h.PublicURL = ""

	r := mustRequest("GET", "/login")
	r.RemoteAddr = "127.0.0.1:8080"
	r.Host = "127.0.0.1:8080"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "admin.gringotts")

	if origin := h.publicOrigin(r); origin != "https://admin.gringotts" {
		t.Fatalf("expected trusted forwarded headers to be honored, got %q", origin)
	}
}

func TestLoginPageIgnoresUntrustedForwardedHeaders(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "https://auth.gringotts", nil, parseTrustedProxies(nil))
	h.PublicURL = ""

	r := mustRequest("GET", "/login")
	r.RemoteAddr = "203.0.113.50:12345"
	r.Host = "admin.example.com"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "evil.example")
	w := httptest.NewRecorder()
	h.LoginPage(w, r)

	loc := w.Header().Get("Location")
	if strings.Contains(loc, "evil.example") {
		t.Fatalf("untrusted forwarded host poisoned redirect: %s", loc)
	}
	if !strings.Contains(loc, "redirect=http%3A%2F%2Fadmin.example.com%2Fauth%2Fcallback") {
		t.Fatalf("expected callback from r.Host, got %s", loc)
	}
}

func TestAuthCallbackFailsFastOnSlowExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/exchange" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.AuthInternalAddr = srv.URL

	r := mustRequest("GET", "/auth/callback?code=test-code")
	w := httptest.NewRecorder()

	start := time.Now()
	h.AuthCallback(w, r)
	elapsed := time.Since(start)

	limit := authExchangeTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("auth callback took %v, want ≤ %v (bounded HTTP exchange)", elapsed, limit)
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
	body := strings.ToLower(w.Body.String())
	if !strings.Contains(body, "timed out") && !strings.Contains(body, "unavailable") {
		t.Fatalf("expected timeout/unavailable error, got: %s", w.Body.String())
	}
}
