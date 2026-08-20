package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

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

