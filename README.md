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
export MUXCORE_INSECURE_DISABLE_TLS=true
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
| `MUXCORE_INSECURE_DISABLE_TLS` | `false` | `true`: plaintext mesh gRPC to core and modules (dev profile only; loud startup warning) |
| `ADMIN_UI_INSECURE` | `false` | Deprecated alias of `MUXCORE_INSECURE_DISABLE_TLS` (also drops the `Secure` cookie flag when no HTTPS cert is set) |
| `MUXCORE_TLS_CA` | system roots | PEM CA bundle used to verify core/module gRPC servers |
| `MUXCORE_TLS_CERT` / `MUXCORE_TLS_KEY` | — | Optional client certificate for mesh mTLS (set both) |
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
| `ADMIN_UI_HEALTH_MONITOR_TOKEN` | (unset) | Bearer for health-monitor `/status` (falls back to `HEALTH_MONITOR_HTTP_TOKEN`) |
| `ADMIN_UI_SCHEDULER_TOKEN` | (unset) | Bearer for scheduler-cron HTTP API (falls back to `SCHEDULER_HTTP_TOKEN`) |
| `ADMIN_UI_DATA_DIR` | `MEDIA_UI_USERDATA_DIR`, else `$XDG_STATE_HOME/muxcore/admin-ui`, else `~/.local/state/muxcore/admin-ui` | Root for durable JSON state (branding, networking, sessions, …) |
| `ADMIN_UI_SESSION_KEY` | generated `session.key` (0600) next to the session file | Key protecting session bearer material at rest (32 bytes base64/hex, or any passphrase → SHA-256) |
| `ADMIN_UI_RESTORE_ROOT` | `BACKUP_RESTORE_DIR`, else `/data/restore` | Allow-listed root for backup restore targets (paths outside → 400) |
| `ADMIN_UI_*_FILE` | under data dir | Per-artifact overrides: `BRANDING`, `NETWORKING`, `PARENTAL`, `PARENTAL_MIGRATION`, `LIVETV`, `PLAYBACK`, `PASSWORD_RESET`, `SESSION` |
| `ADMIN_UI_USERDATA_URL` | mesh `userdata.local` | HTTP base for userdata-local (parental policy resource and PIN sync) |
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
4. Every protected request checks its local session, revalidates a bound provider bearer with `AuthService.Validate`, and requires `Authorizer.Can("admin.access")` on `admin.ui`
5. CSRF protection via double-submit cookie pattern

Validation and authorization each have an eight-second deadline covering
discovery and the RPC. Both send only the current stored bearer in
`x-auth-token` and `authorization` metadata; browser and inherited credentials
are not used. Local-only sessions skip `Validate` and send no bearer to `Can`.
Invalid authentication clears only the matching local binding and redirects to
login (303, or HTMX `HX-Redirect`). Permission denial returns 403; discovery or
provider outages return 503 and retain the cookie. A session revoked during
either RPC cannot reach the protected handler, and a replaced binding is kept
for retry. Established requests and streams are not periodically revalidated.

Successful validation keeps the existing claim policy: changed username, roles
and tenant are persisted, while user ID, permissions, bearer and local expiry
remain unchanged. The tenant comes from the provider's `tenant_id`, without the
login-time claim fallback. Each handler receives its own deep snapshot. Outage
retention requires the provider to distinguish invalid sessions from operational
failures. BFF revalidation, Quick Connect device grants and live integration
remain separate acceptance work.

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

### Identity provider capabilities

User, TOTP, token and invite administration use the discovered identity provider.
If an operation returns `Unimplemented`, its page or panel explains that the
feature is unavailable and omits its controls. Providers are not identified by
name, and capability results are not cached across requests. Connection and
permission failures remain errors rather than appearing as unsupported features
or empty lists.

Invite administration uses the existing AuthService list/create/revoke RPCs,
forwarding the signed-in session token. The form keeps `0` as unlimited uses;
its hours-based lifetime and tenant selection are preserved. Invite redemption
continues through the provider's HTTP flow. Creator attribution comes from the
provider; auth-local can report the calling module when mesh TLS is in use.

If API key rotation creates a token but cannot confirm revocation of the old
one, the page retains the new token for copying and warns that rotation is
incomplete. It does not retry either mutation automatically.

Direct passkey endpoints and live OIDC browser acceptance remain outside this
checkpoint; FR-AUTH-009 is still partial.

### Arr migration connections

The `/migrate` page uses the existing admin authorization and connects directly
to the operator's Radarr, Sonarr or Lidarr endpoint. HTTP and HTTPS endpoints on
localhost or private LANs remain supported, including reverse-proxy path prefixes.
The SDK Integration guard checks resolved addresses at connection time and denies
cloud metadata, link-local, multicast and other blocked special-use destinations.
HTTPS still verifies the server certificate.

Redirects are refused, including redirects within the same host, so the Arr
`X-Api-Key` is sent only to the configured endpoint. Enter the final base URL if
the old address redirects. Arr requests do not use environment HTTP proxies:
direct connections keep DNS resolution and destination checks in the same client.
Requests retain the 30-second timeout; a failed fetch produces an error before
any catalog import or library scan.

### Provider and local sessions

`/devices` lists active sessions from the identity provider using core v0.6.16's
administrative session contract. The list includes signed-in and API-token
sessions for all users, with an optional user-ID filter and 100-entry pages.
Changing the filter starts a new page sequence; continuation tokens are opaque,
and each page reflects current provider state rather than a fixed snapshot.

List and revoke calls forward exactly the signed-in user's provider bearer.
Missing bearers require signing in again; the provider independently enforces
current authentication and the admin role. Unsupported providers get an
explanation without provider controls. Authentication, permission, pagination
and availability failures remain errors, not empty lists or successful revokes.

Revocation targets the exact user ID and independent provider session ID. It
invalidates that provider session on its next validation; it does not revoke the
API key that minted an API-token session. Applications that keep local sign-ins
must revalidate the upstream bearer to enforce this. Global app revalidation and
live provider acceptance remain separate work, so FR-AUTH-007 is still partial.

The **Local admin-panel sessions** view (`/devices?scope=local`) keeps the existing
local revoke and rename controls. Labels apply only to admin-ui sessions. Local
cookie hashes never stand in for provider management IDs, and provider actions
do not automatically remove or rename local sessions.

### Parental policy (provider-backed)

The per-account parental policy is owned by userdata-local's
`GET`/`PUT /api/parental-policy` resource (ADR-0030, userdata-local v0.1.5 and
later). The Users page's parental form reads and writes **only** that resource,
using the signed-in admin's identity-provider bearer (never browser headers) and
the account being edited in `X-MuxCore-User-Id`. Nothing is read from or written
to `parental.json` or the user's writable userdata blob for restrictions.

- **States are distinct.** *Not configured* (no policy yet, which is not the
  same as unrestricted), *Configured: unrestricted* and *Configured:
  restricted* each render differently. The maximum rating is chosen from the
  provider's supported tokens; tags are comma-separated and compared exactly
  after trimming and lower-casing.
- **Revision-checked writes.** Each save sends `expected_revision`. On a 409
  the form shows a conflict notice and reloads the current revision; it never
  retries or overwrites silently.
- **Provider errors are errors.** 401, 403, 404, 5xx, timeouts, unreachable or
  malformed answers and scope mismatches show an error with a retry button and
  no form. They never render as unrestricted or empty.
- **Admin and manager accounts.** A restricted policy on an account holding the
  `admin` or `manager` role is applied but is not a security boundary (those
  roles can change item tags and ratings); the form says so.
- **Invalid submissions are 400s.** Unrestricted mode combined with rule fields
  is rejected, not silently dropped. Validation errors answer 400 with the
  `X-Admin-Swap-Error` header so `assets/csrf.js` still shows them.
- **Write errors are honest.** "Nothing was changed" is only said for definite
  refusals (400/401/403/404/413). After a timeout, 5xx or a mismatched
  acknowledgement the form says the change may not have been saved and asks you
  to reload and check.
- **PIN lock is unchanged** until FR-AUTH-010: it still lives in `parental.json`
  and is mirrored as `pin_hash` into the user's blob. It is a separate form and
  is never sent to the policy provider. It reads `parental.json` with the same
  fail-closed reader as the migration and refuses to save (leaving the file
  untouched) if the file is unreadable or malformed.

#### Migrating `parental.json`

`/users/parental/migrate` (admin role only; others get 403 and nothing is
written) copies the legacy file into the provider (ADR-0031 section 5):

1. Run the **dry run**. It lists every account from the identity provider with
   the policy it would receive and what would happen, and writes nothing.
2. **Apply** with the dry-run digest (SHA-256 over the exact `parental.json`
   bytes and the plan). If the file or the account list changed, the digest no
   longer matches and nothing is written; run the dry run again.

Mapping: the rating is trimmed and upper-cased; tags are split on commas;
`allow_unrated` and `kids_mode` map directly; any rating, tag or kids-mode
setting makes the account `restricted`; `pin_hash` is never sent. A rating the
provider does not support is reported and **skipped** (the account stays
unconfigured). Accounts with no restriction (all-default entry or no entry) are
set to `unrestricted` only if you tick *Set unrestricted for N listed
accounts*, which is unchecked by default. Legacy entries for accounts the
provider does not list are ignored.

If `parental.json` does not exist (for example a wrong data directory) the dry
run says "parental.json not found at <path>" and the opt-in text says the
accounts have no legacy entry because the file was missing. A damaged file stops
the run. Each row keeps its mapping reason ("no legacy entry" or "legacy entry
sets no restriction"). An apply runs on its own bounded context, so closing the
browser does not abort a half-finished run, and it refuses to start if an
existing `parental-migration.json` cannot be read (the history is never
replaced). Both JSON files are written through a freshly created 0600 temporary
file.

Each account is read first: unconfigured accounts are created with
`expected_revision: 0`; an identical configured policy is skipped; a *different*
configured policy is reported as a conflict and never overwritten; 409, 403 and
404 are reported. Re-running is safe. Every write-run outcome is audited as
`admin.parental.migrate` and appended to `parental-migration.json` (0600) in the
data dir. `parental.json` is kept and never deleted.

**Rollout order:** deploy the provider (userdata-local with the policy resource),
then run this migration, then enable BFF enforcement (ADR-0031 slice S5).
Deploying enforcement first leaves every account unconfigured, which the BFF
denies. `muxcorectl users parental set` (muxcorectl-cli) still writes the legacy
file until slice S7, so its changes are **not** seen by the provider; use the admin form until then.

### Operator content ratings

Movie and TV-series library detail pages link to **Content rating**. The page
reads the owning media module's typed classification (media-movies and
media-tvshows v0.1.23). The existing `admin.access` check remains required; saves
also require the current, revalidated `admin` role and a bound provider bearer.
Manager accounts can view this page when authorized, but cannot save ratings.

Choose a supported rating, **Explicit unrated (NR)**, or **Clear rating —
unavailable**. NR is allowed only by policies that allow unrated items. Clearing
leaves restricted accounts denied even if they allow unrated items. Unknown
values and sources also display as unavailable; no rating is inferred from
votes, filenames or editable metadata. All TV seasons and episodes inherit the
series classification. The page explains that restrictions on admin/manager
accounts are not a security boundary because these roles can change ratings or
tags.

Writes use `SetContentRating` with exactly the current session's provider bearer,
then read the owning item back before displaying confirmation. The module RPCs
delegate caller authorization, so the admin route's role check is essential.
These contracts have no revision comparison: saves replace the operator value;
reload first if another admin may have edited it. Failed writes are never
retried automatically. Timeouts or failed/mismatched readback say to reload and
check, because the save may already have happened. Error responses remain
visible through the existing HTMX opt-in error handling and are not cached.

These two routes share a 12-second operation budget across current-session
validation, authorization, discovery, request-body reading and classification
RPCs, leaving time to render beneath the server's 15-second write timeout.
An earlier caller deadline still wins. Rendering after the operation deadline
does not resume provider work. A socket body-read timeout cancels Go's request
context, so its refusal page alone gets a separate one-second rendering bound;
no classification call is made on that path.

Every dispatched `SetContentRating` attempt emits one
`admin.media.content_rating` audit outcome: `confirmed` after matching readback,
`refused` for a definite provider rejection, or `uncertain` after a timeout,
unavailable service or unconfirmed readback. Audits distinguish the requested
value from a confirmed saved value and use fixed reason codes without provider
error text or bearer material. Audit delivery remains asynchronous and bounded;
it does not retry the classification write.

This source workflow is fixture-tested; it does not establish deployment,
authenticated parental-policy HTTP transport (S9), or a live restricted-account
journey. FR-PLAY-007 remains partial.

### Accessibility validation

For T-M4-05 / NFR-A11Y-001, the ordinary Go template suite checks rendered DOM
relationships (labels, unique IDs, ARIA references, names and landmarks),
including settings with identical keys in different modules. The Accessibility
GitHub workflow adds axe-core checks over Go-rendered core-journey documents
and keyboard interaction tests for the responsive sidebar and HTMX navigation.

```bash
go test ./templ
npm ci                  # Node 24; test dependencies only
npm test                # Sidebar focus, Escape, Tab, resize and HTMX behaviour
npm run test:a11y       # Renders fixtures with Go, then runs axe in jsdom
npm run css            # Rebuild embedded CSS after changing Tailwind classes
```

`test:a11y` needs the same private Go-module access as `go test`; it does not
connect to a running stack. Both the Go audit and axe have deliberately broken
control fixtures to check that failures are detected. The axe `color-contrast`
rule is disabled because jsdom has no layout/paint engine; region checks remain
on. These checks do not certify WCAG 2.1 AA. Browser verification of contrast,
visible focus, reduced motion, responsive layout and assistive-technology
behaviour remains outstanding. The existing `templ/a11y_test.go` assertions are
retained alongside the DOM audit in `templ/accessibility_audit_test.go`.

---

## License

GPL-3.0
