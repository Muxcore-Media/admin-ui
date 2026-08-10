# Admin Settings coverage (demo spool)

The Settings page (`GET /settings`) discovers modules via core capability `settings` and calls mesh methods `Settings` / `UpdateSetting`. Any module that advertises `settings` and registers a settings mesh handler appears automatically — no per-module admin-ui code.

## Demo / installer pins (`versions.env` + spool `minimal` / `media` / `default`)

| Module | SettingsProvider? | Notes |
|--------|-------------------|-------|
| `api-rest` | Yes | CORS / gateway knobs |
| `auth-local` | Yes | Local auth settings |
| `database-sqlite` | Yes | `db_path` |
| `secrets-file` | Yes | Secrets file path |
| `encryption-aesgcm` | Yes | `key_file` |
| `call-policy-default` | Yes | Policy file |
| `publish-policy-default` | Yes | Policy file |
| `metadata-tmdb` | Yes | Fixture / API key knobs |
| `media-automation` | Yes | Automation knobs |
| `downloader-native-torrent` | Yes | Engine = fixture for demos |
| `jellyfin` | Yes | Via `RegisterMeshHandler` SettingsHandler |
| `health-monitor` | No | Dashboard `/dashboard/monitor` uses HTTP status API instead |
| `media-movies` / `media-tvshows` | No | Library managed via Media Admin pages, not SettingsProvider |
| `media-scanner` / `media-root-folders` | No | Dedicated Roots / scanner surfaces |
| `request-media` | No | Admin `/request` page + module HTTP UI |
| `notification-default` | Varies | Optional; appear when capability advertised |
| `admin-ui` | No | Consumer of settings, not a provider |
| `metrics-prometheus` / `tracing-otlp` | Yes (when profile on) | Observability profile |
| `scheduler-cron` | No | Managed via `muxcorectl schedules` HTTP API |
| `logging-file` | Yes | When started; knobs for path/level |
| `media-music` (library-plus) | Yes | Mesh settings + library pages |

## Gaps (intentional)

- Modules without a SettingsProvider keep domain-specific admin pages (media list, request, roots, formats).
- Soft-empty Settings page copy remains when no `settings` capability modules are registered — that is expected for a minimal core-only boot.

## Regression check

With the host demo stack up: open `/settings` and confirm each running SettingsProvider module renders at least one group. Failures are usually missing `settings` capability advertisement or mesh registration — fix the module, not admin-ui routing.
