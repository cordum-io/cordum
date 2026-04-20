# Unreleased

This file captures user-visible changes that have landed on `main` but
have not yet been cut into a release. When a release is tagged, copy
these entries into a versioned release note and reset this file.

## Removed

- Removed `core/licensing/compat.go` (the legacy claims-format migration
  layer). License envelopes in the pre-GA top-level `features` + `limits`
  shape are now hard-rejected with the new typed error
  `licensing.ErrUnsupportedLegacyLicenseFormat` — operators running
  such a license must regenerate via `cordum-tools license-generator`
  in the current schema before starting the gateway. Rejection emits a
  structured `slog.Error("legacy license format rejected", ...)` log
  line with `kid` / `org_id` / `license_id` and a `suggested_action`
  hint, and a new SIEM event type `license.legacy_format_rejected`
  (`core/audit.EventLicenseLegacyRejected`) is available for audit
  exporters that want to monitor the brownout. Audit trail at
  [`docs/cleanup/auth-license-compat-audit.md`](../cleanup/auth-license-compat-audit.md).
- Removed `sdk/client.BuildTLSTransport` — the error-swallowing wrapper
  that logged CA-read failures to stderr and returned `nil`. Use
  [`sdk/client.BuildTLSTransportErr`](../../sdk/client/client.go)
  instead, which returns explicit errors. No external callers existed
  (pre-GA). Migration is a straightforward `(tr, err) := ...` swap —
  see `sdk/client/client_test.go` for the pattern. Audit trail at
  [`docs/cleanup/deprecated-symbols-audit.md`](../cleanup/deprecated-symbols-audit.md).

## Added

- **Delegation token service (`/api/v1/agents/{id}/delegate`,
  `/api/v1/agents/verify-delegation`,
  `/api/v1/agents/revoke-delegation`):** Enterprise agent identities can now
  mint Ed25519-signed JWT delegation tokens with bounded `allowed_actions`,
  `allowed_topics`, TTL, chain depth, and revocation by `jti`. Gateway job
  submission verifies delegation tokens, injects `_delegation.*` context for
  Safety Kernel policy when `CORDUM_DELEGATION_POLICY_ENABLED=true`, and emits
  lineage-preserving audit events for issue / verify / revoke. Operator
  guidance lives in [`docs/auth/delegation.md`](../auth/delegation.md), and the
  canonical HTTP contract is now captured in
  [`docs/api/openapi/cordum-api.yaml`](../api/openapi/cordum-api.yaml).
- **Policy Decision Log API (`/api/v1/governance/decisions`):**
  governance-native read surface for policy outcomes, including matched
  rule, verdict, reason, constraints, approval status/decision,
  `agent_id`, and cursor pagination. The backing Redis indexes are
  written synchronously from the authoritative safety-decision path and
  documented in [`docs/governance/decision-log.md`](../governance/decision-log.md).
  Operational tooling now includes `cordumctl governance backfill-decisions`
  for historical reindexing and `cordumctl governance tail` for
  self-healing replay from `sys.audit.export`.
- **Eval dataset store (`/api/v1/evals/datasets`):** Redis-backed CRUD
  API for curated, versioned, immutable policy-regression test fixtures.
  `PUT /api/v1/evals/datasets/{id}` creates a successor version instead
  of mutating in place, so historical datasets remain queryable.
  Datasets are durable by design and can only be destroyed via the
  explicit admin-only `force=true` escape hatch. See
  [`docs/evals/datasets.md`](../evals/datasets.md) for the immutability
  contract, RBAC surface, and curl recipes. New permissions:
  `evals.datasets.read`, `evals.datasets.write`, `evals.datasets.delete`.
  Phase-2 eval-runner and dashboard surfaces ship in sibling tasks
  within the same epic.
