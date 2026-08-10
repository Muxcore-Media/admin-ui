# Changelog

All notable changes to the MuxCore Admin UI are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.9] — 2026-08-10

### Changed

- Settings page discovers SettingsProvider modules via `settings` capability **and** ListAll probe (modules that respond to mesh `Settings` without advertising the capability)

## [Unreleased]

### Added

- List Sync operator UI (`/list-sync`, history, add/remove sources, sync now) via `media.listsync`
- Project scaffold: Go module, build system, directory layout
- Tailwind CSS asset pipeline via standalone CLI
- Static asset embedding via `//go:embed`
- Base layout with dark theme sidebar navigation
- Login page with session management
- Auth middleware delegating to `AuthProvider` / `Authorizer` via core mesh
- Dashboard with live HTMX-polling health grid
- Module list and detail pages
- Cluster map with HTMX-polled live node cards
- Event stream viewer with subscription stats
- Storage provider status page
- Dynamic settings UI for `SettingsProvider` modules
- Audit log browser with filters and pagination
- Config viewer with secret redaction
- Modal and pagination components
- Per-IP rate limiting on login endpoint (exponential backoff after 6 failures)
- CSRF double-submit cookie protection on all mutating routes
- Prometheus-format `/metrics` endpoint (requests, sessions, login stats, Go runtime)
- `golangci-lint` configuration and CI pipeline (5 jobs: lint, css, build, test, vet)
- Session store unit tests (7 tests)

### Security

- Rate limiting: login brute-force protection with 1-minute block after 6 failures
- CSRF: double-submit cookie pattern with per-session tokens via X-CSRF-Token header
- Metrics endpoint: exposes runtime stats without sensitive data
- CSP: restrictive policy (default-src 'self'; script-src 'self'; style-src 'self')
