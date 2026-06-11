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

# Run against a local muxcored
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

---

## Development

```bash
make dev    # Build CSS + run with dev defaults
make css    # Compile Tailwind CSS
make build  # Build production binary
make test   # Run tests
make lint   # golangci-lint + go vet
```

---

## Architecture

```
Browser ──HTTP──→ admin-ui ──gRPC──→ muxcored
                  (Templ + HTMX)     (Discovery, Events, Storage, Mesh, Health)
```

The admin module is a standalone Go binary that:

1. Dials core's gRPC endpoint using the Go SDK client
2. Serves an HTTP interface with Templ-rendered HTML and HTMX-driven interactivity
3. Embeds all static assets (Tailwind CSS, HTMX JS) via `//go:embed`
4. Delegates auth to core's `AuthProvider` and `Authorizer` modules via mesh calls

No changes to core are required. All data is accessed through existing gRPC services.

---

## License

GPL-3.0
