# Unreleased

This file captures user-visible changes that have landed on `main` but
have not yet been cut into a release. When a release is tagged, copy
these entries into a versioned release note and reset this file.

## Chore

- Dashboard bug fixes across five hooks/components plus one test-file
  drift. Touched:
  `dashboard/src/pages/settings/SettingsSCIMPage.tsx` — `handleRotate`
  now snapshots `scim.data.configured` at mutation-call time so the
  success toast no longer depends on a stale-closure read of the
  query cache after invalidate/unmount.
  `dashboard/src/components/ui/DataFreshness.tsx` — `formatRelative`
  clamps non-finite and non-positive timestamps to "Not updated" and
  the 10s ticker skips re-rendering while the input is invalid, so
  the button can never surface "Updated NaN ago".
  `dashboard/src/hooks/useAudit.ts` — `useAuditLog` replaced the
  `JSON.stringify(filters)` dep key with a scalar-keyed `useMemo`
  (`stableFilters`) so two identical-content filter objects reuse
  the same memoized result without the per-render stringify
  allocation.
  `dashboard/src/components/audit/AuditFiltersBar.tsx` — the three
  separate `useEffect` hooks that mirrored `actor`, `search`, and
  `resourceId` URL params into local debounced-input state were
  consolidated into one effect with all three deps.
  `dashboard/src/components/settings/EffectiveConfigPanel.tsx`,
  `dashboard/src/pages/govern/ReplayPage.tsx`,
  `dashboard/src/components/packs/MarketplaceBrowser.tsx`, and
  `dashboard/src/components/policy/ExplainResult.tsx` — documented
  the intentional "seed from prop once, then ignore prop updates"
  `useState` patterns inline so the draft-form / URL-deep-link /
  collapsible-card semantics are explicit. Test-file drift fixed in
  `dashboard/src/pages/ApprovalsPage.test.tsx` (renamed from
  `.test.ts`). New vitest tests landed at
  `dashboard/src/components/ui/DataFreshness.test.tsx` (covers `0`
  and `NaN` inputs plus the interval tick) and in
  `dashboard/src/hooks/useAudit.test.ts` (asserts the filtered memo
  is referentially stable for content-equal filter objects). No
  backend changes, no new feature flags, no audit/log surface
  touched — dashboard is the surface.
- Pruned Docusaurus `version-2.9` snapshot from
  `cordum/docs-site/versioned_docs/` — the version was never
  released publicly (no matching git tag) and no cross-repo caller
  referenced it. Docs now track a single current branch until the
  next public release is cut. Audit at
  [`docs/cleanup/versioned-docs-audit.md`](../cleanup/versioned-docs-audit.md).
- Cross-cutting comment sweep: removed residual `backward / legacy /
  deprecated` framing across `cordum/core`, the dashboard, and the
  operator docs where the word was inaccurate prose rather than a
  live architectural constraint. No runtime behavior change (no
  SIEMEvent or slog emissions altered; no handler, store, or RBAC
  surface touched). The durable classification is at
  [`docs/cleanup/backward-legacy-sweep-20260420.md`](../cleanup/backward-legacy-sweep-20260420.md)
  — every remaining hit in the tree maps to either `CONTEXTUAL`
  (architectural invariant still applies), `DOMAIN_VOCAB` (enum
  value or wire contract still in active use), or
  `ALREADY_COVERED_BY_SIBLING_TASK` (owned by a different
  `epic-1cadd6f2` task).

## Added

- Pack signature verification on install. Both `cordumctl pack install`
  and the gateway install endpoint now run the signing library's
  `VerifyPack` against the uploaded bundle before any state change.
  New cordumctl flags `--strict`, `--require-cordum-sig`,
  `--trusted-keys=<dir>`, and `--no-verify` (escape hatch that needs
  `--force` to work in strict mode). Strict mode can be set on the
  client via `CORDUM_PACK_STRICT=true` or on the gateway via
  `CORDUM_GATEWAY_PACK_STRICT=true`; ops can flip strict at runtime
  without a restart with `SET cfg:packs:strict_mode true` in Redis
  (propagates to every gateway replica within one second). Trusted
  keys: client-side `~/.cordum/trusted-keys/` (plus
  `CORDUM_PACK_TRUSTED_KEY_<KID>` env); server-side Redis
  `packs:trusted_keys:<kid>` (plus
  `CORDUM_GATEWAY_PACK_TRUSTED_KEY_<KID>` env bootstrap). Every
  install emits a stable audit event — `pack.install.verified` or
  `pack.install.rejected` — carrying the full decision record for SIEM
  pivots. Rejected installs return `400` with a stable
  `error_code` (`pack.unsigned`, `pack.bad_signature`, `pack.tampered`,
  `pack.unknown_kid`, `pack.missing_cordum_sig`, `pack.malformed`,
  `pack.verify_unavailable`). The installed-pack wire shape
  (`GET /api/v1/packs/installed/{id}`) gains an optional
  `verification` object — `{signed, publisher_id, kid, verified_at,
  has_cordum_counter_sig, signature_algorithm, pack_signature_version,
  warnings[]}` — so future dashboard badges can render trust state
  without another endpoint. Pre-existing installs from before this
  release read with `verification` absent; clients SHOULD render that
  as `{signed: false}`. Operator guide at
  [`docs/packs/signing.md`](../packs/signing.md#installation-and-verification).
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

- Removed the legacy OpenAPI sidecars `docs/api/openapi/cordum-rest.yaml`
  and `docs/api/openapi/cordum.swagger.json`. `docs/api/openapi/cordum-api.yaml`
  is now the single canonical OpenAPI 3 spec, `make openapi` is a pure
  Redocly validation pass, and the local/public Swagger UI wrappers now load
  only that canonical spec. Also removed the legacy prefixed MCP transport
  aliases `/api/v1/mcp/{sse,message,status}`; MCP transport is now exposed
  only at `/mcp/{sse,message,status}` while MCP governance REST endpoints
  remain under `/api/v1/mcp/*`. See
  [`docs/cleanup/openapi-legacy-audit.md`](../cleanup/openapi-legacy-audit.md)
  `Audit re-verification 2026-04-23` for the ground-truth timeline.
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
- **Delegation chain evaluation in Safety Kernel (`PolicyRule.Match.delegation`):**
  Policy authors can now gate rules on delegation chain properties via a
  structured YAML block with `max_depth`, `issuers` (allowlist),
  `require_issuer` (root pin), `required_scope` (subset check), and
  `forbid_delegated` (direct-call gate). Direct calls (no token) remain
  delegation-neutral per the load-bearing "No delegation = direct call,
  passes all delegation rules" rail, except when `forbid_delegated: true`
  explicitly opts into direct-only matching. A new Prometheus counter
  `safety_rule_delegation_match_total{field,outcome}` surfaces per-field
  rejection counts. The `DelegationAuditExtras` helper projects verified
  context into SIEMEvent `Extra` keys (`delegation.depth`,
  `delegation.root_issuer`, `delegation.parent_issuer`, `delegation.chain`,
  `delegation.jti`) while deliberately omitting the full scope list to
  respect the 8 KiB syslog line limit. See
  [`docs/auth/delegation.md#policy-rules`](../auth/delegation.md#policy-rules)
  for YAML schema, worked examples, and the troubleshooting matrix.
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

## Fixed

- Single-step approval workflows no longer get auto-invalidated as
  `stale_request` immediately after `POST /approve`. The gateway
  approve endpoint now locks the current `HashJobRequest(req)` into
  `SafetyDecisionRecord.JobHash`, and
  `scheduler.checkSafetyDecision` preserves a prior JobHash from
  gateway submit instead of clobbering it with a
  post-effective-config-mutation hash. Hash-fence store read failures
  now retry without publishing instead of falling through the input
  fail-open path. This is a bug fix, not an API
  contract change; any client that only observed the spurious
  `invalidate_stale_request` path should now see the benign approval
  succeed again. Follow-up to commit `297937c7` and guard task
  `task-035cdc8e`.
- **Session token entropy failure surface
  (`core/controlplane/gateway/handlers_auth.go`):** `buildUserLoginResponse`
  now returns the opaque `errSessionTokenEntropy` sentinel instead of a
  wrapped `crypto/rand` error on entropy-source failure, and the handler
  maps that to a generic 500 with no Set-Cookie and no token in the body.
  A server-side `slog.Error` still captures the underlying reader error
  for operators. Previously the `fmt.Errorf("crypto/rand: %w", err)`
  return leaked reader-level diagnostics up the call stack on any
  future caller that rendered the error text.
- **Audit consumer 1 MiB event guard (`core/audit/consumer.go`):**
  `NATSAuditConsumer.handle` now drops (ack-skip) any alert payload
  larger than `maxAuditEventBytes` (1 MiB) before calling
  `json.Unmarshal`. A crafted producer could otherwise pin the decoder
  on a giant allocation and starve the queue-group worker; the guard
  keeps the subscription loop flowing for legit traffic behind the
  oversized event.
- **Safety kernel decision cache non-positive guard
  (`core/controlplane/safetykernel/kernel.go`):** a `<=0` value for
  `SAFETY_DECISION_CACHE_MAX_SIZE` now emits a WARN noting the override
  was ignored and falls back to `defaultDecisionCacheMaxSize` (10 000).
  Silent acceptance of `cacheMax==0` would have disabled the cache
  entirely and routed every request through the policy evaluator,
  mimicking a policy-bundle outage.
- **Delegation issue TTL overflow bound
  (`core/controlplane/gateway/handlers_delegation.go`):** the
  `/api/v1/agents/{id}/delegate` handler now rejects `ttl_seconds`
  values greater than `maxDelegationTTLSeconds` (1 year) with a 400.
  Without this pre-multiplication bound, `time.Duration(foo) *
  time.Second` could wrap int64 nanoseconds into a negative duration
  and sneak past the service-layer `maxTTL` check.
- **Scheduler test harness parity (`core/controlplane/scheduler/`):**
  `fakeJobStore` and `fakeReconcileStore` now implement the full
  `model.JobStore` interface (including `GetJobEvents`), restoring
  `go test ./core/controlplane/scheduler/...` after the interface
  expansion. No production-path change.
- **P0 context-propagation and timestamp hardening sweep** across five
  server-internal sites. No public API signature changes; all five
  defects were reachable only from the gateway, safety kernel, audit
  consumer, or Redis-backed stores and never surface on the wire:
  `core/controlplane/gateway/handlers_auth.go` —
  `buildLoginResponse` / `buildUserLoginResponse` / cookie expiry /
  `expiresAt` parsing now compute timestamps in UTC so cross-replica
  `ExpiresAt` / `LastLoginAt` strings are monotonic regardless of the
  gateway host's local zone.
  `core/audit/consumer.go` — `NATSAuditConsumer` gains a lifecycle
  `ctx`/`cancel` pair; the per-message chain-append + SIEM export
  timeouts now derive from that parent so `Close()` aborts in-flight
  work instead of orphaning a handler past the JetStream ack deadline.
  `core/infra/store/redis_store.go` —
  `PutContext` / `GetContext` / `PutResult` / `GetResult` now reject
  nil ctx explicitly, short-circuit on `ctx.Err()`, and wrap Redis
  errors with operation-level context so saga retries can match on
  the wrapped error.
  `core/controlplane/safetykernel/kernel.go` — `setPolicy` /
  `setPolicyWithBundleCount` are now ctx-accepting and return an
  error; the Redis snapshot write on the reload-under-lock path
  derives its deadline from the caller's ctx rather than
  `context.Background()` so a cancelled watchPolicy goroutine no
  longer holds a Redis round-trip open past shutdown.
  `core/infra/store/agent_identity_store.go` — every method
  (`Create` / `Get` / `List` / `Update` / `Delete` / `GetByWorkerID`
  / `LinkWorker` / `UnlinkWorker`) now guards `s == nil || s.client
  == nil` uniformly, and `LinkWorker` / `UnlinkWorker` wrap the
  underlying Redis error so callers can distinguish "not initialised"
  from "transient Redis fault".
