package handler

import (
	"net/http"
	"os"
	"strings"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) ConfigPage(w http.ResponseWriter, r *http.Request) {
	var entries []templates.ConfigEntry
	for _, env := range os.Environ() {
		key, val, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		if !strings.HasPrefix(key, "ADMIN_UI_") && !strings.HasPrefix(key, "MUXCORE_") {
			continue
		}
		redacted := strings.Contains(strings.ToUpper(key), "KEY") ||
			strings.Contains(strings.ToUpper(key), "SECRET") ||
			strings.Contains(strings.ToUpper(key), "TOKEN") ||
			strings.Contains(strings.ToUpper(key), "PASSWORD") ||
			strings.Contains(strings.ToUpper(key), "CERT") ||
			val == ""
		if !redacted {
			entries = append(entries, templates.ConfigEntry{Key: key, Value: val})
		} else {
			entries = append(entries, templates.ConfigEntry{Key: key, Redacted: true})
		}
	}

	data := templates.ConfigPageData{
		Version: h.version,
		Entries: entries,
	}

	content := templates.ConfigPage(data)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Configuration", nav, content)
	h.render(w, r, component)
}
