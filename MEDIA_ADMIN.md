# Media Admin UI surfaces

Admin-ui library pages beyond basic browse, backed by extended `MediaAdminService`
(`contracts-media-admin`) plus `FormatService` for quality profiles.

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
| `GET /media/{moduleID}/collections/{id}` | `collections` |
| `GET /media/{moduleID}/calendar` | `calendar` |

Profiles (`media.formats` → `FormatService`):

| Path | Purpose |
|------|---------|
| `GET /formats/profiles` | list |
| `GET /formats/profiles/new` + `POST /formats/profiles` | create |
| `GET|POST /formats/profiles/{id}` | edit |
| `POST /formats/profiles/{id}/delete` | delete |

Item detail binds `quality_profile_id` via a select when profiles are available;
`UpdateMetadata` persists that field (and `root_folder_path`) from the metadata map.

## Smoke checklist

1. Movies: Browse → Missing → Tags → Collections → open collection
2. TV: Browse → Missing → Tags → Calendar (date range)
3. Profiles: create / edit / delete; bind profile on a movie/series detail page
4. Subnav hides unsupported tabs (no Calendar on movies, no Collections on TV)
