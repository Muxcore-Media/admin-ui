# Changelog

All notable changes to the MuxCore Admin UI are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.14] - 2026-10-05


### Changed

- Root package is now the importable library `adminui` (exports `Main(version string)`); the binary entry point moved to `cmd/module` so core's spool resolver (ADR-0012, FR-EXT-002) can build it. Build with `go build ./cmd/module`; `-ldflags "-X main.version=..."` is unchanged. Makefile and Dockerfile updated.

## [0.1.13] — 2026-08-29

### Added

- Durable JSON state under `ADMIN_UI_DATA_DIR` (branding, networking, parental, livetv, playback, password-reset, sessions) with per-file overrides
- File-backed session store (`ADMIN_UI_SESSION_FILE`) so restarts keep operator sessions
- Parental controls sync to userdata-local `prefs.parental` for consumer playback enforcement
- CI `golangci-lint` job; optional `ADMIN_UI_METRICS_TOKEN` gate on `/metrics`

### Changed

- `NetworkingSave` applies `public_url` and `trusted_proxies` to the running handler without restart
- CSRF tokens are per-browser cookie (random, 24h) instead of daily UTC hash
- SSE routes (`/cluster/sse`, `/streams/events`) clear write deadlines so streams survive the 15s server timeout
- README / `.env.example` document auth-internal, public URL, health monitor, data-dir, and file paths; CI badge points at the GitHub Actions workflow

### Fixed

- `MEDIA_ADMIN.md` item detail route documents `GET /media/{moduleID}/item/{id}`

## [0.1.12] — 2026-08-20

### Added

- **TRaSH Guides sync UX** — `POST /formats/sync-trash` on Formats list (score set, radarr/sonarr, import profiles; shows upserted/skipped/profile counts). Manual sync; schedule via `FORMATS_TRASH_SYNC` / module settings or external cron (scheduler-cron not wired).
- **Arr migrate wizard** — `/migrate` imports Sonarr/Radarr libraries into media-movies / media-tvshows (dry-run + progress/error summary). HTTP Arr client in `arrmigrate` (httptest fixtures only).

## [0.1.10] — 2026-08-10

### Changed

- Settings discovery uses only the `settings` capability (ListAll probe removed; all catalog SettingsProviders advertise the cap)

## [0.1.9] — 2026-08-10

### Changed

- Settings page discovers SettingsProvider modules via `settings` capability **and** ListAll probe (modules that respond to mesh `Settings` without advertising the capability)

## [0.1.13] - 2026-10-05

### Fixed
- List-sync edit no longer disables the source on every save.
- Invite links are only rejected when the server reports `valid: false`.
- Builds against published module contracts (core v0.6.2, no filesystem `replace`); templ build errors fixed.

### Removed
- List-sync per-source import settings and the history "removed" count (not in media-list-sync v0.1.10).
- Movie "Trailers & extras" section (`ListTrailers` no longer exists in media-movies).

### Added

- Request approval queue on `/request` (Approve/Deny pending via request-media)
- Invite links admin page `/invites` (create/copy/revoke via auth-local)
- Organize / rename UI (`/rename/organize`) via media-rename `BatchRename`
- Alternate titles panel on library detail (movies + TV)
- TV episode file remove on season tree; movie trailers panel; minimum availability select
- Collection monitor + Sync missing from TMDB
- Backup CreateBackup includes library DB dirs when `MOVIES_DB_PATH` / `TVSHOWS_DB_PATH` set
- Request Media search/add (`GET|POST /request`) in nav — Radarr/Sonarr-style add via `request-media`
- Library item Refresh metadata / Delete (optional delete files) on detail pages
- Monitored checkbox + TV series type selector on item metadata form
- TV season/episode monitor tree on series detail (`TvManagementService`)
- Automation: remove from wanted queue, release blocklist list/clear, delay profile edit, cutoff unmet
- Movie file panel on detail (list + remove; optional delete from disk)
- Manual import (`/import`) via scanner `ListImportCandidates` + `ImportPath`
- Release profiles CRUD at `/formats/release-profiles`
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
