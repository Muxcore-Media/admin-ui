package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
)

func TestPeerModuleHTTPTTimeoutsBound(t *testing.T) {
	cases := []struct {
		name string
		dial time.Duration
		read time.Duration
		page time.Duration
	}{
		{"tagging", taggingDialTimeout, taggingReadTimeout, taggingPageTimeout},
		{"request", requestDialTimeout, requestReadTimeout, requestPageTimeout},
		{"music", musicDialTimeout, musicReadTimeout, musicPageTimeout},
		{"marketplace", marketplaceDialTimeout, marketplaceReadTimeout, marketplacePageTimeout},
		{"scheduler", schedulerDialTimeout, schedulerReadTimeout, schedulerPageTimeout},
	}
	for _, c := range cases {
		if c.dial != 3*time.Second {
			t.Fatalf("%s dial: got %v, want 3s", c.name, c.dial)
		}
		if c.read < 4*time.Second || c.read > 8*time.Second {
			t.Fatalf("%s read out of range: %v", c.name, c.read)
		}
		if c.page < c.dial+c.read {
			t.Fatalf("%s page budget %v must cover dial+read", c.name, c.page)
		}
	}
}

func TestRequestPageFailsFastOnSlowBackend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/requests" {
			<-r.Context().Done()
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.RequestMediaURL = srv.URL

	r := mustRequest("GET", "/request")
	w := httptest.NewRecorder()

	start := time.Now()
	h.RequestPage(w, r)
	elapsed := time.Since(start)

	limit := requestReadTimeout + 2*time.Second
	if elapsed > limit {
		t.Fatalf("request page took %v, want ≤ %v (bounded HTTP read)", elapsed, limit)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 error page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="request-page"`) {
		t.Fatal("expected request page markup")
	}
	if !strings.Contains(strings.ToLower(body), "unreachable") &&
		!strings.Contains(strings.ToLower(body), "timeout") &&
		!strings.Contains(strings.ToLower(body), "deadline") &&
		!strings.Contains(strings.ToLower(body), "cancel") {
		t.Fatalf("expected timeout/unreachable error in body, got: %s", truncate(body, 400))
	}
}
