package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/Muxcore-Media/core/sdk/go/client"

	"github.com/Muxcore-Media/admin-ui/handler"
	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
)

//go:embed assets/dist/* assets/htmx.min.js assets/sse.js assets/csrf.js assets/webauthn.js assets/nav.js
var staticAssets embed.FS

var version = "0.0.0-dev"

type Config struct {
	Addr       string
	CoreAddr   string
	Insecure   bool
	TLSCert    string
	TLSKey     string
	SessionTTL time.Duration
	LogLevel   string
	LogFormat  string
	AuthAddr   string
}

func loadConfig() Config {
	ttl, _ := time.ParseDuration(env("ADMIN_UI_SESSION_TTL", "30m"))
	return Config{
		Addr:       env("ADMIN_UI_ADDR", ":8080"),
		CoreAddr:   env("ADMIN_UI_CORE_ADDR", "localhost:9090"),
		Insecure:   env("ADMIN_UI_INSECURE", "") == "true",
		TLSCert:    env("ADMIN_UI_TLS_CERT", ""),
		TLSKey:     env("ADMIN_UI_TLS_KEY", ""),
		SessionTTL: ttl,
		LogLevel:   env("ADMIN_UI_LOG_LEVEL", "info"),
		LogFormat:  env("ADMIN_UI_LOG_FORMAT", "text"),
		AuthAddr:   env("ADMIN_UI_AUTH_ADDR", "http://localhost:9401"),
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func main() {
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

	var opts2 []client.Option
	if cfg.Insecure {
		opts2 = append(opts2, client.WithInsecure())
	}

	c, err := client.Dial(cfg.CoreAddr, opts2...)
	if err != nil {
		slog.Warn("core connection failed, starting in degraded mode", "error", err)
	} else {
		coreClient = c
		coreConnected = true
		slog.Info("connected to core", "addr", cfg.CoreAddr)
	}

	ss := session.NewStore(cfg.SessionTTL)
	loginRL := newRateLimiter(6, 1*time.Minute)
	mutationRL := newRateLimiter(30, 30*time.Second) // 30 mutations per 30s window
	met := newMetrics()
	csrfKey := generateCSRFKey()

	h := handler.New(coreClient, ss, cfg.TLSCert != "" || !cfg.Insecure, version, met, coreConnected, cfg.AuthAddr, loginRL.Reset)

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
		fmt.Fprintf(w, `{"status":"%s","version":"%s"}`, status, version)
	})

	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"version":"%s"}`, version)
	})

	mux.Handle("GET /metrics", http.HandlerFunc(met.serve))

	h.RegisterRoutes(mux)

	mux.HandleFunc("GET /no-auth", func(w http.ResponseWriter, r *http.Request) {
		component := templates.ErrorLayout("No Auth Provider", templates.NoAuthPage())
		component.Render(r.Context(), w)
	})

	go func() {
		for range time.NewTicker(30 * time.Second).C {
			met.activeSess.Store(int64(ss.Count()))
		}
	}()

	mux.HandleFunc("/", h.NotAuthHandler)

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      withMiddleware(mux, csrfKey, loginRL, mutationRL),
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
		coreClient.Close()
	}
}

func generateCSRFKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		slog.Error("csrf key generation failed", "error", err)
		os.Exit(1)
	}
	return hex.EncodeToString(b)
}

func csrfToken(key string) string {
	h := sha256.Sum256([]byte(key + ":" + time.Now().Format("20060102")))
	return hex.EncodeToString(h[:16])
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

func withMiddleware(next http.Handler, csrfKey string, loginRL *rateLimiter, mutationRL *rateLimiter) http.Handler {
	var inner http.Handler = next

	inner = recoveryMiddleware(inner)
	inner = requestLoggingMiddleware(inner)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" || r.URL.Path == "/health" {
			inner.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'; object-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		// CSRF token cookie on all responses
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			token := csrfToken(csrfKey)
			http.SetCookie(w, &http.Cookie{
				Name:     "csrf-token",
				Value:    token,
				Path:     "/",
				HttpOnly: false,
				Secure:   r.TLS != nil,
				SameSite: http.SameSiteLaxMode,
			})
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
			ip := extractIP(r)
			if !loginRL.Allow(ip, r.URL.Path, r.Method, r.UserAgent()) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "too many login attempts", http.StatusTooManyRequests)
				return
			}
		}

		// Rate limit mutating endpoints
		if isMutatingEndpoint(r.URL.Path) && (r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodDelete || r.Method == http.MethodPatch) {
			ip := extractIP(r)
			if !mutationRL.Allow(ip, r.URL.Path, r.Method, r.UserAgent()) {
				w.Header().Set("Retry-After", "30")
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
		}

		inner.ServeHTTP(w, r)
	})
}

// isMutatingEndpoint returns true for paths that perform data mutations.
// These endpoints get rate-limited separately from login.
func isMutatingEndpoint(path string) bool {
	switch {
	case path == "/auth/callback":
		return true
	case path == "/logout":
		return true
	case hasPrefix(path, "/settings/"):
		return true
	case hasPrefix(path, "/users"):
		return true
	case hasPrefix(path, "/media/"):
		return true
	case hasPrefix(path, "/api/"):
		return true
	default:
		return false
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
