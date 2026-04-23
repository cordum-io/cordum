# Cordum Enterprise

Enterprise features ship in cordum core behind signed license entitlements.
The core repo is licensed under BUSL-1.1 (free for self-hosted/internal use;
no competing hosted/managed offering). The formerly separate
`cordum-enterprise` repo was retired 2026-04-23 — see
`docs/release-notes/unreleased.md`.

## Current enterprise features (all in core)

- Gateway SSO/SAML and OIDC (`core/controlplane/gateway/auth/{saml,oidc_flow}.go`)
- SCIM provisioning (`core/controlplane/gateway/auth/scim.go`)
- Advanced RBAC (role hierarchy, custom permissions, route enforcement)
- SIEM audit export (`core/audit/{webhook,syslog,datadog,cloudwatch}.go`)
- Legal hold on audit data (`core/audit/legal_hold.go`)
- Velocity rules with dashboard enforcement
- Agent identity entitlement
- Multi-tenant API keys + RBAC
- User/password authentication with bcrypt-secured credentials
- Break-glass admin mode with structured audit trail

License loading, validation, and tier enforcement live in `core/licensing/`.
The licensing system uses Ed25519 signature verification and enforces tiers
(Community/Team/Enterprise) with entitlement limits across all services
(gateway rate limits, scheduler concurrency, workflow steps, safety kernel
policy bundles, audit retention). Licenses degrade gracefully to Community tier
on expiry.

License **issuance** tooling (signing CLI, agreement generator) lives in
`cordum-tools/cmd/licensegen/`.

Configuration variables:
- `CORDUM_LICENSE_FILE` — path to the signed license file
- `CORDUM_LICENSE_TOKEN` — inline license token (alternative to file)
- `CORDUM_LICENSE_PUBLIC_KEY` — inline Ed25519 public key for signature verification
- `CORDUM_LICENSE_PUBLIC_KEY_PATH` — path to the Ed25519 public key file (alternative to inline)

## Authentication

The platform supports multiple authentication methods:

### API Key Authentication (default)
- Set via `CORDUM_API_KEY` or `CORDUM_API_KEYS`
- Used for programmatic access (scripts, CI/CD, workers)
- Sent via `X-API-Key` header

### User/Password Authentication
- Enable with `CORDUM_USER_AUTH_ENABLED=true`
- Users stored in Redis with bcrypt-hashed passwords
- Supports login via username or email
- Admin can create users via `POST /api/v1/users`
- Users can change password via `POST /api/v1/auth/password`

### SSO (SAML and OIDC) (Enterprise entitlement)
- Integrates with SAML and OIDC identity providers (Okta, Azure AD, Google Workspace, Auth0)
- Marked with "Enterprise" badge in dashboard
- Configure via SAML environment variables or OIDC environment variables — see `docs/configuration-reference.md`

## Planned enterprise extensions

- Air-gapped deployment guide
- FIPS 140-2 compliance mode
- Dedicated support tooling

## Licensing model

- Core (`cordum`): Business Source License 1.1 (free for self-hosted use; no
  competing hosted/managed offering). Ships both OSS and commercial surfaces;
  commercial features unlocked by signed license.
- Protocol/SDK (`cap`): Apache-2.0.

## Where to look

- License loading + enforcement: `core/licensing/`
- License generator CLI: `cordum-tools/cmd/licensegen/`
- Retirement note: `docs/release-notes/unreleased.md`
