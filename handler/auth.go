package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capAuth             = "auth"
	methodAuth          = "Authenticate"
	methodCan           = "Can"
	authExchangeTimeout = 5 * time.Second
)

func authHTTPDo(_ context.Context, req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: authExchangeTimeout}).Do(req)
}

var staticNavLinks = []templates.NavLink{
	// Overview
	{Label: "Dashboard", Path: "/", Icon: "dashboard", Group: "Overview"},

	// Daily admin — common household operator tasks
	{Label: "Users", Path: "/users", Icon: "users", Group: "Daily admin"},
	{Label: "Invites", Path: "/invites", Icon: "mail", Group: "Daily admin"},
	{Label: "Libraries", Path: "/libraries", Icon: "library", Group: "Daily admin"},
	{Label: "Request", Path: "/request", Icon: "inbox", Group: "Daily admin"},
	{Label: "Approvals", Path: "/approvals", Icon: "check-sq", Group: "Daily admin"},
	{Label: "Queue", Path: "/queue", Icon: "list", Group: "Daily admin"},
	{Label: "Wanted / Missing", Path: "/wanted", Icon: "alert-circle", Group: "Daily admin"},
	{Label: "Calendar", Path: "/calendar", Icon: "calendar", Group: "Daily admin"},
	{Label: "Backups", Path: "/backups", Icon: "archive", Group: "Daily admin"},
	{Label: "Branding", Path: "/branding", Icon: "palette", Group: "Daily admin"},
	{Label: "Activity", Path: "/activity", Icon: "pulse", Group: "Daily admin"},

	// Library — paths, naming, and quality setup
	{Label: "Root Folders", Path: "/roots", Icon: "folder", Group: "Library"},
	{Label: "Library Scan", Path: "/library-scan", Icon: "repeat", Group: "Library"},
	{Label: "Naming", Path: "/rename/templates", Icon: "pencil", Group: "Library"},
	{Label: "Metadata", Path: "/metadata", Icon: "tag", Group: "Library"},
	{Label: "Formats", Path: "/formats", Icon: "sliders", Group: "Library"},
	{Label: "Music", Path: "/music", Icon: "music", Group: "Library"},
	{Label: "Tagging", Path: "/tagging", Icon: "tags", Group: "Library"},
	{Label: "Profiles", Path: "/formats/profiles", Icon: "award", Group: "Library"},
	{Label: "Release Profiles", Path: "/formats/release-profiles", Icon: "badge", Group: "Library"},

	// Automation
	{Label: "Acquisition Health", Path: "/acquisition-health", Icon: "activity", Group: "Automation"},
	{Label: "Automation", Path: "/automation", Icon: "zap", Group: "Automation"},
	{Label: "Release Search", Path: "/release-search", Icon: "search", Group: "Automation"},
	{Label: "Import", Path: "/import", Icon: "download", Group: "Automation"},
	{Label: "Subtitles", Path: "/subtitles", Icon: "captions", Group: "Automation"},
	{Label: "List Sync", Path: "/list-sync", Icon: "repeat", Group: "Automation"},
	{Label: "Tasks", Path: "/tasks", Icon: "check-sq", Group: "Automation"},

	// Playback
	{Label: "Streams", Path: "/streams", Icon: "monitor", Group: "Playback"},
	{Label: "Watch Stats", Path: "/watchstats", Icon: "bar-chart", Group: "Playback"},
	{Label: "Transcode", Path: "/transcode", Icon: "film", Group: "Playback"},
	{Label: "Playback", Path: "/playback", Icon: "play", Group: "Playback"},
	{Label: "Live TV", Path: "/livetv", Icon: "tv", Group: "Playback"},
	{Label: "Jellyfin", Path: "/jellyfin", Icon: "server", Group: "Playback"},

	// Monitoring
	{Label: "Events", Path: "/events", Icon: "radio", Group: "Monitoring"},
	{Label: "Audit", Path: "/audit", Icon: "shield", Group: "Monitoring"},
	{Label: "Logs", Path: "/logs", Icon: "file-text", Group: "Monitoring"},

	// Advanced — modules, networking, migrations, and power-user tools
	{Label: "Modules", Path: "/modules", Icon: "box", Group: "Advanced"},
	{Label: "Marketplace", Path: "/marketplace", Icon: "bag", Group: "Advanced"},
	{Label: "Plugins", Path: "/plugins", Icon: "layers", Group: "Advanced"},
	{Label: "Cluster", Path: "/cluster", Icon: "cluster", Group: "Advanced"},
	{Label: "Settings", Path: "/settings", Icon: "gear", Group: "Advanced"},
	{Label: "Networking", Path: "/networking", Icon: "globe", Group: "Advanced"},
	{Label: "API Keys", Path: "/keys", Icon: "key-round", Group: "Advanced"},
	{Label: "Storage", Path: "/storage", Icon: "hard-drive", Group: "Advanced"},
	{Label: "Devices", Path: "/devices", Icon: "smartphone", Group: "Advanced"},
	{Label: "Auth / SSO", Path: "/auth", Icon: "key", Group: "Advanced"},
	{Label: "Migrate", Path: "/migrate", Icon: "swap", Group: "Advanced"},
	{Label: "Maintainer", Path: "/maintainer", Icon: "wrench", Group: "Advanced"},
}

type AuthStatus struct {
	Available bool   `json:"available"`
	ModuleID  string `json:"module_id,omitempty"`
}

func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	// Redirect browsers to the public auth origin (Caddy), not loopback.
	callback := h.publicOrigin(r) + "/auth/callback"
	redirectURL := h.AuthAddr + "/login?redirect=" + url.QueryEscape(callback)
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	// POST /login is no longer handled by admin UI — redirect to auth-local.
	h.LoginPage(w, r)
}

func (h *Handler) authExchangeBase() string {
	if h.AuthInternalAddr != "" {
		return h.AuthInternalAddr
	}
	return h.AuthAddr
}

func (h *Handler) publicOrigin(r *http.Request) string {
	if h.PublicURL != "" {
		return strings.TrimRight(h.PublicURL, "/")
	}
	trusted := peerIsTrustedProxy(r, h.TrustedProxies)
	scheme := "http"
	if r.TLS != nil || (trusted && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	host := r.Host
	if trusted {
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			host = fwd
		}
	}
	return scheme + "://" + host
}

func (h *Handler) AuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "code required", http.StatusBadRequest)
		return
	}

	// Exchange over the internal URL — browsers use AuthAddr (Caddy), but the
	// host often cannot resolve/trust https://auth.*.
	body, _ := json.Marshal(map[string]string{"code": code})
	exchangeURL := h.authExchangeBase() + "/login/exchange"
	exCtx, exCancel := context.WithTimeout(r.Context(), authExchangeTimeout)
	defer exCancel()
	req, err := http.NewRequestWithContext(exCtx, http.MethodPost, exchangeURL, strings.NewReader(string(body)))
	if err != nil {
		slog.Warn("auth: callback - build exchange request failed", "error", err, "url", exchangeURL)
		http.Error(w, "auth unavailable", http.StatusServiceUnavailable)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := authHTTPDo(exCtx, req)
	if err != nil {
		slog.Warn("auth: callback - exchange request failed", "error", err, "url", exchangeURL)
		msg := "auth unavailable"
		if exCtx.Err() == context.DeadlineExceeded {
			msg = fmt.Sprintf("auth exchange timed out after %s", authExchangeTimeout)
		}
		http.Error(w, msg, http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "code exchange failed", http.StatusUnauthorized)
		return
	}

	var result struct {
		Token    string         `json:"token"`
		UserID   string         `json:"user_id"`
		Username string         `json:"username"`
		Roles    []string       `json:"roles"`
		TenantID string         `json:"tenant_id"`
		Claims   map[string]any `json:"claims"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		http.Error(w, "invalid response", http.StatusInternalServerError)
		return
	}

	if h.ResetLoginRate != nil {
		h.ResetLoginRate(extractClientIP(r, h.TrustedProxies))
	}

	tenantID := strings.TrimSpace(result.TenantID)
	if tenantID == "" && result.Claims != nil {
		if v, ok := result.Claims["tenant_id"].(string); ok {
			tenantID = strings.TrimSpace(v)
		}
	}

	// Create local session.
	sessionToken, err := h.Sessions.CreateWithTenant(result.UserID, result.Username, tenantID, result.Roles, nil)
	if err != nil {
		slog.Error("auth: callback - session create failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Sessions.BindAuthLocalToken(sessionToken, result.Token)

	if h.loginMetrics != nil {
		h.loginMetrics.IncSuccess()
	}

	h.auditLog(r.Context(), result.UserID, "admin.login", "session", "", map[string]string{
		"username": result.Username,
	})

	origin := h.publicOrigin(r)
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secure || strings.HasPrefix(origin, "https://"),
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
	_ = json.NewEncoder(w).Encode(status)
}
