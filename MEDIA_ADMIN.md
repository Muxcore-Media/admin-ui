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
| `GET /media/{moduleID}/{id}` | item detail |
| `POST /media/{moduleID}/{id}/metadata` | update metadata |
| `GET /media/{moduleID}/{id}/artwork` | artwork partial |

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

Item detail binds `quality_profile_id` via a select when profiles are available,
and `root_folder_path` via a select when roots are available (falls back to text
input if the roots module is down). `UpdateMetadata` persists those fields from
the metadata map.

## Smoke checklist

1. Movies: Browse → Missing → Tags → Collections → open collection
2. TV: Browse → Missing → Tags → Calendar (date range)
3. Profiles: create / edit / delete; bind profile on a movie/series detail page
4. Root Folders: add path via browser; confirm free-space badge; bind root on item detail
5. Naming Templates: create alternate movie/TV pattern; assign on a root
6. Subnav hides unsupported tabs (no Calendar on movies, no Collections on TV)
