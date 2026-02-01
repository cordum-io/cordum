# Cordum Enterprise

Enterprise features are delivered by the `cordum-enterprise` repo and require
signed licenses. The core repo stays focused on the platform-only control plane
and is licensed under BUSL-1.1.

## Current enterprise features

- Enterprise API gateway binary with license enforcement
- Enterprise auth provider (multi-tenant API keys + RBAC)
- User/password authentication with bcrypt-secured credentials
- SSO/SAML integration
- Audit export (JSON, CSV, SIEM)
- Advanced RBAC controls

License configuration (e.g., `CORDUM_LICENSE_KEY`, `CORDUM_LICENSE_PATH`) is
documented in the `cordum-enterprise` repo.

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

### SSO/SAML (Enterprise)
- Integrates with identity providers (Okta, Azure AD, etc.)
- Marked with "Enterprise" badge in dashboard
- Configure via SAML environment variables

## Planned enterprise extensions

- Air-gapped deployment guide
- FIPS 140-2 compliance mode
- Dedicated support tooling

## Licensing model

- Core (`cordum`): Business Source License 1.1 (free for self-hosted use; no
  competing hosted/managed offering).
- Enterprise (`cordum-enterprise`): commercial EULA.
- Protocol/SDK (`cap`): Apache-2.0.

## Where to look

- Enterprise repo: `cordum-enterprise`
- License docs: `cordum-enterprise/docs/license.md`
