package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capAuth    = "auth"
	methodAuth = "Authenticate"
	methodCan  = "Can"
)

var staticNavLinks = []templates.NavLink{
	{Label: "Dashboard", Path: "/", Icon: "#"},
	{Label: "Modules", Path: "/modules", Icon: "#"},
	{Label: "Cluster", Path: "/cluster", Icon: "#"},
	{Label: "Events", Path: "/events", Icon: "#"},
	{Label: "Activity", Path: "/activity", Icon: "#"},
	{Label: "Formats", Path: "/formats", Icon: "#"},
	{Label: "Profiles", Path: "/formats/profiles", Icon: "#"},
	{Label: "Root Folders", Path: "/roots", Icon: "#"},
	{Label: "Naming", Path: "/rename/templates", Icon: "#"},
	{Label: "Storage", Path: "/storage", Icon: "#"},
	{Label: "Users", Path: "/users", Icon: "#"},
	{Label: "Settings", Path: "/settings", Icon: "#"},
	{Label: "Audit", Path: "/audit", Icon: "#"},
}

type AuthStatus struct {
	Available bool   `json:"available"`
	ModuleID  string `json:"module_id,omitempty"`
}

func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	// Redirect to the auth module's login page.
	redirectURL := h.AuthAddr + "/login?redirect=" + url.QueryEscape("http://"+r.Host+"/auth/callback")
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	// POST /login is no longer handled by admin UI — redirect to auth-local.
	h.LoginPage(w, r)
}

func (h *Handler) AuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "code required", http.StatusBadRequest)
		return
	}

	// Exchange the one-time code for a session token via auth-local's exchange endpoint.
	body, _ := json.Marshal(map[string]string{"code": code})
	resp, err := http.Post(h.AuthAddr+"/login/exchange", "application/json", strings.NewReader(string(body)))
	if err != nil {
		slog.Warn("auth: callback - exchange request failed", "error", err)
		http.Error(w, "auth unavailable", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "code exchange failed", http.StatusUnauthorized)
		return
	}

	var result struct {
		Token    string   `json:"token"`
		UserID   string   `json:"user_id"`
		Username string   `json:"username"`
		Roles    []string `json:"roles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "invalid response", http.StatusInternalServerError)
		return
	}

	if h.ResetLoginRate != nil {
		h.ResetLoginRate(extractClientIP(r, h.TrustedProxies))
	}

	// Create local session.
	sessionToken, err := h.Sessions.Create(result.UserID, result.Username, result.Roles, nil)
	if err != nil {
		slog.Error("auth: callback - session create failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if h.loginMetrics != nil {
		h.loginMetrics.IncSuccess()
	}

	h.auditLog(r.Context(), result.UserID, "admin.login", "session", "", map[string]string{
		"username": result.Username,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.Sessions.TTL().Seconds()),
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		if sess, ok := h.Sessions.Get(cookie.Value); ok {
			h.auditLog(r.Context(), sess.UserID, "admin.logout", "session", "", map[string]string{
				"username": sess.Username,
			})
		}
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

func (h *Handler) findFirstModule(ctx context.Context, capability string) (*discoveryv1.ModuleInfoProto, error) {
	if h.Core == nil {
		return nil, fmt.Errorf("core not connected")
	}

	modules, err := h.Core.Discovery.FindByCapability(ctx, capability)
	if err != nil {
		return nil, fmt.Errorf("discovery error: %w", err)
	}
	if len(modules) == 0 {
		return nil, fmt.Errorf("no module with capability %q", capability)
	}
	return modules[0], nil
}

func (h *Handler) AuthStatus(w http.ResponseWriter, r *http.Request) {
	status := AuthStatus{Available: false}
	if h.Core != nil {
		if mod, err := h.findFirstModule(r.Context(), capAuth); err == nil {
			status.Available = true
			status.ModuleID = mod.GetId()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
