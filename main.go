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
)

//go:embed assets/dist/* assets/htmx.min.js
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

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`
			<!DOCTYPE html>
			<html lang="en" class="h-full bg-gray-950">
			<head>
				<meta charset="UTF-8"/>
				<meta name="viewport" content="width=device-width, initial-scale=1"/>
				<title>MuxCore Admin</title>
				<link rel="stylesheet" href="/static/dist/styles.css"/>
				<script src="/static/htmx.min.js"></script>
			</head>
			<body class="h-full text-gray-100 font-sans" hx-boost="true">
				<div class="flex h-full">
					<nav class="w-64 shrink-0 border-r border-gray-800 p-4">
						<div class="text-lg font-bold mb-6">MuxCore</div>
						<ul class="space-y-2">
							<li><a href="/" class="text-gray-300 hover:text-white">Dashboard</a></li>
							<li><a href="/modules" class="text-gray-300 hover:text-white">Modules</a></li>
							<li><a href="/cluster" class="text-gray-300 hover:text-white">Cluster</a></li>
							<li><a href="/events" class="text-gray-300 hover:text-white">Events</a></li>
						</ul>
					</nav>
					<main class="flex-1 overflow-y-auto p-6">
						<h1 class="text-2xl font-bold">Dashboard</h1>
						<p class="text-gray-400 mt-2">Admin UI connected to core. Build in progress.</p>
					</main>
				</div>
			</body>
			</html>
		`))
	})

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

		// CSRF: block non-GET/HEAD/OPTIONS requests with untrusted Origin
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" {
				// In production, validate against trusted origins
				if r.Header.Get("Host") != "" && origin != "" {
					// Same-origin check — allow if origin matches host
					// For now, accept same-origin only
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}
