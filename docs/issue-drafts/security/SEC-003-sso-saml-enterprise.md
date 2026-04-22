# Add SSO/SAML integration

Repo: cordum-enterprise

## Problem
`SECURITY.md` claims SSO/SAML integration, but OSS only exposes extension routes for enterprise providers.

## Proposed
- Implement SSO/SAML endpoints and auth provider in enterprise gateway.
- Provide config discovery for the dashboard login flow.

## Acceptance
- Enterprise gateway exposes SAML auth endpoints.
- UI can initiate SSO login and consume metadata.
- Docs updated in enterprise repo.

## References
- SECURITY.md
- docs/enterprise.md
- core/controlplane/gateway (RouteRegistrar hook)
