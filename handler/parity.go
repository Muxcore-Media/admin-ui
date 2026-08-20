package handler

import (
	"net/http"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) renderParityPage(w http.ResponseWriter, r *http.Request, title, summary string, links []templates.NavLink, notes []string) {
	content := templates.ParitySectionPage(title, summary, links, notes)
	nav := h.nav(r.URL.Path)
	component := templates.Layout(title, nav, content)
	h.render(w, r, component)
}
