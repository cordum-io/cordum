# Tamper-evident audit logging

Repo: cordum

## Problem
`SECURITY.md` claims append-only, tamper-evident audit logging, but audit data is stored in Redis lists/config without integrity chaining.

## Proposed
- Add an audit log store that writes append-only records with hash chaining and optional HMAC.
- Record key security events (policy publish/rollback, approvals, auth changes).
- Provide a verify endpoint/utility.

## Acceptance
- Audit records include prev_hash and hash (and optional HMAC).
- Verification detects tampering or missing entries.
- Docs updated with retention and key management guidance.

## References
- core/controlplane/gateway/policy_bundles.go
- core/workflow/store_redis.go
- SECURITY.md
