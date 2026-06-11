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
- **Auth delegation**: All auth delegated to core's `AuthProvider` and `Authorizer` modules
- **CSRF protection**: Origin header validation on all mutating HTTP methods
- **Content Security Policy**: Strict CSP header on all responses (`default-src 'self'; script-src 'self'; style-src 'self'`)
- **Security headers**: X-Content-Type-Options, X-Frame-Options, Referrer-Policy, Permissions-Policy
- **TLS**: Optional HTTPS listener via `ADMIN_UI_TLS_CERT` / `ADMIN_UI_TLS_KEY`

### Not yet implemented

- Rate limiting on login endpoint (planned Phase 5)
- CSRF double-submit cookie pattern (planned Phase 5)
- Audit logging of admin actions (planned Phase 5)
- Prometheus metrics endpoint (planned Phase 5)

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
