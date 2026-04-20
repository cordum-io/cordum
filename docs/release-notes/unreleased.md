# Unreleased

This file captures user-visible changes that have landed on `main` but
have not yet been cut into a release. When a release is tagged, copy
these entries into a versioned release note and reset this file.

## Added

- Pack signing toolchain. New `core/packs/signing` library (Ed25519,
  domain-separated `cordum.pack.v1`) produces a canonical manifest of
  `pack.yaml` plus every file referenced by
  `resources.schemas|workflows` and `overlays.config|policy`, hashes
  each with SHA-256, and signs with an operator-supplied Ed25519
  private key. New `cordumctl pack` subcommands — `keygen` (0600
  private key + auto-derived `pack-<8hex>` kid), `sign <root>` (writes
  YAML or JSON `pack.yaml.sig` envelope next to the manifest),
  `verify-signature <root>` (validates the envelope + re-walks the
  pack on disk, surfacing any hash drift), and `export-key` (prints
  `{kid, algorithm, public_key_b64}` JSON for registry submission).
  Publishers rotate keys via additive KID deploys; operators pin
  trusted keys with `--trusted-keys <dir>`. Full operator guide at
  [`docs/packs/signing.md`](../packs/signing.md).

## Removed

- Removed the pre-GA compat shims `core/licensing/compat.go` and
  `core/controlplane/gateway/auth_compat.go`. License envelopes in the
  legacy top-level `features` + `limits` shape are now hard-rejected
  with the typed error `licensing.ErrUnsupportedLegacyLicenseFormat` —
  operators running such a license must regenerate via
  `cordum-tools license-generator` in the current schema before
  starting the gateway. Rejection emits a structured
  `slog.Error("legacy license format rejected", ...)` log line with
  `kid` / `org_id` / `license_id` and a `suggested_action` hint, and
  the new SIEM event type `license.legacy_format_rejected`
  (`core/audit.EventLicenseLegacyRejected`) is available for audit
  exporters that want to monitor the brownout. Gateway callers now
  import `core/controlplane/gateway/auth` directly instead of using the
  old alias shim. Audit trail at
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
- **Enterprise RBAC + break-glass hardening sweep:** the remaining
  raw-role enterprise routes now use named permissions for
  `/api/v1/audit/{export,verify,legal-hold,legal-holds}`,
  `/api/v1/auth/keys*`, `/api/v1/license{,/usage}`,
  `/api/v1/telemetry/{status,inspect,export,usage,consent}`,
  `/api/v1/locks`, `/api/v1/topics*`,
  `/api/v1/policy/velocity-rules*`, `/api/v1/mcp/{outbound,prompts,tools,usage,verify-signature}`,
  `/api/v1/packs*`, `/api/v1/marketplace/{packs,install}`,
  `/api/v1/pools*`, `/api/v1/workers/credentials*`,
  `/api/v1/agents/revoke-delegation`, and the policy-shadow result
  routes. The remaining emergency-only surfaces
  (`/api/v1/license/reload`, `/api/v1/admin/locks`, manual lock
  mutation, auth recovery/session routes, and `/api/v1/stream`) now
  share explicit break-glass semantics: every admission emits the
  `license.breakglass_activated` SIEM event, structured warn logs, and
  `license_breakglass_decisions_total{decision,state}` metrics, while
  the dashboard license banner now calls out degraded break-glass mode
  instead of presenting it as a generic error.
