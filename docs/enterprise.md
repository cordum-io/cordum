# Cordum Enterprise

Enterprise features are delivered by the `cordum-enterprise` repo and require
signed licenses. The core repo stays focused on the platform-only control plane
and is licensed under BUSL-1.1.

## Current enterprise features

- Enterprise API gateway binary
- License enforcement at startup
- Enterprise auth provider (multi-tenant API keys + RBAC)

## Planned enterprise extensions

- SSO/SAML
- SIEM export
- Advanced RBAC controls
- Dedicated support tooling

## Licensing model

- Core (`cordum`): Business Source License 1.1 (free for self-hosted use; no
  competing hosted/managed offering).
- Enterprise (`cordum-enterprise`): commercial EULA.
- Protocol/SDK (`cap`): Apache-2.0.

## Where to look

- Enterprise repo: `cordum-enterprise`
- Licensing tooling: `cordum-tools`
- License docs: `cordum-enterprise/docs/license.md`
