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

const invalidInviteMsg = "This invite link is invalid or has already been used."

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

func TestInvalidInvitePeek(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"404", http.StatusNotFound, "", true},
		{"410", http.StatusGone, "", true},
		{"400 valid false", http.StatusBadRequest, `{"valid":false,"reason":"expired"}`, true},
		{"400 valid true", http.StatusBadRequest, `{"valid":true}`, false},
		{"400 no valid field", http.StatusBadRequest, `{"error":"bad request"}`, false},
		{"200 ok", http.StatusOK, `{"role":"user"}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := invalidInvitePeek(tc.status, []byte(tc.body)); got != tc.want {
				t.Fatalf("invalidInvitePeek(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
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
	if strings.Contains(body, `data-testid="redeem-form"`) {
		t.Fatal("form should not render without a token")
	}
}

func TestHouseholdRedeemPageSoftEmptyNoAuthBase(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

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
		if r.URL.Path == "/api/invite/peek" && r.URL.Query().Get("token") == "goodtoken" {
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
	if !strings.Contains(body, `name="username"`) {
		t.Fatal("expected username field in form")
	}
	if !strings.Contains(body, `name="password"`) {
		t.Fatal("expected password field in form")
	}
	if !strings.Contains(body, "user") {
		t.Fatal("expected invite role to be displayed")
	}
}

func TestHouseholdRedeemPageInvalidToken(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"410 gone", http.StatusGone, "gone"},
		{"404 not found", http.StatusNotFound, "not found"},
		{"400 valid false", http.StatusBadRequest, `{"valid":false,"reason":"revoked"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/invite/peek" {
					if strings.HasPrefix(strings.TrimSpace(tc.body), "{") {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(tc.body))
					} else {
						http.Error(w, tc.body, tc.status)
					}
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()

			ss := session.NewStore(0)
			h := New(nil, ss, false, "test", nil, false, srv.URL, nil, nil)
			h.AuthInternalAddr = srv.URL

			r := mustRequest("GET", "/invite/redeem?token=bad")
			w := httptest.NewRecorder()
			h.HouseholdRedeemPage(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, `data-testid="redeem-error"`) {
				t.Fatal("expected error for invalid token")
			}
			if !strings.Contains(body, invalidInviteMsg) {
				t.Fatalf("expected friendly invalid invite message, body: %s", truncate(body, 400))
			}
			if strings.Contains(body, `data-testid="redeem-form"`) {
				t.Fatal("form should not render for an invalid token")
			}
		})
	}
}

func TestHouseholdRedeemSubmitMissingCredentials(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "http://127.0.0.1:19401", nil, nil)
	h.AuthInternalAddr = h.AuthAddr

	formData := url.Values{"token": {"tok123"}, "username": {""}, "password": {""}}
	r, _ := http.NewRequest("POST", "/invite/redeem", strings.NewReader(formData.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:12345"

	w := httptest.NewRecorder()
	h.HouseholdRedeemSubmit(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 form re-render, got %d", w.Code)
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, `data-testid="redeem-error"`) {
		t.Fatal("expected validation error for missing credentials")
	}
	if !strings.Contains(respBody, "Username and password are required.") {
		t.Fatal("expected username/password validation message")
	}
}

func TestHouseholdRedeemSubmitSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/invite/redeem" && r.Method == http.MethodPost {
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

	formData := url.Values{"token": {"tok123"}, "username": {"alice"}, "password": {"secret123"}}
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
