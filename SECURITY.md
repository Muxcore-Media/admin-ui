# Security Policy

## Status

Pre-1.0 beta software. APIs and interfaces are not yet stable.

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |
| < 0.1   | :x:                |

## Reporting a Vulnerability

**Do not open a public issue.** Report via GitHub Security Advisories:
https://github.com/Muxcore-Media/admin-ui/security/advisories

Acknowledgment within **72 hours**. Target patch: **7 days** critical, **30 days** moderate.

## Scope

### Implemented

- **Session management**: Server-side session store, random UUID tokens, configurable TTL
- **Auth**: Login redirects to `ADMIN_UI_AUTH_ADDR`; session established after code exchange at `/auth/callback`. Authorization via core-discovered `Authorizer` (`Can("admin.access")` on `admin.ui`)
- **CSRF protection**: Double-submit cookie — token set on GET/HEAD responses, validated as `X-CSRF-Token` header against `csrf-token` cookie on POST/PUT/DELETE/PATCH
- **Content Security Policy**: Strict CSP on all non-health/metrics responses (`default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'; object-src 'none'`, no `unsafe-inline`)
- **Security headers**: X-Content-Type-Options, X-Frame-Options, Referrer-Policy, Permissions-Policy
- **TLS**: Optional HTTPS listener via `ADMIN_UI_TLS_CERT` / `ADMIN_UI_TLS_KEY` (cookies marked `Secure` when TLS is active)
- **Rate limiting**: Per-IP counter on `POST /login`; after 6 attempts, 1-minute block (`Retry-After: 60`)
- **Trusted proxies**: `X-Forwarded-For` honored only when the TCP peer is in `ADMIN_UI_TRUSTED_PROXIES` (default loopback)
- **Prometheus metrics**: `/metrics` endpoint exposing request counts, active sessions, login stats, Go runtime metrics
- **Audit logging**: Admin mutations (auth, users, settings, formats, roots, rename, media library, passkeys) write fire-and-forget entries via core `AuditService.Log`; the audit browser queries via `AuditService.Query`

## Security Model

The admin UI is a sidecar module — it connects to core via gRPC and has no
special privileges. All data access goes through core's existing gRPC services
with their own auth and authorization checks. The admin module's own session
management is independent and server-side only.

## Disclosure Policy

1. Reporter submits private report
2. Maintainers triage within 72 hours, assign severity
3. Fix developed in private fork; reporter credited (with permission)
4. GitHub Security Advisory published with fix release

## Safe Harbor

We will not pursue legal action against researchers who:
- Test against their own MuxCore instance
- Avoid accessing or modifying data that does not belong to them
- Make a good-faith effort to avoid degradation of service
- Follow this policy's reporting and disclosure process
