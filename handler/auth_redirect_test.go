package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
