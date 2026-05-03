# Encryption at rest (configurable)

Repo: cordum

## Problem
`SECURITY.md` claims encryption at rest, but Redis-backed stores write plaintext payloads.

## Proposed
- Add optional encryption wrapper for Redis-backed stores (contexts/results/artifacts).
- Configure via env with key rotation support.

## Acceptance
- Encrypted payloads are unreadable without key.
- Encryption is opt-in to preserve dev behavior.
- Docs updated with env vars and rotation guidance.

## References
- core/infra/memory/redis_store.go
- core/infra/artifacts/redis_store.go
- SECURITY.md
