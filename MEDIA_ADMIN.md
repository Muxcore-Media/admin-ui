# Media Admin UI surfaces

Admin-ui library pages beyond basic browse, backed by extended `MediaAdminService`
(`contracts-media-admin`) plus `FormatService` for custom formats and quality
profiles, `RootFolderService` for library roots, and `RenameService` for naming
templates.

## Contract (`contracts-media-admin`)

New RPCs on `MediaAdminService`:

- `ListMissing`, `ListTags` / `CreateTag` / `DeleteTag` / `SetItemTags`
- `ListCollections` / `GetCollectionItems` (movies)
- `GetCalendar` (TV)
- `GetMediaTypeInfo.features`: `missing`, `tags`, `collections`, `calendar`
- `ListItems.tag_id` for tag-filtered browse

Library modules implement these via a `mediaAdminServer` adapter so method names
do not collide with type-specific management gRPC.

## Admin UI routes

Per library module (`media.library`):

| Path | Feature gate |
|------|----------------|
| `GET /media/{moduleID}` | browse (search, pagination, tag filter, posters) |
| `GET /media/{moduleID}/missing` | `missing` |
| `GET|POST /media/{moduleID}/tags` | `tags` |
| `GET /media/{moduleID}/collections` | `collections` |
| `GET /media/{moduleID}/collections/{collectionID}` | `collections` |
| `GET /media/{moduleID}/calendar` | `calendar` |
| `GET /media/{moduleID}/item/{id}` | item detail (releases, history, seasons for TV) |
| `POST /media/{moduleID}/{id}/metadata` | update metadata (incl. monitored, series_type, profile, root) |
| `POST /media/{moduleID}/{id}/refresh` | metadata refresh |
| `POST /media/{moduleID}/{id}/delete` | delete item (optional delete_files) |
| `POST /media/{moduleID}/{id}/season/{seasonID}/monitor` | TV season monitor |
| `POST /media/{moduleID}/{id}/episode/{episodeID}/monitor` | TV episode monitor |
| `POST /media/{moduleID}/item/{id}/episode/{episodeID}/files/delete` | TV episode file remove |
| `POST /media/{moduleID}/item/{id}/titles` | add alternate title |
| `POST /media/{moduleID}/item/{id}/titles/{titleID}/delete` | remove alternate title |
| `POST /media/{moduleID}/collections/{collectionID}/monitor` | collection monitor |
| `POST /media/{moduleID}/collections/{collectionID}/sync` | sync missing collection parts |
| `GET /media/{moduleID}/{id}/artwork` | artwork partial |
| `GET|POST /request` | TMDB search & add via `request-media` |

Custom formats (`media.formats` → `FormatService`):

| Path | Purpose |
|------|---------|
| `GET /formats` | list |
| `GET /formats/new` + `POST /formats` | create |
| `GET|POST /formats/{id}` | edit |
| `POST /formats/{id}/delete` | delete |

Profiles (`media.formats` → `FormatService`):

| Path | Purpose |
|------|---------|
| `GET /formats/profiles` | list |
| `GET /formats/profiles/new` + `POST /formats/profiles` | create |
| `GET|POST /formats/profiles/{id}` | edit |
| `POST /formats/profiles/{id}/delete` | delete |

Root folders (`media.roots` → `RootFolderService`):

| Path | Purpose |
|------|---------|
| `GET /roots` | list with free-space / accessible |
| `GET /roots/new` + `POST /roots` | create (path browser) |
| `GET /roots/browse` | HTMX directory browser partial |
| `GET|POST /roots/{id}` | edit |
| `POST /roots/{id}/delete` | delete |

Naming templates (`media.renamer` → `RenameService`):

| Path | Purpose |
|------|---------|
| `GET /rename/templates` | list |
| `GET /rename/templates/new` + `POST /rename/templates` | create |
| `GET|POST /rename/templates/{id}` | edit |
| `POST /rename/templates/{id}/delete` | delete |
| `GET|POST /rename/organize` | BatchRename preview / apply |

Item detail binds `quality_profile_id` via a select when profiles are available,
and `root_folder_path` via a select when roots are available (falls back to text
input if the roots module is down). `UpdateMetadata` persists those fields from
the metadata map.

## Smoke checklist

1. Request: Search movie/TV → add → appears in library / request history
2. Movies: Browse → Missing → Tags → Collections → open collection
3. TV: Browse → Missing → Tags → Calendar (date range) → season monitor toggles
4. Item detail: Refresh metadata, toggle monitored, Delete (DB only)
5. Profiles: create / edit / delete; bind profile on a movie/series detail page
6. Root Folders: add path via browser; confirm free-space badge; bind root on item detail
7. Naming Templates: create alternate movie/TV pattern; assign on a root
8. Subnav hides unsupported tabs (no Calendar on movies, no Collections on TV)

## Approval Queue (`/approvals`)

The `/approvals` page is the household operator's admin queue for requests submitted via `/request`.

### What it does

| Section | Description |
|---------|-------------|
| **Pending** | Requests awaiting a decision. Each row has an **Approve** button and a **Deny** button with an optional reason field. |
| **Recent** | Non-pending requests (approved, denied, added, watchlisted) for at-a-glance history. |

### Wiring to request-media

| Action | HTTP call |
|--------|-----------|
| Pending queue | `GET /api/requests?status=pending` |
| Recent history | `GET /api/requests` (client-side filter: non-pending) |
| Approve | `POST /api/requests/{id}/approve` — body `{"by":"<username>"}` |
| Deny | `POST /api/requests/{id}/deny` — body `{"by":"<username>","reason":"<text>"}` |

Identity is forwarded via `X-MuxCore-User`, `X-MuxCore-Roles`, and (when `TENANT_MODE=1`) `X-Tenant-ID` / `X-Auth-Claims-Tenant`. No change to request-media is needed.

### Configuration

| Variable | Default | Notes |
|----------|---------|-------|
| `ADMIN_UI_REQUEST_MEDIA_URL` | _(empty)_ | Direct HTTP base (e.g. `http://127.0.0.1:9410`). When empty, admin-ui discovers the module by the `media.request` capability via MuxCore mesh. |

If neither resolves, the page renders a soft-empty notice (HTTP 200) and does not break the rest of the admin shell.
