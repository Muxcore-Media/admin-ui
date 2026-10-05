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

Open `http://localhost:8080` in a browser. Login redirects to the auth module (`ADMIN_UI_AUTH_ADDR`); after login, the browser returns via `/auth/callback`.

---

## Requirements

- Go 1.26+
- A running `muxcored` instance (v0.1.0+)
- An auth module reachable at `ADMIN_UI_AUTH_ADDR` (for login)
- An `Authorizer` module registered with core (for `admin.access`)
- Tailwind CSS standalone CLI (for CSS builds — `make css` downloads it)

---

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `ADMIN_UI_ADDR` | `:8080` | HTTP listen address |
| `ADMIN_UI_CORE_ADDR` | `localhost:9090` | Core gRPC address |
| `ADMIN_UI_INSECURE` | `false` | Disable TLS for core gRPC (dev only) |
| `ADMIN_UI_AUTH_ADDR` | `http://localhost:9401` | Auth module base URL (login + code exchange) |
| `ADMIN_UI_TLS_CERT` | — | TLS cert file path (enables HTTPS) |
| `ADMIN_UI_TLS_KEY` | — | TLS key file path |
| `ADMIN_UI_SESSION_TTL` | `30m` | Session lifetime |
| `ADMIN_UI_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `ADMIN_UI_LOG_FORMAT` | `text` | Log format: `text` or `json` |
| `ADMIN_UI_TRUSTED_PROXIES` | loopback | Comma-separated CIDRs whose `X-Forwarded-For` is trusted (empty → `127.0.0.0/8`, `::1/128`) |
| `ADMIN_UI_AUTH_INTERNAL_ADDR` | same as `ADMIN_UI_AUTH_ADDR` | Server-side auth base for OAuth code exchange (use LAN URL when browser uses public auth) |
| `ADMIN_UI_PUBLIC_URL` | — | Public origin override for OAuth callbacks (e.g. `https://admin.zem.systems`) |
| `ADMIN_UI_HEALTH_MONITOR_URL` | `http://127.0.0.1:9203` | Health-monitor HTTP base for dashboard panel |
| `ADMIN_UI_DATA_DIR` | `$TMP/muxcore-admin-ui` or `MEDIA_UI_USERDATA_DIR` | Root for durable JSON state (branding, networking, sessions, …) |
| `ADMIN_UI_*_FILE` | under data dir | Per-artifact overrides: `BRANDING`, `NETWORKING`, `PARENTAL`, `LIVETV`, `PLAYBACK`, `PASSWORD_RESET`, `SESSION` |
| `ADMIN_UI_USERDATA_URL` | mesh `userdata.local` | HTTP base for parental prefs sync to userdata-local |
| `ADMIN_UI_METRICS_TOKEN` | — | When set, `/metrics` requires `Authorization: Bearer <token>` |

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

When TLS is enabled, the session and CSRF cookies are marked `Secure`.

### JSON logging (production)

```bash
export ADMIN_UI_LOG_FORMAT=json
export ADMIN_UI_LOG_LEVEL=info
```

Produces structured JSON log lines for ingestion by log aggregators (Loki, Datadog, Splunk).

### Health checks

| Endpoint | Purpose | Response |
|----------|---------|----------|
| `/health` | Liveness + readiness | `{"status":"ok","version":"..."}` or `{"status":"degraded","version":"..."}` (503) when core is down |
| `/metrics` | Prometheus scraping | Plain text Prometheus metrics |

### Reverse proxy

When placing behind nginx or Caddy, forward:

- `X-Forwarded-For` — client IP for rate limiting (honored only when the TCP peer is in `ADMIN_UI_TRUSTED_PROXIES`; otherwise `RemoteAddr` is used)

`X-Real-IP` is not used. Untrusted peers cannot spoof client IP via XFF. CSRF uses a double-submit cookie (`csrf-token` + `X-CSRF-Token`), not Origin/Host checks.

---

## Architecture

```
Browser ──HTTP──→ admin-ui ──gRPC──→ muxcored
                  (Templ + HTMX)     (Discovery, Events, Storage, Mesh, Health)
```

**Auth flow:**
1. Browser → `GET /login` → redirect to `ADMIN_UI_AUTH_ADDR/login?redirect=.../auth/callback`
2. Auth module returns with `?code=...` → admin-ui `POST`s `{code}` to `ADMIN_UI_AUTH_ADDR/login/exchange`
3. On success, server-side session created (cookie-based, in-memory store)
4. Every subsequent request validates session cookie and requires `Authorizer.Can("admin.access")` on `admin.ui`
5. CSRF protection via double-submit cookie pattern

**Live updates:**
- Dashboard health grid: HTMX polling every 5s
- Cluster nodes: HTMX + SSE (`/cluster/sse`, trigger `sse:cluster-update`)
- Events: HTMX polling every 3s (stats every 10s)
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
