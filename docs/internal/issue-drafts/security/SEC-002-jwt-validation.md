# Add JWT validation for API gateway

Repo: cordum

## Problem
`SECURITY.md` claims JWT validation, but OSS gateway only validates API keys.

## Proposed
- Add optional JWT validation for HTTP requests (Authorization: Bearer) when configured.
- Support standard claims (exp/nbf/iss/aud) and allowlist algorithms.
- Make JWT optional unless production mode requires it.

## Acceptance
- JWT validation rejects expired/invalid tokens when enabled.
- Config documented in docs/configuration.md.
- Tests cover valid/invalid tokens.

## References
- SECURITY.md
- docs/CORE.MD
- core/controlplane/gateway
