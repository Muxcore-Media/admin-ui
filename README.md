# admin-ui

HTMX + Tailwind CSS administrative dashboard for MuxCore.

Provides a web-based admin interface served at the root path (`/`). Routes are registered on the core API server at startup.

## Features

- Dashboard with module overview and counts
- Modules page listing all registered modules with state
- Settings page showing `MUXCORE_*` environment variables
- Setup wizard for first-run configuration
- Marketplace browser for module discovery

## Capabilities

- `admin.ui` — Web-based administration UI
- `admin.dashboard` — Dashboard and overview panels
- `admin.settings` — Environment variable viewer and setup wizard
- `admin.marketplace` — Marketplace catalog browser

## Usage

```go
import "github.com/Muxcore-Media/admin-ui"

mod := adminui.NewModule()
mgr.Register(mod, nil)
```
