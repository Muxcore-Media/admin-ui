package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

func TestFailedDownloadsPageSoftEmpty(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/failed-downloads")
	w := httptest.NewRecorder()
	h.FailedDownloadsPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="failed-downloads-page"`) {
		t.Fatal("expected failed-downloads page testid")
	}
	if !strings.Contains(body, `data-testid="failed-downloads-soft-empty"`) {
		t.Fatalf("expected soft empty state, got: %s", truncate(body, 500))
	}
}

func TestFailedDownloadsPageHealthyEmpty(t *testing.T) {
	core, cleanup := startAutomationHealthyFixture(t)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/failed-downloads")
	w := httptest.NewRecorder()
	h.FailedDownloadsPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="failed-downloads-page"`) {
		t.Fatal("expected failed-downloads page testid")
	}
	if !strings.Contains(body, `data-testid="failed-downloads-empty"`) {
		t.Fatalf("expected healthy empty state, got: %s", truncate(body, 500))
	}
	if strings.Contains(body, `data-testid="failed-downloads-table"`) {
		t.Fatal("did not expect failures table on healthy empty page")
	}
}

func TestFailedDownloadsPageFixture(t *testing.T) {
	core, cleanup := startAutomationFixture(t)
	defer cleanup()

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/failed-downloads")
	w := httptest.NewRecorder()
	h.FailedDownloadsPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, `data-testid="failed-downloads-page"`) {
		t.Fatal("expected failed-downloads page testid")
	}
	if !strings.Contains(body, `data-testid="failed-downloads-table"`) {
		t.Fatalf("expected failures table, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "Fight Club.Fixture") {
		t.Fatalf("expected stuck import title, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "Bad Release") {
		t.Fatalf("expected failed download title, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="failed-downloads-stuck-badge"`) {
		t.Fatalf("expected stuck badge, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="failed-downloads-retry"`) {
		t.Fatalf("expected retry button for stuck import, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="failed-downloads-dismiss"`) {
		t.Fatalf("expected dismiss button, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, `data-testid="failed-downloads-wanted-link"`) {
		t.Fatalf("expected wanted queue link, got: %s", truncate(body, 600))
	}
	if strings.Contains(body, "Completed Movie") {
		t.Fatal("completed record should not appear in failed downloads table")
	}
}
