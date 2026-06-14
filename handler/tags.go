package handler

import (
	"net/http"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

func (h *Handler) TagsPage(w http.ResponseWriter, r *http.Request) {
	tags := h.Tags.List()
	content := templates.TagsPage(tags)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Tags", nav, content)
	h.render(w, r, component)
}

func (h *Handler) TagsCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form")
		http.Redirect(w, r, "/tags", http.StatusSeeOther)
		return
	}

	name := r.FormValue("name")
	if !h.Tags.Add(name) {
		toast(w, "error", "Invalid tag name")
		http.Redirect(w, r, "/tags", http.StatusSeeOther)
		return
	}

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "tags.create", "tag", name, nil)
	toast(w, "success", "Tag created")
	http.Redirect(w, r, "/tags", http.StatusSeeOther)
}

func (h *Handler) TagsDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	h.Tags.Remove(name)
	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "tags.delete", "tag", name, nil)
	toast(w, "success", "Tag deleted")
	http.Redirect(w, r, "/tags", http.StatusSeeOther)
}
