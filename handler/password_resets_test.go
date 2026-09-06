package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/admin-ui/session"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

func writePasswordResetFixture(t *testing.T, dir string, requests any) {
	t.Helper()
	path := filepath.Join(dir, "password-resets.json")
	raw, err := json.Marshal(map[string]any{"requests": requests})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPasswordResetQueue_emptyFileMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)

	got := loadPasswordResetQueue(nil)
	if !got.SoftEmpty {
		t.Fatalf("expected soft empty, got %+v", got)
	}
	if got.Error != "" {
		t.Fatalf("unexpected error: %q", got.Error)
	}
	if len(got.Rows) != 0 {
		t.Fatalf("expected no rows, got %d", len(got.Rows))
	}
}

func TestLoadPasswordResetQueue_oneRow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)
	writePasswordResetFixture(t, dir, []map[string]any{{
		"id":         "req-1",
		"username":   "alice",
		"note":       "forgot at login",
		"created_at": time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		"status":     "pending",
	}})

	got := loadPasswordResetQueue([]*authv1.UserInfo{{
		Id:       "user-1",
		Username: "alice",
	}})
	if got.SoftEmpty || got.Error != "" {
		t.Fatalf("unexpected empty/error: %+v", got)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got.Rows))
	}
	if got.Rows[0].Username != "alice" || got.Rows[0].UserID != "user-1" {
		t.Fatalf("row=%+v", got.Rows[0])
	}
	if got.Rows[0].Note != "forgot at login" {
		t.Fatalf("note=%q", got.Rows[0].Note)
	}
}

func TestLoadPasswordResetQueue_unreadableFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)
	path := filepath.Join(dir, "password-resets.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := loadPasswordResetQueue(nil)
	if got.Error == "" {
		t.Fatal("expected read error")
	}
	if got.SoftEmpty {
		t.Fatal("error state should not be soft empty")
	}
}

func TestPasswordResetsPage_empty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)

	r := mustRequest(http.MethodGet, "/password-resets")
	w := httptest.NewRecorder()
	h.PasswordResetsPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="password-resets-page"`) {
		t.Fatal("expected password resets page")
	}
	if !strings.Contains(body, `data-testid="password-resets-empty"`) {
		t.Fatal("expected empty state")
	}
}

func TestPasswordResetsPage_oneRow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)
	writePasswordResetFixture(t, dir, []map[string]any{{
		"id":         "req-1",
		"username":   "bob",
		"note":       "locked out",
		"created_at": time.Date(2026, 3, 1, 15, 30, 0, 0, time.UTC),
		"status":     "pending",
	}})

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)

	r := mustRequest(http.MethodGet, "/password-resets")
	w := httptest.NewRecorder()
	h.PasswordResetsPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`data-testid="password-reset-row"`,
		`bob`,
		`locked out`,
		`data-testid="password-reset-no-user"`,
		`data-testid="password-reset-dismiss"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q", want)
		}
	}
}

func TestPasswordResetsDismiss(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_DATA_DIR", dir)
	writePasswordResetFixture(t, dir, []map[string]any{{
		"id":       "req-1",
		"username": "bob",
		"status":   "pending",
	}})

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)

	r := mustRequest(http.MethodPost, "/password-resets/req-1/dismiss")
	r.SetPathValue("id", "req-1")
	w := httptest.NewRecorder()
	h.PasswordResetsDismiss(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	got := loadPasswordResetQueue(nil)
	if !got.SoftEmpty {
		t.Fatalf("expected queue empty after dismiss, got %+v", got)
	}
}
