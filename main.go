package main

import (
	"context"
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
	Addr       string
	CoreAddr   string
	Insecure   bool
	TLSCert    string
	TLSKey     string
	SessionTTL time.Duration
	LogLevel   string
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
	h := handler.New(c, ss, cfg.TLSCert != "" || !cfg.Insecure)

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

	h.RegisterRoutes(mux)

	mux.HandleFunc("GET /no-auth", func(w http.ResponseWriter, r *http.Request) {
		component := templates.ErrorLayout("No Auth Provider", templates.NoAuthPage())
		component.Render(r.Context(), w)
	})

	mux.HandleFunc("/", h.NotAuthHandler)

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      withMiddleware(mux),
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

func withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" {
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				}
				expected := scheme + "://" + r.Host
				if origin != expected {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}
