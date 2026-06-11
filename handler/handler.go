package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Muxcore-Media/core/sdk/go/client"

	"github.com/Muxcore-Media/admin-ui/session"
	"github.com/a-h/templ"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capAuthorizer = "authorizer"

type LoginMetrics interface {
	IncSuccess()
	IncFailure()
}

type Handler struct {
	Core          *client.Client
	Sessions      *session.Store
	secure        bool
	events        *eventRing
	version       string
	loginMetrics  LoginMetrics
	coreConnected bool
}

func New(core *client.Client, store *session.Store, secure bool, version string, lm LoginMetrics, connected bool) *Handler {
	h := &Handler{
		Core:          core,
		Sessions:      store,
		secure:        secure,
		events:        newEventRing(100),
		version:       version,
		loginMetrics:  lm,
		coreConnected: connected,
	}
	h.startEventSubscription(context.Background())
	return h
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.requireNoAuth(h.LoginPage))
	mux.HandleFunc("POST /login", h.requireNoAuth(h.Login))
	mux.HandleFunc("GET /logout", h.Logout)

	mux.HandleFunc("GET /", h.requireAuth(h.Dashboard))
	mux.HandleFunc("GET /dashboard/health", h.requireAuth(h.HealthGrid))

	mux.HandleFunc("GET /modules", h.requireAuth(h.ModuleList))
	mux.HandleFunc("GET /modules/{id}", h.requireAuth(h.ModuleDetail))

	mux.HandleFunc("GET /cluster", h.requireAuth(h.ClusterPage))
	mux.HandleFunc("GET /cluster/nodes", h.requireAuth(h.ClusterNodes))

	mux.HandleFunc("GET /events", h.requireAuth(h.EventsPage))
	mux.HandleFunc("GET /events/stream", h.requireAuth(h.EventsStream))
	mux.HandleFunc("GET /events/stats", h.requireAuth(h.EventStatsPanel))

	mux.HandleFunc("GET /storage", h.requireAuth(h.StoragePage))

	mux.HandleFunc("GET /settings", h.requireAuth(h.SettingsPage))
	mux.HandleFunc("POST /settings/{moduleID}/{key}", h.requireAuth(h.SettingsUpdate))

	mux.HandleFunc("GET /audit", h.requireAuth(h.AuditPage))

	mux.HandleFunc("GET /config", h.requireAuth(h.ConfigPage))

	mux.HandleFunc("GET /auth/status", h.AuthStatus)
}

func (h *Handler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.coreConnected {
			nav := templates.Nav(navLinks, r.URL.Path)
			content := templates.DashboardPage(0, "", true)
			component := templates.Layout("Disconnected", nav, content)
			h.render(w, r, component)
			return
		}

		cookie, err := r.Cookie("session")
		if err != nil {
			redirectToLogin(w, r)
			return
		}

		sess, ok := h.Sessions.Get(cookie.Value)
		if !ok {
			redirectToLogin(w, r)
			return
		}

		if err := h.checkAuthorized(r.Context(), sess); err != nil {
			slog.Warn("authorization denied", "user", sess.Username, "path", r.URL.Path, "error", err)
			component := templates.Forbidden()
			h.render(w, r, component)
			return
		}

		ctx := context.WithValue(r.Context(), ctxSessionKey, sess)
		next(w, r.WithContext(ctx))
	}
}

func (h *Handler) requireNoAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.coreConnected {
			nav := templates.Nav(navLinks, r.URL.Path)
			content := templates.DashboardPage(0, "", true)
			component := templates.Layout("Disconnected", nav, content)
			h.render(w, r, component)
			return
		}

		if cookie, err := r.Cookie("session"); err == nil {
			if _, ok := h.Sessions.Get(cookie.Value); ok {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}
		next(w, r)
	}
}

func (h *Handler) checkAuthorized(ctx context.Context, sess *session.Session) error {
	if h.Core == nil {
		return nil
	}

	mod, err := h.findFirstModule(ctx, capAuthorizer)
	if err != nil {
		return nil
	}

	type canRequest struct {
		Action   string `json:"Action"`
		Resource string `json:"Resource"`
		Session  struct {
			UserID      string   `json:"UserID"`
			Username    string   `json:"Username"`
			Roles       []string `json:"Roles"`
			Permissions []string `json:"Permissions"`
		} `json:"Session"`
	}

	req := canRequest{
		Action:   "admin.access",
		Resource: "admin.ui",
	}
	req.Session.UserID = sess.UserID
	req.Session.Username = sess.Username
	req.Session.Roles = sess.Roles
	req.Session.Permissions = sess.Permissions

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal authz request: %w", err)
	}

	resp, err := h.Core.Mesh.Call(ctx, mod.GetId(), methodCan, payload)
	if err != nil {
		return fmt.Errorf("authz call failed: %w", err)
	}

	var result struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("unmarshal authz response: %w", err)
	}

	if !result.Allowed {
		return fmt.Errorf("access denied")
	}
	return nil
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) NotAuthHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.Sessions.GetFromRequest(r); ok {
		w.WriteHeader(http.StatusNotFound)
		nav := templates.Nav(navLinks, r.URL.Path)
		content := templates.NotFound()
		component := templates.Layout("Not Found", nav, content)
		if err := component.Render(r.Context(), w); err != nil {
			slog.Error("render 404", "error", err)
		}
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	if err := component.Render(r.Context(), w); err != nil {
		slog.Error("template render failed", "path", r.URL.Path, "error", err)
	}
}

type contextKey string

const ctxSessionKey contextKey = "session"

func SessionFromContext(ctx context.Context) *session.Session {
	s, _ := ctx.Value(ctxSessionKey).(*session.Session)
	return s
}
