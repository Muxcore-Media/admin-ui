package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
)

func TestRedeemTimeouts(t *testing.T) {
	if redeemDialTimeout != 3*time.Second {
		t.Fatalf("redeem dial: got %v, want 3s", redeemDialTimeout)
	}
	if redeemReadTimeout < 4*time.Second || redeemReadTimeout > 8*time.Second {
		t.Fatalf("redeem read out of range: %v", redeemReadTimeout)
	}
	if redeemPageTimeout < redeemDialTimeout+redeemReadTimeout {
		t.Fatalf("redeem page budget %v must cover dial+read", redeemPageTimeout)
	}
}

func TestHouseholdRedeemPageNoToken(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.AuthAddr = "http://127.0.0.1:19401"
	h.AuthInternalAddr = h.AuthAddr

	r := mustRequest("GET", "/invite/redeem")
	w := httptest.NewRecorder()
	h.HouseholdRedeemPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="household-redeem-page"`) {
		t.Fatal("expected redeem page markup")
	}
	if !strings.Contains(body, `data-testid="redeem-error"`) {
		t.Fatal("expected error banner for missing token")
	}
	// No form should be shown when the token is absent
	if strings.Contains(body, `data-testid="redeem-form"`) {
		t.Fatal("form should not render without a token")
	}
}

func TestHouseholdRedeemPageSoftEmptyNoAuthBase(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	// No AuthAddr/AuthInternalAddr set → soft empty

	r := mustRequest("GET", "/invite/redeem?token=abc123")
	w := httptest.NewRecorder()
	h.HouseholdRedeemPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="household-redeem-page"`) {
		t.Fatal("expected redeem page markup")
	}
	if !strings.Contains(body, `data-testid="redeem-error"`) {
		t.Fatal("expected error when auth base not configured")
	}
}

func TestHouseholdRedeemPageValidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/invites/validate" && r.URL.Query().Get("token") == "goodtoken" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"role":"user","expires_at":"2099-01-01T00:00:00Z","created_by":"admin"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, srv.URL, nil, nil)
	h.AuthInternalAddr = srv.URL

	r := mustRequest("GET", "/invite/redeem?token=goodtoken")
	w := httptest.NewRecorder()
	h.HouseholdRedeemPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="redeem-form"`) {
		t.Fatalf("expected form to render with a valid token, body: %s", truncate(body, 400))
	}
	if !strings.Contains(body, "goodtoken") {
		t.Fatal("expected token to be embedded in hidden form field")
	}
	if !strings.Contains(body, "user") {
		t.Fatal("expected invite role to be displayed")
	}
}

func TestHouseholdRedeemPageExpiredToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/invites/validate" {
			http.Error(w, "gone", http.StatusGone)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, srv.URL, nil, nil)
	h.AuthInternalAddr = srv.URL

	r := mustRequest("GET", "/invite/redeem?token=expired")
	w := httptest.NewRecorder()
	h.HouseholdRedeemPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="redeem-error"`) {
		t.Fatal("expected error for expired token")
	}
	if strings.Contains(body, `data-testid="redeem-form"`) {
		t.Fatal("form should not render for an expired token")
	}
}

func TestHouseholdRedeemSubmitMissingDisplayName(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "http://127.0.0.1:19401", nil, nil)
	h.AuthInternalAddr = h.AuthAddr

	body := url.Values{"token": {"tok123"}, "display_name": {""}, "pin": {""}}
	r := mustRequest("POST", "/invite/redeem")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Body = http.NoBody
	r, _ = http.NewRequest("POST", "/invite/redeem", strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:12345"

	w := httptest.NewRecorder()
	h.HouseholdRedeemSubmit(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 form re-render, got %d", w.Code)
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, `data-testid="redeem-error"`) {
		t.Fatal("expected validation error for missing display name")
	}
}

func TestHouseholdRedeemSubmitSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/invites/redeem" && r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"u1","username":"alice"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, srv.URL, nil, nil)
	h.AuthInternalAddr = srv.URL

	formData := url.Values{"token": {"tok123"}, "display_name": {"Alice"}, "pin": {""}}
	r, _ := http.NewRequest("POST", "/invite/redeem", strings.NewReader(formData.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:12345"

	w := httptest.NewRecorder()
	h.HouseholdRedeemSubmit(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect after success, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/login") {
		t.Fatalf("expected redirect to /login, got %q", loc)
	}
}

func TestHouseholdRedeemRoutesRegistered(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/invite/redeem"},
		{"POST", "/invite/redeem"},
	} {
		r := mustRequest(tc.method, tc.path)
		_, pattern := mux.Handler(r)
		if pattern == "" {
			t.Fatalf("expected route registered for %s %s", tc.method, tc.path)
		}
	}
}
