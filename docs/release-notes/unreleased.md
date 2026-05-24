# Unreleased

This file captures user-visible changes that have landed on `main` but
have not yet been cut into a release. When a release is tagged, copy
these entries into a versioned release note and reset this file.

## Changed

- **core: extracted the Unix-timestamp → RFC3339 formatter into
  `core/infra/timeutil`.** Five inline formatters across the
  dashboard-facing handler layer (`handlers_chat.go` auto-detect,
  `handlers_governance.go` + `handlers_governance_approvals.go` millis,
  `handlers_policy_bundles.go` micros, `handlers_velocity.go` seconds)
  each carried their own copy of `time.Unix*(ts).UTC().Format(RFC3339)`
  plus a `ts <= 0` guard. A schema change flipping an endpoint's unit
  used to update only the one matching formatter with no mechanical
  way to catch drift across siblings. `core/infra/timeutil/format.go`
  now exports `FormatUnixAuto` (magnitude cascade matching
  handlers_chat.go byte-for-byte, 1e18/1e15/1e12 cutoffs) plus four
  typed variants `FromSeconds` / `FromMillis` / `FromMicros` /
  `FromNanos` for callers that know the unit at compile time. All
  five helpers return empty string on `ts <= 0`, matching the
  pre-refactor guards. Output strings are byte-for-byte identical to
  the inline versions on the same input; no dashboard change
  required. The named wrappers in each handler (`chatCreatedAt`,
  `governanceTimestamp`, `millisToRFC3339`, `timestampFromMicros`)
  stay as 1-line forwarders so their call sites don't change. Closes
  task-e396a874.

- **core: extracted `proto.Clone((*pb.JobRequest))` guard-pattern into
  `core/protocol/protoutil/CloneJobRequest`.** Consolidates 4 inline
  call sites (`handlers_jobs.go`, `scheduler/engine.go`,
  `scheduler/saga.go`, `reqhash/reqhash.go`) onto one helper with
  a typed ok-check and nil guard. The saga.go site was missing the
  ok-check entirely before this migration — see the paired `Fixed`
  entry for the latent nil-deref that closed. The other 2
  `proto.Clone(JobMetadata)` sites in saga.go (different type) are
  NOT migrated here — a separate `CloneJobMetadata` helper can be
  added later if drift emerges there. Closes task-625b2ed1.

- **gateway: removed `packs_compat.go` and `policy_compat.go` (233 lines
  of pure-alias shims).** Both files existed only to re-export
  types/consts/functions from the `core/controlplane/gateway/packs` and
  `core/controlplane/gateway/policybundles` sub-packages back into the
  gateway package so older call sites could use unqualified names
  (`packManifest`, `policyBundleSnapshot`, etc.) without importing the
  sub-packages directly. Every one of the ~40 callers now imports
  `packs` and/or `policybundles` and references the fully-qualified
  `packs.PackManifest` / `policybundles.PolicyBundleSnapshot` shape.
  `resolveAgentForAudit` (the one real method that lived in
  `policy_compat.go`, not an alias) moved to `handlers_agents.go` near
  the other agent-identity helpers. Internal refactor only — no public
  API change, no JSON-on-the-wire change, no behavior change.
  Closes task-a828e179.

- **core: extracted the Redis CAS retry loop into
  `core/infra/redisutil/Retry`.** Four production call sites across
  `gateway/auth/keystore_redis.go` (RevokeKey) and `gateway/mcp_approvals.go`
  (Consume, Resolve, SweepExpired) used to carry a hand-rolled
  `for attempt := 0; attempt < N; attempt++ { client.Watch(...) ; continue on
  TxFailedErr ; return on other }` loop. Each copy could drift on its own
  (off-by-one budget, missing `continue`, wrong error class) — a prior
  production incident (task-035cdc8e) was exactly this retry-loop edge
  case. Behavior preserved exactly: default 3-attempt budget (overridable
  via `WithMaxAttempts(n)`; mcp_approvals keeps its `mcpCASMaxAttempts=5`),
  retry only on `redis.TxFailedErr`, bubble any other error on the first
  attempt that sees it, honour `ctx.Done()` between attempts. Closures that
  capture outer variables (e.g. `consumed`, `final`, `result`, `expired`)
  keep working unchanged because `fn` is still passed directly to
  `client.Watch`. New `redisutil.ErrMaxAttemptsExceeded` sentinel wraps the
  last TxFailedErr via `%w` so callers that want to distinguish budget
  exhaustion from other errors can do so with `errors.Is`. Two call sites
  originally suggested by the audit (`configsvc/service.go:128`,
  `core/infra/store/job_store.go:2711`) are left inline: both are
  single-Watch with typed error translation (→`ErrRevisionConflict` and
  →`ApprovalConflictError`), not retry loops, so the helper does not apply.
  Closes task-c7e419d8.

- **core: extracted JobRequest canonicalisation into a single shared
  helper at `core/protocol/reqhash`.** The scheduler, gateway, and
  infra/store packages each used to carry their own copy of the same
  strip-labels + protojson-roundtrip + deterministic-marshal + sha256
  routine; task-fa783d7a landed a reconciliation inside
  `store.hashApprovalJobRequest` to close a divergence that was
  auto-DENYing benign approvals. task-090ab6af finishes the
  unification: the canonicalisation logic now lives in exactly one
  place (`reqhash.Canonical` + `reqhash.Hash`); the public
  `scheduler.HashJobRequest` and `store.hashApprovalJobRequest` are
  preserved as two-line forwarders so the 20+ call sites across
  handlers, tests, and the reconciler keep compiling transparently,
  while the previously-duplicated private helpers are deleted
  outright. Also fixed five bare
  `protojson.Unmarshal` call sites in `core/infra/store/job_store.go`
  (`GetJobRequest`, `ApplyApprovalRepair`, `ResolveApproval`, and the
  two `PolicyConstraints` loaders) to pass
  `UnmarshalOptions{DiscardUnknown: true}`, so forward-compat proto
  fields from a newer SDK no longer break the read path or leak into
  the in-memory proto. New regression test
  `TestGetJobRequest_DiscardsUnknownJSONFields` pins the invariant at
  the store boundary. Redis WATCH/MULTI atomic store-and-hash was
  evaluated and explicitly rejected for this release; the trade-off
  analysis and re-visit triggers live in the internal decisions log
  (Cordum engineering). Closes task-090ab6af.

## Security

- **WebSocket quarantine-redaction fail-closed**
  (`core/controlplane/gateway/handlers_stream.go`) — the filter that strips
  `ResultPtr` + `ArtifactPtrs` from DENIED `JobResult` packets before
  broadcasting to WebSocket subscribers previously FAILED OPEN on
  `proto.Clone` type-assertion failure AND on the defensive
  `cloned.GetJobResult() == nil` branch, returning the ORIGINAL packet with
  sensitive content intact. Redis-stored result payloads may contain PII,
  user prompts, secrets, or model outputs; leaking them to any WS subscriber
  (including cross-tenant subscribers with `allowCrossTenant` granted) is a
  data-exposure bug. The filter now fails CLOSED: on any clone or
  sanitisation failure it returns nil, `enqueueBusPacket` drops the
  broadcast, `cordum_gateway_ws_quarantine_redaction_drops_total` increments,
  and an error is logged with `job_id` + `trace_id`. The next state-change
  event will follow in the normal stream cadence, so dashboards recover
  without operator intervention. See task-1d4e6b4c.

## Fixed

- **scheduler/saga.go: fixed a latent nil-deref in the JobRequest
  clone path at `buildCompensationRequest`.** The inline
  `req := proto.Clone(base).(*pb.JobRequest)` lacked the ok-check that
  every other `proto.Clone(x).(*pb.JobRequest)` site in the codebase
  already had. On a proto.Clone type-assertion failure (vanishingly
  rare, but reachable on library upgrade or memory-pressure
  corruption), the next line would dereference nil and panic the
  scheduler mid-compensation. The migration to the new
  `core/protocol/protoutil.CloneJobRequest` helper enforces the
  ok-check at every call site, so this class of drift cannot recur.
  Operator impact: none in the happy path; on the impossible-to-
  observe failure path the scheduler now returns a wrapped error
  (`compensation: clone base job request: ...`) instead of crashing
  the worker goroutine. Closes task-625b2ed1.

- **audit: `SyslogExporter.Close` now logs at Warn when the underlying
  `net.Conn.Close` returns an error.** Previously the failure was
  returned opaquely to the `BufferedExporter` close cascade, where
  generic close-cascade handling could absorb it silently — masking
  half-open sockets and TCP-stack fsync failures that left buffered
  audit events unflushed. The returned-error contract is unchanged
  (`BufferedExporter` still observes the error); operators now also
  see a `syslog: close failed` Warn line with `network`, `address`,
  and `error` fields. Closes task-8db173c5.

- **Investigated and dismissed (not a bug).** The 2026-04-23 bug-hunt
  agent flagged two additional sites that on verification are
  already correctly handled; documented here so future audits don't
  re-litigate them:
  - `core/controlplane/gateway/handlers_jobs.go:201`
    `statusCacheObj.Get()` was flagged as returning `any` without a
    type guard. False positive: `statusCache.Get()` returns a typed
    `map[string]any`, not `any`; `writeJSON(w, cached)` is
    type-safe at compile time and `map[string]any` always marshals
    to valid JSON.
  - `core/auth/delegation/token.go` `NewTokenService` wrong-size
    signing-key handling was re-flagged as a silent drop. Current
    code (since the original delegation feature commit `09f4838a`)
    already returns `(*TokenService, error)` with
    `ErrInvalidSigningKey` when the signing key's own public key is
    not `ed25519.PublicKeySize`. No fix needed.

- **gateway: WebSocket `SetReadDeadline` errors at connection setup are now
  propagated instead of silently discarded.** `handleStream` and
  `handleJobStream` both called `_ = ws.SetReadDeadline(...)` on the just-
  accepted connection; if the underlying socket was already compromised
  (race with client disconnect, tcp reset) the error was dropped and the
  read loop entered with no deadline, so the server waited indefinitely for
  a frame that would never arrive. Extracted a small `wsReadDeadliner`
  interface plus a `setReadDeadlineOrError` helper so both call sites now
  log at `Warn`, set the disconnect state, `ws.Close()`, and return. The
  `SetPongHandler` callback already propagated its SetReadDeadline error
  and is unchanged. See task-1d4e6b4c bug #2.

- **gateway: `revalidateWSAuthWithRetry` now surfaces the last transient
  error after 3 exhausted retries instead of returning nil (fail-silent).**
  A NATS timeout or Redis outage during credential revalidation would
  previously keep a potentially-revoked session alive for the full
  2-minute revalidation window. The retry loop now returns a wrapped error
  after exhaustion; the two callers already branch on `err != nil` to close
  the connection, so the dashboard auto-reconnects and re-authenticates
  within the revalidation budget rather than running on stale auth. The
  `ctx.Done()` branch still returns nil — caller-initiated shutdown is not
  a failure. See task-1d4e6b4c bug #3.

- **safety-kernel: `shadowTimeout` now actually bounds the per-submission
  shadow evaluation loop.** Previously the bounded context returned by
  `context.WithTimeout` in `core/controlplane/safetykernel/shadow_eval.go`
  was discarded (`_, cancel := ...`), so the timeout never applied and a
  slow shadow-policy eval (or a slow audit emit) could extend loop
  duration indefinitely, contradicting the documented "absolute wall-clock
  budget for processing every shadow for this submission". The fix
  captures the bounded ctx, plumbs it through `evalShadowSafely`, and
  adds a `ctx.Err()` check at the top of each bundle iteration.
  Granularity is per-bundle: one bundle may still exceed the timeout by
  its own eval time, but subsequent bundles are skipped, and partial
  shadow-event counts are expected behavior on timeout (observability
  dashboards filtering by tenant may see fewer `shadow_eval` events on
  submissions that hit the bound). Operators tuning `shadowTimeout` via
  `ShadowEvaluatorOptions.ShadowTimeout` will see the expected wall-clock
  bound going forward. Closes task-681f83cd.

- **policy shadow: registered the six shadow-policy gateway routes so the
  dashboard's PromoteShadowDialog works end-to-end.** The handlers
  (`handlePutPolicyShadow`, `handleGetPolicyShadow`, `handleDeletePolicyShadow`
  in `core/controlplane/gateway/handlers_policy_shadow.go`; the three
  `handleShadowResults*` handlers in `handlers_shadow_results.go`) existed
  with direct-call tests but were never wired into `registerRoutes`, so every
  network call from the dashboard — activate, fetch, deactivate, plus the
  summary / comparisons / timeseries analytics — 404'd at the mux.

  Wired as:
  - `PUT    /api/v1/policy/shadows/{id}` — activate/replace (idempotent upsert).
  - `GET    /api/v1/policy/shadows/{id}` — fetch (404 when no shadow active).
  - `DELETE /api/v1/policy/shadows/{id}` — deactivate.
  - `GET    /api/v1/policy/shadows/{id}/results/summary?from=&to=`
  - `GET    /api/v1/policy/shadows/{id}/results/comparisons?from=&to=[&diff=&cursor=&limit=]`
  - `GET    /api/v1/policy/shadows/{id}/results/timeseries?from=&to=&bucket={1m|5m|15m|1h|1d}`

  The top-level `/policy/shadows/{id}` URL replaces the originally proposed
  `/policy/bundles/{id}/shadow`: that pattern overlaps
  `/api/v1/policy/bundles/snapshots/{id}` at `/bundles/snapshots/shadow` and
  Go 1.22+ ServeMux rejects the conflict at registration (neither pattern is
  strictly more specific; disambiguator routes are not honored). The new
  path mirrors the existing `/api/v1/policy/snapshots/{id}` shape so operator
  muscle memory carries across the two features.

  Also corrected three dashboard wire bugs in
  `dashboard/src/hooks/useShadowPolicy.ts` that would have prevented a clean
  end-to-end flow even after registration:
  - `useActivateShadow` now sends `PUT` (was `POST`) — matches the backend's
    idempotent-upsert semantics.
  - Summary / comparisons / timeseries query strings now emit `to=` (was
    `until=`) to match `parseShadowResultsRange` in
    `handlers_shadow_results.go`.
  - All six hooks target the new `/policy/shadows/{id}[/results/*]` paths.

  While touching the results handlers, unified tilde decoding:
  `extractBundleIDFromPath` in `handlers_shadow_results.go` now decodes
  `~` → `/` the same way `policybundles.BundleIDFromRequest` does, so the
  `/shadow` and `/shadow/results/*` surfaces resolve identical canonical
  bundle IDs for the same wire path. Closes task-44807b2c.

- **audit: chain is now instantiated unconditionally at gateway boot.**
  Previously the Redis-backed Merkle audit chain silently disabled when
  (a) the plan's SIEM export entitlement was blocked for a non-discard
  `CORDUM_AUDIT_EXPORT_TYPE` — `initAuditPipeline` returned
  `(nil, nil, nil)` after `NewExporterFromEnvWithEntitlements` returned
  a nil buffered exporter, or (b) direct transport (`AUDIT_TRANSPORT`
  unset) was used without routing through the NATS consumer —
  `auditSender` was assigned the raw buffered exporter on the direct
  path with no chain wrapper, so `/api/v1/audit/verify` reported
  `total_events=0` even though audit writes appeared healthy at the API
  boundary. After this change the chainer is created first and wired
  through `newAuditChainSender` on every write path, so neither
  scenario can disable the chain. The `null`/`discard`/`chain-only`
  backends are no longer required just to keep the chain alive; they
  remain supported for operators who want a measured no-op SIEM target.
  Operators who set `CORDUM_AUDIT_EXPORT_TYPE=null` explicitly continue
  to work unchanged — the compose default moved from `null` to unset,
  but `${VAR:-default}` respects an explicit value. See
  [`docs/deployment/audit-chain.md`](../deployment/audit-chain.md) for
  the updated backend-type table and 503 runbook. Cross-ref:
  task-e1d54a75 (the DiscardExporter hotfix this supersedes).
  Closes task-096de016.

- **scheduler: worker online transitions now trigger an immediate
  flush of pending dispatch for that pool,** eliminating the first-run
  latency that previously made scale-from-zero deployments wait up to
  5 minutes before the first pending job left its queue. The
  `MemoryRegistry` now exposes `UpdateHeartbeatWithTransition`, the
  engine's heartbeat handler type-asserts that method and schedules a
  non-blocking `scheduleFlushOnWorkerOnline` on transition, a
  per-pool `flushLatch` collapses concurrent heartbeats from a
  freshly-scaled fleet into a single flush per pool, and
  `defaultFlushDispatchForPool` reuses the existing
  `handleJobRequest` dispatch path — no forked dispatch logic. New
  metric `cordum_scheduler_dispatch_flush_on_worker_online_total{pool}`
  counts flushes that moved at least one pending job. See
  [`docs/scheduler.md`](../scheduler.md) §Flush-on-worker-online for
  the full design. Closes task-7a2514ae.

## Corrections

- `task-fa783d7a` description references a "three-layer hotfix" (60s
  grace period in `ClassifyApprovalRepair` + `preMutationHash`
  threading through `checkSafetyDecisionTracedWithHash` + gateway
  approve lock) supposedly applied on 2026-04-19. That implementation
  was explored in a local commit on `super/all-local-work-2026-04-20`
  but was **never merged to `main`**; the team abandoned it in favor
  of the canonicaliser-based "proper fix" that currently ships on
  `main` (commit `b06c22fe`) and on the follow-up branch. The DoD
  items are superseded as follows:
  - DoD #1 (cherry-pick the hotfix) — **SUPERSEDED.** Re-introducing
    the 60s grace period in `ClassifyApprovalRepair` would mask the
    real bug and is a regression; see the file-header comment in
    `core/infra/store/approval_repair_regression_test.go` which
    explicitly states the grace period "is gone; it masked the real
    bug".
  - DoD #2 (regression test for the grace period in
    `core/infra/store/job_store_test.go`) — **NOT APPLICABLE.** No
    grace period to cover.
  - DoD #3 (regression test for `preMutationHash` in
    `core/controlplane/scheduler/engine_test.go`) — **NOT APPLICABLE.**
    The canonicaliser (`scheduler.HashJobRequest` in
    `core/controlplane/scheduler/job_hash.go`) strips `approval_*`
    labels, `bus.LabelBusMsgID`, and `config.EffectiveConfigEnvVar`
    and protojson-roundtrips the request, so the post-mutation hash
    equals the pre-mutation hash by construction — no separate
    pre-mutation capture is needed. Existing coverage in
    `core/controlplane/scheduler/job_hash_stale_request_test.go`
    (6 tests) pins this contract.
  - DoD #4 (mock-bank $200 smoke reaches `succeeded`) — covered by
    this task's Phase 5 smoke run.
  - DoD #5 (architectural followup filed) — covered by this task's
    Phase 6 follow-up Moe task (unify canonical hash into a shared
    package + evaluate atomic Redis store-and-hash).
  - DoD #6 (no regression in legitimate stale detection) — covered by
    the existing
    `TestClassifyApprovalRepair_RealPayloadDrift_StillTripsStaleRequest`
    regression in
    `core/infra/store/approval_repair_regression_test.go`.

  This task's scope is therefore *verify-and-harden* the shipped
  canonicaliser, close one latent cross-package divergence
  (`store.hashApprovalJobRequest` did not protojson-roundtrip), and
  file the architectural follow-up.

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
  next public release is cut. Internal versioned-docs audit captures
  the full evidence (Cordum engineering).
- Cross-cutting comment sweep: removed residual `backward / legacy /
  deprecated` framing across `cordum/core`, the dashboard, and the
  operator docs where the word was inaccurate prose rather than a
  live architectural constraint. No runtime behavior change (no
  SIEMEvent or slog emissions altered; no handler, store, or RBAC
  surface touched). The durable classification is captured in the
  internal backward-legacy sweep audit (Cordum engineering)
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

## Retired

- **cordum-enterprise repo retired.** All enterprise features — SAML/OIDC SSO,
  SCIM provisioning, advanced RBAC, SIEM export (webhook/syslog/Datadog/
  CloudWatch), legal hold, velocity rules, agent identity — now live in cordum
  core behind license entitlements per epic-4da6e4f8. The separate repo is
  archived on GitHub; historical commits and security advisories remain
  readable. Operators upgrading from a cordum-enterprise binary must switch to
  the core gateway with an appropriate license; see
  [`docs/enterprise.md`](../enterprise.md). Workspace wiring removed in the
  same PR: the `cordum-tools/go.mod` `replace ../cordum-enterprise` directive
  is gone, and workspace docs (`CLAUDE.md`, `REPOSITORY_MAP.md`,
  `cordum-tools/CLAUDE.md` + `AGENTS.md` + `docs/enterprise.md` +
  `docs/dev_sync.md`, this repo's `docs/repo_split.md` + `docs/enterprise.md` +
  `deploy/k8s/README.md` + `README.md` + `docs-site/docs/concepts/
  enterprise.md`) all point at the in-core surface and call out the
  retirement. Closes task-b7c6c2f1.

## Removed

- **docs-site: removed `versioned_docs/version-2.9/` and
  `versioned_sidebars/version-2.9-sidebars.json`.** The 2.9 docs snapshot
  was never published to an external release (`docs-site/versions.json`
  remains `[]` — no public version cut has ever happened). The
  precursor sweep under task-e8a0ff88 was supposed to catch this but
  the directory either slipped past the original pass or was re-added
  afterward; this task closes the gap. `npm run build` succeeds after
  the deletion with only pre-existing broken-link warnings (documented
  in mem-c4de6900) that are unrelated to version-2.9. Closes
  task-ec3fce1e.

- Removed the legacy OpenAPI sidecars `docs/api/openapi/cordum-rest.yaml`
  and `docs/api/openapi/cordum.swagger.json`. `docs/api/openapi/cordum-api.yaml`
  is now the single canonical OpenAPI 3 spec, `make openapi` is a pure
  Redocly validation pass, and the local/public Swagger UI wrappers now load
  only that canonical spec. Also removed the legacy prefixed MCP transport
  aliases `/api/v1/mcp/{sse,message,status}`; MCP transport is now exposed
  only at `/mcp/{sse,message,status}` while MCP governance REST endpoints
  remain under `/api/v1/mcp/*`. The internal OpenAPI legacy audit
  (`Audit re-verification 2026-04-23`) holds the ground-truth timeline
  (Cordum engineering).
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
  old alias shim. Audit trail in the internal auth-license compat
  audit (Cordum engineering).
- Removed `sdk/client.BuildTLSTransport` — the error-swallowing wrapper
  that logged CA-read failures to stderr and returned `nil`. Use
  [`sdk/client.BuildTLSTransportErr`](../../sdk/client/client.go)
  instead, which returns explicit errors. No external callers existed
  (pre-GA). Migration is a straightforward `(tr, err) := ...` swap —
  see `sdk/client/client_test.go` for the pattern. Audit trail in
  the internal deprecated-symbols audit (Cordum engineering).

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

- `tools/scripts/e2e_test.sh` Phase 4 no longer fails on TLS compose
  stacks because `examples/hello-worker-go` started with plaintext bus
  URLs and `hello-pack` was missing from the topic registry
  (`task-73bc2227`). The script now auto-detects `./certs/ca/ca.crt`,
  uses `tls://` / `rediss://`, passes `NATS_TLS_CA` / `NATS_TLS_CERT` /
  `NATS_TLS_KEY` plus `REDIS_TLS_CA` / `REDIS_TLS_CERT` / `REDIS_TLS_KEY`
  to the worker using the same env-var names as core services, and installs
  the new `examples/hello-worker-go/pack/pack.yaml` before job submit so
  `job.hello-pack.echo` is registered. Missing Phase 4 readiness/completion
  is a hard failure, and the readiness probe handles the canonical
  `/api/v1/workers` `items` response shape. Gateway `unknown_topic` errors now
  include tenant-filtered `registered_topics` (capped at 20) and
  `topics_endpoint` for faster diagnosis.

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
- **Approvals — worker-originated approval gates no longer
  auto-invalidated by `policy.attachment_id`
  (`core/protocol/reqhash/reqhash.go`):** the shared canonicaliser
  `reqhash.Canonical` now also strips the platform-injected
  `policy.attachment_id` label (alongside `approval_*`,
  `bus.LabelBusMsgID`, and `config.EffectiveConfigEnvVar`). That label
  is attached during policy attachment *after* the submit-time JobHash
  is pinned, so the reconciler's recomputed `RequestHash` used to
  diverge from the pinned hash and wrongly classify a still-pending
  worker-originated approval (e.g. a workflow-step approval gate) as
  `invalidate_stale_request` → `DENIED` about one reconciler tick after
  creation — before a human could approve. The TOCTOU fence is
  unchanged: a genuine request-payload change still trips
  `invalidate_stale_request`. The temporary 15-minute stopgap
  (`config/timeouts.yaml` `reconciler.scan_interval_seconds` raised to
  `900`, which also delayed dispatch/running timeout enforcement) is
  removed — restored to the `30`s default. See `task-821a6ebb`.
- **Approvals — canonical hash parity
  (`core/infra/store/job_store.go`):** the store-side
  `hashApprovalJobRequest` now protojson-roundtrips the cloned
  `JobRequest` (via `protojson.MarshalOptions{EmitUnpopulated: true}`
  → `protojson.UnmarshalOptions{DiscardUnknown: true}`) after
  stripping `approval_*` labels, `bus.LabelBusMsgID`, and
  `config.EffectiveConfigEnvVar`, matching
  `core/controlplane/scheduler/HashJobRequest` byte-for-byte. Closes
  a latent hash-divergence risk where an in-memory `JobRequest` proto
  carrying forward-compat unknown fields (e.g. from a newer SDK)
  would hash differently on the store side than the scheduler side,
  tripping the reconciler's `invalidate_stale_request` classifier on
  a benign approval. No production behavior change in the common
  path (`SetJobRequest` already persists via protojson, which drops
  unknowns before the reconciler reads the request), but the store's
  canonical hash is now identical to the scheduler's canonical hash
  on every logical input — not just on the Redis-read subset of
  inputs. Follow-up task filed for canonical-hash unification into a
  single shared package + atomic `SetJobRequest`-and-hash write. See
  `task-fa783d7a`.
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
