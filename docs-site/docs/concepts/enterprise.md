---
sidebar_position: 13
title: "Enterprise Features"
slug: /concepts/enterprise
---

# Cordum Enterprise

Enterprise features ship in cordum core and unlock at runtime when a valid
signed license is present. The core repo is licensed under BUSL-1.1. The
formerly separate `cordum-enterprise` repo was retired 2026-04-23 — see the
release notes for the retirement bullet.

## Current enterprise features (all in core)

- Gateway SSO/SAML/OIDC + SCIM provisioning
- Advanced RBAC (role hierarchy, custom permissions, full route enforcement)
- SIEM audit export (webhook, syslog, Datadog, CloudWatch)
- Legal hold on audit data
- Velocity rules with dashboard enforcement
- Agent identity entitlement
- Multi-tenant API keys + RBAC
- User/password authentication with bcrypt-secured credentials
- Break-glass admin mode with structured audit trail

License loading, validation, and tier enforcement live in `core/licensing/`.
The licensing system uses Ed25519 signature verification and enforces three
tiers (Community/Team/Enterprise) with entitlement limits across all services
(gateway rate limits, scheduler concurrency, workflow steps, safety kernel
policy bundles, audit retention). Licenses degrade gracefully to Community tier
on expiry.

License **issuance** (signing CLI, agreement generator) lives in
`cordum-tools`.

Configuration variables (set in core):
- `CORDUM_LICENSE_FILE` — path to the signed license file
- `CORDUM_LICENSE_TOKEN` — inline license token (alternative to file)
- `CORDUM_LICENSE_PUBLIC_KEY` — Ed25519 public key for signature verification

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

### SSO (SAML/OIDC — Enterprise entitlement)
- Integrates with identity providers via SAML or OIDC (Okta, Azure AD, etc.)
- Marked with "Enterprise" badge in dashboard
- Configure via the SAML or OIDC environment variables (see `docs/auth.md`)

## Planned enterprise extensions

- Air-gapped deployment guide
- FIPS 140-2 compliance mode
- Dedicated support tooling

## Licensing model

- Core (`cordum`): Business Source License 1.1 (free for self-hosted use; no
  competing hosted/managed offering). Ships both the OSS and commercial
  surfaces; commercial features unlocked at runtime by a signed license.
- Protocol/SDK (`cap`): Apache-2.0.

## Where to look

- License loading + enforcement: `core/licensing/`
- License generator CLI: `cordum-tools/cmd/licensegen/`
- Retirement note: `docs/release-notes/unreleased.md`
