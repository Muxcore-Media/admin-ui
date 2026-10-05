package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

// requestWithSession injects sess into the request context so handlers that
// call SessionFromContext see a populated session (mirrors requireAuth in prod).
func requestWithSession(method, path string, sess *session.Session) *http.Request {
	r := mustRequest(method, path)
	ctx := context.WithValue(r.Context(), ctxSessionKey, sess)
	return r.WithContext(ctx)
}

func TestRequestPageSoftEmpty(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/request")
	w := httptest.NewRecorder()
	h.RequestPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft-empty page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="request-page"`) {
		t.Fatal("expected request page markup")
	}
	if !strings.Contains(body, `data-testid="request-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 500))
	}
}

func TestRequestPagePendingApproveButtons(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/requests", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "r-pend", "itemType": "movie", "tmdbId": 550, "title": "Fight Club", "year": 1999, "status": "pending", "requestedBy": "bob"},
			{"id": "r-ok", "itemType": "movie", "tmdbId": 218, "title": "The Terminator", "year": 1984, "status": "added"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	r := mustRequest("GET", "/request")
	w := httptest.NewRecorder()
	h.RequestPage(w, r)
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="request-pending-queue"`) {
		t.Fatalf("expected pending queue, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, `data-testid="request-approve"`) || !strings.Contains(body, `data-testid="request-deny"`) {
		t.Fatal("expected approve/deny buttons")
	}
}

func TestInvitesPageSoftEmpty(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/invites")
	w := httptest.NewRecorder()
	h.InvitesPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="invites-page"`) {
		t.Fatal("expected invites page")
	}
	if !strings.Contains(body, `data-testid="invites-soft-empty"`) {
		t.Fatalf("expected soft-empty, got: %s", truncate(body, 500))
	}
}

func TestRequestPageFixtureData(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/requests", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "r1", "itemType": "movie", "tmdbId": 550, "title": "Fight Club", "year": 1999, "status": "wanted"},
		})
	})
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": 550, "title": "Fight Club", "year": 1999, "type": "movie", "overview": "fixture"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	r := mustRequest("GET", "/request?q=Fight+Club&type=movie")
	w := httptest.NewRecorder()
	h.RequestPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="request-search-results"`) {
		t.Fatalf("expected search results, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "Fight Club") {
		t.Fatal("expected fixture title")
	}
	if !strings.Contains(body, `data-testid="request-history"`) {
		t.Fatal("expected request history section")
	}
}

// TestRequestListSendsCallerID verifies that RequestPage forwards X-Caller-Id
// on the list GET so request-media httpCallerCtx can authenticate the call.
func TestRequestListSendsCallerID(t *testing.T) {
	var gotCaller string
	upstream := http.NewServeMux()
	upstream.HandleFunc("/api/requests", func(w http.ResponseWriter, r *http.Request) {
		gotCaller = r.Header.Get("X-Caller-Id")
		if gotCaller == "" {
			http.Error(w, "missing X-Caller-Id", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	srv := httptest.NewServer(upstream)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	sess := &session.Session{UserID: "uid-1", Username: "alice"}
	r := requestWithSession("GET", "/request", sess)
	w := httptest.NewRecorder()
	h.RequestPage(w, r)

	if gotCaller != "uid-1" {
		t.Fatalf("X-Caller-Id: want %q, got %q", "uid-1", gotCaller)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected page 200, got %d", w.Code)
	}
}

// TestRequestListCallerIDFallsBackToUserID verifies Username="" falls back to UserID.
func TestRequestListCallerIDFallsBackToUserID(t *testing.T) {
	var gotCaller string
	upstream := http.NewServeMux()
	upstream.HandleFunc("/api/requests", func(w http.ResponseWriter, r *http.Request) {
		gotCaller = r.Header.Get("X-Caller-Id")
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	srv := httptest.NewServer(upstream)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	sess := &session.Session{UserID: "uid-42", Username: ""}
	r := requestWithSession("GET", "/request", sess)
	w := httptest.NewRecorder()
	h.RequestPage(w, r)

	if gotCaller != "uid-42" {
		t.Fatalf("X-Caller-Id fallback: want %q, got %q", "uid-42", gotCaller)
	}
}

// TestRequestApproveSendsCallerID verifies RequestApprove sends X-Caller-Id and
// that the stub rejects the call when the header is absent.
func TestRequestApproveSendsCallerID(t *testing.T) {
	var gotCaller string
	upstream := http.NewServeMux()
	upstream.HandleFunc("POST /api/requests/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		gotCaller = r.Header.Get("X-Caller-Id")
		if gotCaller == "" {
			http.Error(w, "missing X-Caller-Id", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(upstream)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	sess := &session.Session{UserID: "uid-1", Username: "alice", Roles: []string{"admin"}}
	r := requestWithSession("POST", "/request/req-99/approve", sess)
	r.SetPathValue("id", "req-99")
	w := httptest.NewRecorder()
	h.RequestApprove(w, r)

	if gotCaller != "uid-1" {
		t.Fatalf("approve X-Caller-Id: want %q, got %q", "uid-1", gotCaller)
	}
}

// TestRequestDenySendsCallerID verifies RequestDeny sends X-Caller-Id and
// that the stub rejects the call when the header is absent.
func TestRequestDenySendsCallerID(t *testing.T) {
	var gotCaller string
	upstream := http.NewServeMux()
	upstream.HandleFunc("POST /api/requests/{id}/deny", func(w http.ResponseWriter, r *http.Request) {
		gotCaller = r.Header.Get("X-Caller-Id")
		if gotCaller == "" {
			http.Error(w, "missing X-Caller-Id", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(upstream)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	sess := &session.Session{UserID: "uid-1", Username: "alice", Roles: []string{"admin"}}
	r := requestWithSession("POST", "/request/req-99/deny", sess)
	r.SetPathValue("id", "req-99")
	w := httptest.NewRecorder()
	h.RequestDeny(w, r)

	if gotCaller != "uid-1" {
		t.Fatalf("deny X-Caller-Id: want %q, got %q", "uid-1", gotCaller)
	}
}
