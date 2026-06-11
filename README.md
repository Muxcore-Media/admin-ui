# MuxCore Admin UI

[![CI](https://github.com/Muxcore-Media/admin-ui/actions/workflows/ci.yml/badge.svg)](https://github.com/Muxcore-Media/admin-ui/actions)
[![Go Version](https://img.shields.io/badge/Go-1.26-blue)](https://go.dev/)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

**Web dashboard for MuxCore — modules, cluster, events, audit, and settings.**

A sidecar module that connects to a running `muxcored` instance via gRPC and serves a browser-based admin interface using Go + Templ + HTMX + Tailwind CSS. No JavaScript framework, no Node.js build step — everything is compiled into a single Go binary.

---

## Quick start

```bash
# Build the binary
make build

# Run against a local muxcored (dev mode, no TLS)
export ADMIN_UI_CORE_ADDR=localhost:9090
export ADMIN_UI_INSECURE=true
./admin-ui
# → Listening on :8080
```

Open `http://localhost:8080` in a browser. If no `AuthProvider` module is registered with core, the UI shows a configuration page. Deploy an auth module first, then log in.

---

## Requirements

- Go 1.26+
- A running `muxcored` instance (v0.1.0+)
- An `AuthProvider` module registered with core (for login)
- Tailwind CSS standalone CLI (for CSS builds — `make css` downloads it)

---

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `ADMIN_UI_ADDR` | `:8080` | HTTP listen address |
| `ADMIN_UI_CORE_ADDR` | `localhost:9090` | Core gRPC address |
| `ADMIN_UI_INSECURE` | `false` | Disable TLS for core gRPC (dev only) |
| `ADMIN_UI_TLS_CERT` | — | TLS cert file path (enables HTTPS) |
| `ADMIN_UI_TLS_KEY` | — | TLS key file path |
| `ADMIN_UI_SESSION_TTL` | `30m` | Session lifetime |
| `ADMIN_UI_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `ADMIN_UI_LOG_FORMAT` | `text` | Log format: `text` or `json` |

---

## Production deployment

### Docker

```bash
docker build -t admin-ui .
docker run -d --restart=unless-stopped \
  -p 8080:8080 \
  -e ADMIN_UI_CORE_ADDR=core:9090 \
  -e ADMIN_UI_LOG_FORMAT=json \
  admin-ui
```

The Docker image uses a multi-stage build: Tailwind CSS is compiled in the first stage, the Go binary is built in the second, and the final runtime is `alpine:3.21` (non-root user, ~10MB image).

### TLS (HTTPS for the admin UI)

```bash
export ADMIN_UI_TLS_CERT=/etc/ssl/cert.pem
export ADMIN_UI_TLS_KEY=/etc/ssl/key.pem
./admin-ui
```

When TLS is enabled, the session and CSRF cookies are marked `Secure`. The HTTP server uses TLS 1.2+ with secure cipher suites.

### JSON logging (production)

```bash
export ADMIN_UI_LOG_FORMAT=json
export ADMIN_UI_LOG_LEVEL=info
```

Produces structured JSON log lines for ingestion by log aggregators (Loki, Datadog, Splunk).

### Health checks

| Endpoint | Purpose | Response |
|----------|---------|----------|
| `/health` | Liveness + readiness | `{"status":"ok"}` or `{"status":"degraded"}` (503) when core is down |
| `/metrics` | Prometheus scraping | Plain text Prometheus metrics |

### Reverse proxy

When placing behind nginx or Caddy, ensure these headers are forwarded:

- `X-Forwarded-For` — for client IP in rate limiting and logs
- `Host` — for CSRF origin validation

---

## Architecture

```
Browser ──HTTP──→ admin-ui ──gRPC──→ muxcored
                  (Templ + HTMX)     (Discovery, Events, Storage, Mesh, Health)
```

**Auth flow:**
1. Browser → `POST /login` → admin-ui calls `AuthProvider.Authenticate()` via core mesh
2. On success, server-side session created (cookie-based, in-memory store)
3. Every subsequent request validates session cookie + optionally checks `Authorizer.Can("admin.access")`
4. CSRF protection via double-submit cookie pattern

**Live updates:**
- Dashboard health grid: HTMX polling every 5s
- Cluster nodes: HTMX polling every 10s
- Events: HTMX polling every 3s
- All static assets embedded in binary (zero disk I/O at runtime)

---

## Development

```bash
make dev        # Build CSS + run with dev defaults
make css        # Compile Tailwind CSS
make css-watch  # Watch mode for Tailwind
make build      # Build production binary
make test       # Run tests
make lint       # golangci-lint + go vet
make clean      # Remove build artifacts
make fmt        # Format Go + Templ files
```

---

## License

GPL-3.0
