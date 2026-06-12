package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"

	"github.com/Muxcore-Media/admin-ui/session"
	"github.com/a-h/templ"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capAuthorizer   = "authorizer"
	capMediaLibrary = "media.library"
	maxRequestBody  = 1 << 20 // 1 MB
)

func limitBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
}

func toast(w http.ResponseWriter, typ, msg string) {
	// Merge with any existing HX-Trigger header.
	existing := w.Header().Get("HX-Trigger")
	var data string
	if existing != "" && existing != "null" {
		data = existing[:len(existing)-1] + ","
	} else {
		data = "{"
	}
	data += `"show-toast":{"type":"` + typ + `","message":"` + escapeJSON(msg) + `"}}`
	w.Header().Set("HX-Trigger", data)
}

func escapeJSON(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			out = append(out, '\\', '\\')
		case '"':
			out = append(out, '\\', '"')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

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
	AuthAddr      string // internal URL for server-to-server exchange calls
	AuthPublicURL string // public URL for browser redirects (e.g. https://local-auth.digifender.com)

	TrustedAuthHeader string // header name from proxy (e.g. X-Auth-User); when present, bypasses normal auth

	ResetLoginRate func(ip string)

	mediaMu      sync.RWMutex
	mediaModules []*discoveryv1.ModuleInfoProto

	mediaRefreshCh chan struct{}
	mediaSubCancel func()

	connCache   map[string]*grpc.ClientConn
	connCacheMu sync.Mutex
}

func New(core *client.Client, store *session.Store, secure bool, version string, lm LoginMetrics, connected bool, authAddr, authPublicURL, trustedAuthHeader string, resetLoginRate func(ip string)) *Handler {
	h := &Handler{
		Core:           core,
		Sessions:       store,
		secure:         secure,
		events:         newEventRing(100),
		version:        version,
		loginMetrics:   lm,
		coreConnected:  connected,
		AuthAddr:       authAddr,
		AuthPublicURL:     authPublicURL,
		TrustedAuthHeader: trustedAuthHeader,
		ResetLoginRate:    resetLoginRate,
		mediaRefreshCh: make(chan struct{}, 1),
		connCache:      make(map[string]*grpc.ClientConn),
	}
	if connected && core != nil {
		h.refreshMediaNavLinks(context.Background())
		h.startMediaEventSubscription(context.Background())
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
	mux.HandleFunc("GET /cluster/sse", h.requireAuth(h.ClusterSSE))

	mux.HandleFunc("GET /events", h.requireAuth(h.EventsPage))
	mux.HandleFunc("GET /events/stream", h.requireAuth(h.EventsStream))
	mux.HandleFunc("GET /events/stats", h.requireAuth(h.EventStatsPanel))

	mux.HandleFunc("GET /storage", h.requireAuth(h.StoragePage))

	mux.HandleFunc("GET /settings", h.requireAuth(h.SettingsPage))
	mux.HandleFunc("POST /settings/{moduleID}/{key}", h.requireAuth(h.SettingsUpdate))

	mux.HandleFunc("GET /audit", h.requireAuth(h.AuditPage))

	mux.HandleFunc("GET /config", h.requireAuth(h.ConfigPage))

	mux.HandleFunc("GET /media/{moduleID}", h.requireAuth(h.MediaLibraryList))
	mux.HandleFunc("GET /media/{moduleID}/{id}", h.requireAuth(h.MediaLibraryItem))
	mux.HandleFunc("POST /media/{moduleID}/{id}/metadata", h.requireAuth(h.MediaLibraryUpdate))
	mux.HandleFunc("GET /media/{moduleID}/{id}/artwork", h.requireAuth(h.MediaLibraryArtwork))

	mux.HandleFunc("GET /users", h.requireAuth(h.UsersPage))
	mux.HandleFunc("GET /users/create-form", h.requireAuth(h.UsersCreateForm))
	mux.HandleFunc("POST /users", h.requireAuth(h.UsersCreate))
	mux.HandleFunc("DELETE /users/{id}", h.requireAuth(h.UsersDelete))
	mux.HandleFunc("GET /users/{id}/detail", h.requireAuth(h.UsersDetail))
	mux.HandleFunc("POST /users/{id}/password", h.requireAuth(h.UsersSetPassword))
	mux.HandleFunc("POST /users/{id}/roles", h.requireAuth(h.UsersSetRoles))
	mux.HandleFunc("GET /users/{id}/totp", h.requireAuth(h.UsersTOTPStatus))
	mux.HandleFunc("POST /users/{id}/totp", h.requireAuth(h.UsersTOTP))
	mux.HandleFunc("GET /users/{id}/tokens", h.requireAuth(h.UsersTokens))
	mux.HandleFunc("POST /users/{id}/tokens", h.requireAuth(h.UsersTokens))
	mux.HandleFunc("DELETE /users/{id}/tokens/{tokenId}", h.requireAuth(h.UsersTokens))
	mux.HandleFunc("GET /users/{id}/passkeys", h.requireAuth(h.PasskeyList))
	mux.HandleFunc("DELETE /users/{id}/passkeys/{credId}", h.requireAuth(h.PasskeyDelete))
	mux.HandleFunc("GET /api/auth/passkey/register/{id}/begin", h.requireAuth(h.PasskeyBeginRegister))
	mux.HandleFunc("POST /api/auth/passkey/register/{id}/complete", h.requireAuth(h.PasskeyCompleteRegister))

	mux.HandleFunc("GET /search", h.requireAuth(h.Search))
	mux.HandleFunc("GET /metrics/view", h.requireAuth(h.MetricsPage))

	mux.HandleFunc("GET /auth/callback", h.AuthCallback)
	mux.HandleFunc("GET /auth/status", h.AuthStatus)
}

func (h *Handler) nav(currentPath string) templ.Component {
	links := make([]templates.NavLink, len(staticNavLinks), len(staticNavLinks)+4)
	copy(links, staticNavLinks)

	h.mediaMu.RLock()
	for _, mod := range h.mediaModules {
		links = append(links, templates.NavLink{
			Label: mod.GetName(),
			Path:  "/media/" + mod.GetId(),
			Icon:  "#",
		})
	}
	h.mediaMu.RUnlock()

	return templates.Nav(links, currentPath)
}

func (h *Handler) refreshMediaNavLinks(ctx context.Context) {
	if h.Core == nil {
		return
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaLibrary)
	if err != nil {
		slog.Warn("media: FindByCapability failed", "error", err)
		return
	}
	h.mediaMu.Lock()
	h.mediaModules = mods
	h.mediaMu.Unlock()
}

func (h *Handler) startMediaEventSubscription(ctx context.Context) {
	if h.Core == nil {
		return
	}

	if h.mediaSubCancel != nil {
		h.mediaSubCancel()
	}

	ch, cancel, err := h.Core.Events.Subscribe(ctx, "*")
	if err != nil {
		slog.Warn("media: subscribe to module.registered failed", "error", err)
		return
	}

	h.mediaSubCancel = cancel

	go func() {
		for ev := range ch {
			evType := ev.GetType()
			if evType != "module.registered" && evType != "module.unregistered" {
				continue
			}

			payload := ev.GetPayload()
			type moduleEventPayload struct {
				ModuleID     string   `json:"module_id"`
				Capabilities []string `json:"capabilities"`
			}
			var p moduleEventPayload
			if err := json.Unmarshal(payload, &p); err != nil {
				continue
			}

			if evType == "module.unregistered" {
				slog.Info("media: module unregistered, refreshing tabs", "module", p.ModuleID)
				h.refreshMediaNavLinks(ctx)
				continue
			}

			for _, cap := range p.Capabilities {
				if cap == capMediaLibrary {
					slog.Info("media: new media library module registered", "module", p.ModuleID)
					h.refreshMediaNavLinks(ctx)
					break
				}
			}
		}
	}()
}

func (h *Handler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.coreConnected {
			nav := h.nav(r.URL.Path)
			content := templates.DashboardPage(templates.DashboardData{Disconnected: true})
			component := templates.Layout("Disconnected", nav, content)
			h.render(w, r, component)
			return
		}

		// Trusted header auth: if configured and the proxy set the header,
		// create an admin session on-the-fly.
		if h.TrustedAuthHeader != "" {
			if headerVal := r.Header.Get(h.TrustedAuthHeader); headerVal != "" {
				sess := h.sessionFromTrustedHeader(r.Context(), headerVal)
				if sess != nil {
					ctx := context.WithValue(r.Context(), ctxSessionKey, sess)
					next(w, r.WithContext(ctx))
					return
				}
			}
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

// sessionFromTrustedHeader creates or returns a cached admin session
// for a user identified by a trusted proxy header.
func (h *Handler) sessionFromTrustedHeader(ctx context.Context, headerVal string) *session.Session {
	// Normalize: treat the header value as the username.
	// Check for an existing session keyed by this header value.
	existing := h.Sessions.GetByUserID(headerVal)
	if existing != nil {
		return existing
	}

	token, err := h.Sessions.Create(headerVal, headerVal, []string{"user", "admin"}, nil)
	if err != nil {
		slog.Warn("trusted header: session create failed", "header", h.TrustedAuthHeader, "value", headerVal, "error", err)
		return nil
	}

	sess, _ := h.Sessions.Get(token)
	return sess
}

func (h *Handler) requireNoAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.coreConnected {
			nav := h.nav(r.URL.Path)
			content := templates.DashboardPage(templates.DashboardData{Disconnected: true})
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
		return fmt.Errorf("core not connected")
	}

	mod, err := h.findFirstModule(ctx, capAuthorizer)
	if err != nil {
		return fmt.Errorf("authorizer unavailable: %w", err)
	}

	addr := mod.GetHttpAddr()
	if addr == "" {
		return fmt.Errorf("authorizer has no gRPC address")
	}

	conn, err := h.cachedConn(ctx, addr)
	if err != nil {
		return fmt.Errorf("dial authorizer %s: %w", addr, err)
	}

	ac := authv1.NewAuthServiceClient(conn)
	cresp, err := ac.Can(ctx, &authv1.CanRequest{
		UserId:   sess.UserID,
		Action:   "admin.access",
		Resource: "admin.ui",
	})
	if err != nil {
		return fmt.Errorf("authz call failed: %w", err)
	}

	if !cresp.Allowed {
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
		nav := h.nav(r.URL.Path)
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

// cachedConn returns a cached gRPC connection for the given address,
// dialing one if none exists. Callers must not close the returned conn.
func (h *Handler) cachedConn(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	h.connCacheMu.Lock()
	defer h.connCacheMu.Unlock()

	if conn, ok := h.connCache[addr]; ok {
		return conn, nil
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	h.connCache[addr] = conn
	return conn, nil
}

// cleanupConnCache closes and removes all cached gRPC connections.
func (h *Handler) cleanupConnCache() {
	h.connCacheMu.Lock()
	defer h.connCacheMu.Unlock()
	for addr, conn := range h.connCache {
		conn.Close()
		delete(h.connCache, addr)
	}
}

// auditLog writes an audit entry asynchronously. Errors are logged but not returned
// to avoid blocking the request flow.
func (h *Handler) auditLog(ctx context.Context, actor, action, resource, resourceID string, details map[string]string) {
	if h.Core == nil || h.Core.Audit == nil {
		return
	}
	go func() {
		traceID := ""
		if tid, ok := ctx.Value("trace_id").(string); ok {
			traceID = tid
		}
		if _, err := h.Core.Audit.Log(ctx, actor, action, resource, resourceID, traceID, details); err != nil {
			slog.Warn("audit log failed", "action", action, "error", err)
		}
	}()
}
