package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
)

func TestListAndTailLogFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_LOG_DIR", dir)

	if err := os.WriteFile(filepath.Join(dir, "core.log"), []byte("line1\nline2\nline3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth-local.log"), []byte("auth ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("nope"), 0o600)

	files := listLogFiles(dir)
	if len(files) != 2 {
		t.Fatalf("expected 2 log files, got %d", len(files))
	}
	if files[0].Name != "auth-local.log" {
		t.Fatalf("expected sorted auth-local first, got %s", files[0].Name)
	}

	tail, err := tailLogFile(dir, "core.log", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tail, "line2") || !strings.Contains(tail, "line3") {
		t.Fatalf("expected last two lines, got %q", tail)
	}
	if strings.Contains(tail, "line1") {
		t.Fatalf("expected line1 trimmed from tail, got %q", tail)
	}

	if _, err := tailLogFile(dir, "../etc/passwd", 10); err == nil {
		t.Fatal("expected path escape rejection")
	}
	if _, err := tailLogFile(dir, "missing.log", 10); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestLogsPageListsFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_LOG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "admin-ui.log"), []byte("hello admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/logs?file=admin-ui.log")
	w := httptest.NewRecorder()
	h.LogsPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="logs-page"`) {
		t.Fatal("expected logs page")
	}
	if !strings.Contains(body, "admin-ui.log") {
		t.Fatalf("expected file listed, got: %s", truncate(body, 500))
	}
	if !strings.Contains(body, "hello admin") {
		t.Fatalf("expected tail content, got: %s", truncate(body, 500))
	}
}

func TestLogsPartialTail(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADMIN_UI_LOG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "core.log"), []byte("partial-line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest("GET", "/logs/partial?file=core.log")
	w := httptest.NewRecorder()
	h.LogsPartial(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "partial-line") {
		t.Fatalf("expected tail, got %s", w.Body.String())
	}
}
