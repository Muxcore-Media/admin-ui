package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capAuth    = "auth"
	methodAuth = "Authenticate"
	methodCan  = "Can"
)

var navLinks = []templates.NavLink{
	{Label: "Dashboard", Path: "/", Icon: "#"},
	{Label: "Modules", Path: "/modules", Icon: "#"},
	{Label: "Cluster", Path: "/cluster", Icon: "#"},
	{Label: "Events", Path: "/events", Icon: "#"},
	{Label: "Storage", Path: "/storage", Icon: "#"},
	{Label: "Settings", Path: "/settings", Icon: "#"},
	{Label: "Audit", Path: "/audit", Icon: "#"},
}

type AuthStatus struct {
	Available bool   `json:"available"`
	ModuleID  string `json:"module_id,omitempty"`
}

func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	component := templates.LoginPage("")
	component.Render(r.Context(), w)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		component := templates.LoginForm("invalid form data")
		component.Render(r.Context(), w)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	if username == "" || password == "" {
		component := templates.LoginForm("username and password are required")
		component.Render(r.Context(), w)
		return
	}

	sess, err := h.authenticate(r.Context(), username, password)
	if err != nil {
		slog.Warn("login failed", "username", username, "error", err)
		component := templates.LoginForm("invalid username or password")
		component.Render(r.Context(), w)
		return
	}

	token, err := h.Sessions.Create(sess.UserID, sess.Username, sess.Roles, sess.Permissions)
	if err != nil {
		slog.Error("session create failed", "error", err)
		component := templates.LoginForm("internal error")
		component.Render(r.Context(), w)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.Sessions.TTL().Seconds()),
	})

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		h.Sessions.Revoke(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	connected := h.Core != nil
	content := templates.DashboardPage(connected)
	nav := templates.Nav(navLinks, "/")
	component := templates.Layout("Dashboard", nav, content)
	component.Render(r.Context(), w)
}

func (h *Handler) HealthGrid(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	// Placeholder — Phase 1 will implement real health data
	w.Write([]byte(`<div class="text-sm text-gray-400">Health data loading...</div>`))
}

func (h *Handler) AuthStatus(w http.ResponseWriter, r *http.Request) {
	status := h.checkAuthProviders(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (h *Handler) authenticate(ctx context.Context, username, password string) (*authSession, error) {
	mod, err := h.findFirstModule(ctx, capAuth)
	if err != nil {
		return nil, fmt.Errorf("auth provider unavailable: %w", err)
	}

	creds := map[string]any{
		"Type": "password",
		"Data": []byte(username + ":" + password),
	}
	payload, err := json.Marshal(creds)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}

	resp, err := h.Core.Mesh.Call(ctx, mod.GetId(), methodAuth, payload)
	if err != nil {
		return nil, fmt.Errorf("auth call failed: %w", err)
	}

	var sess authSession
	if err := json.Unmarshal(resp, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal auth response: %w", err)
	}

	return &sess, nil
}

func (h *Handler) checkAuthProviders(ctx context.Context) AuthStatus {
	mod, err := h.findFirstModule(ctx, capAuth)
	if err != nil {
		return AuthStatus{Available: false}
	}
	return AuthStatus{
		Available: true,
		ModuleID:  mod.GetId(),
	}
}

func (h *Handler) findFirstModule(ctx context.Context, capability string) (*discoveryv1.ModuleInfoProto, error) {
	modules, err := h.Core.Discovery.FindByCapability(ctx, capability)
	if err != nil {
		return nil, fmt.Errorf("discovery error: %w", err)
	}
	if len(modules) == 0 {
		return nil, fmt.Errorf("no module with capability %q", capability)
	}
	return modules[0], nil
}

type authSession struct {
	UserID      string   `json:"UserID"`
	Username    string   `json:"Username"`
	Roles       []string `json:"Roles"`
	Permissions []string `json:"Permissions"`
	Token       string   `json:"Token"`
}

func (s *authSession) Safe() authSession {
	return authSession{
		UserID:      s.UserID,
		Username:    s.Username,
		Roles:       append([]string(nil), s.Roles...),
		Permissions: append([]string(nil), s.Permissions...),
	}
}
