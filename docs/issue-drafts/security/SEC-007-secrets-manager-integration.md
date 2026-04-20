# Secrets manager integration (Vault/AWS)

Repo: cordum

## Problem
`SECURITY.md` claims integration with Vault and AWS Secrets Manager, but OSS only detects secret refs.

## Proposed
- Add secret resolver interface and providers for Vault and AWS Secrets Manager.
- Expose helper APIs for workers/gateway to resolve secret:// refs.

## Acceptance
- secret://vault/... and secret://aws-sm/... resolve when configured.
- Docs updated with env vars and IAM requirements.
- Tests cover ref parsing and failure modes.

## References
- core/infra/secrets/secrets.go
- SECURITY.md
- docs/CORE.MD
