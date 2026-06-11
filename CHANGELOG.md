# Changelog

All notable changes to the MuxCore Admin UI are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Project scaffold: Go module, build system, directory layout
- Tailwind CSS asset pipeline via standalone CLI
- Static asset embedding via `//go:embed`
- Base layout with dark theme sidebar navigation
- Login page with session management
- Auth middleware delegating to `AuthProvider` / `Authorizer` via core mesh
- Dashboard with live HTMX-polling health grid
- Module list and detail pages
- Cluster map with SSE-powered live updates
- Event stream viewer with subscription stats
- Storage provider status page
- Dynamic settings UI for `SettingsProvider` modules
- Audit log browser with filters and pagination
- Config viewer with secret redaction
- CSP hardening, security headers, error handling
- Production hardening: rate limiting, session expiry, CSRF, graceful shutdown
- CI pipeline: lint, build, test
