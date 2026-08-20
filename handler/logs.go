package handler

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func logDir() string {
	return envOr("ADMIN_UI_LOG_DIR", envOr("MVP_RUN", filepath.Join(os.TempDir(), "muxcore-logs")))
}

func (h *Handler) LogsPage(w http.ResponseWriter, r *http.Request) {
	dir := logDir()
	files := listLogFiles(dir)
	selected := filepath.Base(strings.TrimSpace(r.URL.Query().Get("file")))
	var tail string
	var errMsg string
	if selected != "" {
		content, err := tailLogFile(dir, selected, 200)
		if err != nil {
			errMsg = err.Error()
		} else {
			tail = content
		}
	}

	var events []templates.EventItem
	if h.events != nil {
		events = h.events.Snapshot()
	}

	data := templates.LogsPageData{
		LogDir:   dir,
		Files:    files,
		Selected: selected,
		Tail:     tail,
		Error:    errMsg,
		Events:   events,
	}
	content := templates.LogsLivePage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Logs", nav, content)
	h.render(w, r, component)
}

func (h *Handler) LogsPartial(w http.ResponseWriter, r *http.Request) {
	dir := logDir()
	selected := filepath.Base(strings.TrimSpace(r.URL.Query().Get("file")))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if selected != "" {
		content, err := tailLogFile(dir, selected, 200)
		if err != nil {
			_ = templates.LogTailPanel(selected, "", err.Error()).Render(r.Context(), w)
			return
		}
		_ = templates.LogTailPanel(selected, content, "").Render(r.Context(), w)
		return
	}
	var events []templates.EventItem
	if h.events != nil {
		events = h.events.Snapshot()
	}
	_ = templates.EventTable(events).Render(r.Context(), w)
}

func listLogFiles(dir string) []templates.LogFileInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []templates.LogFileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".log") && !strings.HasSuffix(lower, ".jsonl") {
			continue
		}
		info, err := e.Info()
		size := int64(0)
		mod := ""
		if err == nil {
			size = info.Size()
			mod = info.ModTime().Format("2006-01-02 15:04:05")
		}
		out = append(out, templates.LogFileInfo{
			Name:    name,
			Size:    size,
			ModTime: mod,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// tailLogFile returns the last maxLines of a log under dir. name must be a base name (no path).
func tailLogFile(dir, name string, maxLines int) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid log file name")
	}
	full := filepath.Join(dir, name)
	// Ensure resolved path stays under dir
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	absFile, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absFile, absDir+string(os.PathSeparator)) && absFile != absDir {
		return "", fmt.Errorf("path escape rejected")
	}
	f, err := os.Open(full)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	if maxLines <= 0 {
		maxLines = 200
	}
	lines := make([]string, 0, maxLines)
	sc := bufio.NewScanner(f)
	// Allow long log lines (up to 1 MiB)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > maxLines {
			lines = lines[1:]
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	joined := strings.Join(lines, "\n")
	if !utf8.ValidString(joined) {
		joined = strings.ToValidUTF8(joined, "�")
	}
	return joined, nil
}
