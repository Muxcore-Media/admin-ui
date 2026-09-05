package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

// pendingFixture is the JSON returned by a stub request-media /api/requests?status=pending.
var pendingFixture = []map[string]any{
	{"id": "req-1", "itemType": "movie", "tmdbId": 550, "title": "Fight Club", "year": 1999, "status": "pending", "requestedBy": "alice"},
	{"id": "req-2", "itemType": "tv", "tmdbId": 1396, "title": "Breaking Bad", "year": 2008, "status": "pending", "requestedBy": "bob"},
}

var recentFixture = []map[string]any{
	{"id": "req-3", "itemType": "movie", "tmdbId": 218, "title": "The Terminator", "year": 1984, "status": "approved", "requestedBy": "carol", "approvedBy": "admin"},
	{"id": "req-4", "itemType": "movie", "tmdbId": 807, "title": "Se7en", "year": 1995, "status": "denied", "requestedBy": "dave", "denyReason": "already owned"},
}

// newStubRequestMedia builds an httptest.Server that stubs request-media.
// pendingCh, if non-nil, receives the approve/deny POST bodies.
func newStubRequestMedia(t *testing.T, pendingRows, allRows []map[string]any, actionCh chan<- []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/requests", func(w http.ResponseWriter, r *http.Request) {
		rows := allRows
		if r.URL.Query().Get("status") == "pending" {
			rows = pendingRows
		}
		_ = json.NewEncoder(w).Encode(rows)
	})

	mux.HandleFunc("/api/requests/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if actionCh != nil {
			actionCh <- body
		}
		w.WriteHeader(http.StatusNoContent)
	})

	return httptest.NewServer(mux)
}

func TestApprovalsPageSoftEmpty(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/approvals")
	w := httptest.NewRecorder()
	h.ApprovalsPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="approvals-page"`) {
		t.Fatal("missing approvals-page testid")
	}
	if !strings.Contains(body, `data-testid="approvals-soft-empty"`) {
		t.Fatalf("expected soft-empty, got: %s", truncate(body, 500))
	}
}

func TestApprovalsPageUpstreamError(t *testing.T) {
	// Stub that always returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal", http.StatusInternalServerError)
	}))
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	r := mustRequest("GET", "/approvals")
	w := httptest.NewRecorder()
	h.ApprovalsPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (soft error page), got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="approvals-error"`) {
		t.Fatalf("expected error banner, got: %s", truncate(body, 500))
	}
}

func TestApprovalsPageHappyPath(t *testing.T) {
	allRows := append(pendingFixture, recentFixture...)
	srv := newStubRequestMedia(t, pendingFixture, allRows, nil)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	r := mustRequest("GET", "/approvals")
	w := httptest.NewRecorder()
	h.ApprovalsPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="approvals-pending-list"`) {
		t.Fatalf("expected pending list, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, "Fight Club") || !strings.Contains(body, "Breaking Bad") {
		t.Fatal("expected pending titles")
	}
	if !strings.Contains(body, `data-testid="approvals-approve"`) {
		t.Fatal("expected approve button")
	}
	if !strings.Contains(body, `data-testid="approvals-deny"`) {
		t.Fatal("expected deny button")
	}
	if !strings.Contains(body, `data-testid="approvals-recent-list"`) {
		t.Fatalf("expected recent list, got: %s", truncate(body, 800))
	}
	if !strings.Contains(body, "The Terminator") || !strings.Contains(body, "Se7en") {
		t.Fatal("expected recent titles")
	}
	if !strings.Contains(body, "already owned") {
		t.Fatal("expected deny reason displayed")
	}
}

func TestApprovalsApproveRequest(t *testing.T) {
	ch := make(chan []byte, 1)
	allRows := append(pendingFixture, recentFixture...)
	srv := newStubRequestMedia(t, pendingFixture, allRows, ch)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	r := mustRequest("POST", "/approvals/req-1/approve")
	r.SetPathValue("id", "req-1")
	w := httptest.NewRecorder()
	h.ApprovalsApprove(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", w.Code)
	}
	select {
	case body := <-ch:
		var got map[string]string
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("approve payload not valid JSON: %s", body)
		}
		if got["by"] == "" {
			t.Error("approve payload missing 'by' field")
		}
	default:
		t.Fatal("request-media approve endpoint not called")
	}
}

func TestApprovalsDenyRequest(t *testing.T) {
	cases := []struct {
		name           string
		reason         string
		wantReasonSent bool
	}{
		{"with reason", "duplicate", true},
		{"no reason", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := make(chan []byte, 1)
			allRows := append(pendingFixture, recentFixture...)
			srv := newStubRequestMedia(t, pendingFixture, allRows, ch)
			defer srv.Close()

			ss := session.NewStore(0)
			h := New(nil, ss, false, "test", nil, false, "", nil, nil)
			h.RequestMediaURL = srv.URL

			form := url.Values{"reason": {tc.reason}}
			r, _ := http.NewRequest("POST", "/approvals/req-1/deny", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.RemoteAddr = "127.0.0.1:12345"
			r.SetPathValue("id", "req-1")
			w := httptest.NewRecorder()
			h.ApprovalsDeny(w, r)

			if w.Code != http.StatusSeeOther {
				t.Fatalf("expected 303 redirect, got %d", w.Code)
			}
			select {
			case body := <-ch:
				var got map[string]string
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("deny payload not valid JSON: %s", body)
				}
				if got["by"] == "" {
					t.Error("deny payload missing 'by' field")
				}
				if tc.wantReasonSent && got["reason"] != tc.reason {
					t.Errorf("want reason=%q, got %q", tc.reason, got["reason"])
				}
				if !tc.wantReasonSent && got["reason"] != "" {
					t.Errorf("expected no reason in payload, got %q", got["reason"])
				}
			default:
				t.Fatal("request-media deny endpoint not called")
			}
		})
	}
}

func TestApprovalsNoPendingShowsEmpty(t *testing.T) {
	srv := newStubRequestMedia(t, []map[string]any{}, recentFixture, nil)
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	r := mustRequest("GET", "/approvals")
	w := httptest.NewRecorder()
	h.ApprovalsPage(w, r)

	body := w.Body.String()
	if !strings.Contains(body, `data-testid="approvals-pending-empty"`) {
		t.Fatalf("expected pending-empty note, got: %s", truncate(body, 500))
	}
}
