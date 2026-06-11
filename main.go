package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Muxcore-Media/core/sdk/go/client"

	"github.com/Muxcore-Media/admin-ui/handler"
	"github.com/Muxcore-Media/admin-ui/session"
	templates "github.com/Muxcore-Media/admin-ui/templ"
)

//go:embed assets/dist/* assets/htmx.min.js assets/sse.js
var staticAssets embed.FS

var version = "0.0.0-dev"

type Config struct {
	Addr        string
	CoreAddr    string
	Insecure    bool
	TLSCert     string
	TLSKey      string
	SessionTTL  time.Duration
	LogLevel    string
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

	switch cfg.LogLevel {
	case "debug":
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "warn":
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "error":
		slog.SetLogLoggerLevel(slog.LevelError)
	default:
		slog.SetLogLoggerLevel(slog.LevelInfo)
	}

	slog.Info("starting admin-ui", "version", version, "addr", cfg.Addr, "core", cfg.CoreAddr)

	var opts []client.Option
	if cfg.Insecure {
		opts = append(opts, client.WithInsecure())
	}

	c, err := client.Dial(cfg.CoreAddr, opts...)
	if err != nil {
		slog.Error("failed to dial core", "error", err)
		os.Exit(1)
	}
	defer c.Close()
	slog.Info("connected to core", "addr", cfg.CoreAddr)

	ss := session.NewStore(cfg.SessionTTL)
	loginRL := newRateLimiter()
	met := newMetrics()
	csrfKey := generateCSRFKey()

	h := handler.New(c, ss, cfg.TLSCert != "" || !cfg.Insecure, version, met)

	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.FileServer(http.FS(staticAssets)))

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"version":"%s"}`, version)
	})

	mux.Handle("GET /metrics", met.handler(http.HandlerFunc(met.serve)))

	h.RegisterRoutes(mux)

	mux.HandleFunc("GET /no-auth", func(w http.ResponseWriter, r *http.Request) {
		component := templates.ErrorLayout("No Auth Provider", templates.NoAuthPage())
		component.Render(r.Context(), w)
	})

	// Track active sessions for metrics
	go func() {
		for range time.NewTicker(30 * time.Second).C {
			met.activeSess.Store(int64(ss.Count()))
		}
	}()

	mux.HandleFunc("/", h.NotAuthHandler)

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      withMiddleware(met.handler(mux), csrfKey, loginRL),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("listening", "addr", cfg.Addr)
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			if err := srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("server error", "error", err)
				os.Exit(1)
			}
		} else {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("server error", "error", err)
				os.Exit(1)
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}

func generateCSRFKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("csrf key generation: %v", err))
	}
	return hex.EncodeToString(b)
}

func csrfToken(key string) string {
	h := sha256.Sum256([]byte(key + ":" + time.Now().Format("20060102")))
	return hex.EncodeToString(h[:16])
}

func withMiddleware(next http.Handler, csrfKey string, loginRL *rateLimiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		// CSRF: set token cookie on all responses
		if r.Method == http.MethodGet {
			token := csrfToken(csrfKey)
			http.SetCookie(w, &http.Cookie{
				Name:     "csrf-token",
				Value:    token,
				Path:     "/",
				HttpOnly: false, // JS needs to read it for header
				Secure:   r.TLS != nil,
				SameSite: http.SameSiteLaxMode,
			})
		}

		// CSRF: check double-submit cookie on mutating requests
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete || r.Method == http.MethodPatch {
			cookie, err := r.Cookie("csrf-token")
			if err != nil {
				http.Error(w, "Forbidden: missing csrf token", http.StatusForbidden)
				return
			}
			header := r.Header.Get("X-CSRF-Token")
			if header == "" {
				http.Error(w, "Forbidden: missing X-CSRF-Token header", http.StatusForbidden)
				return
			}
			if cookie.Value != header {
				http.Error(w, "Forbidden: csrf token mismatch", http.StatusForbidden)
				return
			}
		}

		// Rate limit login endpoint
		if r.URL.Path == "/login" && r.Method == http.MethodPost {
			ip := extractIP(r)
			if !loginRL.Allow(ip) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "too many login attempts", http.StatusTooManyRequests)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
