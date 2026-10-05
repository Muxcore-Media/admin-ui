package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfinePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "existing"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	ok := map[string]string{
		"":                               root,
		"sub":                            filepath.Join(root, "sub"),
		"existing/new/deeper":            filepath.Join(root, "existing", "new", "deeper"),
		"a/../b":                         filepath.Join(root, "b"),
		filepath.Join(root, "abs-child"): filepath.Join(root, "abs-child"),
	}
	for in, want := range ok {
		got, err := confinePath(root, in)
		if err != nil || got != want {
			t.Errorf("confinePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	bad := []string{
		"..",
		"../sibling",
		"sub/../../etc",
		"/etc/passwd",
		"/tmp/muxcore-restore",
		root + "-sibling",
		"escape",
		"escape/child",
		"bad\x00name",
	}
	for _, in := range bad {
		if got, err := confinePath(root, in); err == nil {
			t.Errorf("confinePath(%q) = %q, want error", in, got)
		}
	}

	if _, err := confinePath("relative/root", "x"); err == nil {
		t.Error("expected error for non-absolute root")
	}
}

func TestConfinePathRootNotLocal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent", "restore")
	got, err := confinePath(root, "x")
	if err != nil || got != filepath.Join(root, "x") {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := confinePath(root, "../x"); err == nil {
		t.Fatal("expected lexical escape rejected")
	}
}

func TestRestoreRootEnv(t *testing.T) {
	t.Setenv("ADMIN_UI_RESTORE_ROOT", "")
	t.Setenv("BACKUP_RESTORE_DIR", "")
	if got := restoreRoot(); got != defaultRestoreRoot {
		t.Fatalf("default = %q", got)
	}
	t.Setenv("BACKUP_RESTORE_DIR", "/srv/restore/")
	if got := restoreRoot(); got != "/srv/restore" {
		t.Fatalf("BACKUP_RESTORE_DIR = %q", got)
	}
	t.Setenv("ADMIN_UI_RESTORE_ROOT", "/data/admin-restore")
	if got := restoreRoot(); got != "/data/admin-restore" {
		t.Fatalf("ADMIN_UI_RESTORE_ROOT = %q", got)
	}
}

func TestBackupsRestoreRejectsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ADMIN_UI_RESTORE_ROOT", root)
	h := &Handler{}
	for _, target := range []string{"/etc", "../../etc", "/tmp/muxcore-restore"} {
		form := url.Values{"target_path": {target}}
		req := httptest.NewRequest(http.MethodPost, "/backups/b1/restore", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", "b1")
		rec := httptest.NewRecorder()
		h.BackupsRestore(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("target %q: status %d, want 400", target, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "restore root") {
			t.Fatalf("target %q: unclear error %q", target, rec.Body.String())
		}
	}
}
