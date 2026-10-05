package adminui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/Muxcore-Media/core/sdk/go/client"

	"github.com/Muxcore-Media/admin-ui/handler"
	"github.com/Muxcore-Media/admin-ui/internal/meshdial"
	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
)

//go:embed assets/dist/* assets/branding/* assets/htmx.min.js assets/sse.js assets/csrf.js assets/webauthn.js assets/nav.js assets/streams-map.js assets/streams-live.js assets/transcode-flow.js
var staticAssets embed.FS

var version = "0.0.0-dev"

type Config struct {
	Addr             string
	CoreAddr         string
	Insecure         bool
	TLSCert          string
	TLSKey           string
	SessionTTL       time.Duration
	LogLevel         string
	LogFormat        string
	AuthAddr         string
	AuthInternalAddr string
	PublicURL        string
	TrustedProxies   string
	HealthMonitorURL string
	MetricsToken     string
}

func loadConfig() Config {
	ttl, _ := time.ParseDuration(env("ADMIN_UI_SESSION_TTL", "30m"))
	authAddr := resolveBrowserAuthAddr(env("ADMIN_UI_AUTH_ADDR", "http://localhost:9401"))
	return Config{
		Addr:             env("ADMIN_UI_ADDR", ":8080"),
		CoreAddr:         env("ADMIN_UI_CORE_ADDR", "localhost:9090"),
		Insecure:         env("ADMIN_UI_INSECURE", "") == "true",
		TLSCert:          env("ADMIN_UI_TLS_CERT", ""),
		TLSKey:           env("ADMIN_UI_TLS_KEY", ""),
		SessionTTL:       ttl,
		LogLevel:         env("ADMIN_UI_LOG_LEVEL", "info"),
		LogFormat:        env("ADMIN_UI_LOG_FORMAT", "text"),
		AuthAddr:         authAddr,
		AuthInternalAddr: env("ADMIN_UI_AUTH_INTERNAL_ADDR", authAddr),
		PublicURL:        env("ADMIN_UI_PUBLIC_URL", ""),
		TrustedProxies:   env("ADMIN_UI_TRUSTED_PROXIES", ""),
		HealthMonitorURL: env("ADMIN_UI_HEALTH_MONITOR_URL", "http://127.0.0.1:9203"),
		MetricsToken:     env("ADMIN_UI_METRICS_TOKEN", ""),
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// resolveBrowserAuthAddr keeps browser login redirects on the public auth URL when
// ADMIN_UI_AUTH_ADDR is mistakenly set to vault LAN/ZT (breaks CSRF on auth-local).
func resolveBrowserAuthAddr(configured string) string {
	configured = strings.TrimRight(strings.TrimSpace(configured), "/")
	if configured == "" {
		return configured
	}
	public := strings.TrimRight(strings.TrimSpace(os.Getenv("AUTH_HTTP_URL")), "/")
	if public == "" || !looksLikeInternalAuthURL(configured) {
		return configured
	}
	return public
}

func looksLikeInternalAuthURL(addr string) bool {
	lower := strings.ToLower(addr)
	if strings.HasPrefix(lower, "http://127.") || strings.HasPrefix(lower, "http://localhost") {
		return true
	}
	if strings.Contains(addr, "[") { // ZT/LAN IPv6 literal
		return true
	}
	if strings.HasPrefix(lower, "http://192.168.") || strings.HasPrefix(lower, "http://10.") {
		return true
	}
	return false
}

// Main runs the admin UI server with the given build version. It is the entry
// point used by cmd/module.
func Main(v string) {
	version = v
	cfg := loadConfig()

	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	var logHandler slog.Handler
	opts := &slog.HandlerOptions{Level: logLevel}
	if cfg.LogFormat == "json" {
		logHandler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		logHandler = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(logHandler))

	slog.Info("starting admin-ui", "version", version, "addr", cfg.Addr, "core", cfg.CoreAddr)

	var coreClient *client.Client
	var coreConnected bool

	// Mesh transport security (NFR-SEC-003): TLS unless
	// MUXCORE_INSECURE_DISABLE_TLS=true (dev profile), logged once here.
	_ = meshdial.LogStartup(slog.Default())
	var c *client.Client
	dialOpt, err := meshdial.DialOption()
	if err == nil {
		c, err = client.Dial(cfg.CoreAddr, client.WithGRPCOption(dialOpt))
	}
	if err != nil {
		slog.Warn("core connection failed, starting in degraded mode", "error", err)
	} else {
		coreClient = c
		coreConnected = true
		slog.Info("connected to core", "addr", cfg.CoreAddr)
	}

	sessionPath := handler.SessionFilePath()
	removeLegacySessionFile(sessionPath)
	ss := session.NewFileStore(sessionPath, cfg.SessionTTL)
	slog.Info("admin session store", "path", sessionPath)
	loginRL := newRateLimiter()
	met := newMetrics()
	trustedProxies := parseTrustedProxiesCSV(cfg.TrustedProxies)

	h := handler.New(coreClient, ss, cfg.TLSCert != "" || !cfg.Insecure, version, met, coreConnected, cfg.AuthAddr, loginRL.Reset, trustedProxies)
	h.AuthInternalAddr = cfg.AuthInternalAddr
	h.PublicURL = cfg.PublicURL
	h.HealthMonitorURL = cfg.HealthMonitorURL
	h.UserdataURL = strings.TrimRight(strings.TrimSpace(env("ADMIN_UI_USERDATA_URL", "")), "/")
	h.RequestMediaURL = strings.TrimRight(strings.TrimSpace(env("ADMIN_UI_REQUEST_MEDIA_URL", "")), "/")
	h.HydrateNetworkingFromFile()
	trustedProxies = h.TrustedProxies

	mux := http.NewServeMux()

	assetsFS, err := fs.Sub(staticAssets, "assets")
	if err != nil {
		slog.Error("static assets sub-fs", "error", err)
		os.Exit(1)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(assetsFS))))

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		status := "ok"
		code := http.StatusOK
		if !coreConnected {
			status = "degraded"
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = fmt.Fprintf(w, `{"status":"%s","version":"%s"}`, status, version)
	})

	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":"%s"}`, version)
	})

	mux.Handle("GET /metrics", http.HandlerFunc(met.serve))

	h.RegisterRoutes(mux)

	mux.HandleFunc("GET /no-auth", func(w http.ResponseWriter, r *http.Request) {
		component := templates.ErrorLayout("No Auth Provider", templates.NoAuthPage())
		_ = component.Render(r.Context(), w)
	})

	go func() {
		for range time.NewTicker(30 * time.Second).C {
			met.activeSess.Store(int64(ss.Count()))
		}
	}()

	mux.HandleFunc("/", h.NotAuthHandler)

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      withMiddleware(mux, loginRL, trustedProxies, cfg.MetricsToken),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
		ErrorLog:     slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}

	go func() {
		slog.Info("listening", "addr", cfg.Addr)
		var err error
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			err = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("http shutdown error", "error", err)
	}

	if coreClient != nil {
		_ = coreClient.Close()
	}
}

// removeLegacySessionFile deletes the plaintext session file that earlier
// releases kept under os.TempDir(); those sessions are not migrated (users
// sign in again).
func removeLegacySessionFile(current string) {
	legacy := filepath.Join(handler.LegacyTempDataDir(), "sessions.json")
	if filepath.Clean(current) == legacy {
		return
	}
	if err := os.Remove(legacy); err == nil {
		slog.Info("removed legacy plaintext session file; users must sign in again", "path", legacy)
	}
}

func newCSRFCookieValue() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte("fallback-csrf-token"))
	}
	return hex.EncodeToString(b)
}

func ensureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie("csrf-token"); err == nil && c.Value != "" {
		return c.Value
	}
	token := newCSRFCookieValue()
	http.SetCookie(w, &http.Cookie{
		Name:     "csrf-token",
		Value:    token,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: false,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	return token
}

func sseWriteDeadlineMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cluster/sse" || r.URL.Path == "/streams/events" {
			rc := http.NewResponseController(w)
			_ = rc.SetWriteDeadline(time.Time{})
		}
		next.ServeHTTP(w, r)
	})
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered",
					"path", r.URL.Path,
					"method", r.Method,
					"error", rec,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lrw, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.statusCode,
			"duration", time.Since(start).String(),
		)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *loggingResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush preserves http.Flusher for SSE (e.g. /cluster/sse).
func (w *loggingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer for http.ResponseController.
func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func withMiddleware(next http.Handler, loginRL *rateLimiter, trustedProxies []net.IPNet, metricsToken string) http.Handler {
	inner := sseWriteDeadlineMiddleware(next)

	inner = recoveryMiddleware(inner)
	inner = requestLoggingMiddleware(inner)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			inner.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/metrics" {
			if metricsToken != "" {
				auth := r.Header.Get("Authorization")
				if auth != "Bearer "+metricsToken {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
			}
			inner.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'; object-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			ensureCSRFCookie(w, r)
		}

		// CSRF double-submit check on mutating requests
		if r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodDelete || r.Method == http.MethodPatch {
			cookie, err := r.Cookie("csrf-token")
			if err != nil {
				slog.Warn("csrf: missing cookie", "path", r.URL.Path, "method", r.Method)
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			headerVal := r.Header.Get("X-CSRF-Token")
			if headerVal == "" {
				slog.Warn("csrf: missing header", "path", r.URL.Path, "method", r.Method)
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			if cookie.Value != headerVal {
				slog.Warn("csrf: token mismatch", "path", r.URL.Path, "method", r.Method)
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}

		// Rate limit login
		if r.URL.Path == "/login" && r.Method == http.MethodPost {
			ip := extractClientIP(r, trustedProxies)
			if !loginRL.Allow(ip, r.URL.Path, r.Method, r.UserAgent()) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "too many login attempts", http.StatusTooManyRequests)
				return
			}
		}

		inner.ServeHTTP(w, r)
	})
}
