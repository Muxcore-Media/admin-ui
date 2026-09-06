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

func TestDevicesPageRendersEmpty(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest(http.MethodGet, "/devices")
	w := httptest.NewRecorder()
	h.DevicesPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "devices-empty") && !strings.Contains(body, "No active admin sessions") {
		t.Fatalf("expected empty-state text, body excerpt: %.200s", body)
	}
}

func TestDevicesPageRendersActiveSessions(t *testing.T) {
	ss := session.NewStore(time.Hour)
	_, _ = ss.Create("u1", "alice", nil, nil)

	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest(http.MethodGet, "/devices")
	w := httptest.NewRecorder()
	h.DevicesPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "alice") {
		t.Fatal("expected alice in session list")
	}
}

func TestDevicesPageNilStoreShowsError(t *testing.T) {
	h := New(nil, nil, false, "test", nil, false, "", nil, nil)
	r := mustRequest(http.MethodGet, "/devices")
	w := httptest.NewRecorder()
	h.DevicesPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unavailable") {
		t.Fatal("expected unavailable error message")
	}
}

func TestDevicesRevokeRedirects(t *testing.T) {
	ss := session.NewStore(time.Hour)
	tok, _ := ss.Create("u1", "alice", nil, nil)

	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest(http.MethodPost, "/devices/"+tok+"/revoke")
	r.SetPathValue("token", tok)
	w := httptest.NewRecorder()
	h.DevicesRevoke(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if _, ok := ss.Get(tok); ok {
		t.Fatal("expected session revoked")
	}
}

func TestDevicesRenameUpdatesLabel(t *testing.T) {
	ss := session.NewStore(time.Hour)
	tok, _ := ss.Create("u1", "alice", nil, nil)

	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	form := url.Values{"label": {"Office Laptop"}}
	r := httptest.NewRequest(http.MethodPost, "/devices/"+tok+"/rename", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("token", tok)
	w := httptest.NewRecorder()
	h.DevicesRename(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	sess, ok := ss.Get(tok)
	if !ok {
		t.Fatal("expected session still exists after rename")
	}
	if sess.Label != "Office Laptop" {
		t.Fatalf("expected label %q, got %q", "Office Laptop", sess.Label)
	}
}

func TestDevicesRenameNilStoreIsNoop(t *testing.T) {
	h := New(nil, nil, false, "test", nil, false, "", nil, nil)

	form := url.Values{"label": {"any"}}
	r := httptest.NewRequest(http.MethodPost, "/devices/tok/rename", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("token", "tok")
	w := httptest.NewRecorder()
	h.DevicesRename(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
}

func TestDevicesPageShowsLabel(t *testing.T) {
	ss := session.NewStore(time.Hour)
	tok, _ := ss.Create("u1", "alice", nil, nil)
	ss.RenameSession(tok, "Living Room TV")

	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest(http.MethodGet, "/devices")
	w := httptest.NewRecorder()
	h.DevicesPage(w, r)

	if !strings.Contains(w.Body.String(), "Living Room TV") {
		t.Fatal("expected device label in page body")
	}
}
