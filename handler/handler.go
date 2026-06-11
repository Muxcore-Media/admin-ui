package handler

import (
	"net/http"

	"github.com/Muxcore-Media/core/sdk/go/client"

	"github.com/Muxcore-Media/admin-ui/session"
)

type Handler struct {
	Core     *client.Client
	Sessions *session.Store
}

func New(core *client.Client, store *session.Store) *Handler {
	return &Handler{
		Core:     core,
		Sessions: store,
	}
}

func (h *Handler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		_, ok := h.Sessions.Get(cookie.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}
