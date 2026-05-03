# API key rotation and expiry

Repo: cordum

## Problem
`SECURITY.md` claims API key rotation, but keys are static env values with no expiry or reload.

## Proposed
- Support key metadata (id, role, expires_at) and optional key file reload.
- Add rotation docs and sample config.

## Acceptance
- Expired keys are rejected.
- Key reload works without process restart when using file-based keys.
- Docs updated with rotation guidance.

## References
- core/controlplane/gateway/basic_auth.go
- docs/configuration.md
- SECURITY.md
