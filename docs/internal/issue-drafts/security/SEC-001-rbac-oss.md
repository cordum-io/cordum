# Enforce RBAC roles in OSS gateway

Repo: cordum

## Problem
`SECURITY.md` claims RBAC with fine-grained permissions, but OSS auth is API-key allowlist only and `RequireRole` is a no-op. Any valid key can reach admin-only endpoints.

## Proposed
- Extend API key entries to include role, principal, tenant, and optional cross-tenant flags.
- Enforce `RequireRole` in the basic auth provider using the resolved role.
- Disallow role escalation via headers unless explicitly enabled.

## Acceptance
- Admin-only endpoints return 403 when key role is not admin.
- Role is resolved from key metadata; header overrides are disabled by default.
- Docs updated with key format examples.

## References
- core/controlplane/gateway/basic_auth.go
- core/controlplane/gateway/authorize.go
- SECURITY.md
- docs/system_overview.md
