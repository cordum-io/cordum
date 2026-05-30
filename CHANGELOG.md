# Changelog

All notable changes to this project will be documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added

#### core/mcp - destructive taint-lookup fail-closed flag (2026-05-31, task-098b8d02)

- Added default-OFF `CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE`: when enabled, a destructive MCP call whose session-taint lookup errors is held with `REQUIRE_HUMAN` (`taint_lookup_unavailable_failclosed`) instead of proceeding as clean. Read-side taint persist remains fail-open, tainted destructive calls still hard-DENY with `session_tainted_prompt_injection`, and flag-OFF preserves the existing fail-open behavior for compatibility.

#### core/edge/shadow/ci — GitLab CI / Jenkins / Buildkite / CircleCI shadow detectors (2026-05-18, task-5d8c904c, EDGE-143.3)

- `core/edge/shadow/ci/` (new package) — observe-only shadow detectors for the four additional CI providers covered by design doc §8.2–§8.5. Files: `provider.go` (shared types: `Provider` enum aliasing `shadow.CIProvider*` constants, `Run` snapshot, `OIDCClaims`/`OIDCConfig`/`OIDCClaimsProvider`/`OIDCTokenProvider`, `TenantResolver` interface, `ProviderScanner` interface, `Observer` interface + `nopObserver`, `Config`, bounded scan constants `MaxRunsPerScan=30`/`MaxBuildsPerScan=30`/`MaxResponseBodyBytes=2 MiB`/`DefaultHTTPTimeout=15s`); `detector.go` (`Detector` orchestrator + `EmitRun` shared pipeline calling exactly one `shadow.Store.CreateFinding` per run with `SourceType=ci`, `CIProvider` set to the exact provider enum, §6.3/§6.4 resolved tenant/principal, §14 false-positive controls, `SignalSet`/`Confidence`/`FirstSeen`/`LastSeen`/`RetentionClass=ShadowRetentionDefault`); `oidc.go` (generic coreos/go-oidc/v3 verifier + `LoadOIDCConfigFromEnv` implementing Q6 binding ruling — GitLab.com SaaS default `https://gitlab.com`, self-hosted GitLab + Jenkins/Buildkite/CircleCI operator-only, literal `disabled` forces tier-2 fallback); `mapping.go` (`DefaultResolver` with §6.3 OIDC→org/repo→quarantine and §6.4 oidc_subject→workflow_actor→unknown precedence); `redaction.go` (`RedactCIPath` per-provider scheme prefixes + URL-query strip + `shadow.RedactPath`; `SanitizeEvidenceText` with `secretLikePatterns` including GitLab `glpat-` PAT shape; `SanitizeEnvKeys` 64-byte cap + dedup; `capEvidenceSummary` runs `shadow.StripSecretMarkers` as defense-in-depth before bounding to `shadow.MaxEvidenceSummaryBytes`); `observer.go` (`PrometheusObserver` with bounded `source_type` label clamped to `gitlab_ci|jenkins|buildkite|circleci|other`); `http.go` (shared minimal read-only HTTP client with bearer/token-header/basic-auth injection, `io.LimitReader` body cap, `redactURLForError` strips scheme://host + query from wrapped errors); `gitlab.go` / `jenkins.go` / `buildkite.go` / `circleci.go` (per-provider scanners over native read API surfaces). No new external dependencies — the four scanners share one minimal HTTP client rather than vendoring `xanzy/go-gitlab` (archived March 2025) or `go-buildkite/v3`. 23 focused tests under `core/edge/shadow/ci/` covering OIDC fallback per provider (8), tenant/principal precedence (6), per-provider end-to-end emit (4), observer bounded labels (2), redaction + env-key dedup (5), cross-provider observe-only-via-HTTP-method assertion (1). Q6 binding governor ruling lives on `comment-a17f4f1c` on `task-de50a293`.

#### core/mcp — Cross-process MCP retry-dedupe via Redis SETNX (2026-05-18, task-4e5d34d2)

- `core/mcp/dedupe_store.go` (new) introduces the `DedupeStore` interface (`LoadOrStore`/`Store`/`Delete`) plus `NewInProcessDedupeStore()` (sync.Map-backed default) and public constants `MCPDedupeKeyPrefix = "mcp:dedupe:"` + `MCPDedupeTTL = 10m`. `core/mcp/dedupe_redis.go` (new) adds `RedisDedupeStore` (SET NX EX MCPDedupeTTL winner-selection, GET decode on NX-miss, in-process fail-soft on any Redis error per taskRail #3) and `SelectDedupeStore(hint, client)` honoring `CORDUM_MCP_DEDUPE_BACKEND={redis,memory}` (defaults to Redis when client wired, in-process otherwise; unknown values fall back to in-process without panic). `ToolCallDeps.DedupeState` type changed from `*sync.Map` to `DedupeStore`; `dedupeBegin`/`dedupeFinish` now handle both backends with a 50ms poll + MCPDedupeTTL deadline-breaker on the Redis loser path. `core/controlplane/gateway/mcp_policy_wire.go` passes `s.jobStore.Client()` into `BuildMCPPolicyDeps` so multi-instance HA gateway deployments collapse identical retries across instances; today's single-instance gateway keeps in-process semantics. Test coverage: `core/mcp/dedupe_redis_test.go` (6 tests including TestRedisDedupeStore_CrossProcessCollapses), `core/mcp/policy_evaluate_dedupe_inprocess_test.go` (4 tests including error-delete + ctx-cancel waiter contracts), `core/controlplane/gateway/mcp_policy_wire_dedupe_backend_test.go` (5 backend-selection tests).

### Fixed

#### sdk/runtime — heartbeat & handshake BusPacket `trace_id` fix (2026-05-31, task-6e9c230d)

- The SDK worker-runtime heartbeat and handshake publishers emitted `BusPacket`s with an empty `trace_id`. The live path (`runtime.HeartbeatPayload`/`WithMemory`/`WithProgress`, used by every CAP worker) delegates to CAP's `capworker.HeartbeatPayloadWithProgress`, which hardcodes `TraceId:""`; the cordum wrapper's old `applyHeartbeatOptions` left it untouched (and returned the raw payload unchanged on the no-option path). CAP v2.x `ValidateBusPacket` rejects an empty `trace_id`, so the scheduler/gateway bus consumer dropped **every** `sys.heartbeat` (`bus: packet invalid … missing required field(s): trace_id`) → degraded/empty worker-capacity TTL registry and broken pool scheduling. The SDK now stamps `trace_id = workerID` (the natural correlation id, equal to `SenderId`): `sdk/runtime/bus_helpers.go` replaces `applyHeartbeatOptions` with `finalizeHeartbeatPayload`, which always re-stamps `trace_id` (and `created_at` when nil, to satisfy CAP's stricter SDK validator) before applying options; the inline `Worker` heartbeat/handshake builders in `sdk/runtime/worker.go` set `TraceId` in the struct literal *before* signing so it is inside the signed bytes. Two demo workers that build heartbeats inline (`demo/quickstart/worker`, `demo/mock-bank/worker`) were swept for the same omission. `sys.heartbeat` is a non-durable core-NATS subject (no JetStream backlog), so rejects stop the moment each emitter runs the fixed SDK. Regression tests `TestHeartbeatPayload_SetsTraceID`, `TestWorker_HeartbeatPacket_SetsTraceID`, `TestWorker_HandshakePacket_SetsTraceID` assert the published packets pass CAP `ValidateBusPacket`. The upstream CAP root cause (`HeartbeatPayloadWithProgress` hardcoding `TraceId:""`) is tracked separately in task-672ad19a. Sibling fix: the `sys.audit.export` `trace_id` fix in task-15f8c51f.

#### core/mcp — content session-taint scan-coverage hardening (2026-05-31, task-a3871d1b)

- The read-result prompt-injection scan (`scanResultForSessionTaint`) previously concatenated all `Content[].Text` plus `StructuredContent` and truncated at one global 256 KiB cap, so an injection in a field *after* earlier large content — or in `StructuredContent` once `Content` filled the cap, or split across the cap boundary — was never scanned and the session was never tainted, letting a subsequent destructive call through. Replaced `assembleResultScanContent` with `scanResultUnits`: each `Content[].Text` and each `StructuredContent` string leaf is scanned as its own bounded window, oversized fields are split with overlap (so a phrase straddling a boundary is still caught), and scanning stops at the first finding or a total-bytes/window-count DoS budget. Enforcement stays content/shape-aware (no tool-name glob); the clean-session negative control (no taint → delete allowed) is preserved. New `taint_policy_test.go` cases cover boundary, structured-content, overlap, negative-control, and DoS-bounded coverage. The deliberate fail-open taint posture is unchanged; a destructive-path fail-closed recommendation is captured separately (`Monday-demo/artifacts/taint-fail-open-decision.md`) pending governor sign-off.

#### core/audit — SIEM audit export `trace_id` fix (2026-05-30, task-15f8c51f)

- `NATSAuditPublisher.Send` now stamps a non-empty `TraceId` on the `sys.audit.export` BusPacket (correlation-first: `Extra["trace_id"]` → `EventHash` → generated UUID). CAP `ValidateBusPacket` rejects an empty/whitespace `trace_id`, so before this fix the bus NAK-looped **every** audit-export event and the `NATSAuditConsumer` — and therefore all four SIEM sinks (webhook/syslog/Datadog/CloudWatch) — was never reached. Confined to `core/audit/nats.go`; regression test `TestNATSAuditPublisher_SetsTraceID` asserts the precedence and that the published packet passes CAP validation.

#### Cross-cutting Go hygiene fail-closed sweep (2026-05-20, task-0c2d2ffe)

- Binary integrity request-size handling now uses typed `errors.As(*http.MaxBytesError)` detection; Redis MCP upstream registry readiness no longer mutates the clock function during concurrent access; cordumctl atomic settings/config writers fsync temp files before close+rename; `AdvanceFloor` removes temp files on every non-success path; file log ingestion closes the active file on context cancellation; MCP policy dedupe publishes panic errors before re-panicking so duplicate callers fail closed; MCP gateway health now returns 401 when tenant resolution fails.
- Audit webhook export now fails closed when `CORDUM_AUDIT_EXPORT_WEBHOOK_URL` is set without a 32+ character `CORDUM_AUDIT_EXPORT_WEBHOOK_SECRET`, matching the existing short-secret rejection and surfacing unsigned-webhook misconfiguration at startup.

#### core/edge/runtimeingest — Runtime replay correctness sweep (2026-05-20, task-bdd3e81d)

- Runtime ingest replay keys now use `sha256(tenant_id):sha256(collector_id)` under `edge:rt:nonce:*`, preventing delimiter collisions between tenant/collector tuples that contain `:`. **Operator migration:** this key format is wire-incompatible with existing replay-window entries; wait one `ReplayWindowTTL` (1 hour) after deploy for old keys to age out, or flush matching `edge:rt:nonce:*` keys during rollout.
- Runtime ingest append-failure cleanup releases nonce reservations with a detached 5s context so client disconnects/timeouts do not leave orphan replay entries. Parent session/execution validation now caches lookups per request, reducing same-parent 256-event batches to one session lookup and one execution lookup before append.
- Runtime replay opt-out parsing now rejects unrecognized `CORDUM_EDGE_RUNTIME_REPLAY_REQUIRED` values instead of treating typos as the default, and runtime ingestion docs now match the adapter's `RedactionHashNone` behavior plus `invalid_request` raw-field error code.

#### core/controlplane/gateway + core/edge — Approval governance fail-closed audit fixes (2026-05-20, task-433b0830)

- Wired the governance evaluator into nested delegation issuance so scope escalation fires on real handler requests and records the firing governance rule in delegation SIEM audit extras.
- Approval self-approval guards now persist/check composite requester identities (API-key hash + principal) for MCP and Edge approvals; submitter API-key hashes now use 8 sha256 bytes instead of 4.
- Approval reason/note free text is redacted and size-capped before job-label persistence and audit emission; approve/reject audit entries now carry `reason` plus redacted note metadata.
- Edge approval lifecycle cleanup now skips expired tuple approvals, sweeps pending expirations periodically with metrics, removes rejected/expired approvals from action-hash indexes, bounds approval-analytics cache entries, and returns typed claim conflicts for terminal approval states.
- MCP approval handlers no longer echo raw internal errors to clients; they log full details server-side and return generic stable error codes, including `approval_already_resolved` for terminal-state conflicts.

#### core/controlplane — Fail-closed output policy + scheduler retry caps on Redis read errors (2026-05-20, task-8b4077ba)

- Output policy now fails closed when a result-pointer Redis `Get` returns a non-`redis.Nil` error: it emits a WARN with the pointer key and quarantines the output with a `pointer_unreadable` finding instead of scanning empty bytes and allowing by default.
- Scheduler dispatch now preserves retry caps when `JobStore.GetAttempts` is unreadable: max-scheduling and policy `max_retries` gates log WARN context and pessimistically treat the unknown attempt count as the cap, preventing poison-pill redis-timeout retry loops.

#### core/{controlplane/gateway/auth,edge} — Audit-fix: SSRF hardening (MCP non-strict default + OIDC DNS-rebinding + JWT issuer fallback) (2026-05-20, task-18f139d2)

- **MAJOR — `core/controlplane/gateway/handlers_mcp_upstreams_helpers.go` `mcpUpstreamPolicyInputs`** — when an operator declares a tenant allowlist (`safety.mcp.allowed_upstreams`) but does not explicitly set `safety.mcp.policy_mode`, the helper now returns `enterprise-strict` instead of empty. Pre-fix the validator's strict gates (allowlist enforcement, HTTPS-only, DNS-fail-closed) stayed dormant despite the operator's clear intent, so plain HTTP upstreams and hostnames that fail DNS at registration but rebind to internal IPs later passed through. Plain HTTP is now rejected by default in every mode and can only be restored through trusted tenant config `safety.mcp.allow_plain_http=true`; query/body downgrades are rejected. Regression: `TestMCPUpstreamPolicyInputs_DefaultsToStrictWhenAllowlistExists`, `TestRegisterMCPUpstream_HTTPRejectedWhenAllowlistConfigured`, `TestRegisterMCPUpstream_HTTPAllowedWithTenantOptIn`.
- **MAJOR — `core/edge/mcp_upstream_registry.go` + `core/edge/mcp_upstream_validation.go` IP pinning** — `UpstreamServer` gains a server-managed `resolved_ips []string` field; `ValidateMCPUpstream` records the registration-time DNS resolution onto the record (sorted + deduped). New `RevalidateMCPUpstreamAtUse(ctx, upstream)` re-resolves the hostname at dial time and rejects when the current IP set differs from the pinned registration-time IP set, lands in the SSRF deny-set (loopback, RFC1918, link-local incl. 169.254.169.254, IPv6 ULA, multicast, unspecified), or matches a cloud-metadata host. The DNS-rebinding window between registration and use is closed: hostnames that resolved public at register-time but later swap to a different public IP or IMDS are refused before the dial. `core/mcp` gateway upstream handling now invokes this use-time guard before selecting an enabled upstream. New package var `edge.MCPHostLookup` allows tests to inject deterministic resolver responses. Regression: `TestValidateMCPUpstream_PinsResolvedIPs`, `TestRevalidateMCPUpstreamAtUse_RejectsRebindToInternalIP`, `TestRevalidateMCPUpstreamAtUse_RejectsDifferentPublicIP`, `TestMCPGatewayUpstreamPathRevalidatesPinnedIPs`.
- **MAJOR — `core/controlplane/gateway/auth/oidc.go` `validateOIDCURL` + `newOIDCHTTPClient`** — `ensurePublicHost` now runs in ALL environments unless `CORDUM_OIDC_ALLOW_PRIVATE=true` (dev/test escape hatch); pre-fix the check was gated on `env.IsProduction()`, so dev/staging deployments accepted an OIDC issuer URL that pointed at `metadata.google.internal` / 169.254.169.254 and leaked discovery + JWKS to the cloud metadata service. New `newOIDCHTTPClient()` installs a `net.Dialer.Control` hook that inspects the resolved IP at connect time and refuses dials to private/loopback/link-local addresses, providing DNS-rebinding defense in depth for both the initial discovery and every subsequent `refreshJWKS` call (the discovery-time URI is reused verbatim, so an attacker controlling DNS could rebind between fetches without this guard). Regression: `TestOIDC_RejectsMetadataHostInAllEnvs`, `TestOIDC_AllowsPrivateHostWhenOptedIn`, `TestNewOIDCHTTPClient_GuardsPrivateIPsAtConnect`, `TestNewOIDCHTTPClient_AllowsPrivateWhenOptedIn`.
- **MAJOR — `core/controlplane/gateway/auth/jwt.go` `newJWTValidatorFromEnv`** — when `env.IsProduction()` is true and `CORDUM_JWT_ISSUER` is unset, the constructor now returns `error("jwt: CORDUM_JWT_ISSUER required in production")` (refuse-to-start at gateway init). Pre-fix it silently defaulted to the well-known string `"cordum"` (or `"cordum-dev"` in non-prod), so an attacker who learned the platform default could forge an `iss` claim and pair it with `allow_cross_tenant:true` against any HS256 secret they obtained. Mirrors the existing audience guard at `validateClaims`. Non-prod continues to fall back to `"cordum-dev"` so local development requires no extra env-var. Regression: `TestJWTValidator_RefusesToStartInProdWithoutIssuer`, `TestJWTValidator_ProdModeViaCordumEnv`, `TestJWTValidator_NonProdStillDefaultsIssuer`.

**Operator migration — refuse-to-start (#4) is operational impact:**
- Production deployments (`CORDUM_PRODUCTION=true` or `CORDUM_ENV=production`) MUST set `CORDUM_JWT_ISSUER` explicitly before the next gateway restart. Self-hosted demos that previously relied on the `"cordum"` default will refuse to start otherwise — set the variable to the same string (`CORDUM_JWT_ISSUER=cordum`) to preserve existing tokens, or rotate to a new value if no live signed tokens depend on the old issuer.
- Dev/test deployments using a local mock OIDC IdP (httptest, docker-compose) must opt into the loopback/private escape hatch with `CORDUM_OIDC_ALLOW_PRIVATE=true`; the in-tree test fixtures (`newMockOIDCServer`, `newMockOIDCFlowServer`, `newMockOIDCServerWithCounter`) already set this automatically for unit tests.
- Tenants that intentionally use cleartext MCP upstreams must set `safety.mcp.allow_plain_http=true`; otherwise registration rejects `http://` endpoints even outside enterprise-strict mode. This is a tenant config setting only, never a request parameter.
- `MCPUpstreamServer.resolved_ips` is a server-managed read-only field; clients MUST NOT submit values on write.

Test additions: `core/edge/mcp_upstream_ssrf_test.go`, `core/controlplane/gateway/handlers_mcp_upstreams_ssrf_test.go`, `core/controlplane/gateway/auth/jwt_ssrf_test.go`, `core/controlplane/gateway/auth/oidc_ssrf_test.go`. `go test -count=3 ./core/edge/ ./core/controlplane/gateway/auth/ ./core/controlplane/gateway/` PASS on targeted runs (edge 0.5s, auth 10.8s, gateway 1.5s). `go vet` + `gofmt -l` clean.

#### core/mcp — Audit-fix: MCP gateway policy bypass + dedupe cache + upstream validation (1 CRITICAL + 5 HIGH) (2026-05-20, task-72958802)

- **CRITICAL — `core/mcp/policy_evaluate.go` semanticDedupeKeyForCall** — folds `sha256(canonicalJSON(params.Arguments))` into the action hash via new `actionTupleHashWithArgs` + `canonicalArgumentsHash` helpers. Pre-fix, tools whose args carried no path-like key (cordum_audit_query?q=, cordum_query_policy?bundle=, generic SQL `query=`, any cordum_* tool with non-path args) collapsed every distinct arg set into one dedupe slot for the full MCPDedupeTTL — call #2 with attacker-chosen args silently served call #1's cached result without re-evaluating policy, calling upstream, or emitting a pre-event audit row. **DEPLOY COORDINATION: Dedupe key change is wire-incompatible with existing cached entries. Operators MUST flush `mcp:dedupe:*` keys before deploying the new binary, otherwise pre-flush call #2s with distinct args may still hit stale post-flush keys at one TTL crossover.**
- **HIGH — `core/mcp/policy_evaluate.go` DENY cache invalidation** — when `EvaluateToolCall` returns DENY, the new `skipDedupeCache` flag routes the defer through `dedupeFinishNoCache` which publishes the outcome to in-process waiters AND deletes the cross-process Redis slot. Pre-fix, DENY decisions wrote a 10-min COMPLETED record so a subsequent policy-bundle update that flipped the tuple to ALLOW returned stale DENY for the full TTL.
- **HIGH — `core/mcp/outbound/nonce.go` per-call ctx** — `NonceStore.SeenAndRecord` interface now accepts `ctx context.Context` as its first parameter. `RedisNonceStore` drops the captured `context.Background()` field and wraps the caller ctx with `NonceStoreCommandTimeout = 500ms` so a wedged Redis surfaces as a bounded error instead of a stuck verifier goroutine (DoS amplifier). `InMemoryNonceStore` accepts ctx for interface uniformity but ignores it (no I/O). `Verifier.VerifyRequest` accepts ctx and propagates it; all internal call sites updated.
- **HIGH — `core/controlplane/gateway/handlers_mcp_verify.go` verifier reload** — package-level `sync.Once` singleton replaced with per-`*server` `atomic.Pointer[mcpVerifierState]` backed by `buildMCPVerifierFromEnv` + new `s.reloadMCPVerifier()` method. Pre-fix the trust store was built once per OS process from `os.Environ()` and could not be reloaded after operator key rotation — the comment claimed "lazy getter per server" but the implementation was a package-global. Now `reloadMCPVerifier` clears the pointer so the next call re-reads env.
- **HIGH — `core/controlplane/gateway/handlers_mcp_upstreams_helpers.go` config-driven policy mode** — `mcpUpstreamPolicyInputs` now reads `safety.mcp.policy_mode` from the configsvc Effective tree via new `extractMCPPolicyMode` helper instead of returning `""` unconditionally. The `PolicyModeEnterpriseStrict` branch in `mcp_upstream_validation.go` (HTTPS-only, allowlist gate, fail-closed-on-DNS) is now reachable from the API surface — per-tenant strict-mode opt-in is wired. Coordinates with the default-strict + plain-HTTP rejection work in task-18f139d2.
- **HIGH — `core/mcp/bridge_readonly.go` getJSON SSRF guard** — applies `validateAndResolveOutboundURL` + `pinnedDialer` (mirroring `doRequest` in bridge.go) so every read-only path (jobs/runs/workflows/packs/topics/workers/agents/approvals/audit-query/audit-verify/status) gets the IP-pin protection that GETs were previously missing on `b.baseURL`. DNS rebinding attacks targeting internal hosts (RFC1918, link-local, IMDS) are now rejected pre-dial.
- **MEDIUM — Code-naming unification** — `edgeErrCodeReplayWindowFull` renamed `REPLAY_WINDOW_FULL → replay_window_full` to match the 24 other `/api/v1/edge/*` codes (lower_snake). `mcp_verifier_unavailable` renamed → new `errorCodeMCPVerifierUnavailable = "MCP_VERIFIER_UNAVAILABLE"` to match the 6 other MCP gateway codes (UPPER_SNAKE). OpenAPI enum + dashboard codegen updated to match.
- **MEDIUM — `core/controlplane/gateway/handlers_mcp_verify.go` body cap + decoder error sanitization** — `mcpVerifySignatureMaxBodyBytes = 64 KiB` applied via `http.MaxBytesReader` before json decode (admin-authenticated CPU pin via repeated ecdsa.VerifyASN1 calls on oversized bodies). Decoder errors no longer leak Go struct shape — server logs detail, client gets generic `"request body is not valid JSON"` 400.
- **MEDIUM — `core/mcp/outbound/signing.go` nonce TTL = 2*clockSkew** — pre-fix `nonceStore.SeenAndRecord(nonce, v.clockSkew)` set TTL equal to the timestamp acceptance window, so a nonce from a packet arriving at the late edge of the clockSkew window had no remaining store coverage when its timestamp twin arrived at the early edge of the next window. Now `2*v.clockSkew` ensures every accepted timestamp has full replay coverage.
- **MEDIUM — `core/controlplane/gateway/handlers_mcp.go` SSE re-auth first tick** — `mcpSSEReauthFirstTick = 1s`; the ticker fires the first re-auth shortly after connect, then `ticker.Reset(mcpSSEReauthInterval)` reverts to the longer 5-min cadence. Pre-fix a revoked API key remained usable on an established SSE for up to 5 minutes. (Confirmed already-applied at HEAD via task-1a2c6cf8 commit 5e79e45d; this entry documents the audit-fix coverage.)

Test additions: `core/mcp/policy_evaluate_audit_dedupe_test.go`, `core/mcp/bridge_readonly_audit_test.go`, `core/mcp/outbound/nonce_audit_test.go`, `core/controlplane/gateway/handlers_mcp_verify_audit_test.go`, `core/controlplane/gateway/handlers_mcp_audit_test.go`, `core/controlplane/gateway/handlers_edge_errors_audit_test.go`. `go test ./core/mcp/... -count=3` PASS (31s). `go test ./core/mcp/outbound/... -count=3` PASS (2.3s). Focused gateway audit-test set at `-count=3` PASS (6.2s).

#### PR #276 code-cleanup bundle — multi-signal dedup cap + sanitizeUpstreamError signature (2026-05-17, task-513e3c1b)

- `core/edge/shadow/finding_store_redis.go` — defence-in-depth cap added at the entry of `listFindingsByMultiSignal` (line 511) so a future internal caller that bypasses the handler-layer `parseShadowFindingListQuery` gate cannot trigger unbounded fan-out across signal indexes. Reuses existing `maxShadowSignalSetEntries=16` const and `ErrValidation` sentinel; mirrors the `CreateFindingExtension` signal-cap pattern at finding_store.go:634. (Cap addition was captured by sibling commit 19e7dab1.)
- `core/edge/shadow/finding_store_redis_test.go` — `TestListFindings_MultiSignalCapEnforcedAtListLayer` pins the redundant store-layer check: submitting 17 signals returns `ErrValidation` with message `"signals exceeds max 16 entries"` and zero accumulated findings. PASS at `-count=3`.
- `core/mcp/policy_evaluate.go` — dropped the unused `ArgumentRedactor` parameter from `sanitizeUpstreamError` (option a from the PR review): function body uses the package-level `upstreamErrorRedactPatterns` regex set, so the param was misleading on the signature. Updated the sole call site at line 835 and refreshed the InvokeToolWithPolicy default-deps comment that referenced the helper. No behavior change.

#### CI — PR #276 Sub-C: CI / release infra cluster (#6, #23, #28) (2026-05-17, task-ba430336)

- `Makefile` — extended the explicit-`bash` invocation pattern (established by the `release-local:` target in commit 1f409017) to the three `soak-ws*` targets at :139 / :143 / :147. They previously bare-invoked `./tools/scripts/ws_soak_test.sh <mode>`, which relies on the script's 0755 mode bit and `#!/bin/bash` shebang resolving — both of which can fail on Windows checkouts (git reports the file 100644) and minimal CI containers (no `/bin/bash`). Now reads `bash tools/scripts/ws_soak_test.sh <mode>`, mirroring `release-local:` at :157. Resolves the sibling-target scope of CodeRabbit PR #276 #28.
- CodeRabbit PR #276 #6 (release.yml step-level `if: secrets.X != ''` antipattern at :255) and #23 (binaries-pr-validation.yml missing `tools/scripts/release-local.sh` paths trigger at :24) were **already resolved upstream** before this task claimed:
  - **#6** → commit 1f409017 (May 16, "EDGE-151 reopen #1") introduced the dedicated `Probe for Tier 1 secrets` step (release.yml:283-295) materialising `GPG_RELEASE_KEY_PRIVATE` via `env:` and writing `has_gpg=true|false` to `$GITHUB_OUTPUT`. Downstream signing gates on `steps.tier1_secrets.outputs.has_gpg`; the per-job sign-darwin/sign-windows pattern at :128-138 / :183-194 uses the same output-based gate. Fail-closed canonical-tag gate lives at `publish` (:344-355): refuses to publish a `cordum-io/cordum` release tag when `release/SHA256SUMS.asc` is absent.
  - **#23** → commit c3623021 (May 16, "review: address 13 CodeRabbit findings on PR #276") added `'tools/scripts/release-local.sh'` to the `pull_request: paths:` filter at binaries-pr-validation.yml:16.
- Cross-workflow audit (`.github/workflows/` = 17 yml files): `grep -P 'if:\s*(\$\{\{)?\s*secrets\.'` returned **0 step-level antipattern hits** at HEAD. The cluster is fully scrubbed.
- Verification: `actionlint .github/workflows/release.yml .github/workflows/binaries-pr-validation.yml` EXIT=0; `python -m yamllint -d relaxed` EXIT=0 (only pre-existing line-length warnings); `python -c "import yaml; yaml.safe_load(open(p)) ..."` parses both files cleanly. `make release-local` not exercised locally (no `make` on the worker's MSYS PATH; exercised by binaries-pr-validation.yml install-tamper job on every PR).

#### core/mcp — PR #276 Sub-B: deterministic retry-dedupe key + production DedupeState wiring (2026-05-17, task-23ccc2d8)

- `core/mcp/policy_evaluate.go` — replaced the `EventIDFactory`-derived `dedupeKey` (was: random per call → dedupe never hit across retries) with semantic `computeSemanticDedupeKey(tenant, server, tool, action_hash, session, execution, principal)` returning hex SHA-256 over the canonical form joined by 0x1F unit-separator (collision-resistant against pipe-bearing tenant / tool identifiers). The new helper `semanticDedupeKeyForCall(ctx, params, server)` resolves CallMetadata + `ActionTupleHash` before the pipeline runs, so two retries of the same logical tool call share a single singleflight slot regardless of EventID drift. Updated `dedupeBegin` / `dedupeFinish` signatures to take the pre-computed key. EventID stays random for tracing. (Resolves CodeRabbit PR #276 #4.)
- `core/controlplane/gateway/mcp_policy_wire.go` — `BuildMCPPolicyDeps` now wires `DedupeState: &sync.Map{}` so production retry-dedupe is enabled (previously the field was `nil` and `dedupeBegin` short-circuited, leaving every retry to emit a fresh pre/post pair).
- `core/mcp/policy_evaluate_dedupe_test.go` (new) — `TestPolicyEvaluate_RetryIdempotent` (5 sequential retries with default random `EventIDFactory` collapse to 1 upstream call + 2 events), `TestPolicyEvaluate_ConcurrentRace` (20 goroutines with shared `sync.Map` and a `gate`-blocked upstream caller collapse to 1 upstream call + 2 events via atomic counter), `TestDedupeKey_SemanticDerivation` (8-row table covering same-inputs / per-field divergence / pipe-smuggling delimiter collision resistance / cross-tenant safety). All pass at `-count=10` with zero flake.
- In-process singleflight (`sync.Map.LoadOrStore` + done channel) is race-safe within a process; the CodeRabbit #5 cross-process concern is scope-reduced per architect authorization and tracked as follow-up Moe task task-4e5d34d2 (Redis SETNX-backed `DedupeStore` for multi-instance HA). Today's gateway is single-instance per deployment unit.
- Doc: `docs/mcp-server.md` § Retry dedupe.

#### CI — EDGE-PR276-CI: Resolved 3 high-severity CodeQL `go/allocation-size-overflow` alerts on PR #276 (2026-05-17, task-8002b1ee)

- `core/edge/shadow/finding_store_redis.go` — extracted `clampListPageSize(int) int` helper that bounds a caller-supplied page size to `[1, MaxListPageSize=200]` (substituting `DefaultListPageSize=50` for non-positive input). Replaced the inline clamp in `ListFindings` with a helper call, and added a defence-in-depth `limit = clampListPageSize(limit)` at the top of `listFindingsByMultiSignal` so the bound is visible inside the function's scope at every `make([]ShadowAgentFinding, 0, limit)` / `make(map[string]struct{}, limit)` site. The functional bound is unchanged (already enforced by the caller); the change surfaces the sanitizer to CodeQL's interprocedural dataflow so the rule's `go/allocation-size-overflow` analysis recognises the cap. Resolves alerts on `finding_store_redis.go:394` (EDGE-141 commit 4b905793, `ListFindings`), `:491` and `:492` (EDGE-143.5 commit f8a96949, `listFindingsByMultiSignal`).
- `core/edge/shadow/finding_store_redis_test.go` — `TestClampListPageSize` (10-row table) pins the bound contract; `TestListFindings_LimitCapAcrossPaths` (3 sub-tests) exercises end-to-end caps on the single-signal path, the multi-signal path, and the zero-limit default fallback with `total = MaxListPageSize + 25` records. All shadow-package tests PASS under `-count=3` (0 regressions vs baseline).

#### CI — EDGE-PR276-CI: Resolved Dashboard Tests workflow failure on PR #276 (2026-05-17, task-ee9e72a1)

- `dashboard/src/api/generated/` — regenerated via `pnpm run generate-api` (orval) to match the OpenAPI spec. EDGE-141 (ShadowAgentFinding store + APIs), EDGE-142 (shadow remediation generator), EDGE-143.5 (ShadowAgentFinding §10.1/§10.2 extensions), EDGE-144 (runtime event ingestion adapter), and EDGE-151 + EDGE-151-FOLLOWUP (binary-integrity events) had added OpenAPI routes/schemas in `docs/api/openapi/cordum-api.yaml` without regenerating the matching TypeScript models. The CI step `pnpm run check-api-codegen` therefore detected DRIFT and exited 1 BEFORE vitest could run on PR #276 run 25995773025 job 76409883974 (commit `edc61060`). After regeneration the check returns `[check-api-codegen] OK — generated tree matches the spec`.
- `dashboard/src/components/timeline/lanes/MCPLane.tsx`, `dashboard/src/pages/AgentsPage.tsx` — removed two stale `// eslint-disable-next-line react-hooks/exhaustive-deps` comments that referenced an unconfigured rule. `eslint-plugin-react-hooks` is not in `dashboard/package.json` and `dashboard/eslint.config.mjs` does not register a `react-hooks` plugin, so ESLint reported the disable comments as `Definition for rule 'react-hooks/exhaustive-deps' was not found` (2 errors) on the `pnpm run lint` step. No behavior change — the comments had no enforcement effect.
- Verification on commit `f66aa9c2` (HEAD of `wip/2026-05-15-orphan-rescue`): `pnpm run check-api-codegen` EXIT=0; `gh pr checks 276` reports `Dashboard Tests: SUCCESS (pass)` on CI run 25998861418. tsc-error-count and vitest-failed-count on tracked HEAD code: 0 / 0 (no regression vs branch-point baseline per DASHBOARD VERIFICATION RAIL).

#### CI — EDGE-PR276-CI: Resolved Lint workflow failure on PR #276 (2026-05-17, task-4dafd1dd)

- **golangci-lint stage** cleared across follow-up commits 3e7c5b41, 0d77e64f, f66aa9c2: `errcheck` on `cmd/cordumctl/mcp_attach.go:21,39,44,61,68` + `cmd/cordumctl/mcp_attach_common.go:70,76,80,85` + `core/controlplane/gateway/handlers_edge_binary_integrity.go` `fmt.Fprint*` / `Body.Close()`, plus `unused` drop of dead `validFindingStatus` map and S1011 `for/append` → `append(slice, slice...)` simplification on `core/edge/shadow/k8s/signals_test.go:309`. All sites the rule named are wrapped with `if err := …; err != nil { … }` or guarded `_ = …` per safe-discard rule.
- **EDGE-071 secret-leak audit stage** (`tools/scripts/lint_no_secret_log.sh` Phase 2) cleared on commit 5273b348: `cordum_fake_` prefix added to 11 real-secret-shape literals across `cmd/cordumctl/shadow_remediate_test.go:246,250`, `core/controlplane/gateway/handlers_edge_shadow_agents_test.go:303`, `core/controlplane/gateway/handlers_edge_shadow_remediation_test.go:139`, `core/edge/shadow/finding_store_redis_test.go:101`, `core/edge/shadow/k8s/signals_test.go:271-275`, `core/edge/shadow/remediation_test.go:210,213,424,466,479`. Redaction-under-test semantics preserved — the regex still matches the `sk-ant-…20-char-suffix` shape inside the prefixed string, so `Contains("sk-ant-")` post-redaction assertions still hold.
- Lint check on CI run 25999261697 / job 76419290299 (commit 5273b348) = SUCCESS in 2m34s. No production code touched; pure test-fixture + errcheck wrapping. Local verification: `golangci-lint run --timeout=5m` (v2.11.4, mirrors CI) → 0 issues; `bash tools/scripts/lint_no_secret_log.sh` (with untracked peer-WIP excluded as CI sees) → OK; `go test ./core/edge/shadow/ ./core/edge/shadow/k8s/ ./core/controlplane/gateway/ ./cmd/cordumctl/ -count=1 -timeout 5m` → all 4 packages PASS (shadow 1.045s, k8s 0.234s, gateway 86.562s, cordumctl 13.020s).

#### Edge — EDGE-142 Shadow remediation generator (2026-05-17, task-4cd8299f)

- `core/edge/shadow/remediation.go` (new) — public contract for the remediation generator: `RemediationActionKind` enum (8 kinds — `attach_mcp_gateway`, `use_cordumctl_edge_claude`, `deploy_managed_settings`, `disable_unmanaged_config`, `route_through_llm_proxy`, `run_edge_doctor`, `investigate_process`, `manual_review`), `RemediationAudience` enum (`dev|enterprise|both`), `RemediationSeverity` enum, `RemediationStep`, `RemediationPlan`, `RemediationAPIRequest`, `GeneratorOptions` with `OmitCommands` semantic (zero = include, true = strip). Shape-agnostic `findingFeatures` projection lets both `ShadowAgentFinding` (EDGE-141) and `Finding` (EDGE-140 scanner) feed the same generator.
- `core/edge/shadow/remediation_generator.go` (new) — pure, side-effect-free classifier + step emitter. Side-effect free: no filesystem, no network, no Redis, no Safety Kernel, no Cordum Jobs. Deterministic step IDs + ordering (byte-stable JSON across repeated calls). Audience-aware resolver (`enterprise` upgrades `use_cordumctl_edge_claude` → `deploy_managed_settings`). Backup-precedes-disable ordering, every destructive step `preview_only=true` + `requires_backup=true` + `destructive=true`. Severity from risk (low/medium/high/critical→high). 16 KiB plan-size cap with manual-review fallback.
- `core/edge/shadow/remediation_generator.go` — redaction routed through the existing `stripSecretMarkers` helper (no second redaction subsystem). `safeProductName` and `sortedSignals` strip secrets AND non-printable runes so a malicious uploader cannot smuggle terminal escapes / `sk-…` tokens via `agent_product` or `signal_set` into the operator-facing summary. All commands use literal placeholders only (`<gateway-url>`, `<tenant-id>`, `<principal-id>`, `<output-dir>`, `<llm-proxy-url>`, `<api-key-helper-command>`, `<unmanaged-config-path>`, `<path-to-managed-settings.json>`, `<finding-id>`) — never live secrets.
- `core/controlplane/gateway/handlers_edge_shadow_remediation.go` (new) + `gateway.go` (+1 route registration line) — `POST /api/v1/edge/shadow-agents/{finding_id}/remediation`. Auth: `PermAuditRead` or `admin` role (same gate as the existing GET handler). Body is optional `{audience, omit_commands}`. Response wraps the plan with `finding_id` + `tenant_id`. Uses existing Edge helpers throughout — `requireEdgePermissionOrRole`, `shadowFindingStoreOrUnavailable`, `edgeTenantFromRequest`, `requireEdgePathParam`, `decodeJSONBody`, `writeEdgeJSONDecodeError`, `writeEdgeError`, `writeShadowFindingStoreError`, `writeEdgeInternalError`. Read-only — no store mutation, no audit emission, no Cordum Job creation.
- `cmd/cordumctl/shadow_remediate.go` (new) + `cmd/cordumctl/shadow.go` (dispatch extended `<scan>` → `<scan|remediate>`) — `cordumctl shadow remediate --finding-file <path|-> [--audience dev|enterprise|both] [--json] [--omit-commands]`. Offline by default; never calls Gateway, never requires API keys. Auto-detects lifecycle vs scanner finding shape via `finding_id`/`agent_product`/`product` discriminators. Stdin via `-` reads up to 64 KiB (`io.LimitReader` cap). Exit 0 success, 2 parse/validation/unsupported flag.
- `docs/api/openapi/cordum-api.yaml` — added the `POST /api/v1/edge/shadow-agents/{finding_id}/remediation` operation + 6 new schemas: `ShadowAgentRemediationRequest`, `ShadowAgentRemediationResponse`, `ShadowRemediationPlan`, `ShadowRemediationStep`, `ShadowRemediationAPIRequest`, `ShadowRemediationActionKind`. `TestOpenAPICoverage` and `TestRouteCoverage*` pass under `-count=1`.
- `docs/edge/shadow-remediation.md` (new) — classification table, audience matrix, placeholder reference, backup/preview/destructive contract, full API and CLI examples, error-code mapping, advisory-only limitations and future-enforcement seam.
- `docs/edge/shadow-agent-findings.md` — cross-linked to the new remediation doc.
- 25 new generator tests (`core/edge/shadow/remediation_test.go`) + 9 Gateway-handler tests (`handlers_edge_shadow_remediation_test.go`) + 11 CLI tests (`cmd/cordumctl/shadow_remediate_test.go`). Coverage: dev/enterprise/both audience parity, classification across signals + evidence types + product paths, nil/empty/unknown fallback, secret-marker stripping in product label + signal label + evidence summary + metadata, terminal-escape stripping, backup-before-disable ordering, deterministic JSON across repeated calls, OmitCommands strips Command + APIRequest.Body, invalid audience defaulting, oversized metadata bounding, scanner shape parity, tenant isolation (cross-tenant returns 404 not 403), missing finding, store unavailable, no-tenant-header rejection, no-secret-leakage in CLI text + JSON output, stdin support, deterministic text across runs, invalid JSON exit-2, missing flag exit-2, unknown flag exit-2.
- No dashboard files changed; the DASHBOARD VERIFICATION RAIL does not apply. No Cordum Jobs created, no Safety Kernel call, no enforcement — task rail #1 "advisory unless enforcement mode is explicitly implemented later" is honoured everywhere.

#### Edge — EDGE-143.6 Operator exception API + Q8 step-up auth (2026-05-17, task-cb1f5f2f)

- `core/edge/shadow/exception.go` (new) — operator-defined exception model implementing design doc §10.3. `Exception` struct (ExceptionID with `shadow_exc_` prefix, TenantID, CreatedBy, CreatedAt, ExpiresAt, Reason ≤512 bytes, ScopeSourceType/ScopeSourceID/ScopeSignalSet/ScopeRiskLevel, Status `active|revoked|expired`, StepUpFactor, RevokedBy/RevokedAt/RevocationReason). `CreateExceptionRequest`, `ListExceptionsQuery`, `ExceptionPage`, `RevokeExceptionRequest`. New constants: `FindingStatusManagedSkip="managed_skip"`, `FalsePositiveReasonOperatorException="operator_exception"`, `StepUpFactorMFARecent|SignedAdminToken|None`. `normalizeAndValidateException` enforces tenant + created_by required, expires_at must be future AND ≤90 days (§10.3 max), reason ≤512 bytes, source_type enum-gated, signal_set ≤16 entries each matching `[a-z0-9_]{1,32}` deduped, risk enum-gated. `matchesFinding` predicate: same tenant AND source_type match AND (source_id empty OR equal) AND risk match AND (signal_set empty OR any-of overlap); expired/revoked exceptions never match.
- `core/edge/shadow/exception_redis.go` (new) — `RedisStore` implementations: `CreateException` (per-tenant cap of 1000 enforced before write; pipelined SET + tenant-ZADD with best-effort rollback on failure), `GetException` (cross-tenant probe → `ErrNotFound`; lazy `active→expired` transition on read), `ListExceptions` (ZRevRangeByScore on tenant index + MGet + in-memory scope filter, cursor-paginated via creation-score), `RevokeException` (active→revoked terminal; idempotent same-revoker, `ErrTerminalConflict` on different-revoker double-revoke), `MatchActiveExceptions` (bounded tenant-scan via `ZRevRange[0,1000]` + scope predicate), `recordExceptionMembership` (best-effort per-exception finding-membership ZSET). New keyspace: `edge:shadow:exception:<id>`, `edge:shadow:exception:tenant:<tenant_id>`, `edge:shadow:index:exception:<id>` (latter from §10.5 spec).
- `core/edge/shadow/finding_store.go` — extended `shadow.Store` interface with 5 new methods (CreateException / GetException / ListExceptions / RevokeException / MatchActiveExceptions).
- `core/edge/shadow/finding_store_redis.go` — extended `CreateFinding` with emit-time suppression: after validation, if no caller-supplied ExceptionID, scans active exceptions and stamps the first scope match's `ExceptionID`, sets `FalsePositiveReason="operator_exception"`, and flips `Status=managed_skip` BEFORE the index pipeline runs. Existing `IncludeManagedSkip=false` filter at finding_store_redis.go:932 already hides such records from default `ListFindings`; `?include_managed_skip=true` returns them. After pipeline success, best-effort `recordExceptionMembership` adds the finding to the exception's per-exception index.
- `core/controlplane/gateway/handlers_edge_shadow_exception_handlers.go` (new, ~400 lines) — 4 handlers: `handleCreateShadowException` (POST `/api/v1/edge/shadow/exception`), `handleGetShadowException` (GET `/api/v1/edge/shadow/exception/{exception_id}`), `handleRevokeShadowException` (DELETE same path; reads exception FIRST to mirror create-time step-up gate per Q8), `handleListShadowExceptions` (GET `/api/v1/edge/shadow/exceptions`). Baseline auth `requireEdgePermissionOrRole(auth.PermAuditExport, "admin", "user")`. Step-up gate `requireExceptionStepUp(w, r, required)` returns `(StepUpFactor, ok)`: admin role → `signed_admin_token`; explicit `PermShadowExceptionHighRisk` under entitled RBAC → `mfa_recent`; gate-failure writes 403 with code `step_up_required` and `details.required = "mfa_recent|signed_admin_token"`. CreatedBy + StepUpFactor are stamped from auth, NOT trusted from wire body. Tenant from `X-Tenant-ID` header.
- `core/controlplane/gateway/handlers_edge_shadow_agents.go` — `handleCreateShadowAgentFinding`: after CreateFinding returns a finding with `ExceptionID != ""`, calls `emitShadowExceptionAppliedAudit(r, principal, finding)` which loads the exception and emits `shadow_agent.exception_applied` carrying the authoritative `step_up_factor` recorded at the exception's create time (so SIEM rules pivot on auth-tier-at-time-of-action without joining live RBAC state).
- `core/controlplane/gateway/gateway.go` — 4 new routes registered after the existing `/shadow-agents/.../remediation` route.
- `core/controlplane/gateway/auth/rbac.go` — new constant `PermShadowExceptionHighRisk = "shadow.exception.high_risk"` analogous to `PermDelegationImpersonate`. Added to `AllPermissions` together with `PermDelegationImpersonate` (which was previously omitted from the canonical list).
- `core/controlplane/gateway/handlers_edge_errors.go` — new error code `edgeErrCodeStepUpRequired = "step_up_required"` with usage doc.
- `core/audit/exporter.go` — added 3 event constants `EventShadowAgentExceptionCreated`, `EventShadowAgentExceptionRevoked`, `EventShadowAgentExceptionApplied`. `core/audit/soc2.go` — matching SOC2 mappings (CC7.2 + CC8.1 for create/revoke; CC7.2 for applied).
- `docs/api/openapi/cordum-api.yaml` — 4 new paths (createShadowException / getShadowException / revokeShadowException / listShadowExceptions) + 4 new schemas (`ShadowException`, `CreateShadowExceptionRequest`, `RevokeShadowExceptionRequest`, `ListShadowExceptionsResponse`); reused existing `TenantID` parameter and `EdgeBadRequest|Unauthorized|Forbidden|NotFound|Conflict|ServiceUnavailable|InternalServerError` response refs + `EdgeError` schema. NO parallel doc surfaces.
- `docs/edge/shadow-scanner.md` — added §9.4 documenting the 4 endpoints, baseline + step-up auth pattern, scope predicate, 90-day expires_at cap, emit-time suppression mechanics, 3 audit events with their Extra payloads, the 3-value `StepUpFactor` enum mapping, and the per-tenant cap of 1000.
- 10 new store tests (`core/edge/shadow/exception_test.go`) + 11 new handler tests (`core/controlplane/gateway/handlers_edge_shadow_exception_handlers_test.go`). All PASS under `-count=3` (store 2.817s, handler 2.171s). Broader regression: shadow ok, audit ok, auth ok 91.569s, gateway default-tag ok 83.965s, gateway integration-tag ok 85.038s. Zero new failures vs branch-point baseline.
- Q8 binding governor ruling on task-de50a293 (`comment-a17f4f1c`) enforced: risk=high CREATE requires step-up (admin role OR PermShadowExceptionHighRisk); risk=high REVOKE uses same auth tier as the original create; failure returns 403 with code `step_up_required`. step_up_factor recorded on every audit event (incl. `"none"` for medium/low) so SIEM can pivot on auth-tier-at-time-of-action. NO parallel auth subsystem — reuses the existing `requirePermissionOrRole` pattern analogous to `auth.PermDelegationImpersonate`.

#### Edge — EDGE-143.7 Shadow remediation cluster + CI scope templates (2026-05-17, task-8ab4001f)

- `core/edge/shadow/k8s_templates.go` (new) — 4 step-builder functions extending the EDGE-142 remediation generator with the §12.1 Kubernetes scope templates: `buildTenantLabelMissingSteps` (`namespace_untenanted` signal → `kubectl … label namespace … cordum.io/tenant-id=<id> --overwrite` patch + indicator-pod re-annotation), `buildUnmanagedWorkloadSteps` (`unmanaged_workload` → Option A WorkloadAllowlist append OR Option B `cordum-agentd` sidecar strategic-merge patch, marked `requires_backup=true`), `buildRebaseAgentImageSteps` (`untrusted_agent_image` → `kubectl set image` to `<cordum-allowlisted-registry>` after `jsonpath` confirmation), `buildExtendEgressPolicySteps` (`egress_bypass` → NetworkPolicy YAML patch with `kubectl diff` preview + `kubectl apply` adding `<llm-proxy-cidr>` + `cordum.io/llm-proxy` namespaceSelector to `egress.to[]`). Image string extracted from `evidence_summary` `image=…` marker or `metadata.container_image` via `extractImageFromFeatures`.
- `core/edge/shadow/ci_templates.go` (new) — 3 step-builder functions for the §12.1 CI scope templates: `buildAddCordumEdgeAttachSteps` (`missing_cordum_attach` → per-provider workflow snippet via `cordumEdgeAttachSnippet`; covers `github_actions` with `cordum/cordum-edge-attach@v1`, `gitlab_ci` with include block, `jenkins` groovy stage with `withCredentials`, `buildkite` with `cordum/cordum-edge-attach#v1` plugin, `circleci` with `cordum/cordum-edge-attach@1.0` orb, plus generic shell fallback for `other`), `buildConfigureOIDCTrustSteps` (`unmanaged_oidc` → per-provider `CORDUM_EDGE_SHADOW_OIDC_TRUST_<short>` + `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_<short>` env block per Q6 with well-known issuer URLs pre-filled: `token.actions.githubusercontent.com`, `gitlab.com`, `agent.buildkite.com`, `oidc.circleci.com/org/<org-id>`, plus `<jenkins-oidc-issuer-url>` placeholder), `buildRouteCISDKThroughProxySteps` (`direct_provider_endpoint`+`source_type=ci` → Option A per-provider env block setting `ANTHROPIC_BASE_URL`/`OPENAI_BASE_URL` to `<llm-proxy-url>` OR Option B `POST /api/v1/edge/shadow/exception` template referencing EDGE-143.6 with mandatory `<rfc3339-timestamp>` `expires_at`; enterprise audience adds a third step recommending central CI-template integration).
- `core/edge/shadow/remediation.go` — added 7 new `RemediationActionKind` constants (`apply_tenant_label`, `adopt_unmanaged_workload`, `rebase_agent_image`, `extend_egress_policy`, `add_cordum_edge_attach`, `configure_oidc_trust`, `route_ci_sdk_through_proxy`) and extended the shape-agnostic `findingFeatures` projection with 7 §10.1 fields (`clusterID`, `namespace`, `workloadKind`, `workloadName`, `podUID`, `repo`, `workflowID`) plus matching `normalizeShadowAgentFinding` field copies.
- `core/edge/shadow/remediation_generator.go` — `classify()` extended to dispatch the 4 K8s + 3 CI scope signals BEFORE the EDGE-142 generic signals so a K8s or CI finding can never decay to the local-wrapper path; `direct_provider_endpoint` requires `source_type=ci` to disambiguate from the local-scope `direct_provider_url` signal. `buildSteps()` switch extended with 7 new cases. `remediationSummary`/`riskExplanation`/`recommendedAction`/`safetyNotes` extended with K8s + CI scope wording — Q5 enforce-scope-out reminders ("Cordum NEVER mutates Kubernetes cluster state", "Cordum NEVER mutates CI provider repos, workflows, or trust roots") added to the safety notes for the 7 new kinds.
- `core/edge/shadow/testdata/` (new directory) — 15 golden files covering all 7 templates (4 K8s plus 1 CI direct-provider-SDK plus 5+5 per-provider sub-variants for missing-Cordum-attach and unmanaged-OIDC). Regenerate intentional changes with `go test ./core/edge/shadow/ -run 'TestK8sTemplate_|TestCITemplate_' -update`.
- `docs/edge/shadow-remediation.md` — classification table extended with the 7 new rows; added a §"EDGE-143.7 — Kubernetes + CI scope templates (design doc §12.1)" section documenting per-template trigger, inputs (§10.1 fields), output shape, plus the Q5 enforce-scope-out structural guarantee — all 7 builder signatures listed verbatim so a future reviewer can see at a glance that no Kubernetes / GitHub / GitLab / Jenkins / Buildkite / CircleCI client handle ever reaches a template function.
- 19 new test cases (`core/edge/shadow/k8s_templates_test.go` 8 cases + `ci_templates_test.go` 11 cases) all PASS under `-count=3`. Coverage: per-template action-kind assertion, command-substring assertions (kubectl flag set, YAML patch shape, env-var name), per-provider snippet tags for CI templates, severity propagation (egress-bypass → high, direct-provider-SDK → high), `TestK8sTemplate_NoMutation` and `TestCITemplate_NoMutation` enforce that no template emits a Cordum-side mutating API request — only EDGE-143.6's `POST /api/v1/edge/shadow/exception` is whitelisted as the explicit operator-acked alternative.
- Templates output operator-actionable text ONLY — NO Cordum-side cluster or CI mutation. Q5 enforce-scope-out (binding governor ruling `comment-a17f4f1c` on parent task-de50a293) is structurally guaranteed: every builder signature is `(kind RemediationActionKind, f findingFeatures)` or `(…, audience RemediationAudience)`; no Kubernetes client / dynamic client / REST mapper / discovery client / GitHub client / GitLab API client is reachable from any builder. Reuses EDGE-142's existing template-registration + dispatch (switch on `RemediationActionKind`) + helper-extension pattern. No parallel generator package, no parallel doc, no separate enforce path.

#### Edge — EDGE-143.1 Kubernetes shadow detector library (2026-05-17, task-8f72d421)

- `core/edge/shadow/k8s/` (new package) — observe-only Kubernetes shadow-agent detector library. Vendors `k8s.io/client-go@v0.34.8` as the **first Kubernetes dependency in cordum** (transitive: `k8s.io/api`, `k8s.io/apimachinery`). 9 signal extractors implementing design doc §7.1 verbatim: `k8s_heartbeat_missing` (with §14 N=3-consecutive-poll gate for false-positive suppression), `k8s_unmanaged_process` (PodSpec.Containers[].Command/Args[0] leading-token match), `k8s_unmanaged_mcp_service` (Service.Spec.Ports[].Name ∈ {mcp, mcp-stdio, mcp-sse, mcp-http} missing cordum gateway adoption label), `k8s_unmanaged_workload` (OwnerReference traversal + allowlist check), `k8s_untrusted_agent_image` (registry-prefix match), `k8s_namespace_untenanted` (namespace missing tenant label + indicator aggregation), `k8s_admission_observed` (admission log tail; observe-only, never installs webhook), `k8s_egress_bypass` (NetworkPolicy.Spec.Egress.To outside LLM proxy allowlist; risk=high), `k8s_ephemeral_indicator` (per §14 NEVER auto-promoted without corroboration). Tenant + principal mapping per design doc §6.1/§6.2 5-tier precedence chain: pod label → namespace label → cluster config → SA config → quarantine. Data minimization at extraction time per §5: no env-var values, no Secret bodies, no command-arg values beyond leading token, no full URLs with query strings — defense-in-depth via duplicated 8-pattern secret-marker regex strip (mirrors `core/edge/shadow/redaction.go:23-36`) + ≤2048-byte length cap on every persisted string.
- `core/edge/shadow/k8s/` — observe-mode-only by **type-system constraint**: detector struct exposes only read methods of `kubernetes.Interface` via narrow sub-interfaces (`podLister`, `nsLister`, `svcLister`, `saLister`, `npLister`); no Create/Update/Patch/Delete is reachable from the detector. Defense-in-depth `CORDUM_EDGE_SHADOW_K8S_ENFORCE` env var defaults OFF; current task ships **zero enforce code** — future enforce-mode requires the ADR per design doc §11.3. Emits findings via the existing EDGE-141 `shadow.Store.CreateFinding` API (no parallel store) with the EDGE-143.5 §10.1 typed fields (`source_type="kubernetes"`, `cluster_id`, `namespace`, `workload_kind`, `workload_name`, `pod_uid`, `tenant_source`, `principal_source`, `signal_set`, `confidence`, `first_seen`, `last_seen`, `retention_class="shadow_default"`). Observability via `Observer` interface (`RecordFindingEmit(signal, risk)` counter hook + `EmitAudit(audit.SIEMEvent)` audit hook) with bounded labels per design doc §13 (no tenant/cluster_id/namespace/workload_name as metric labels — high cardinality).
- 14 new tests across `detector_test.go` + `signals_test.go` — table-driven against `k8s.io/client-go/kubernetes/fake.NewSimpleClientset()` + miniredis-backed `shadow.Store`. Coverage: per-signal field-mapping assertions, §14 N-consecutive-poll gate, OwnerReference traversal for workload-kind resolution, 5-tier tenant mapping precedence (one t.Run per tier), data-minimization canary defense (pod with 5 canary strings across Env/Args/Secret-mount; asserts none appear in any persisted finding field), observe-mode no-mutation gate (fake.Actions() must contain zero create/update/patch/delete verbs), §10.1 typed-fields round-trip, observability spy assertions.

#### Edge — EDGE-143.5 ShadowAgentFinding store extensions (2026-05-17, task-973d8bd7)

- `core/edge/shadow/finding_store.go` — added `ShadowFindingRetentionClass` enum (`shadow_short` / `shadow_default` / `shadow_long`) + `SourceType*` and `CIProvider*` constant blocks; extended `ShadowAgentFinding` and `CreateFindingRequest` with the 23 §10.1 fields (`source_type`, `source_id`, `cluster_id`, `namespace`, `workload_kind`, `workload_name`, `pod_uid`, `ci_provider`, `repo`, `ref`, `workflow_id`, `job_id`, `run_id`, `runner_id`, `tenant_source`, `principal_source`, `signal_set`, `confidence`, `first_seen`, `last_seen`, `false_positive_reason`, `exception_id`, `retention_class`); extended `ListFindingsQuery` with the 11 §10.2 filter dimensions; added `validateShadowExtensions` (enum gates + byte caps per §10.1, signal_set ≤16 entries `[a-z0-9_]{1,32}`, confidence ∈ [0,1], first_seen ≤ last_seen, ci_provider+repo mutual, retention_class enum); added `applyReadDefaults` for §10.4 backward-compat (`source_type=""` → `local` on read).
- `core/edge/shadow/finding_store_redis.go` — added 4 new index key prefixes + helpers: `edge:shadow:index:source:<v>`, `edge:shadow:index:cluster:<v>`, `edge:shadow:index:repo:<provider>:<org/repo>`, `edge:shadow:index:signal:<v>`. **NOT tenant-scoped** per the Q7 binding governor ruling (store-level federation, not detector-level); cross-tenant index entries are filtered out by `ListFindings`' read-time tenant gate and **never deleted** as stale (cross-tenant data-loss guard via new `indexIsTenantScoped` discriminator on `chooseIndex`). `CreateFinding` pipeline emits to all 4 new indexes conditionally (source always; cluster only when set; repo composite only when ci_provider+repo both set; signal per non-empty entry). `chooseIndex` priority extended (repo > cluster > source-non-local > signal-single > status > risk > agent > owner > tenant) with the `source_type=local` backward-compat fallback to the tenant index. Multi-signal any-of (`signals[] > 1`) bypasses `chooseIndex` via a new `listFindingsByMultiSignal` path: per-signal `zScanDescending` + in-memory dedupe + post-filter; single-page only. `matchesPostFilters` extended with all 11 §10.2 dimensions including `includeManagedSkip` (default `false` excludes findings with `false_positive_reason != ""`). `opportunisticCleanup` ZREMs from the source bucket for all 4 enum values; cluster/repo/signal are open-set and intentionally leak (matches the existing agent/owner cleanup pattern; correctness preserved because the deleted JSON key is treated as stale on next read).
- `core/edge/shadow/finding_store_redis.go` — per-finding terminal-retention: added `WithShadowRetentionClasses` opt + `shadowRetention` field on `RedisStore` + `defaultShadowRetention()` baseline (7d/90d/365d); `shadowRetentionFromEnv` overlays `CORDUM_EDGE_SHADOW_RETENTION_SHORT/DEFAULT/LONG` (positive `time.ParseDuration` values only; `0` / negative / malformed → startup error per the EDGE-141 convention). `retentionFor(rc)` returns the class TTL; empty class falls back to `terminalRetention` so legacy EDGE-141 records keep their existing lifecycle. `isExpiredTerminal` and the `transitionFinding` terminal-`Expire` block both use `retentionFor(f.RetentionClass)`.
- `core/edge/shadow/finding_store_redis.go` — `NewRedisStore` signature changed from `(*RedisStore)` to `(*RedisStore, error)` so env-var parse failures surface fail-fast at startup (§10.5). Updated 2 callers: `core/controlplane/gateway/gateway.go` (wraps `fmt.Errorf("shadow finding store: %w", err)`) and `core/controlplane/gateway/handlers_edge_shadow_agents_test.go` (fatals the test).
- `core/controlplane/gateway/handlers_edge_shadow_agents.go` — `shadowAgentCreateRequest` wire type extended with all 23 §10.1 fields (omitempty); `handleCreateShadowAgentFinding` forwards all 23 from body to store. `parseShadowFindingListQuery` extended with 11 §10.2 query params — `source_type` and `ci_provider` are enum-gated, `cluster_id`/`namespace`/`repo`/`exception_id` byte-capped, `repo` without `ci_provider` returns 400, repeated `?signal=` capped at 16 entries with per-entry regex, `confidence_min` ∈ [0,1] ParseFloat-validated, `first_seen_after`/`last_seen_before` RFC3339-parsed, `include_managed_skip` ParseBool. All reject paths use the existing `writeEdgeError`+`edgeErrCodeInvalidRequest` envelope.
- `docs/api/openapi/cordum-api.yaml` — `ShadowAgentFinding` and `CreateShadowAgentFindingRequest` schemas extended with the 23 §10.1 properties (typed: enum / number+format:float+min/max / array+maxItems+items.pattern / string+format:date-time+nullable). `GET /api/v1/edge/shadow-agents` extended with the 11 §10.2 query params (`source_type` enum, `cluster_id`/`namespace`/`repo`/`exception_id` typed strings with maxLength, `signal` array with `explode:true`+`maxItems:16`+items.pattern, `confidence_min` number with min/max, `first_seen_after`/`last_seen_before` date-time, `include_managed_skip` boolean). All optional so legacy responses validate. Both `TestOpenAPICoverage` and `TestRouteCoverage` pass under default and `-tags=integration`.
- `docs/edge/shadow-scanner.md` — added §9.1 documenting the shipped §10 deltas (23 fields, 11 filters, 4 indexes, retention class table) + the 3 env vars + the §10.4 backward-compat behavior + the Q7 cross-tenant safety contract.
- 16 new tests (`core/edge/shadow/finding_store_extensions_test.go` — 13 functions including the table-driven `TestExtensionsFilters_PerDim` 15-subtest matrix and `TestExtensionsRetention_EnvVarParseError` 3-subtest matrix) + 2 new handler-level validation tests (`handlers_edge_shadow_agents_extensions_test.go` — 16-subtest validation matrix + 17-signal-cap test, separate file to dodge the task-4cd8299f file collision on the existing `handlers_edge_shadow_agents_test.go`). Coverage includes: full-field round-trip; legacy JSON read-back (defaults `source_type=local`); per-dimension filter routing; combined AND query; the Q7 cross-cluster no-tenant-leak invariant (3 tenants share an index, query returns only the asking tenant's records, AND verifies the other tenant's record is NOT deleted); ZADD-on-create for the 4 new indexes including conditional skip for empty fields; ZREM cleanup of the source bucket; per-retention-class TTL boundaries at day 8/95/370; legacy `retention_class=""` fallback to `terminalRetention`; all §10.2 query-param parse errors return 400; 17 signals cap rejected.
- Backward-compatible with EDGE-141: legacy records continue to round-trip and surface in lists; the gateway startup is unchanged in behavior when no `CORDUM_EDGE_SHADOW_RETENTION_*` env vars are set. **NOT tenant-scoped indexes** (Q7) are intentional design, not a bug — see `docs/edge/shadow-scanner.md` §9.1 for the cross-tenant safety contract.

#### Edge — EDGE-151-DOWNGRADE binary version-floor enforcement (2026-05-17, task-3166dda6)

- `tools/sign/version.go` (new) — `SemverCompare`, `VerifyVersionFloor`, `AdvanceFloor`, `ReadFloor`, `EmbedVersion`, `ParseVersion`, plus `FloorMetadata` struct and four new error sentinels (`ErrDowngradeAttempt`, `ErrFloorAdvanceFailed`, `ErrInvalidVersion`, `ErrNoVersionEmbedded`). Hand-rolled semver-2.0 comparator with natural-sort for `<alpha-prefix><digit-suffix>` pre-release identifiers (so `v1.0.0-rc2 < v1.0.0-rc10` matches operator expectations) — no new external dep added. Floor file write is atomic via write-tmp + `os.Rename`.
- `tools/sign/verifier.go` — `parseManifest` now skips lines starting with `#`, tolerating the new `# version: vN.N.N` metadata line emitted by `EmbedVersion`.
- `tools/sign/cmd/version-cli` (new) — `compare <a> <b>` and `monotonic-or-fail <new> <prior>` subcommands. The CI release-tag monotonicity gate and the install path share the same comparator through this binary.
- `tools/scripts/install.sh` + `install.ps1` — parse the embedded version from the manifest, resolve the persisted floor file (default `$HOME/.cordum/binary-version-floor.json`; override via `CORDUM_BINARY_FLOOR_FILE` / `-FloorFile`), refuse `BINARY-VERIFY-FAIL: downgrade attempt <cand> < <floor>` unless `--rollback-operator-override --rollback-reason <text>` is supplied. On successful activation the floor is rewritten atomically and a `binary-floor-advance` or `binary-floor-rollback` audit JSON-line is emitted on stderr (additive to the existing `binary-verify-{ok,fail}` schema, with extra `from`/`to`/`operator`/`reason` fields). Argv-parse-time guard refuses the override flag without a non-empty reason. Both shells explicitly reject malformed `# version:` strings before the compare (defense-in-depth against the `semver_lt` exit-2 sentinel silently riding through).
- `tools/scripts/release-local.sh` — embeds `# version: $RELEASE_LOCAL_VERSION` (default `v0.0.0-dev`) into SHA256SUMS before detach-signing.
- `.github/workflows/release.yml` — new `version-monotonicity` job runs first on `v*` tag push: refuses any tag whose semver is not strictly greater than the most-recent prior tag, preventing accidental sibling-release downgrade at tag time. The existing `sign-manifest` job now embeds `# version: <pushed-tag>` into the manifest before GPG detach-sign so production releases ship with the floor-enforcement metadata.
- `docs/security/binary-signing.md` — §2(g) flipped from "OUT OF SCOPE" to "ADDRESSED by EDGE-151-DOWNGRADE"; new §8A documents the manifest-embed format, the floor file shape and location, the operator-override flag pair, the audit-event schema, the CI monotonicity gate, and the out-of-scope residuals (floor-file tampering, CI-bypass via repo-admin, non-semver tags, external CVE lookup).
- 14 new automated test cases: 8 in `tools/sign/version_test.go` covering SemverCompare ordering (incl. pre-release and natural-sort), VerifyVersionFloor refuse/accept paths, AdvanceFloor + ReadFloor roundtrip + malformed-rejection, EmbedVersion idempotent + conflict-rejection, ParseVersion + missing-version detection. 8 in `tools/sign/cmd/version-cli/main_test.go` covering the CI gate's monotonic-or-fail and compare subcommands. 1 in `tools/sign/verifier_test.go` confirming the existing parseManifest tolerates the embedded `# version:` line. `install_test.sh` and `install_test.ps1` extended to 7+ scenarios each covering downgrade-refused, legit-upgrade, operator-rollback (with reason), garbage-version-rejection, and rollback-without-reason-refused.
- `cmd/cordum-agentd/` NOT touched — the daemon does not perform runtime upgrades; the install scripts are the only floor-mutating actors.

#### Edge — EDGE-144 runtime event ingestion adapter design + skeleton (2026-05-17, task-f2bf3c65)

- `core/edge/runtimeingest/` (new package) — `Adapter` validates + maps bounded, redacted runtime telemetry envelopes (process exec, file read/write, network connect, DNS query) into existing `edge.AgentActionEvent` records with `layer=runtime`, `decision=RECORDED`, `rule_tier=""`. `DecodeBatch` uses strict-schema (`json.Decoder.DisallowUnknownFields`) so smuggled raw keys (argv/args/cmdline/command_line/env/environment/file_content/file_contents/packet/payload/body/request_body/response_body/headers/header/cookie/cookies/secret/secrets/token/tokens/password/passwords/api_key/apikey/private_key/dns_response/response) are rejected at the wire boundary before reaching the redactor. Per-batch cap (`MaxRuntimeBatchEvents=256`), per-envelope JSON-size cap (`MaxRuntimeEnvelopeBytes=4 KiB`), per-redacted-string cap (`MaxRuntimeRedactedStringBytes=256`), per-label-entries cap (`MaxRuntimeLabelEntries=16`). Deterministic FNV-1a sampling keyed by `source_id|kind|source_event_id` so retries land in the same bucket; `MapResult.Dropped` reports `sampled_out` drops without persisting. Stable `EventID = "runtime-<fnv64a hex>"` so two batches with the same envelope idempotently produce the same event id.
- `core/controlplane/gateway/handlers_edge_runtime_ingest.go` (new) — `POST /api/v1/edge/runtime/events`. **Disabled by default**: returns 503 `service_unavailable` until `CORDUM_EDGE_RUNTIME_INGEST_ENABLED=true|1|yes` is set. When enabled, reuses existing Edge auth (`requireEdgePermissionOrRole(PermJobsWrite, "admin", "user")`), tenant resolution (`edgeTenantFromRequest` — X-Tenant-ID required, body/header mismatch → 403 `tenant_mismatch`), parent validation (`validateEdgeEventParents` — missing session/execution → 404, execution-session mismatch → 400 `execution_session_mismatch`), and persists via the existing atomic `edge.Store.AppendEvents` path — never NATS, never raw Redis. All-or-nothing batch acceptance — a single invalid envelope aborts the whole batch with no partial Redis writes. Per-route 1 MiB body cap via `http.MaxBytesReader` independent of the global maxBody middleware.
- `core/controlplane/gateway/gateway.go` — registered `POST /api/v1/edge/runtime/events` route between `/sessions/{id}/export` and the memory-pointers block; `core/controlplane/gateway/edge_routes_test.go` — added the new route to `edgeRouteExpectations()` so future refactors cannot silently drop it.
- `docs/api/openapi/cordum-api.yaml` — added the runtime/events path + 9 schemas (`EdgeRuntimeIngestSource`, `EdgeRuntimeProcessSummary`, `EdgeRuntimeFileSummary`, `EdgeRuntimeNetworkSummary`, `EdgeRuntimeDNSSummary`, `EdgeRuntimeEventEnvelope` with `additionalProperties:false`, `EdgeRuntimeIngestRequest`, `EdgeRuntimeIngestDropReport`, `EdgeRuntimeIngestResponse`). Validates clean: `bash tools/scripts/openapi-validate.sh` → "Woohoo! Your API description is valid".
- `docs/edge/runtime-ingestion.md` (new) — full operator contract: schema-mapping table for all 5 runtime kinds → existing `EventKind` constants; wire envelope JSON example; field rules including the forbidden raw-key list; constants table (caps, sampling); tenant + source-id auth flow; disabled-by-default rationale (503 chosen over 404 so operators can probe and managed-settings can flip the flag without redeploying); response shape; full error-code mapping table; 4-phase rollout (skeleton → metrics → adapter daemon → dashboard, all separate future tasks); "Why this shape" closure section.
- Tests (15 gateway + 50 adapter = 65 new tests, all PASS under `-count=3`): disabled→503, missing tenant header, no auth, body/header tenant mismatch, cross-tenant parents (no leak), missing parent, execution-session mismatch, empty batch, missing source_id, forbidden raw key (raw URL not echoed in error), oversize batch, GET method rejected, end-to-end Redis persistence with redaction round-trip (AKIA token does not survive), all-or-nothing partial-batch rejection. Adapter: each-kind mapping, stable EventID, unknown kind, required-field gate, batch-cap boundary (exact-cap accepted, cap+1 rejected), envelope-byte cap, label overflow, redaction of secret-shape strings in file/network/DNS, status enum mapping, artifact pointer pass-through, artifact cross-tenant reject, deterministic sampling stability, and DisallowUnknownFields rejecting 26 distinct forbidden top-level keys.
- **Out of scope for this task** (deferred to future EDGE-* tasks): the eBPF/Tetragon/Falco collector binary, runtime enforcement, dashboard timeline surface, new retention class, NATS subject, per-event Cordum Job mapping. Skeleton ships with the route present and disabled so operators can roll forward without redeploying.

#### Edge — EDGE-141 shadow finding store + APIs (2026-05-17, task-06aaab74)

- `core/edge/shadow/finding_store.go` (new) — `ShadowAgentFinding` lifecycle record + `FindingStatus` enum (`detected|resolved|suppressed`) distinct from the scanner-only EDGE-140 `Finding.Status`. Reuses `RedactConfigSummary` / `RedactPath` / `stripSecretMarkers` from the existing scanner package; defines a smaller `EvidencePointer` (URI + SHA256 + retention + redaction + per-tenant ownership) because shadow findings have no session/execution context and the existing `edgecore.ArtifactPointer` validation rejects empty session/execution/event ids. Validation rejects empty tenant/owner/principal/agent_product/risk/evidence_type, requires `evidence_summary` OR `evidence_artifact_ptr`, caps summary at 2 KiB, runs secret-marker stripping, generates `edge_shadow_<hex>` ids when callers omit them, and normalises UTC timestamps via an injectable `now` func.
- `core/edge/shadow/finding_store_redis.go` (new) — `shadow.RedisStore` wraps the shared gateway Redis client (no new connection, no Close ownership). Keyspace per PRD: `edge:shadow:finding:<id>` JSON + `edge:shadow:index:<tenant>` broadest ZSET + `edge:shadow:index:<tenant>:status:/:risk:/:agent:/:owner:` secondaries. `CreateFinding` is atomic via `TxPipeline` with best-effort JSON rollback on partial failure; idempotent on byte-equal re-create. `ListFindings` picks the narrowest index (status > risk > agent > owner > tenant fallback) and post-filters with bounded over-scan; cursor is opaque (`<score>:<id>`); stale or cross-tenant index members are cleaned opportunistically. `ResolveFinding` / `SuppressFinding` use atomic JSON + status-index move; terminal-state cross transitions return `ErrTerminalConflict`. Configurable `DefaultTerminalRetention=90d`; expired terminals are hidden from get/list and purged on the next list call.
- `core/controlplane/gateway/handlers_edge_shadow_agents.go` (new) — 5 handlers + the /ignore alias under `/api/v1/edge/shadow-agents`. All routes reuse existing Edge auth (`requireEdgePermissionOrRole(PermAuditExport|PermAuditRead, "admin")`), tenant resolution (`edgeTenantFromRequest`), and error envelope (`writeEdgeError`). Store errors map to 400/404/409/503/sanitised-500. Emits `shadow_agent.{detected,resolved,suppressed}` audit events on success ONLY; payloads carry tenant + finding + agent + risk + status + redacted_path + optional artifact pointer URI/SHA256 + bounded reason. `evidence_summary` is deliberately omitted from audit payloads (defense-in-depth despite ingest-time redaction).
- `core/audit/exporter.go` — added 3 audit event constants `EventShadowAgentDetected/Resolved/Suppressed`. `core/audit/soc2.go` — SOC2 control mapping (detected → CC7.2; resolved/suppressed → CC7.2 + CC8.1).
- `core/controlplane/gateway/gateway.go` — added `shadowFindingStore shadow.Store` field; init alongside `edgeStore`; registered 6 routes (canonical + /ignore alias).
- Tests (20 new across 2 files): store-level miniredis coverage (create persists all fields, rejects missing/oversized/secret evidence, get tenant isolation, list filters & cursor pagination, invalid cursor 400, resolve+suppress idempotency, cross-terminal 409, terminal retention hides expired + cleans stale indexes, nil-store ErrStoreUnavailable, closed-store error surfaces); gateway-level routing through `s.registerRoutes(mux)` (create→get→list happy path with audit + redaction assertions, tenant isolation 404, missing tenant header 400, bad filters 400, 503 on nil store, resolve+suppress audit emission, cross-terminal 409, /ignore alias path, raw-secret-stripping at the boundary, route-table coverage, resolve immutability for tenant_id/finding_id/evidence_summary/owner).
- `docs/api/openapi/cordum-api.yaml` — 6 path objects + 6 schemas (`ShadowAgentFinding`, `ShadowEvidencePointer`, `ShadowAgentFindingPage`, `CreateShadowAgentFindingRequest`, `ResolveShadowAgentFindingRequest`, `SuppressShadowAgentFindingRequest`). Net OpenAPI delta: +7 documented operations covering every new route.
- `docs/edge/shadow-agent-findings.md` (new) — operator-facing reference for the lifecycle states, endpoints, filters, evidence/redaction contract, retention, audit event names, SOC2 mappings, and the explicit scope boundaries (observe-only; no enforcement / remediation / dashboard / Cordum Job creation in this task).
- Observe/warn ONLY. No dashboard work, no enforcement hooks, no Cordum Job creation, no new Redis connection or event bus.

#### Docs — EDGE-143 design doc for Kubernetes / CI shadow detection (2026-05-17, task-de50a293)

- `docs/edge/kubernetes-ci-shadow-detector-design.md` (new) — design-only architecture & privacy specification for cluster-scope and CI-scope shadow-agent detection. Extends the EDGE-140 local-host scanner (`docs/edge/shadow-scanner.md`) and the in-flight EDGE-141 server-side finding store + EDGE-142 remediation generator to Kubernetes pods and CI runners (GitHub Actions, GitLab CI, Jenkins, Buildkite, CircleCI). Specifies: (a) detection signals — pod/namespace/workload metadata, workflow/job/runner metadata, direct provider-traffic hostname/category/counts only, never payload; (b) tenant/principal mapping precedence chains (pod label → namespace label → cluster config → SA config → quarantine; OIDC claim → repo map → workspace map → quarantine); (c) data minimization with extraction-time redaction + EDGE-140 secret-shape regex strip + 2048-byte cap; (d) proposed additive `ShadowAgentFinding` field set (`source_type`, `cluster_id`, `namespace`, `workload_kind/name`, `pod_uid`, `ci_provider`, `repo`, `workflow_id/job_id/run_id`, `runner_id`, `signal_set`, `confidence`, `first_seen/last_seen`, `false_positive_reason`, `exception_id`, `retention_class`) with backward-compat default `source_type="local"` for existing rows; (e) exception API `POST /api/v1/edge/shadow/exceptions`; (f) three rollout modes (observe-only default, warn = webhook to operator pipeline, enforce = ADR-gated future); (g) remediation classes mapped to EDGE-142 (`attach_mcp_gateway`, `attach_edge_session`, `deploy_managed_settings`, `route_via_llm_proxy`, `register_ci_workflow`, `declare_exception`, `resolve_manually`) with mandatory preview + backup/change-ticket gate on every mutating action; (h) false-positive controls for fork PRs, dependabot/renovate, ephemeral runners, managed-but-late heartbeat, telemetry gaps; (i) security/privacy review checklist (14 PR-template items); (j) 8 open questions blocking implementation tasks. **No production cluster code, no dashboard surface, no Cordum Jobs per agent action, no customer-cluster mutation, no TLS interception, no payload or secret capture by default** — design is observe-first, P3, and requires human signoff before any follow-up implementation task (EDGE-143.1 through EDGE-143.10 proposed) is filed.
- `docs/edge/shadow-scanner.md` — §9 Roadmap updated: EDGE-141/142 status refreshed from PLANNING to WORKING, EDGE-143 row links to the new design doc. The legacy assertion that EDGE-143 would design the P3 dashboard surface is corrected to note the dashboard is intentionally deferred (per the new design doc §17, the dashboard is its own future task family with its own ADR).

#### Dashboard — EDGE-105 MCP lane on Edge Session detail (2026-05-17, task-a04699dc)

- `dashboard/src/components/timeline/lanes/MCPLane.tsx` (new) — the MCP timeline lane mounted below the existing P0 hook timeline on `EdgeSessionDetailPage`. Classifies each `AgentActionEvent` into Servers / Tools / Approvals / Failures categories and renders one row per MCP-relevant event with a distinct icon + decision badge + relative timestamp. Click expands an inline inspector body. Honors the global `<MotionConfig reducedMotion="user">` wrapper.
- `dashboard/src/components/timeline/inspector/MCPInspector.tsx` (new) — six-field row-expand body: upstream server (`event.labels.mcp_server` with `agentProduct` fallback), tool name, decision (StatusBadge), approval link (router `Link` to `/approvals/<approvalRef>` when set), redacted args (`inputRedacted`), redacted result (`labels.result_redacted`). Renders an artifact-pointer chip with sha256-short when `event.artifactPtrs[0]` is present; external link uses `rel="noopener noreferrer"`.
- `dashboard/src/state/mcpLaneFilters.ts` (new) — Zustand slice + URL parse/serialize helpers for the chip-toggle filter state. The URL query `?mcp_lane=servers,tools,approvals,failures` shares the filter across operators; invalid tokens are silently dropped, and an empty parse falls back to the default all-active state.
- `dashboard/src/lib/redaction.ts` (new) — client-side defense-in-depth sanitizer. `sanitizeMCPField` trusts the `<name>_redacted` suffix verbatim (server-side redaction is authoritative); if only a bare sensitive field (`prompt`, `tool_input`, `result`, `args`, etc.) is present it returns the stable placeholder `"[redacted by client sanitizer]"`. `sanitizeMCPPayload` serializes the redacted blob when any `_redacted` key is present, else walks the raw payload and replaces sensitive field values.
- `dashboard/src/pages/EdgeSessionDetailPage.tsx` — three-line ADD only: import `MCPLane` and mount `<MCPLane events={events} />` between the existing P0 timeline section and `EdgeArtifactsPanel`. Zero modifications to the P0 hook timeline, `groupEdgeEvents`, or `TimelineGroupRow`; task rail #2 ("do not duplicate P0 timeline components; extend reusable lanes") honored.
- Tests (22 new): `MCPLane.test.tsx` (10) covers all event-kind rendering, the chip-toggle filter, the empty state, the bare-leak defense, the trusted `_redacted` pass-through, approval-link navigation, the artifact-pointer chip, the strict axe-core gate, the XSS escape, and URL parsing. `MCPInspector.test.tsx` (3) covers six-field render and absent-data fallbacks. `redaction.test.ts` (9) covers the field- and payload-level sanitizer contracts.
- `docs/mcp-server.md` — new "Dashboard MCP Lane" section documenting the lane surface, filter chip UX, inspector layout, redaction contract, empty state, and accessibility posture.

#### Tools — Self-test harness for the EDGE-068 argv-only exec lint (2026-05-17, task-c000a477)

- `tools/scripts/lint_no_secret_log.test.sh` (new, executable) — exercises Phase 4 of `lint_no_secret_log.sh` against three fixture corpora under `tools/scripts/testdata/lint_no_secret_log/`:
  - `phase4_pass/` — argv-only `exec.Command` plus the `go test -c` false-positive defense (the `-c` flag here is the go-test compile flag, not a shell command flag).
  - `phase4_fail/` — eight shell-spawn patterns: `sh -c`, `/bin/sh -c`, `bash -c`, `cmd /C`, `cmd.exe /c`, `powershell -Command`, a multi-line `exec.CommandContext` split across source lines (exercises the awk paren tracker), and `/usr/bin/sh -c` (absolute path).
  - `phase4_exception/` — the `cmd/cordumctl/doctor.go:878-883` runtime.GOOS branch with `// no-shell-exec-lint: operator-confirmed doctor repair only` markers, plus a minimum-shape inline marker.
  Plus T11 default-tree invariant: with `LINT_SCAN_ROOTS_OVERRIDE` unset, the lint must still exit 0 on the real `cmd/` and `core/` trees so the fixture corpus cannot bleed into the production scan.
- `tools/scripts/lint_no_secret_log.sh` — Phase 4 only: new `LINT_SCAN_ROOTS_OVERRIDE` env var (colon-separated dirs) replaces the default `cmd/` + `core/` find roots when set. Used exclusively by the test harness. Unset preserves the historical scan behaviour bit-for-bit.
- `docs/no-shell-exec-lint.md` (new) — documents the argv-only convention, why hook-boundary subprocesses must not route through a shell, the `// no-shell-exec-lint: <reason>` marker shape and required reason text, the current exception list (`cmd/cordumctl/doctor.go:880,882`), and the procedure for adding a new exception (architect review + marker + exception-list update in the same commit).
- `.github/workflows/ci.yml` — `go-test` job now runs `bash tools/scripts/lint_no_secret_log.test.sh` immediately after `lint_no_secret_log.sh` so a regression that silently weakens the guard fails CI.

### Changed

#### Dashboard — Jobs + Job Detail refresh: unified search, fold I/O into Overview, remove Policy Trace tab (2026-05-16, task-cafacca3)

- `dashboard/src/pages/JobsPage.tsx` — main search input placeholder expanded to "Search jobs (ID, topic, pool, tenant, session, run, trace)"; the underlying `filtered` predicate now matches `pool`, `tenant`, and `getJobParentRefs(j).sessionId` alongside the prior ID/topic/trace/run fields so users don't need to open the advanced filter bar to find jobs by those fields.
- `dashboard/src/pages/JobDetailPage.tsx` — tabs reduced from 5 to 2 (Overview + Audit Chain). Inputs + Outputs tab content (Context BlobViewer + Result BlobViewer) folded into Overview as `CollapsibleSection` rows below `AgentExecutionsPanel`. Policy Trace tab removed entirely (unused per task description); `GovernanceTimeline` import deleted from this page — the component file stays because `RunDetailPage.tsx` still consumes it. Legacy `?tab=inputs|outputs|policy-trace` deep-links gracefully migrate to Overview via the activeTab derivation so bookmarks don't 404.
- Tests: 4 obsolete tab-click tests (Inputs/Outputs/Overview-clears-tab-param + the 5-label tab-set assertion) deleted as the assertions test removed UI. New replacement asserts exactly 2 tabs (Overview + Audit Chain) and that GovernanceTimeline does not mount. One pre-existing test (`does not double-print ctx.run_id`) refined from page-wide assertion to the GenericContext-curated-row invariant — the new Context BlobViewer in Overview legitimately echoes raw ctx in JSON.

#### Dashboard — Agent Fleet consolidated from 4 tabs to 2 (2026-05-16, task-083581ca)

- `dashboard/src/pages/AgentsPage.tsx` — `topTabs` reduced 4→2 (Fleet Overview + Identities). Pool Topology absorbed into Fleet Overview as a segmented view-mode toggle (Table / By Pool) with `?view=by-pool` query-state persistence. Agent Registry deleted as redundant — its worker table duplicated Fleet Overview's; the standalone `AgentRegistryTab` function and the `useWorkers` import removed (~75 LOC). Identity Directory renamed to Identities (same content + EntitlementGate wrapper).
- Backward-compatibility migration: mount-time `useEffect` rewrites legacy `?tab=pools|registry|identity` query params via `setSearchParams(..., { replace: true })` so existing bookmarks resolve to the new tab+view combination (pools → fleet+view=by-pool; registry → fleet; identity → identities).
- `dashboard/src/pages/AgentsPage.test.tsx` — 2 new assertions: exactly `{Fleet Overview, Identities}` top-level tabs render; `{Pool Topology, Agent Registry, Identity Directory}` labels are absent. 3/3 PASS post-impl.

#### Dashboard — Edge promoted to top-level sidebar section; redundant Dead Letters entry removed (2026-05-16, task-266f21ad)

- `dashboard/src/components/layout/AppShell.tsx` — APP_SHELL_NAV_SECTIONS now ships 6 sections (Run, **Edge**, Govern, Catalog, Audit, Settings). The new `Edge` section sits between Run and Govern and contains: `Edge Sessions` (`/edge/sessions`), `Edge Approvals` (`/edge/approvals`), `Edge Audit` (`/edge/audit`). The Edge Sessions item moved out of Run so the Edge subsystem has visible breadth in the IA instead of being buried as one item in Run.
- `Dead Letters` sidebar entry removed from the Audit section. DLQ content has been folded into JobsPage as `?status=dlq` since task-0bcb9411; the sidebar entry was a redirect-stub that added navigation noise without a dedicated destination. `/dlq` URL still redirects via `App.tsx::DlqRouteRedirect` so bookmarked links resolve.
- `dashboard/src/App.tsx` — added 2 Navigate redirect routes:
  - `/edge/approvals` → `/approvals?lane=edge`
  - `/edge/audit` → `/audit?event_type_prefix=edge`
  Pathname-prefix routing (`findActiveSection`) keeps Edge highlighted at click time; post-redirect the section reflects WHERE the user IS (Run/Approvals or Audit), an accepted UX trade-off vs introducing a query-aware section matcher.
- `dashboard/src/pages/ApprovalsPage.tsx` — reads `?lane` from the URL; when `lane=edge`, filters approvals to those where `decisionSummary.source.startsWith("edge")`. Today's /approvals feed primarily carries gateway approvals; edge-source approvals populate the filtered view once edge sources are routed through the global feed.
- `dashboard/src/pages/AuditLogPage.tsx` — adds an `event_type_prefix` query state via nuqs; when set, filters events client-side by `e.action.startsWith(prefix)`. Same dependency on a future audit-feed wiring for the prefix to surface non-zero results; the URL contract is wired now.
- Tests: `dashboard/src/components/layout/AppShell.test.tsx` (updated existing 3 assertions + added 4 new) and `dashboard/src/App.routing.test.ts` (added 3 redirect-route source-regex assertions including `/dlq` preservation). 44/44 PASS post-impl.

### Added

#### EDGE-143.2 — GitHub Actions CI shadow detector (observe-only) (2026-05-17, task-42467eb5)

- `core/edge/shadow/github/` — new observe-only GitHub Actions detector library using `github.com/google/go-github/v74/github` and the existing EDGE-141 `shadow.Store.CreateFinding` path. Six signal extractors implement design doc §8.1: known agent action reference or `run:` leading-token match, missing `cordum/cordum-edge-attach` / EdgeSession heartbeat, self-hosted runner without Cordum label, CI env-var **names** only, known agent/MCP config-file presence with redacted path/summary, and direct-provider endpoint hostnames from operator-supplied log metadata.
- OIDC attribution follows §6.3/§6.4 precedence with real `coreos/go-oidc` verification (discovery/JWKS signature, expiry, issuer, and audience): verified claims first (`iss` default `https://token.actions.githubusercontent.com` per Q6 / `comment-a17f4f1c`, audience default `cordum-edge`, overrides via `CORDUM_EDGE_SHADOW_OIDC_TRUST_github` and `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_github`, `sub` persisted as `principal_id`), then configured org/repo map, then quarantine/`unknown`. `CORDUM_EDGE_SHADOW_OIDC_TRUST_github=disabled` skips the claims provider and falls back to org/repo mapping.
- False-positive controls per §14: ephemeral runner suppresses the self-hosted-runner signal; fork PRs route to quarantine with `fork_pr_ephemeral`; scheduled jobs, dependabot/renovate, test fixtures, and dev sandboxes are tagged with bounded `false_positive_reason` values.
- Data minimization is enforced at extraction time: no env-var values, no `${{ secrets.* }}` expansions, no raw log bodies, no full URLs with query strings, no config-file contents. Evidence stores bounded repo/run/signal metadata, sanitized env-var names, `github://org/repo/path` redacted paths, `shadow.RedactConfigSummary` output, and provider hostnames only.
- Observability: bounded Prometheus observer metrics `cordum_edge_shadow_finding_emit_total{source_type="github_actions",signal,risk}`, `cordum_edge_shadow_oidc_verify_total{provider,result}`, and `cordum_edge_shadow_gh_rate_limit_remaining{provider}`; audit events use `Action="shadow_agent.observed"` / `EventType="edge.shadow_finding_created"` with `source_type=github_actions`, and OIDC failures emit `shadow_agent.oidc_verify_failed`.
- Tests: `core/edge/shadow/github/detector_test.go`, `reopen_test.go`, and `oidc_verifier_test.go` cover all six signal extractors, run-command leading tokens, EdgeSession heartbeat suppression/error audit, OIDC default/override/disabled plus real verifier signature/expiry/audience paths, OIDC subject principal mapping, quarantine/unknown fallback, six FP controls, data-minimization canaries, §10.1 typed CI fields, and observability. Focused package passed at `go test ./core/edge/shadow/github/... -count=3`.

#### EDGE-143.8 — `cordumctl edge doctor --shadow-cluster` / `--shadow-ci` preview flags (2026-05-17, task-0518f4b8)

- `cmd/cordumctl/edge_doctor_shadow.go` — adds the `--shadow-cluster=<kubeconfig-path>` and `--shadow-ci=<provider>:<token-or-config-path>` flags to the existing `cordumctl edge doctor` command. Both are **read-only previews**: they invoke the EDGE-143.1 Kubernetes / EDGE-143.2 GitHub Actions / EDGE-143.3 (GitLab|Jenkins|Buildkite|CircleCI) shadow detectors in dry-run mode against operator-supplied kubeconfig / CI tokens and print the would-emit findings to stdout. Findings are **never persisted** to the EDGE-141 store and **never mutate** the cluster or CI provider. The `dryRunStore` impl of `shadow.Store` records `CreateFinding` calls to an in-memory slice; every read method returns `ErrNotFound` or an empty page so no caller can mistake dry-run state for real EDGE-141 state.
- Kubernetes preview reuses `k8s.NewDetector(...)` from `core/edge/shadow/k8s` unchanged; the detector's `kubeReader` interface (`detector.go:115`) only declares `listPods` / `listNamespaces` / `listServices` / `getServiceAccount` / `listNetworkPolicies`, so no Create / Update / Patch / Delete verb is reachable from any code path in this command. Client built via `k8s.io/client-go/tools/clientcmd.NewNonInteractiveDeferredLoadingClientConfig` so the loader never prompts. Default config mirrors `k8s/detector_test.go:71-82` fixture defaults (KnownAgentImages, ImageRegistryAllowlist, MCPPortNames).
- CI preview wiring note: EDGE-143.8 originally shipped with provider-not-supported responses until EDGE-143.2/.3 detector modules landed. EDGE-143.2 now provides the GitHub Actions detector library contract; `cordumctl edge doctor --shadow-ci github:...` still needs a follow-up wiring step to call it, while GitLab/Jenkins/Buildkite/CircleCI remain gated on EDGE-143.3. The `edgeDoctorCIHTTPTransport http.RoundTripper` seam is in place for `TestEdgeDoctor_ShadowCI_ReadOnly` to assert no POST / PUT / PATCH / DELETE is ever issued once providers are wired. Unsupported providers (e.g. `azuredevops`) print `provider <X> not recognized; supported: github/gitlab/jenkins/buildkite/circleci`; malformed specs (missing colon) print `--shadow-ci value must be in <provider>:<token-or-config-path> format`.
- `--json` flag emits a machine-parsable envelope `{mode, dry_run, provider, findings}` where `findings` is always present as `[]shadow.ShadowAgentFinding` (empty slice when zero findings — never nil — so JSON consumers do not need a nil guard).
- Tests: `cmd/cordumctl/edge_doctor_shadow_test.go` — 9 tests + 11 subtests covering `--shadow-cluster` happy path / read-only invariant / JSON shape / finding emission / empty-path validation; `--shadow-ci` 5-provider unsupported case + unknown-provider case + malformed-spec case + 6-provider read-only HTTP invariant; flag-registration introspection. `go test ./cmd/cordumctl/ -count=3 -run 'TestEdgeDoctor_Shadow|TestEdgeDoctor_Flags_Registered'` → ok 0.226s (0 failures, 0 flakes). Full suite `go test ./cmd/cordumctl/... -count=3` → ok 58.781s. Adjacent `core/edge/shadow`, `core/edge/shadow/k8s`, `core/edge/shadow/network` all PASS at -count=3.
- Documentation: new file `docs/cordumctl/edge-doctor.md` covers usage syntax, value formats, what each flag invokes, the read-only invariant, JSON envelope shape, and partial-support note for providers gated on EDGE-143.2/.3.
- `go.mod`: +1 indirect `github.com/spf13/pflag v1.0.6` (transitively from `k8s.io/client-go/tools/clientcmd`). No direct dependencies added.

#### EDGE-143.4 — Network-signal aggregator for direct LLM provider traffic (observe-only) (2026-05-17, task-2b0edf73)

- `core/edge/shadow/network/` — new library package that reads operator-supplied egress / proxy logs (file path, UDP syslog endpoint, or stdin stream) and classifies direct LLM-provider traffic per the design doc §9.1 **lawful-metadata catalog**: persisted fields are limited to `hostname` (e.g. `api.anthropic.com`), `category` (`anthropic_api`/`openai_api`/`google_api`), `count_bucket` (1/10/100/1000/10000/100000+), workload identity (post-PII), and operator-supplied `endpoint_hash`. `enforceCatalog` strips anything outside the catalog at the ProcessRecord boundary (defense in depth — IPs, full URLs with query strings, bearer tokens, request/response bodies, and User-Agent strings can never reach a persisted finding even if a careless ingestor populates them on `LogRecord`).
- **NG7 contract — observe-only, no Cordum-side capture.** Cordum does NOT intercept network traffic; the detector reads what the operator has already produced. The package contains zero raw-socket / pcap / TLS-interception code; `TestNetworkDetector_Ingest_NoCaptureNG7` grep-asserts the absence of `pcap.*`, `gopacket`, `afpacket`, `net.ListenPacket`, `syscall.SOCK_RAW`, `crypto/tls.Server`/`NewListener`, and `x/sys/unix.SOCK_RAW` across the package source. Syslog ingest uses `net.ListenUDP` as a server-side bind (READ-only; the conn handle is never used to send bytes back).
- **Q2 PII handling per binding governor ruling** ([comment-a17f4f1c on task-de50a293](docs/edge/kubernetes-ci-shadow-detector-design.md)): `CORDUM_EDGE_SHADOW_PII_MODE=pseudonymize|hash|drop` env flag controls the `principal_id` transform (the value IS pseudonymous personal data per GDPR Art. 4(1) when sourced from `github.actor` / CI usernames). `pseudonymize` (default): first 3 chars of raw + first 8 hex chars of SHA-256(raw) → 11-char stable correlation handle. `hash`: first 16 hex chars of SHA-256(raw) only. `drop`: `"dropped"` sentinel. Invalid value fails-fast at `NewDetector` startup. Tests `TestNetworkDetector_PIIMode_{Pseudonymize,Hash,Drop}` cover all 3 modes with explicit SHA-256 round-trip assertions.
- **§9.3 risk classification** — `quarantine` tenant → `risk=high`; missing-attach (workload not in `Config.KnownAttachWorkloadIDs`) → `risk=medium`; stale heartbeat (last-attach older than `HeartbeatStaleThreshold`, default 5 min) → `risk=high`; fresh attach → `risk=low`. Tested via `TestNetworkDetector_RiskClassification_{NoAttach,StaleHeartbeat,QuarantineTenant}`.
- **§6.1 tenant + principal precedence** — `workload_identity` (Config.WorkloadTenantMap hit) → `oidc` (Config.OIDCTenantMap hit) → `quarantine`. Recorded in `finding.TenantSource` / `finding.PrincipalSource` via the typed-source enum constants `TenantSourceWorkloadIdentity` / `TenantSourceOIDC` / `TenantSourceQuarantine` (and Principal- equivalents). Tested via `TestNetworkDetector_TenantMapping_WorkloadOIDC` with 3 sub-cases.
- **EDGE-141 reuse** — findings persist via `shadow.Store.CreateFinding` with `SourceType=shadow.SourceTypeNetwork`, `SourceID="network:<Config.SourceID>"`, `Hostname=<provider host>`, `RedactedPath=shadow.RedactPath("network://<category>/<endpoint_hash>")`, `EvidenceType="network_direct_provider_traffic"`, `SignalSet=["direct_provider_traffic"]`, `RetentionClass=shadow.ShadowRetentionDefault`, `FirstSeen`/`LastSeen=now`. No parallel finding store, no parallel emit pipeline.
- **Observability** — `Observer` interface emits `RecordFindingEmit(signal, risk, ingestSource)`, `RecordPIIModeActive(mode)` (gauge, set once at `NewDetector`), `RecordLogRecord(ingestSource, result)` (per-record counter; results: `emitted`/`skipped_unknown_host`/`skipped_no_hostname`/`store_error`/`process_error`), and `EmitAudit(audit.SIEMEvent{Action:"shadow_agent.observed", Decision:"observed", Extra:{finding_id, source_type=network, signal, hostname, category, count_bucket, tenant_id, tenant_src, principal_src, risk}})`. Bounded labels only — tenant / workload / hostname / category are NEVER metric labels (high cardinality); they live on the finding + audit Extra payload. `NoopObserver` is the safe default when callers have not wired observability.
- **Tests** — 13 scenario tests covering 3 ingest sources, NG7 grep-walk, §9.1 catalog enforcement with adversarial bannedURL / bannedBearer / bannedIP record, all 3 PII modes, all 3 risk classes, all 3 tenant-mapping precedence tiers, full §10.1 typed-field assertion, and observability spy. `go test ./core/edge/shadow/network/ -count=3` → ok 1.245s, 0 failures, 0 flakes. `go vet ./core/edge/shadow/network/...` clean.
- **Documentation** — `docs/edge/shadow-scanner.md` §9.3 extended with the operator-facing summary; `docs/edge/managed-settings-deploy.md` §11 adds the **GDPR / UK-DPA processing-record template** per Q2 (data categories, lawful basis, retention, controller / processor split, DSAR contact). No new top-level doc, no audit.md update, no dashboard surface — strictly an additive library landing.

#### EDGE-104 — cordumctl mcp attach/preview/rollback for Claude Code, Codex, and Cursor (2026-05-16, task-9351f243)

- `cmd/cordumctl/mcp_attach.go` + `mcp.go` — new `cordumctl mcp <preview|attach|rollback>` verbs alongside existing `pending|approve|reject|tools|keygen|upstream`. `attach` requires `--apply` to write; without it falls through to a preview run so operators see exactly what would change.
- `cmd/cordumctl/mcp_attach_common.go` — shared `AttachAdapter` interface + lifecycle helpers (`PreviewAttach`, `ApplyAttach`, `RollbackAttach`). Backup-on-modify via `<path>.bak.<unix_ms>` with deterministic newest-first restore. Atomic writes via tempfile + `os.Rename` (cross-fs fallback to copy+remove). Mode `0600` on written files. Secret redaction in preview output masks `sk-*`, `ghp_*`/`gho_*`, `Bearer <token>` patterns before any stdout write.
- `cmd/cordumctl/mcp_adapter_claude_code.go` — targets `~/.claude.json`, user-scope `mcpServers` map. HTTP/SSE entries render as `{type, url}`; stdio as `{command, args}`. Preserves all sibling keys (project list, history, theme) verbatim via `map[string]any` round-trip + stable-sorted re-serialize.
- `cmd/cordumctl/mcp_adapter_codex.go` — targets `~/.codex/config.toml`. Hand-rolled byte-level splicer rather than adding a TOML library dep: locates `[mcp_servers.<id>]` blocks, replaces in place for an existing gateway entry, appends with a blank-line separator for a new one. Operator-authored comments + whitespace in non-mcp_servers sections are byte-preserved. HTTP gateways render as a stdio invocation of `cordumctl mcp proxy --endpoint <URL>` since Codex's documented MCP transport is stdio-only.
- `cmd/cordumctl/mcp_adapter_cursor.go` — targets `~/.cursor/mcp.json`. Same JSON-merge primitive as Claude Code; only the stdio entry shape differs (Cursor docs require explicit `type: "stdio"`).
- Schema provenance constants on every adapter (`<Client>SchemaURL` + `<Client>SchemaDate`, fetched 2026-05-16 from current docs). Exposed via `AttachSchemaProvenance(client)` for audit scripts.
- Tests: `cmd/cordumctl/mcp_attach_test.go` — 11 test functions × 3 clients (`claude_code`, `codex`, `cursor`) covering preview missing/existing/malformed, apply creates-new/backs-up-existing, rollback restores/missing-backup-fails, secret redaction, idempotency, per-platform default path resolution (Linux + Windows home shapes), and dispatcher-level `attach` without `--apply` writes nothing.
- Attach is the convenience/adoption path; for enterprise enforcement use the managed-settings flow (`cordumctl edge managed-settings export`, EDGE-150). The canonical cordum-gateway upstream config comes from the EDGE-101 registry (`core/edge/mcp_upstream_registry.go`).
- Docs: `docs/mcp-server.md` § "Attach Commands (EDGE-104)" covers subcommand surface, per-client paths, backup semantics, rollback, secret redaction guarantees, and schema-version tracking.

#### EDGE-103 reopen #1 — approval-required payload + Edge mint dual-write (2026-05-16, task-968d6646)

- `core/mcp/registry.go` — `ApprovalRequired` struct extended with
  `ApprovalRef`, `ArgsHash`, `ExpiresAt`, `PolicySnapshot`, `RetryHint`
  (all `omitempty`). Resume authority is now `ApprovalRef`; `ApprovalID`
  stays for backward-compat SIEM correlation only. Doc comment makes the
  contract explicit so client SDKs branch on the right handle.
- `core/mcp/approval_hold.go` — new exported `BuildMCPApprovalBinding(tenant, server, params, policySnapshot) (actionHash, inputHash)` centralises the hash tuple that mint + consume MUST agree on. Both sides call this helper so a refactor cannot accidentally drift one path's hashing and surface `args_mismatch` on every legitimate retry. `ProcessApprovalClaim` rewired to use the helper.
- `core/controlplane/gateway/mcp_gate.go` — `gatewayApprovalGate` gains `edgeStore` / `policySnapshot` / `serverName` fields wired via new `WithEdgeApprovalMint`. `gate.Check` now populates the full `ApprovalRequired` contract and dual-writes an `EdgeApproval` alongside the legacy `MCPApprovalStore` record when `mcp.CallMetadata` carries `SessionID`/`ExecutionID` — the Edge ref becomes the resume handle. Falls back to legacy-only (approval_ref == approval_id, resumable via retry-with-same-args) when Edge metadata is absent so HTTP MCP transit without an `EdgeSession` keeps working.
- `core/controlplane/gateway/handlers_mcp.go` — `wireMCPApprovalGate` calls `gate.WithEdgeApprovalMint(s.edgeStore, s.mcpPolicySnapshotFunc(), mcpPolicyServerName)` when `s.edgeStore` is non-nil so production boot exercises the dual-write path automatically.
- Tests: `core/controlplane/gateway/mcp_gate_test.go:TestGate_ApprovalRequiredCarriesResumeMetadata` (new field assertions), `core/mcp/approval_hold_test.go:TestBuildApprovalClaimRequest_MintAndConsumeProduceMatchingHashes` + `TestBuildMCPApprovalBinding_StripsApprovalRef` (helper determinism + `_approval_ref` strip), `core/mcp/approval_hold_test.go:TestProcessApprovalClaim_TypedConflictKind` extended with `consumed`/`tuple_mismatch`/`cross_tenant`/`rejected` subtests (all 7 production `ApprovalConflictKind` values), `core/controlplane/gateway/mcp_policy_boot_test.go:TestMCPProdBoot_ApprovalHoldWiredWhenFlagOn` (HasApprovalHold==true regression guard for QA's prior dead-path issue), and `core/mcp/server_approval_hold_e2e_test.go` (4 new JSON-RPC E2E tests: -32099 envelope contract, -32096 args_mismatch lifecycle, consume-once + args-strip dispatch, gateway-misconfigured on missing CallMetadata).

#### EDGE-101 - MCP upstream server registry (2026-05-16, task-fb11aa72)

- New `/api/v1/edge/mcp/upstreams` registry API plus `cordumctl mcp upstream`
  subcommand for managing approved upstream MCP servers. Entries store
  name/transport/endpoint-or-command/tenant/auth-secret-ref/labels/risk/enabled
  metadata and keep disabled records visible to admins by default.
- Validation rejects raw secrets (`secret://` refs only), unsafe strict-mode
  local endpoints, shell-metacharacter `stdio` commands, tenant/name key escapes,
  and enterprise-strict entries that are not on the MCP allowlist.
- Updates write a 30-day backup of the previous registry record before overwrite,
  with collision-resistant backup keys for rapid consecutive updates.
- Builds on the EDGE-100 MCP gateway skeleton; EDGE-104 attach/preview commands
  and EDGE-105 dashboard surfaces consume this registry. Structured gateway logs
  emit `mcp-upstream-<op>` outcomes without secret refs or values.

#### REQUIRE_HUMAN threshold routing in safety kernel (2026-05-16, task-96f931fe reopen #1)

- Supersedes the rejected `cf40ce81` (deleted in `75ed120d`, 1138 LOC
  removed). Rejected predecessor placed 1350 LOC in
  `core/policy/actiongates/*` (forbidden by governor amendment
  `comment-e58c8328`) and used a 3-output model with an
  EducationalContext carrier that does not exist in the architecture
  (carved out by architect amendment `comment-79a9e609`). This entry
  describes the replacement implementation.
- New `core/infra/config/safety_policy.go` `RequireHumanThreshold`
  struct (`MinSeverityForDeny string`, `MinConfidenceForDeny float32`,
  `DowngradeWhenPromptOnly bool`) wired as `SafetyPolicy.RequireHuman`.
  Zero value preserves legacy DENY-on-match behavior; operators opt in
  per-tenant via YAML.
- Safety-kernel input-rule dispatch in
  `core/controlplane/safetykernel/kernel.go` now consults the threshold
  inside the matched-rule loop. A `deny`-authored rule downgrades to
  `pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN` when any of three
  conditions hold (logical OR): finding severity below the floor,
  finding confidence below the floor, or
  `DowngradeWhenPromptOnly && input.Action == nil`. Action-bound
  high-severity high-confidence DENYs are unchanged — the
  "unchanged from today" branch the architect amendment carved out.
- 2-output model only. No new `pb.DecisionType` value, no
  EducationalContext field, no `input_text`-derived trust source.
  Audit / dashboard / approval-store paths consume the downgraded
  decisions through the existing `REQUIRE_HUMAN` surface.
- `core/controlplane/safetykernel/input_policy.go` carries the
  `shouldDowngradeDenyToRequireHuman` helper + `severityRank` ordinal
  mapping. 9 GREEN unit tests in
  `core/controlplane/safetykernel/decision_threshold_test.go`:
  5 FP scenarios (defensive `/etc/passwd`, `rm -rf` mention,
  API-key rotation, approval-token logging, metadata-service
  education) assert `DENY → REQUIRE_HUMAN`, plus action-bound
  stays-DENY guard, zero-threshold legacy guard, rule-tier severity
  floor precedence, and severityRank mapping. `-count=3` flake check
  clean.
- Structured log `input rule matched` now records the resolved
  `outputDecision` field so operators see at a glance when a
  `deny`-authored rule routed to `require_human` instead.
- Docs: `docs/safety/decision-thresholds.md` rewritten end-to-end to
  reflect the 2-output dial, the routing table, the 5 FP scenarios,
  the anti-patterns (no session-metadata carrier, no `input_text`
  trust), and the implementation references.
- DoD #6 (AgentShield holdout regression) is **deferred**:
  `agentshield-benchmark` is not our repo per
  `[[feedback_dont_touch_agentshield_benchmark]]`; verifying real
  numeric over-refusal reduction requires running the AgentShield
  regression against a gateway built from this commit which is a
  separate operational task. Functional correctness is verified by the
  9 unit tests above + the full `safetykernel` suite passing
  (`go test ./core/controlplane/safetykernel/... -count=1`).

#### EDGE-102 follow-up — Wire MCPServer.WithPolicyGate at gateway boot (2026-05-16, task-e9d9a37d, bundles task-3d5c4f37)

- Gateway boot path (`core/controlplane/gateway/handlers_mcp.go`
  `startMCPRuntimeFromConfig`) now calls `MCPServer.WithPolicyGate` +
  `WithApprovalHold` against real backing stores when the new
  `mcp.policy_gate_enabled` config flag is true. Default false per
  `feedback_dont_change_deployment_defaults` — operators opt in
  explicitly per deploy; missing config key leaves the gate off.
- New `core/controlplane/gateway/mcp_policy_boot.go` introduces two
  small production adapters that the EDGE-102 surface previously
  routed through noop fallbacks:
  - `edgeStoreEventEmitter` adapts the existing `*edgecore.RedisStore`
    (the same instance `s.edgeStore` Edge events already land on) to
    `mcp.EventEmitter` — `mcp.tool.pre` / `mcp.tool.post` /
    `mcp.tool.failed` events now persist alongside hook + LLM events
    on one canonical stream.
  - `productionArtifactStore` adapts the existing `artifacts.Store`
    (the Redis-backed pointer store the export bundler reads) to
    `mcp.ArtifactStore` — oversized redacted MCP-request/response
    payloads land in artifact storage with content-addressed SHA-256
    pointers carrying tenant/session/execution/event labels for the
    dashboard's evidence-export pivot.
- Approval-hold consume path (`_approval_ref` arg, EDGE-103) now
  resolves claims through `edge.RedisStore.ClaimApproval` with a
  `PolicySnapshot` closure sourced from `loadPolicyBundles` so the
  consume-side snapshot matches mint-side (closes the c530c1c0
  ServerName + snapshot guard from PR #276).
- Bundled scope from task-3d5c4f37: ALLOW_WITH_CONSTRAINTS gate
  decisions now propagate the `_constraints` map through to the
  emitted pre + post + failed events. `AgentActionEvent.Constraints`
  field added (omitempty so legacy ALLOW events keep their wire
  shape); `newPostEvent` derives `Decision` via
  `mapPolicyDecisionToEdge` so an AWC verdict records
  `Decision=constrain` rather than degrading to `allow` on the post
  event. Shape mirrors `core/edge/agentd EvaluateResponse.Constraints`
  per the "No parallel subsystems" epic rail.
- Operator-facing boot log:
  `slog.Info("mcp.policy_gate wired", server_name=cordum.builtin, policy_gate_active, approval_hold_active, emitter, artifact_store)`
  when the flag is on; `slog.Info("mcp.policy_gate skipped", reason)`
  when off. One greppable line per acceptance criterion #4.
- `logToolCallDecision` now records `slog.Int("constraint_count", ...)`
  so AWC bursts are greppable from the live log stream; constraint
  VALUES are never logged (CLAUDE.md security rail).
- New `MCPServer` accessors `HasPolicyGate() / PolicyServerName() /
  PolicyEventEmitter() / PolicyArtifactStore() / HasApprovalHold()`
  double as boot-log inputs and stable surfaces for boot-wiring
  tests / future dashboard health probes.

#### EDGE-140 — Local shadow-agent scanner observe mode (2026-05-16, task-74ac5153)

- New `core/edge/shadow/` package implements an opt-in P3 local scanner
  that detects likely-unmanaged Claude Code / Codex / Cursor MCP
  configurations, known agent process names, and known agent-credential
  env-var names. Observe-mode only: zero enforcement actions, zero
  filesystem mutation, zero subprocess invocation. The static-source
  TestScannerRefusesEnforcement guard greps the package for
  `os.Remove` / `os.WriteFile` / `os.Rename` / `os.RemoveAll` /
  `exec.Command` / `"os/exec"` and fails the build if any appears in a
  non-test file (task rail #2 'no enforcement').
- Privacy boundary: the scanner reads only structural JSON / TOML
  fields (`mcpServers` keys + transport + endpoint hostname) — never
  command-lines, env-var values, prompt content, or any field outside
  the recognised schema. Defence-in-depth `RedactConfigSummary`
  regex-strips 8 secret-shape patterns (`sk-`, `sk-ant-`, `ghp_`,
  `gho_`, `xoxb-`, `Bearer`, `BEGIN PRIVATE KEY`, `BEGIN CERTIFICATE`)
  and bounds output to ≤2048 bytes.
- Managed-config skip: configs carrying the EDGE-150
  `CORDUM_EDGE_MANAGED_POLICY_MODE=enterprise-strict` invariant emit
  `Finding{Status:"managed_skip"}` rather than a shadow flag, so
  enterprise-managed fleets are not drowned in false-positive alerts
  (DoD #4 'managed config not flagged').
- New `cordumctl shadow scan` subcommand wires the scanner with three
  CLI flags: `--enable-shadow-scan` (opt-in), `--output <path>` (mode
  0600 JSONL output; default stdout), `--tenant` / `--principal`
  (override attribution fields). The env-var
  `CORDUM_EDGE_SHADOW_SCAN_ENABLED=true|1|yes` is honoured as an
  equivalent opt-in. Default invocation (no flag, no env) prints a
  polite no-op message and exits 0 so CI pipelines can include the
  command unconditionally.
- Cross-platform process enumeration via
  `github.com/shirou/gopsutil/v3 v3.24.5` (plus 7 indirect transitives
  including go-ole, plan9stats, perfstat, m1cpu, go-sysconf, numcpus,
  wmi); test seam `WithProcessLister` injects a mock so unit tests are
  OS-independent. Symlink-attack hardening via `os.Lstat` (refuses to
  follow); large-config OOM hardening via `io.LimitReader` at 1 MiB
  cap with `Status:"partial"` reporting.
- Threat model + operator runbook: `docs/edge/shadow-scanner.md`
  documents the trust boundary, opt-in gate, detection sources, finding
  schema, managed-config skip semantics, and operational guidance.
  Explicitly NO dashboard surface in this task (task rail #3 'Shadow
  Agents were cut from P0'); future EDGE-141 adds the server-side
  finding store, EDGE-142 the remediation-hint generator, EDGE-143 the
  P3 dashboard design.

#### EDGE-100 — P1 MCP Gateway service skeleton (2026-05-16, task-0ffcac35)

- New `core/mcp/gateway_skeleton.go` exposes `RegisterGatewayRoutes(mux, deps)` +
  `NewGateway(deps)` and four `*Gateway` handlers (HandleHealth, HandleConfig,
  HandleUpstream, HandleClientConnect) for the per-tenant
  `/api/v1/mcp/gateway/*` route family. Reuses `edge.Store` for evidence
  persistence (no parallel store) and surfaces tenant resolution +
  gateway-enable lookup as injectable function fields on `GatewayDeps` so the
  API gateway can plug in its existing `resolveTenant` /
  `requireTenantAccess` / `auth.FromRequest` helpers without inventing
  wrapper types.
- `core/infra/config/mcp.go` now owns `MCPPolicy`, including
  `GatewayEnabled bool`, `UpstreamServers []UpstreamServerConfig`, and
  `AllowedUpstreams []string`. Default `false` ships fail-closed per DoD #1;
  EDGE-101 will populate the runtime upstream registry and wire per-tenant
  enable lookup from the config snapshot. Existing single-server MCP
  endpoints preserved; gateway-mode is strictly additive.
- `core/controlplane/gateway/gateway.go` adds `mcpGatewayHandlers(s)` helper
  that constructs the gateway against `s.edgeStore` (or substitutes a
  503 `gateway_unavailable` stub when the store is unavailable) and
  registers all four routes through the existing `s.registerRoute` +
  `s.instrumented` pipeline. Route table + OpenAPI coverage tests both
  pass.
- Event kinds `mcp.server.connected` + `mcp.server.failed` (pre-existing
  at `core/edge/event.go:51-52`) are now emitted only through
  store-supported EdgeSession + AgentExecution evidence roots with the
  resolved tenant + principal — never from body claims (task rail #3).
  Connect-success creates EdgeSession + AgentExecution + connected event.
  The P1 failed-event case is GatewayEnabled=true with no upstream
  configured; bootstrap/store failures before an AgentExecution exists are
  logged structurally instead of being claimed as orphan events.
- New OpenAPI operations: `getMcpGatewayHealth`, `getMcpGatewayConfig`,
  `postMcpGatewayUpstream`, `postMcpGatewayClientsConnect`.
- New tests: `core/mcp/gateway_skeleton_test.go` and
  `core/mcp/gateway_skeleton_redis_test.go` cover health,
  config-redaction, disabled-default zero records, no-upstream failed-event
  persistence via real RedisStore, tenant attribution, missing tenant,
  append-failure handling, and no orphan failure events; plus
  `core/edge/event_mcp_server_test.go` (2 wire-string constants).
- Dashboard surfacing of gateway events deferred to EDGE-105
  (task-a04699dc) — no dashboard files touched in this task.
- EDGE-101 (upstream registry) + EDGE-104 (real client attach) + EDGE-105
  (dashboard surface) build on this contract.

#### EDGE-152 — cordum-agentd keychain + service bootstrap hardening (2026-05-15, task-00320a80)

- New `core/edge/keychain` package wraps the host OS-native credential
  store (macOS Keychain, Linux Secret Service / libsecret, Windows
  Credential Manager) behind a small `Keyring` interface with a mock
  for tests and a `LoadSecret` helper that selects between strict and
  dev bootstrap policies. Backed by `github.com/zalando/go-keyring
  v0.2.8`.
- `cmd/cordum-agentd` now sources `CORDUM_AGENTD_NONCE` +
  `CORDUM_API_KEY` from the OS keychain at startup before `LoadConfig`
  consumes the env map. Strict mode
  (`CORDUM_AGENTD_STRICT=true` or `CORDUM_EDGE_POLICY_MODE=enterprise-strict`)
  fails closed with a `BOOTSTRAP-FAIL:` diagnostic when the keychain
  is unavailable or unprovisioned; dev mode emits a structured warn
  and falls back to the env value (redacted in logs). The pre-existing
  `redactForStderr` / strict-mode codepath is unchanged. Closes the
  EDGE-031 P0 tradeoff where same-user `ps -E`, `/proc/<pid>/environ`,
  and shell history could expose runtime credentials.
- Service-manager templates: `tools/scripts/launchd/com.cordum.agentd.plist`
  (user-mode launchd), `tools/scripts/systemd/cordum-agentd.service`
  (systemd `--user`, hardened with `NoNewPrivileges` /
  `ProtectSystem=strict` / `SystemCallFilter`), and
  `tools/scripts/windows/cordum-agentd-service.xml` (WinSW). All three
  carry only `CORDUM_AGENTD_STRICT=true` + log level — **no
  secret-bearing env entries**. Provisioning helpers
  `tools/scripts/agentd-install/install.sh` (macOS / Linux) and
  `install.ps1` (Windows) read secret values through sealed prompts
  (`stty -echo` / `Read-Host -AsSecureString`) so values never appear
  on the operator's command line or in shell history.
- Adversarial fixture
  `tools/scripts/agentd-install/synthetic-test/run.sh` provisions
  synthetic test-only secrets, starts cordum-agentd in strict mode,
  and `grep -F`s stdout / stderr / journald / committed unit files for
  the verbatim synthetic bytes. Non-zero exit on any leak.
- Threat model + ops runbook: `docs/security/agentd-keychain.md`
  documents the per-platform mapping, strict/dev mode matrix, trust
  boundary (PREVENTS env-table exposure, shell history, settings.json
  carrying secrets; DOES NOT PREVENT root keychain dump, memory dump,
  social engineering), key-rotation ritual, and structured-log audit
  schema (`keychain.load`, `keychain.env_fallback`,
  `keychain.load.miss`, `keychain.load.unavailable`).
- Dashboard surface for agentd bootstrap status is deferred to a
  sibling task; this work touches no `cordum/dashboard/` files. Sibling
  enterprise-hardening series: EDGE-150 (managed-settings, this
  Unreleased section) and EDGE-151 (binary signing/notarization).

#### EDGE-151 — Hook and agentd binary signing/notarization (2026-05-15, task-909be4cb)

- New `tools/sign/` Go package implements release-time binary integrity:
  pure-Go OpenPGP detached-signature verification over a cosign-compatible
  `SHA256SUMS` manifest, per-binary SHA-256 recomputation in constant time
  via `crypto/subtle`, manifest-path-traversal rejection, and a build-time
  pinned `PinnedReleaseFingerprint` (`-ldflags '-X
  github.com/cordum/cordum/tools/sign.PinnedReleaseFingerprint=<hex>'`)
  that defeats single-file `cordum-release.pub.asc` substitution.
- Release CI (`.github/workflows/release.yml`) ships a two-tier scheme on
  every `v*` tag: Tier 1 always-on GPG-signed `SHA256SUMS` manifest
  produced via `tools/sign/cmd/manifest-cli`, and Tier 2 OS-native code
  signing (Apple Developer ID `codesign --options runtime --timestamp
  --deep --strict` + `xcrun notarytool submit --wait` for darwin
  amd64/arm64; Windows Authenticode `signtool sign /tr digicert /td
  sha256 /fd sha256` for windows amd64) gated on
  `github.repository == 'cordum-io/cordum'` and the relevant secrets.
  Forks without OS-native secrets degrade to Tier 1 with a `::warning::`
  banner and an unsigned-but-hashed manifest. The 5-platform × 3-binary
  matrix (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64,
  windows/amd64 × cordum-hook, cordum-agentd, cordum-claude) is
  cross-compiled on `ubuntu-latest` with `CGO_ENABLED=0`.
- PR-level validation (`.github/workflows/binaries-pr-validation.yml`)
  runs `tools/sign` unit tests + `make release-local` + synthetic
  tampered / unsigned scenarios via `tools/scripts/install_test.sh`,
  plus a key-extension grep guard that fails on any private-key block
  outside `tools/test-keys/TEST-ONLY-*`. The PEM pattern is constructed
  at runtime so the workflow file does not self-match.
- Pre-activation gate in `tools/scripts/install.{sh,ps1}` curls or
  reads the release-dir, imports the trusted pubkey into an ephemeral
  GNUPGHOME, refuses on `unsigned manifest` / `gpg signature invalid` /
  `hash mismatch <name>` / `release pubkey fingerprint <got> does not
  match pinned <want>` / `manifest path traversal` / `post-activation
  hash mismatch` / `codesign verify failed`. Atomic same-fs `mv` with
  cross-fs `cp+rename` fallback, SHA-256 recomputed AFTER move
  (defence-in-depth against sig-then-swap race), `chmod +x`. Dev mode
  via `--dev-allow-unsigned` (`-DevAllowUnsigned` on PowerShell)
  accepts only `tools/test-keys/TEST-ONLY-*` material whose UID carries
  the literal `TEST-ONLY` marker and whose fingerprint is not equal to
  the production pin. Audit-event JSON line per outcome emitted to
  stderr: `{event, hash, path, sig_scheme, fingerprint, reason,
  exit_code}` — no secrets, no full paths.
- `make release-local` (alias `tools/scripts/release-local.sh`) produces
  a host-local dev release in `bin/release-local/` signed by the
  committed TEST-ONLY key under `tools/test-keys/TEST-ONLY-release.*`.
  The TEST-ONLY keypair is regenerable via `tools/test-keys/gen.sh
  [--deterministic]`. Existing hook/agentd fail-closed runtime path
  unchanged — EDGE-151 is release-time integrity only and does not
  modify any file under `cmd/cordum-hook/` or `cmd/cordum-agentd/`
  (task rail #1).
- Threat model + operator runbook: `docs/security/binary-signing.md`
  enumerates what the gate prevents (transit tampering, non-root local
  substitution, accidental corruption) and what it does NOT prevent
  (full-root coordinated swap, GitHub Actions secret compromise,
  Developer ID / Authenticode `.pfx` leak, downgrade attack — OUT OF
  SCOPE per sibling `EDGE-151-DOWNGRADE`, build-environment supply
  chain). Documents pubkey pinning via `-ldflags`, the dual-sign
  rotation procedure, and the BINARY-VERIFY-FAIL triage table.
- Production pubkey provisioning is deferred until Yaron lands the GPG
  release secret triple (`GPG_RELEASE_KEY_PRIVATE`,
  `GPG_RELEASE_KEY_PASSPHRASE`, `RELEASE_FINGERPRINT`); until then the
  install path operates in dev mode only. Dashboard surfacing of
  binary-verify outcomes is deferred to sibling `EDGE-151-DASHBOARD`;
  this work touches no `cordum/dashboard/` files.

#### EDGE-150 — Enterprise managed-settings deployment automation (2026-05-15, task-ebed169a)

- New `cordumctl edge managed-settings <export|verify|rollback-template>`
  subcommand renders `managed-settings.json` + `managed-mcp.json` payloads
  for managed Claude Code workstations, validates a deployed file against
  the 14 enterprise invariants, and re-renders the template atomically for
  synthetic-test rollback. The CLI is operator/MDM-script invoked;
  Cordum never calls Jamf, Intune, or any other MDM API directly.
  (`cmd/cordumctl/edge_managed_settings.go`,
  `cmd/cordumctl/edge_managed_settings_test.go`).
- `core/edge/claude/managed_settings_verify.go` exposes
  `VerifyManagedSettings`/`VerifyManagedSettingsFromPath`, a pure-function
  drift detector that enforces every invariant baked into the template
  generator (`allowManagedHooksOnly`, `disableBypassPermissionsMode`,
  the six required hook families, the three managed-mode env vars,
  `CORDUM_AGENTD_URL` shape, and a serialised-form scan that rejects
  nonce/API-key markers). Bounded by an `io.LimitReader` cap to prevent
  OOM on hostile input.
- `cordumctl edge doctor` adds a `managed_settings_compliance` check
  driven by `--managed-settings-path` /
  `CORDUM_EDGE_MANAGED_SETTINGS_PATH`. Empty path → `skip` so
  non-enterprise hosts do not see a spurious failure; missing file,
  parse error, or any drift → `fail` with a redacted detail line.
- New end-to-end deployment runbook
  `docs/edge/managed-settings-deploy.md` covering the macOS/Jamf
  worked example, Windows/Intune (Settings Catalog + OMA-URI + file),
  Linux/WSL Ansible, post-deploy verification, drift-detection
  cadences, MDM-orchestrated rollback (with the explicit "synthetic
  test fixture only" disclaimer for the CLI rollback path), upgrade
  flow, and operator troubleshooting. Cross-linked from
  `docs/edge/README.md`, `docs/edge/managed-settings-template.md`,
  `docs/edge/cordumctl-edge-doctor.md`, and `docs/edge/cli.md`.

### Fixed

#### core/controlplane — Fail-closed output policy + scheduler retry caps on Redis read errors (2026-05-20, task-8b4077ba)

- Output policy now fails closed when a result-pointer Redis `Get` returns a non-`redis.Nil` error: it emits a WARN with the pointer key and quarantines the output with a `pointer_unreadable` finding instead of scanning empty bytes and allowing by default.
- Scheduler dispatch now preserves retry caps when `JobStore.GetAttempts` is unreadable: max-scheduling and policy `max_retries` gates log WARN context and pessimistically treat the unknown attempt count as the cap, preventing poison-pill redis-timeout retry loops.
#### Audit Log dashboard surfaces the full SIEM feed (2026-05-15, task-00b82b90)

- The Audit Log page was sourcing only `/policy/audit` (policy-bundle subset:
  rule edits, bundle deployments, signature events) and silently omitting
  every other SIEM-chained event family: MCP tool invocations and approvals,
  Edge action attempts and approvals, worker handshakes and trust changes,
  output policy decisions, delegation lineage, auth events. Operators using
  the dashboard could not see the actions they most needed to audit.
- New `GET /api/v1/audit/events` (`handlers_audit_events.go::handleListAuditEvents`,
  route registered at `gateway.go` § 2.7.2) walks the per-tenant Redis Stream
  populated from NATS `sys.audit.export` via `audit.Chainer`. Cursor
  pagination (opaque Redis Stream IDs), default page 100, hard cap
  `MaxAuditEventsLimit = 200`. Filters: `event_type`, `severity`, `from`/`to`
  (RFC3339), `search` (lowercase substring across action / event_type /
  agent_id / job_id / identity / reason). `auth.PermAuditRead` permission gate.
  Strict tenant scoping via `resolveTenant` + `requireTenantAccess`. 503
  `audit_chainer_not_installed` when the chainer is absent. Defense-in-depth
  `redactExtraSecrets` strips keys matching `(?i)secret|token|password|api[_-]?key|private[_-]?key`
  before serialization. Every successful read emits an `audit.read.events`
  meta-event to the same tenant's stream, closing the audit-of-audit loop.
- OpenAPI spec extended with `getAuditEvents` operation + `AuditEvent` /
  `AuditEventsEnvelope` schemas (`docs/api/openapi/cordum-api.yaml`); orval
  regenerated `useGetAuditEvents` (`dashboard/src/api/generated/audit-export/`).
- Dashboard rewired: `AuditLogPage.tsx` now calls `useGetAuditEvents`,
  with a new `mapEvent` translating the SIEM shape (`identity || agent_id`
  → actor, event_type prefix → resource family, `extra.resource_id` chain
  → resourceId, `reason` → detail, `seq` direct from the chained event) to
  the existing on-screen `AuditEvent` row shape. Event-type filter dropdown
  expanded to include Safety / Policy / MCP / Edge / Worker / Topic / Auth /
  Delegation / License / Action-gates families.
- `useAuditEvents` hook in `src/hooks/useAuditEvents.ts` translates 503 →
  human-readable banner ("Audit chain not installed — contact your
  operator"). The pre-existing `useAudit` hook remains in use for the
  policy-bundle drilldown, correlation, and export paths.
- New reference doc: `docs/audit/list-api.md` (contract, tenant scoping,
  cursor stability under concurrent appends, 503 condition, redaction
  defense-in-depth, distinction from `/audit/verify` and `/policy/audit`).
- Tests: 10 backend subtests in `handlers_audit_events_test.go` covering
  permission gating, tenant scoping, cursor pagination stability,
  event_type/time-range filters, 503 condition, secret redaction with a
  full-body regex assertion, bounded limit clamp, empty-stream
  empty-response, and meta-audit emission. Plus
  `TestHandleAuditEvents_HeavyFilterPagesForward` regression for the
  heavy-filter cursor-forward gap (adversarial-review finding).
  Dashboard suite extended with `useAuditEvents.test.ts` (9 cases now
  including two infinite-pagination regressions), `transform.test.ts`
  `mapAuditEvent` describe block (5 cases), and
  `AuditLogPage.siem.test.tsx` (render-level SIEM coverage with
  NuqsTestingAdapter + MSW).
- Reopen #1 fix (QA finding): Audit Log page now consumes
  `useInfiniteAuditEvents` (new TanStack `useInfiniteQuery` variant in
  `src/hooks/useAuditEvents.ts`) so tenants with >200 SIEM events can
  reach older records via the server's `next_cursor`. A "Load more"
  button below the table fetches the next page; the running-count line
  shows "Showing N events · more available" when the cursor is
  non-empty. The page bundles all loaded pages into a single
  client-side flat list for filter + render. Trailing-whitespace lint
  on `docs/audit.md` (CRLF line endings from a pre-existing block plus
  the new cross-link) resolved by normalising the file to LF.

### Added

#### EDGE-103 — MCP approval hold and resume (2026-05-15, task-968d6646)

- `core/mcp/approval_hold.go` (NEW) — `ProcessApprovalClaim` helper that inspects `tools/call` arguments for a server-reserved `_approval_ref` field, atomically consumes the stored approval via `edge.RedisStore.ClaimApproval`, and returns a typed `ApprovalClaimOutcome` carrying `*edge.ApprovalConflictError` on a fail-closed lifecycle conflict. The `_approval_ref` field is stripped BEFORE the upstream tool handler ever sees the payload; the input hash is bound to the stripped form so a caller-side mutation between hold and resume produces an `args_mismatch` denial.
- `core/mcp/server.go` — new JSON-RPC code `-32096` (`approval_lifecycle_error`) distinct from `-32099` (initial `approval_required`). `handleToolsCall` now calls `ProcessApprovalClaim` BEFORE invokeTool when `WithApprovalHold(deps)` is wired. On any `*edge.ApprovalConflictError` the server returns `-32096` with `error.data {kind, approval_ref, reason}`. The snake_case `kind` enum (`not_found / rejected / expired / consumed / args_mismatch / policy_mismatch / tuple_mismatch / self_approval / cross_tenant`) matches the Go `edge.ApprovalConflictKind` so the wire format and the typed error never drift.
- `core/edge/approval_store.go` — new `ApprovalConflictKind` enum + `ApprovalConflictError{Kind, Reason}` wrapping `ErrApprovalConflict` (existing `errors.Is(err, ErrApprovalConflict)` callers keep working). New `ApprovalClaimRequest.CallerAgentID` field for store-level self-approval defense in depth (refused when matching `approval.Requester` OR `approval.ResolverID`).
- `core/edge/store_redis.go` — new `RedisStore.approvalMaxTTL` field + `WithApprovalMaxTTL(d time.Duration) StoreOption`. EnqueueApproval clips a caller-supplied `ExpiresAt` longer than `(createdAt + max)` to that ceiling so a malicious or buggy caller cannot park an approval indefinitely. Non-positive value disables clipping (legacy behaviour preserved).
- `core/edge/approval_store_redis.go` — replaced `approvalClaimMatches` with `classifyApprovalClaimMismatch` returning typed `ApprovalConflictKind`. Self-approval check evaluated FIRST (attacker-surface priority) so a self-approval attempt cannot be masked by simultaneously mutating a benign field. `ClaimApproval` uses `newApprovalConflict(Kind, reason)` so the JSON-RPC layer can dispatch on `errors.As(*ApprovalConflictError)`.
- New documentation: `docs/edge/mcp-approval-hold.md` (protocol overview, JSON-RPC error catalogue + `kind` enum, args canonicalization, policy-snapshot binding, TTL bounds, consume-once, self-approval defense-in-depth at 3 layers, cross-tenant rationale, audit-event schemas).

#### EDGE-102 — MCP tool-call policy gate (2026-05-15, task-032e01fa)

- MCP `tools/call` requests now route through the Edge action-policy pipeline before upstream forwarding. New entry point `core/mcp.InvokeToolWithPolicy` wires `EvaluateToolCall` → `actiongates.Pipeline` (tenant → file → url → mcp → mutation → provenance) → upstream tool handler, with retry dedupe keyed on `<server>|<tool>|<event_id>`. The MCP server activates the policy path via `MCPServer.WithPolicyGate(server, deps)`; un-wired servers fall through to legacy direct dispatch for dev/test.
- New events on `edge.LayerMCP`: `mcp.tool.pre`, `mcp.tool.post`, `mcp.tool.failed`. Each carries `session_id` / `execution_id` / `tenant_id` / `principal_id` from `CallMetadata`, the redacted argument set, and an `artifact_pointer` when the redacted payload exceeds 64 KiB OR contains a high-severity credential family.
- `core/mcp/argument_redactor.go`: redaction regex set extended with `sk-` (Anthropic-style API keys) and `gh[opusr]_` (GitHub PAT/oauth/user/server/refresh tokens). Existing AKIA / sk_live_ / JWT / PEM coverage preserved. Field-name list (password / api_key / token / secret / private_key / authorization / etc.) unchanged.
- New defense-in-depth completeness check `verifyRedactionCompleteness`: after the configured redactor runs, the output is re-scanned for the high-severity sentinel set; if any pattern survives (rule misconfig, partial match, hostile stub), `EvaluateToolCall` returns `redaction_failed` and emits no event. Contract: no raw credential ever lands in a Redis-persisted audit row even when the upstream rule set is incomplete.
- New `core/mcp.CanonicalActionHash(tenant, server, tool, target_path) string` — exported SHA-256 over the normalized tuple that identifies an MCP tool call for approval-lifecycle binding. `BuildActionDescriptorFromToolCall` now extracts the first matching `path` / `file_path` / `target_path` / `filepath` arg and normalizes backslash → forward slash before setting `descriptor.TargetPath`, so Windows and POSIX callers operating on the same logical file converge on a single approval key.
- `core/mcp.MaxToolCallArgsBytes = 1 MiB` hard cap on serialized args; enforced on `json.Marshal` byte length (multibyte UTF-8 cannot smuggle past via lower rune count). Inline event budget is `edge.MaxInputRedactedBytes` = 64 KiB; oversized redacted payloads are written to `edge.ArtifactStore` with a 4 KiB inline summary plus pointer; small payloads with a high-severity finding also get an artifact pointer so forensics retain the full redacted context.
- `REQUIRE_HUMAN` decisions route through the existing `gatewayApprovalGate` with precedence: MCP invariants (always wins) > preapproval lookup > `MCPApprovalStore.ClaimPreApproved` > `EnqueueMCPApproval`. The bridge's pre event carries `decision = require_approval` so the audit trail records the gating point even when resolution is async.
- New documentation: `docs/edge/mcp-tool-policy.md` (gate inputs, verb classification, request flow, decision semantics, redaction field list, artifact-pointer thresholds, tenant isolation contract).

#### Dashboard Phase 5e — per-route error boundaries with route-scoped fallback (2026-05-09, task-adc04293)

- `dashboard/src/components/RouteErrorFallback.tsx`: route-scoped error UI composed from existing `ErrorBanner` primitive plus a Bug-icon mailto "Report bug" link. The mailto body URL-encodes the route name, error message, full stack, and user-agent so the bug report is actionable on first read.
- `dashboard/src/components/RouteBoundary.tsx`: thin wrapper that pairs `ErrorBoundary` with `RouteErrorFallback`, using `useLocation().pathname` as the boundary's `resetKey` so the user navigating away from a broken route auto-clears the boundary state.
- `dashboard/src/components/ErrorBoundary.tsx`: extended with an optional `fallback?: (props: { error, reset }) => ReactNode` render prop. When supplied, the boundary defers to the consumer's UI instead of rendering its built-in "Something went wrong" full-page card. Public type `ErrorBoundaryFallbackProps` is exported for downstream typings.
- `dashboard/src/App.tsx`: every leaf-page route is now wrapped in `<RouteBoundary name="…">`, surfacing a route-specific error UI instead of the generic fallback. Pure-redirect routes (`<Navigate>`-returning components) are not wrapped — they don't render UI. The outermost `ErrorBoundaryWrapper` is retained as the last-resort safety net for AppShell-/Suspense-level render failures that bypass any specific Route.
- Tests: 12 new tests added (5 covering the ErrorBoundary primitive's fallback + reset behavior; 3 integration tests exercising RouteBoundary for throw/navigate-clears/Retry semantics; 4 covering RouteErrorFallback's title + Retry + mailto + generic-message fallback). One pre-existing `App.copilot-session-route.test.tsx` registration-guard regex was loosened from `/element=\{<CopilotSessionPage\b/` to `/<CopilotSessionPage\b/` so the per-route boundary wrap doesn't break a future-friendly drift check.
- DoD #1 (designed empty states across 9 list pages) was fully met by existing code per the inventory in step-1: AgentsPage / JobsPage / ApprovalsPage / AuditLogPage / EdgeSessionsPage / TopicsPage / SchemasPage / PacksPage / govern/QuarantinePage all already render `<EmptyState icon=… title=… description=… action=…>` from `components/ui/EmptyState`. No page redesign needed; the audit table is captured in the task's complete_step note for step-2.

#### Dashboard 5 — Bundles surface: list + detail-with-tabs (2026-05-09, task-220d263a)

- `dashboard/src/pages/policies/BundlesPage.tsx`: filter bar + DataTable list at `/policies/bundles` with scope/search nuqs URL state, status dot, "+ New bundle" CTA. Row-click navigates to detail page. (Step 3, by worker-6b22 commit `22da3212`.)
- `dashboard/src/pages/policies/BundleDetailPage.tsx`: detail page at `/policies/bundles/:id` with the unified `Tabs` primitive and 4 panels — Rules / Versions / Deployments / Diff. Active tab in URL via nuqs (`?tab=rules|versions|deployments|diff`). Each tab is a separate lazy-loaded chunk. Per-tab status badge + back link in PageHeader. (Step 4, worker-6b22 `f064a55b`.)
- `BundleRulesTab.tsx`: rules-in-bundle list reusing the Rules surface row format; "Add rule…" + "+ New rule in this bundle" affordances. (Step 5, worker-6b22 `f064a55b`.)
- `BundleVersionsTab.tsx`: vertical timeline of versions newest-first with author + commit hash + rule-count delta; per-row "Compare with…" picker that sets `?tab=diff&from=...&to=...`. (Step 6, worker-6b22 `f064a55b`.)
- `BundleDeploymentsTab.tsx`: scope×version matrix consuming Backend 2's `GetActiveDeployment` grouping. Click-to-Promote (empty cells) / click-to-Rollback (active cells) gated by `ConfirmDialog`. New `useDeployBundle` + `useRollbackBundle` mutations call Backend 2's `POST /policy/bundles/:id/deploy` + `/rollback`. a11y: row/columnheader scoping + per-cell aria-labels naming the action verb + scope. (Step 7 by worker-c1cf, commit `fa13f3ad`.)
- `BundleDiffTab.tsx`: read-only Monaco DiffEditor side-by-side comparison of two version snapshots' YAML-serialized rule sets, with a summary row "X added · Y removed · Z modified" computed from id-keyed comparison. nuqs URL state `?from=&to=` for deep-link compatibility with the Versions-tab "Compare with…" picker; pickers when unset. DiffEditor lazy-loaded so monaco-editor only ships when the Diff tab is activated. (Step 8 by worker-c1cf, commit `6e3d5a72`.)
- New `dashboard/src/hooks/useBundle.ts` exporting `useBundle/useBundleVersions/useBundleDeployments`; `dashboard/src/hooks/useBundleVersion.ts` (single-version fetch); `useDeployBundle.ts` + `useRollbackBundle.ts` (mutations with cache-invalidation + toast wiring).
- New MSW defaults in `dashboard/src/test-utils/handlers.ts` for every Backend 2 endpoint the new tabs consume (`/policy/bundles`, `/:id`, `/:id/versions`, `/:id/versions/:version`, `/:id/deployments`, `/:id/deploy`, `/:id/rollback`).
- Page tests covering DoD #5: `BundlesPage.test.tsx` (list + filter URL), `BundleDetailPage.test.tsx` (tab navigation), `BundleDeploymentsTab.test.tsx` (matrix + ConfirmDialog wiring), `BundleDiffTab.test.tsx` (Monaco props + summary). DASHBOARD VERIFICATION RAIL: tsc 0 / vitest 238 files / 2052 tests / build 0.
- Note: deployment-timeline Gantt visualization is task Dashboard 6 (`task-7b2862f8`); the matrix is the v1 view.

#### Dashboard verification rail finalization (2026-05-09, task-347388c0 reopen #1)

- Updated `dashboard/docs/process/rail-vitest-green-verification.md` with the approved-rail excerpts post Yaron 2026-05-09 sign-off. Three rails now live in `project.globalRails.customRules` (verbatim text in the doc): DASHBOARD VERIFICATION RAIL (prop-8cc95268), DASHBOARD QA REJECTION FORMAT (prop-5a162a16), and PRE-SUBMIT DOD CHECKLIST (Yaron-2026-05-09). PENDING block annotated with the resolution; original placeholder retained as audit trail. Field-correction note added: rails live in `project.globalRails.customRules`, NOT `allRails.global` (the latter is empty in fetched contexts; the prior reopen mistakenly cited that field).
- Closes the task-347388c0 reopen #1 from QA: DoD #1 (rail exists) + #3 (visible to architects via moe.get_context) verified directly by architect-697e (chat msg-223382a3).

#### Dashboard 1 — Policy Studio foundation routes + type adapters (2026-05-09, task-5d354964)

- Three new lazy-loaded routes wire up the Policy Studio v3 IA per epic-d9a6c0a1 spec: `/policies` (PoliciesPage — Rules surface), `/policies/bundles` (BundlesPage), `/policies/decisions` (DecisionsPage). Each shell renders only `PageHeader` + `EmptyState` from existing primitives — Dashboards 2/5/8 fill the bodies.
- `/govern/overview` becomes a redirect handler (`GovernOverviewRedirect` inline in App.tsx). Reads `?tab=` + `?mode=` and navigates to the new IA: input/output/velocity → `/policies?type=…`; bundles → `/policies/bundles`; scope → `/policies/bundles?view=scope`; evaluation → `/policies/decisions` (preserves `mode`); unknown/missing → `/policies` (Rules default). The existing `/govern/<tab>` PolicyTabRedirect chains through here, so all legacy bookmarks stay valid.
- New `src/lib/policy-studio/` package adds 4 type adapters used across the unified shapes: `ruleTypeLabel` + `ruleTypeIcon` (lucide), `decisionTypeLabel`, `decisionTone` (5-tone palette), `edgeModeLabel`. Co-located test exhaustively iterates every `Object.values(Enum)` variant so missing enum values fail at test time.
- Stale `GovernPolicyOverviewPage` lazy import dropped (TS6133); component file retained on disk for Dashboard 11 cut-over deletion.
- No new shared primitives. No new colors. Routing remains on `react-router-dom` per v2.5 rail.

#### Phase 5d — bundle-size visualizer + soft CI gate (2026-05-09, task-50bbfd7d)

- Installed `rollup-plugin-visualizer@7.0.1` and wired it into `dashboard/vite.config.ts` so every `pnpm run build` emits `dist/stats.html` (treemap, raw + gzip + brotli). `emitFile: true` keeps the file inside `dist/`.
- New `dashboard/scripts/parse-bundle-stats.mjs` reads `dist/assets/*.js` and prints a per-chunk markdown table (raw + gzip + brotli) with totals and the initial-chunk highlight. Soft thresholds (warn-only; always exits 0): initial ≤ 400 KB raw / 120 KB gzip, total ≤ 3100 KB raw / 950 KB gzip — sized ~25-30% above the 2026-05-09 baseline (initial 305 KB / 92 KB gzip; total 2533 KB / 759 KB gzip captured in `dashboard/docs/code-hygiene-sweep.md`).
- `.github/workflows/ci.yml` `dashboard-test` job now: (a) declares `pull-requests: write` permission, (b) runs `pnpm run build` after vitest, (c) computes the bundle-size markdown via the parser, (d) uploads `dist/stats.html` + the markdown as the `dashboard-bundle-stats` artifact (14-day retention), (e) on `pull_request` events posts the markdown via `peter-evans/create-or-update-comment@v4` with `body-includes: "<!-- bundle-size-report -->"` so subsequent pushes update the same comment instead of appending. Main pushes still build + parse but skip the comment step.
- `dashboard/CLAUDE.md` "Bundle size (Phase 5d)" section documents the threshold values, how to read `dist/stats.html` locally, and where to find the baseline.

#### Backend 1.5 — policy-studio yaml additions on dashboard branch + regenerated dashboard TS (2026-05-09, task-e38d99a5)

- `docs/api/openapi/cordum-api.yaml`: surgically extracted Backend 1's 14 unified Rule/Decision/Bundle schemas (lines 10989–11256 of `origin/policy-studio-backend`) and appended them to dashboard branch's yaml so the orval pipeline can regenerate dashboard TS for downstream Dashboard 1+ tasks. Bumped `info.version` `2026-05-09.2` → `2026-05-09.3` (next-after worker-dac4's /policy/audit enrichment .2). worker-dac4's existing /policy/audit edits at lines 1, 3266, 9876, 9883 untouched (disjoint regions).
- `dashboard/src/api/generated/`: regenerated via `pnpm run generate-api`. 17 new TS files surface the unified shapes — `model/{rule,ruleType,ruleStatus,ruleScope,ruleScopeKind,decision,decisionType,decisionSource,bundle,bundleVersion,bundleMetadata,edgeMode,auditMetadata,traceStep}.ts` plus 3 sub-schemas (`ruleMatch`, `ruleDecide`, `traceStepConstraints`). 502 existing files received only the `OpenAPI spec version` comment bump (no semantic changes).
- Bridges Backend 1 (`origin/policy-studio-backend` PR) → Dashboard 1+ track. Per Yaron 2026-05-09 directive (commit/push, no merge until full epic done), the PR opens against `dashboard` and stays in REVIEW; the regenerated types live as commits on dashboard branch and are consumable immediately by parallel Dashboard tasks.

#### Phase 5c command palette recent jobs/agents (2026-05-09, task-095927f8)

- `dashboard/src/components/CommandPalette.tsx` now appends two dynamic groups to the `cmd+k` palette: **Recent Jobs** (top 50, label `${topic} · ${id-prefix}`, deep-link `/jobs/:id`, keywords include id/topic/status/capabilities) and **Recent Agents** (top 50, label `name||id`, deep-link `/agents/:id`, keywords include id/name/pool/status/capabilities). Data sourced from existing `useJobs({ limit: 50 })` and `useWorkers()` React Query hooks; cache-aware so palette open shows last-known recent without an extra fetch.
- Existing static commands (Navigate / Govern / Settings) render unchanged; new sections render below them.
- Fuzzy keyword match is now case-insensitive (`k.toLowerCase().includes(q)`) so mixed-case dynamic keywords (job ids, names) match consistently with the lowercase static keywords.
- `?` keybind to open `KeyboardShortcutsHelp` modal already wired via `dashboard/src/hooks/useKeyboardShortcuts.ts:102-108` with `isEditableTarget` guard at L77-83 (skips when target is INPUT/TEXTAREA/SELECT/contentEditable). Confirmed during inventory; no code change needed for that DoD item.
- New tests: 3 render-based cases in `CommandPalette.recent.test.tsx` covering the two dynamic groups + the empty-state path.

#### Phase 4 drift sweep follow-up #2 closure (2026-05-09, task-82593815)

- `dashboard/src/pages/DesignSystemConvergence.test.ts` extended with a comprehensive sweep test using `import.meta.glob('./**/*.tsx', { query: '?raw', import: 'default', eager: true })` (test files excluded at the glob level). Asserts `RAW_CONTROL_RE` (/<(input|select|textarea)\b/) does not match across `src/pages/**/*.tsx` outside the documented carve-out set. Forward-compatible — catches new pages that introduce raw native form controls without manual list maintenance.
- Companion regression detector: `carve-out pages still hold raw controls` test asserts `LoginPage` + `RunDetailPage` continue to contain raw controls; if a future migration accidentally rewrites them, this test fails forcing a coordinated docs + carve-out-set update rather than silent drift.
- Carve-outs documented inline (linking to `mem-df8a90aa` and `dashboard/docs/design-system-audit.md`): `LoginPage` (native HTML form for browser autofill / password manager interop on auth surface) + `RunDetailPage` (workflow-run console exempted, DoD-3 register).
- DoD #1 (23 pages migrated) confirmed by re-grep at HEAD: zero pages outside the carve-outs contain raw controls — the migration completed via parallel-worker activity (commits prior to claim). No code changes required for batch migration steps.

#### Phase 5a a11y test gate (2026-05-09, task-bf55ddbd; reopen #1 fix)

- `renderWithProviders` (`dashboard/src/test-utils/render.tsx`) accepts an opt-in `runAxe: true` option that returns a `Promise<RenderWithProvidersResult>` and asserts **zero WCAG 2 A/AA violations of any impact** on the rendered container. Strict gate per DoD: only `color-contrast` is disabled (jsdom can't composite backdrop-filter; Lighthouse CI / Phase 5b owns color-contrast). Existing callers without `runAxe` stay synchronous and unchanged. Optional `axeMode: "light" | "dark"` selects the theme axe runs against.
- New `dashboard/src/pages/NotFoundPage.test.tsx` page test opts in via `runAxe: true` to demonstrate the strict gate at the page level — NotFoundPage renders fully synchronously (no async data) so the post-render axe pass exercises the actual customer DOM, not a loading skeleton.
- `dashboard/src/components/UserMenu.test.tsx` first test also opted in (additional component-level coverage; UserMenu's idle render is axe-clean).
- New `dashboard/eslint.a11y.config.mjs` — narrow flat config that escalates the gate-relevant jsx-a11y rules (alt-text, ARIA correctness, heading-has-content, anchor-has-content, iframe-has-title) to `error`. `pnpm run lint:a11y` rewritten to point at this config; cross-platform safe (replaces the broken JSON-arg form that failed under PowerShell shell-quoting).
- `dashboard/src/components/ui/Card.tsx` `CardTitle` refactored to render `<h3>{children}</h3>` (was self-closing with spread props) so `jsx-a11y/heading-has-content` can statically verify content. No behavior change.

#### Workflow governance overlay fields (2026-05-08, task-913b6c6c)

- `WorkflowStep.policy_gate?: "allow" | "deny" | "require_approval"` — optional design-time policy hint surfaced on the WorkflowStudio governance overlay before any run. Validated at workflow-save time; unset means "no hint" (overlay defers to runtime safety decision).
- `WorkflowRunStep.audit_hash?: string` — optional 64-char hex audit-chain event hash for the step's job. Populated by run-record builders that join `StepRun.JobID → SIEMEvent.JobID → SIEMEvent.EventHash`. Populate-strategy left to producers; unset for skipped/upstream-failed steps.
- OpenAPI: `info.version` bumped `2026-04-21.2` → `2026-05-08.2` (reopen #1: `2026-05-08.1` shipped only `WorkflowStep.policy_gate`; `2026-05-08.2` adds `RunStepStatus.audit_hash` so the dashboard governance overlay's audit-hash chip data path is fully contracted). `WorkflowStep.policy_gate` enum + description added under `components/schemas/WorkflowStep`. `RunStepStatus.audit_hash` (nullable string, audit-chain event hash) added under `components/schemas/RunStepStatus`.

#### Runtime WorkflowRunStep.AuditHash population (2026-05-08, task-a45b8eb1)

- New `audit.StepHashSink` interface + `audit.WithStepHashSink` ConsumerOption: optional dependency the audit consumer uses to back-fill `StepRun.AuditHash` after a successful `chainer.Append`. Architectural choice per Option A scope-split (eventual-consistency write-back from the audit-consumer goroutine; the workflow engine cannot synchronously compute the chain hash because `PrevHash` depends on the chain head held by the chainer).
- Post-Append hook in `core/audit/consumer.go handle()`: when chain Append succeeds AND a sink is wired AND the SIEMEvent has both `EventHash` and `JobID`, the consumer calls `sink.UpdateAuditHash(ctx, jobID, eventHash)`. Sink errors are non-fatal (logged + swallowed) — the audit chain entry is durable; the workflow store can be back-filled offline if the sink misses transiently.
- `workflow.RedisStore` now implements the sink: run writes maintain a `StepRun.JobID → run/step` Redis lookup, `UpdateAuditHash` idempotently persists the first audit-chain hash on matching top-level and nested step records, and pending hashes are applied on the next run write if the audit event arrives before the step's JobID index.
- Gateway audit pipeline wiring passes the workflow store into both NATS consumer mode (`audit.WithStepHashSink`) and direct/chain-only senders, so new workflow runs populate the dashboard governance overlay's audit-hash chip as soon as their SIEMEvent is appended to the chain. Skipped/upstream-failed/no-entry steps remain unset.

### Fixed

#### core/controlplane — Fail-closed output policy + scheduler retry caps on Redis read errors (2026-05-20, task-8b4077ba)

- Output policy now fails closed when a result-pointer Redis `Get` returns a non-`redis.Nil` error: it emits a WARN with the pointer key and quarantines the output with a `pointer_unreadable` finding instead of scanning empty bytes and allowing by default.
- Scheduler dispatch now preserves retry caps when `JobStore.GetAttempts` is unreadable: max-scheduling and policy `max_retries` gates log WARN context and pessimistically treat the unknown attempt count as the cap, preventing poison-pill redis-timeout retry loops.
#### UpdateRun lost-update race for concurrent AuditHash writes (2026-05-09, task-a45b8eb1 reopen #2)

- `core/workflow/store_redis.go`: replaced the legacy two-phase Lua-then-Go-merge `UpdateRun` body with an atomic Lua script that performs GET-merge-SET as a single Redis command. The script walks the persisted run's StepRuns (recursively, including `children`) and forwards any populated `audit_hash` into the new payload's StepRuns whose `audit_hash` is empty for the same `job_id`, then SETs the merged payload. This closes the lost-update race the previous reopen left open: a stale UpdateRun whose caller marshaled before a concurrent `UpdateAuditHash` succeeded would otherwise have erased the just-written hash on its SET. With merge baked into the Lua, the GET inside the script always sees the current persisted state, so the race window collapses to zero. Index updates remain in a separate idempotent pipeline (cluster-safe, eventual-consistency tolerant). Removed the now-redundant Go-side `mergePersistedAuditHashes` helper and its tree-walk subroutines; pending-hash recovery (via `wf:run:pending_audit_hash:<jobID>` keys) still runs Go-side for the case where the audit event lands before the run/step is persisted at all.
- `core/workflow/store_redis_test.go`: added two regressions. `TestRedisStoreUpdateRunPreservesAuditHashAcrossInFlightRace` reproduces the exact race state — the audit consumer wrote the hash atomically while the caller's UpdateRun payload was built without it — and asserts that the SET preserves the hash and lands the caller's status mutation. `TestRedisStoreUpdateRunPreservesAuditHashUnderConcurrentInFlightAuditWrite` exercises 200 iterations of concurrent `UpdateRun + UpdateAuditHash` goroutines on the same run/job and asserts the hash survives in every iteration regardless of goroutine ordering. Both tests fail deterministically on a Lua-merge-disabled control implementation, proving they catch the lost-update path.

#### Backend 6 — pack `metadata.aliases` + safety-policy `constraints` extensions (2026-05-10, task-e4e9489c)

- **Pack manifest**: `metadata.aliases: []string` is now optional and additive. When set, topic / pools-patch / timeouts-patch namespace checks accept `job.<id>.*` AND `job.<alias>.*` for each declared alias. Regex `^[a-z][a-z0-9_-]{1,30}$`, max 8 entries, duplicates rejected. Unblocks the CordClaw pack owning `job.openclaw.*` topics under `metadata.id: cordclaw` (task-1e446868). Existing packs without aliases keep validating under the strict prefix rule. See `docs/operations/pack-aliases.md`.
- **Safety-policy `constraints`** schema (`core/infra/config/schema/safety_policy.schema.json`) now permits three additional fields under `rule.constraints`:
  - `max_output_bytes` (integer, 0–16 777 216 / 16 MiB upper bound enforced to prevent OOM via misconfigured large outputs).
  - `allowed_destinations` ([]string — write-target allowlist).
  - `redact_patterns` ([]string — regex patterns redacted before downstream emission).
  Schema retains `additionalProperties: false`; typos still fail. Existing policy fragments without these fields validate unchanged. See `docs/operations/safetykernel-constraints.md`.
- Both changes are additive only — no schema-version bump on either surface. CLI + gateway validators (`cmd/cordumctl/pack.go`, `core/controlplane/gateway/packs/validate.go`) are mirror-updated; pools-patch and timeouts-patch validators thread aliases through. Sibling invariants (pack-id regex, schema-id prefix, workflow-id prefix, pool-name prefix) are unchanged — only the topic namespace check honors aliases. Tests cover alias regex, count cap, duplicates, back-compat, and the new constraint bounds.

#### Cordum Edge P0 (2026-04-30)

EDGE epic shipped 32 P0 tasks for the Compliance Firewall surface — local
hook + agentd + Gateway Edge APIs + Safety Kernel evaluate + approvals +
artifact pointers + dashboard Edge Sessions. P0 final acceptance signed off
under EDGE-032 on 2026-04-30; product, API, CLI, and demo docs are at
[docs/edge/README.md](docs/edge/README.md) with the new-engineer
30-minute walkthrough at [docs/quickstart-edge.md](docs/quickstart-edge.md).

- EDGE-001: P0 architecture decisions and acceptance gate lock
- EDGE-002: Edge data model contracts and JSON schemas
- EDGE-003: Redis Edge store for sessions, executions, events, and indexes
- EDGE-004: Edge redaction and stable input hashing helpers
- EDGE-005: Gateway Edge session and execution APIs
- EDGE-006: Gateway Edge event write, batch, and read APIs
- EDGE-007: edge.event WebSocket stream envelope
- EDGE-008: Deterministic edge action classifier and policy input mapper
- EDGE-008.5: Post-review hardening from PR #243 senior review
- EDGE-008.6: Classifier shell allowlist inversion and adversarial safety tests
- EDGE-008.7: Edge API error shape, event idempotency, and OpenAPI contract
- EDGE-008.7.1: atomic event append + idempotency completion
- EDGE-008.7.2: action_hash determinism — sort RiskTags + add InputHash to approval CAS match
- EDGE-008.8: ADR-010 agentd transport reconciliation
- EDGE-009: Gateway edge evaluate API with Safety Kernel integration
- EDGE-010: Edge policy templates and simulation fixtures
- EDGE-011: Edge approval lifecycle store and Gateway APIs
- EDGE-012: Approval retry and optional inline wait contract
- EDGE-012.1: bind /api/v1/edge/approvals/{ref}/wait to original requester principal
- EDGE-012.2: bind /api/v1/edge/approvals list results to requester principal
- EDGE-013: Edge artifact pointers and evidence export bundle
- EDGE-014: Edge audit events, metrics, and structured logging
- EDGE-015: cordum-hook binary core contract
- EDGE-015.1: cordum-hook DefaultHookTimeout (10s) exceeds Claude's documented 5s deadline
- EDGE-016: Claude Code hook input/output mapper
- EDGE-017: cordum-agentd session manager, heartbeat, and local socket
- EDGE-017.1: cordum-agentd nonce externalization for trusted launchers
- EDGE-017.2: heartbeat OnStatus persists detached from shutdown — final state can be overwritten
- EDGE-017.3: hook receipt event split from decision event on shutdown — half-written audit record
- EDGE-017.4: agentd loopback nonce written to settings.json plaintext — same-user impersonation
- EDGE-017.4.1: Remove deprecated agentd ?nonce= query-param accept after one release
- EDGE-017.5: agentd state-store Windows ACL hardening
- EDGE-018: agentd evaluate client, caching, approvals, and fail modes
- EDGE-018.1: agentd evaluator request coalescing for concurrent identical hooks
- EDGE-019: cordumctl edge claude launch wrapper
- EDGE-020: Claude settings and enterprise managed-settings generators
- EDGE-021: cordumctl edge doctor and local diagnostics
- EDGE-022: Dashboard Edge API types, hooks, and stream invalidation
- EDGE-023: Dashboard Edge Sessions list page
- EDGE-024: Dashboard Edge Session detail timeline and event inspector
- EDGE-025: Dashboard Edge approvals drawer and artifacts panel
- EDGE-026: Dashboard Agent Executions panels on Job and Workflow Run detail
- EDGE-027: Local fake-hook E2E for P0 acceptance
- EDGE-028: Backend integration tests and regression suite
- EDGE-029: Edge docs — product, API, config, CLI, and demo
- EDGE-030: Demo polish and operator runbook
- EDGE-031: Security review and threat-model closure for P0
- EDGE-032: P0 final acceptance, demo signoff, and release readiness

#### Lighthouse CI gate for /login (epic-252d2c07 Phase 5b)

CI now runs Lighthouse against the unauth `/login` surface on every PR
and posts performance / accessibility / best-practices / SEO scores as a
PR comment.

- **`@lhci/cli` 0.15.1** added to `dashboard/devDependencies`.
- **`dashboard/lighthouserc.json`** — desktop preset, 3 runs averaged,
  `temporary-public-storage` upload target. All assertions `warn`-mode
  (perf ≥ 0.7, a11y ≥ 0.9, best-practices ≥ 0.85, SEO off) — no
  PR-blocking yet.
- **`.github/workflows/ci.yml` new `lhci-login` job** — PR-only,
  `continue-on-error: true`, `pull-requests: write` permission for
  comment posting. Reads `.lighthouseci/manifest.json` to format a
  markdown score table via `actions/github-script@v7`.
- **Local run**: `pnpm run lhci` from `dashboard/` (uses
  `start-server-and-test` to boot `vite preview` and tear it down
  cleanly after `lhci autorun`).

Authenticated-surface lhci (HomePage / JobsPage / AuditLogPage / etc.)
deferred to follow-up task **task-63603c2e** (cookie-bridge + test
credentials required).

#### OpenAPI /policy/audit enrichment (epic-252d2c07 follow-up to task-55f813b3)

Closes the spec drift between the gateway handler and the OpenAPI spec for
`GET /api/v1/policy/audit`:

- 9 query params declared (`limit`, `offset`, `action`, `agent_id`,
  `after`, `before`, `search`, `rule_id`, `type`) matching the
  gateway handler at
  `core/controlplane/gateway/handlers_policy_bundles.go:805`.
- Response shape changed from bare `PolicyAuditEntry[]` to a typed
  `PolicyAuditEnvelope` (`{items, total, has_more, offset}`) matching
  the actual handler payload.
- `PolicyAuditEntry` schema enriched from 7 fields to 25 fields
  (existing 7 stay; 4 of them now `deprecated: true` since the backend
  doesn't populate them; 18 backend-only fields added — `resource_*`,
  `actor_id`, `role`, `auth_source` (new `AuthSource` enum),
  `agent_*`, `bundle_ids` plural, `reason`, `decision`,
  `matched_rule`, `policy_version`, `extra`, `snapshot_before` /
  `snapshot_after`, `created_at`).
- `info.version` bumped to `2026-05-09.2`.

Dashboard: `dashboard/src/pages/AuditLogPage.tsx` swapped from a manual
`get<AuditResponse>('/policy/audit?...')` call to the regenerated
`useGetPolicyAudit` hook. The previous `PolicyAuditEntry &
Record<string, unknown>` bridge intersection (added in task-55f813b3
step-7) is removed; the page now consumes the typed shape directly.

#### Cordum Edge P0 cleanup (2026-05-03)

Post-acceptance cleanup batch on top of P0 ship — Docker stack reproducibility,
PRD/roadmap freshness, docs visibility split (public + private subtrees),
PR #243 reviewer follow-ups, fanout bounds, typed-error refactor, hook output
parser alignment, dashboard inspector visibility, gateway approval auto-consume,
and a Windows-specific settings rendering bug.

- EDGE-029.1: PRD.md and PRD_ROADMAP.md freshness sweep for post-2026-04-30 P0 progress
- EDGE-033: Full Docker stack build + end-to-end validation + user-facing run instructions
- EDGE-034: Fix all PR #243 workflow failures (feature/cordum-edge-p0)
- EDGE-035: Address all open reviewer comments on PR #243
- EDGE-036: Split cordum/docs into public + private subtrees with explicit visibility policy
- EDGE-037: Bound Edge execution fanout and large-session deletion cleanup
- EDGE-038: Replace Edge gateway string-matched validation errors with typed errors
- EDGE-039: cordum-hook output parser alignment + EDGE-033 fake-hook E2E continuation to 5/5 PASS
- EDGE-040: Edge session shows 0 events in dashboard timeline despite running status — investigate hook→agentd→Gateway event write-back gap
- EDGE-041: user_prompt_submit + PreToolUse content invisible in dashboard inspector — align hook mapper output with dashboard `_redacted` suffix convention
- EDGE-042: Gateway evaluate does not match freshly-approved approval to new evaluate request — consume retry returns DENY+approval_ref instead of ALLOW
- EDGE-043: EDGE-042 design gaps — rejected/expired approvals silently re-enqueue, 4-page scan limit, no auto-consume unit test
- EDGE-044: EDGE-041 _redacted suffix rename broke Safety Kernel policy matching — every PreToolUse defaults to DENY post-rename
- EDGE-045: cordumctl edge claude renders hook command path with stripped backslashes on Windows — settings.json gets `.bincordum-hook` instead of `./bin/cordum-hook.exe`
- EDGE-046: redactHookBoundaryString over-redacts any prompt containing the substring "secret" — wipes whole value to literal "<redacted>" instead of preserving content
- EDGE-047: cordumctl edge claude UX — 9-flag invocation defeats "drop in front of Claude Code" product story; ship `cordumctl edge init` scaffold + `./cordum.yaml`/`~/.cordum/config.yaml` config auto-discovery + `cordum-claude` standalone shortcut + `--print-config` diagnostic
- BUG-001: TestCollectCopilotSessionDecisionsPaginatesPastUnrelatedTenantDecisions returns len(decisions)=0 want 1 — pre-existing fail in core/controlplane/gateway from PR #233

#### Docs visibility split (EDGE-036)
- Split `cordum/docs/` into PUBLIC (root) + PRIVATE (`docs/internal/`)
  subtrees per `docs/visibility-policy.md`. 26 internal-only audit /
  threat-model / SEC-issue-draft / cleanup-sweep / decision-log /
  acceptance-evidence files moved under `docs/internal/` via `git mv`
  (history preserved). PUBLIC docs cite only PUBLIC docs; verification
  gates documented in `docs/visibility-policy.md`. Banner at
  `docs/internal/INTERNAL.md`.

#### Cordum Edge P0 documentation and Claude Code launcher
- Added Edge product, API, configuration, CLI, and demo documentation for the P0
  Compliance Firewall surface (`docs/edge.md`, `docs/edge-claude-code.md`,
  `docs/demo-edge-claude.md`, and `docs/edge/`).
- Documented `cordumctl edge claude`, local `cordum-hook`/`cordum-agentd`
  behavior, generated settings, approval retry UX, fail modes, token tradeoffs,
  and the developer-wrapper vs enterprise-managed-settings boundary.

#### Control Plane Boundary Hardening
- **Topic registry** (`GET/POST/DELETE /api/v1/topics`) — canonical source of truth for registered topics with pool, schema, pack, and status metadata
- **Submit-time topic validation** — unknown topics rejected with 400 at both gateway and scheduler boundaries; known topics with zero workers stay valid (degraded, `ErrNoWorkers` retry)
- **Submit-time schema enforcement** — job payloads validated against topic's input schema via JSON Schema draft-07. Modes: `SCHEMA_ENFORCEMENT=enforce|warn|off` (default `warn`)
- **Worker credential store** (`GET/POST/DELETE /api/v1/workers/credentials`) — hashed tokens (argon2id) for worker attestation. Modes: `WORKER_ATTESTATION=enforce|warn|off` (default `off`)
- **Worker readiness handshake** — scheduler filters on `ready == true` when `WORKER_READINESS_REQUIRED=true`. Workers must send handshake with `ready_topics` before receiving jobs. Unknown workers allowed (absence ≠ not ready).
- **Dashboard TopicsPage** — unified view of topics with pool, schema, pack, active workers, and degraded indicators
- **cordumctl topic** subcommands: `list`, `create`, `delete`
- **cordumctl worker credential** subcommands: `create`, `list`, `revoke`
- **SDK client methods** — `ListTopics`, `CreateTopic`, `DeleteTopic`, `ListWorkerCredentials`, `CreateWorkerCredential`, `RevokeWorkerCredential`
- **ADR-009** — Architecture Decision Record for canonical `TopicRegistration`, `WorkerCredential`, `WorkerSnapshot` types
- **Pack manifest schema bindings** — `inputSchema`/`outputSchema` fields on pack topic declarations, validated at install time

#### CAP v2.9.0 Integration
- Upgraded CAP dependency from v2.8.6 to v2.9.0
- `Agent.Start()` now publishes handshake automatically in Go, Python, and Node SDKs — all 36+ workers get handshake at startup with zero code changes
- `Heartbeat.auth_token` (field 18) for worker attestation
- `Handshake.ready_topics` (field 6) for readiness declaration
- `publishHandshake()` added to Python and Node SDKs (previously Go-only)
- Migrated all deprecated `SystemAlert` fields (`Level`, `Component`, `Code`) to structured replacements (`Severity`, `SourceComponent`, `ErrorCodeEnum`)

#### Output Policy System
- Two-phase output safety scanning: fast sync metadata checks on scheduler hot path + deeper async content checks over dereferenced result payloads
- gRPC `OutputPolicyService.CheckOutput` contract in `core/protocol/proto/v1/output_policy.proto`
- Output decisions: `ALLOW`, `QUARANTINE`, `REDACT` with typed findings (`secret_leak`, `pii`, `injection`)
- Scanner framework (`core/controlplane/safetykernel/scanners.go`) with configurable output scanners via `config/output_scanners.yaml`
- `OUTPUT_QUARANTINED` job terminal state in scheduler engine
- Output quarantine UX in dashboard: quarantine badge, remediation drawer, artifact panel
- `output_rules` section in safety policy YAML for topic/capability/content-pattern matching

#### MCP Server
- `cmd/cordum-mcp/` — MCP server binary with stdio and HTTP/SSE transport modes
- MCP gateway bridge (`core/controlplane/gateway/gateway_mcp.go`) for tool execution and resource resolution
- MCP data bridge for context/result blob resolution
- MCP tools reference documentation (`docs/mcp-tools-reference.md`)
- MCP resources reference documentation (`docs/mcp-resources-reference.md`)
- MCP server setup guide (`docs/mcp-server.md`) with Claude integration instructions

#### Workflow Engine — New Step Types
- **Switch step**: conditional branching with match expressions and default fallthrough
- **Parallel step**: concurrent execution with configurable max concurrency and failure strategy
- **Loop step**: iterate over arrays/ranges with break conditions
- **Transform step**: JSONPath/template-based data transformation between steps
- **Storage step**: read/write workflow-scoped key-value storage
- **Sub-workflow step**: invoke nested workflows with input/output mapping

#### Dashboard — Complete Rebuild (215 tasks across 12 epics)
- **Foundation**: AppShell layout, sidebar navigation (9+1 items), route system, theme, Zustand state management
- **Command Center** (`/`): Overview page with system metrics, active jobs, agent status, recent activity
- **Agent Fleet** (`/agents`): Worker pool management, heartbeat monitoring, capacity visualization
- **Jobs** (`/jobs`): Job list with filters/search, job detail page, state machine visualization, job submission drawer, artifact panel, memory panel
- **Workflows** (`/workflows`): Workflow list, DAG builder with visual canvas, node config panel, run visualization with real-time overlay, step detail panel
- **Safety Policies** (`/policies`): Policy Studio with visual rule builder, condition group builder, policy simulator with explain results, policy history timeline, bundle editor
- **Approvals** (`/approvals`): Approval cards inbox with urgency indicators, detail panel, bulk approve/reject actions, badge count in sidebar
- **Audit Trail** (`/audit`): Event stream with real-time updates, advanced filters, timeline visualization, PDF/CSV export, audit reports
- **Dead Letter Queue** (`/dlq`): DLQ message list, detail view, retry/purge actions, badge count in sidebar
- **Packs** (`/packs`): Marketplace catalog browser, pack detail view, install/uninstall from UI
- **Settings** (`/settings`): Sub-route layout (config, health, keys, users, MCP), system health tab, users tab with password management, effective config panel, locks panel, setup checklist
- **Schemas**: Schema list, detail view, JSON schema editor
- Cmd+K command palette for quick navigation
- WebSocket streaming (`/api/v1/stream`) for real-time job/workflow updates
- Cross-tab sync for auth state via `useCrossTabSync` hook
- URL-based filter persistence via `useUrlFilters` hook

#### Dashboard — Feature Gaps
- Job submit drawer with topic/prompt/labels form
- Memory panel for job context inspection
- Output quarantine UX with remediation drawer
- Workflow builder sidebar with node palette
- Parallel node and loop node visual components
- Run visualization with real-time step status overlay
- Settings MCP page for MCP server configuration
- Change password section in settings
- Effective config panel showing merged configuration
- Locks panel for distributed lock inspection
- Setup checklist for initial platform configuration

#### SIEM Audit Export
- Buffered audit event exporter with async batching and retry
- Webhook exporter with HMAC-SHA256 signatures and custom headers
- Syslog exporter with RFC 5424 formatting over TCP/UDP
- Datadog log intake exporter (v2 API) with site mapping
- CloudWatch Logs exporter with AWS Signature V4
- `NewExporterFromEnv()` factory for env-var-based backend selection
- Env vars: `CORDUM_AUDIT_EXPORT_TYPE`, `CORDUM_AUDIT_EXPORT_WEBHOOK_URL`, `CORDUM_AUDIT_EXPORT_SYSLOG_ADDR`, `CORDUM_AUDIT_EXPORT_DD_API_KEY`, `CORDUM_AUDIT_EXPORT_DD_SITE`

#### Auth Endpoints
- User/password authentication system separate from API keys
  - `CORDUM_USER_AUTH_ENABLED` to enable user store (Redis-backed with bcrypt)
  - `CORDUM_ADMIN_USERNAME`, `CORDUM_ADMIN_PASSWORD`, `CORDUM_ADMIN_EMAIL` for bootstrap
  - `POST /api/v1/users` endpoint for user creation (admin only)
  - `POST /api/v1/auth/password` endpoint for password changes
- `POST /api/v1/auth/login` — unified login for API keys and user credentials
- `POST /api/v1/auth/logout` — session termination
- Unified login page in dashboard with single card layout
- Enterprise badge for SSO features
- User auth settings in docker-compose.yml and Helm chart

#### Documentation
- `docs/output-policy.md` — Output safety scanning operator guide
- `docs/workflow-step-types.md` — All 12 step types with YAML examples
- `docs/api-reference.md` — Comprehensive REST endpoint reference (105+ endpoints)
- `docs/safety-kernel.md` — Deep reference for input policy, MCP filters, overlays, cache, signatures
- `docs/scheduler-internals.md` — State machine, output policy integration, reconciler, saga, routing
- `docs/dashboard-guide.md` — All dashboard pages, workflows, keyboard shortcuts
- `docs/configuration-reference.md` — Complete config schema, overlay system, env vars master table
- `docs/mcp-server.md` — MCP server modes and Claude integration
- `docs/mcp-tools-reference.md` — MCP tool catalog with schemas and examples
- `docs/mcp-resources-reference.md` — MCP resource catalog with URI templates
- `docs/websocket-streaming.md` — WebSocket protocol, auth, events, reconnection
- `docs/grpc-services.md` — gRPC service reference
- `docs/sdk-reference.md` — SDK reference for gateway client, worker runtime, testing
- `docs/k8s-deployment.md` — Kubernetes deployment guide
- `docs/troubleshooting.md` — Common issues and debug commands
- `docs/production-gate.md` — Production readiness gate script
- `docs/pack.md` — Expanded with development workflow, testing, marketplace publishing, worker registration
- ADR: Output policy architecture decision (`docs/adr/005-output-policy-architecture.md`)
- Tutorials: `docs/tutorials/langchain-guard.md`

#### Packs & Marketplace
- Pack development workflow documentation (create → develop → test → build → verify → publish)
- Pack policy simulation tests (`cordumctl pack verify`)
- Marketplace catalog browser in dashboard
- Pack install/uninstall from dashboard UI

#### Infrastructure
- `.goreleaser.yml` for release builds
- `tools/scripts/production_gate.sh` — pre-deploy verification script
- OpenAPI/Swagger UI in `docs/api/openapi/`
- `cordum-rest.yaml` OpenAPI spec

### Fixed

#### core/controlplane — Fail-closed output policy + scheduler retry caps on Redis read errors (2026-05-20, task-8b4077ba)

- Output policy now fails closed when a result-pointer Redis `Get` returns a non-`redis.Nil` error: it emits a WARN with the pointer key and quarantines the output with a `pointer_unreadable` finding instead of scanning empty bytes and allowing by default.
- Scheduler dispatch now preserves retry caps when `JobStore.GetAttempts` is unreadable: max-scheduling and policy `max_retries` gates log WARN context and pessimistically treat the unknown attempt count as the cap, preventing poison-pill redis-timeout retry loops.
- **edge (EDGE-041)** — cordum-hook mapper now renames every Claude `tool_input` field with a `_redacted` suffix on the wire so the dashboard inspector renders action context. `command` → `command_redacted`, `file_path` → `file_path_redacted`, `old_string` → `old_string_redacted`, `tool_response` → `tool_response_redacted`, etc. Unknown / version-drifted Claude fields fall through to a `tool_input_redacted` bucket so evidence never silently drops content. Builds on c951048d (UserPromptSubmit `prompt_redacted`); classifier accepts both the new and bare keys via multi-alias `inputStringAny` for historical-event compatibility. The dashboard sanitizer (`dashboard/src/api/transform.ts isUnsafeEdgeKey`) trusts only `_redacted`-suffixed keys as defense-in-depth — bare names were silently stripped, leaving `Redacted summary {}` empty in the EdgeEventInspector.
- **edge (EDGE-039)** — agentd evidence event now uses a fresh `agentd-` prefixed event_id instead of reusing the gateway-written `hook.policy_decision` event_id; the old behavior collided in `loadEventByIDInTx` and rejected the events/batch flush with 409 → 503 to hook → empty stdout. Cache-hit path already cleared resp.EventID; fresh-success path was the gap.
- **edge (EDGE-042)** — gateway `/api/v1/edge/evaluate` auto-consumes a reusable approval at the REQUIRE_APPROVAL branch when the request has no explicit `approval_ref`. cordum-hook cannot carry approval_ref across Claude tool retries; without auto-consume the approval-flow gate's "consume" call returned a fresh REQUIRE_APPROVAL with a new approval_ref instead of ALLOW. Lookup paginates the principal-status index (tuple index is SRem'd on consume) and routes through `consumeEdgeEvaluateApproval`.
- **edge (EDGE-044)** — restored the live Edge fake-hook E2E to 5/5 PASS after the EDGE-041 `_redacted`-suffix rename caused fresh-binary-vs-stale-image divergence. Source code at HEAD (post-EDGE-041) classifies renamed keys correctly via existing `inputStringAny` multi-alias coverage; live regression came from the `cordum/api-gateway:dev` Docker image being built before EDGE-041 landed and still using the pre-rename classifier — fresh `cordum-hook`/`cordum-agentd` binaries produced `file_path_redacted`/`command_redacted` keys that the stale gateway image read as missing, falling through to default-deny. Fix is operational: new `make edge-rebuild-e2e` target rebuilds local Edge binaries AND the api-gateway image in lockstep before strict-mode E2E runs; `docs/LOCAL_E2E.md` flags the rebuild as MANDATORY before `CORDUM_INTEGRATION=1`. Five new classifier regression tests in `core/edge/classifier_test.go` lock the renamed-key contract at the unit layer so any future alias gap surfaces in the focused suite, not in the live e2e.
- **scheduler (task-625b2ed1)** — fixed a latent nil-deref in `buildCompensationRequest` (saga.go). The inline `proto.Clone(base).(*pb.JobRequest)` lacked the ok-check every sibling clone site had; on a proto.Clone type-assertion failure the next line would dereference nil and panic the scheduler mid-compensation. Migration to the new `core/protocol/protoutil.CloneJobRequest` helper enforces the ok-check at every call site. Operator impact: none in the happy path; the failure path now returns a wrapped error instead of crashing.
- **audit (task-8db173c5)** — `SyslogExporter.Close` now logs at Warn when the underlying `net.Conn.Close` returns an error (fields: `network`, `address`, `error`). Previously the error was returned opaquely to the `BufferedExporter` close cascade where it could be absorbed silently, masking half-open sockets and TCP-stack fsync failures. Returned-error contract is unchanged.
- **gateway (task-1d4e6b4c bug #2)** — WebSocket `SetReadDeadline` errors at connection setup (`handleStream`, `handleJobStream`) are now propagated: on failure the handler logs at Warn, sets the disconnect state, closes the ws, and returns. Previously the error was discarded and the read loop ran with no deadline, so the server waited indefinitely for a frame that never arrived.
- **gateway (task-1d4e6b4c bug #3)** — `revalidateWSAuthWithRetry` now surfaces the last transient error after 3 exhausted retries instead of returning nil. A NATS/Redis outage during revalidation previously kept a potentially-revoked session alive for the full 2-minute revalidation window; callers already branch on `err != nil` and will close the connection, letting the dashboard auto-reconnect. `ctx.Done()` still returns nil — caller-initiated shutdown is not a failure.
- **safety-kernel (task-681f83cd)** — `shadowTimeout` now actually bounds the per-submission shadow evaluation loop. The `context.WithTimeout` return was previously discarded; captured + plumbed through `evalShadowSafely` with a `ctx.Err()` check at bundle-iteration top.

#### Critical
- **NATS reconnect** — Safety kernel and scheduler re-subscribe to `sys.config.changed` on NATS reconnect. Previously degraded silently to 30s polling on network partition.
- **Config scope corruption** — `SetWithRetry` now deep-merges config updates, preserving existing keys. Policy bundles no longer silently wiped by pools config pushes. Startup migration moves stale bundles to correct scope.
- **E2E TLS job dispatch** (`task-73bc2227`) — fixed `tools/scripts/e2e_test.sh` Phase 4 on TLS compose stacks. The script now auto-detects `./certs/ca/ca.crt`, uses `tls://` / `rediss://`, passes `NATS_TLS_CA` / `NATS_TLS_CERT` / `NATS_TLS_KEY` plus `REDIS_TLS_CA` / `REDIS_TLS_CERT` / `REDIS_TLS_KEY` to `examples/hello-worker-go`, installs `./examples/hello-worker-go/pack/pack.yaml` so `job.hello-pack.echo` is registered before submit, treats missing Phase 4 readiness/completion as hard failures while parsing the canonical `/api/v1/workers` `items` response, and the gateway `unknown_topic` response now includes tenant-filtered `registered_topics` (capped at 20) plus `topics_endpoint`.
- **cordumctl topic registration** — `pack install` now registers topics in topic registry; `pack uninstall` removes them. Fixes #171.
- **cordumctl lock release** — `runPackInstall` and `runPackUninstall` return errors instead of `os.Exit(1)`, ensuring deferred lock release fires on all error paths.
- **Safety Kernel NATS subscription** — subscribes to `sys.config.changed` for immediate policy reload (was poll-only with 30s delay).
- **cordumctl JSON tags** — `packTests` structs now have `json:"..."` tags matching YAML tags, fixing silent registry data corruption.

#### High
- **Panic recovery** — all NATS subscription callbacks wrapped with `defer recover()` + stack trace logging
- **Readiness filter** — unknown workers allowed (absence ≠ not ready), preventing new worker traffic starvation
- **Credential cache** — async refresh in NATS handler (prevents scheduler throughput collapse), merge-on-failure (prevents stale cache)
- **Rollback reporting** — cordumctl rollback errors tracked and returned, non-zero exit on partial rollback
- **Approval stale_request false negative** — single-step approval workflows no longer get auto-invalidated as `stale_request` immediately after `POST /approve`. The gateway approve endpoint now locks the current `HashJobRequest(req)` into `SafetyDecisionRecord.JobHash`, and `scheduler.checkSafetyDecision` preserves a prior `JobHash` from gateway submit instead of clobbering it with a post-effective-config mutation hash; hash-fence store read failures retry without publishing instead of falling through the input fail-open path. This is a bug fix, not an API contract change; clients that only observed the spurious `invalidate_stale_request` path should now see the benign approval succeed again. Follow-up to commit `297937c7` and guard task `task-035cdc8e`.
- **Dashboard memory leaks** — duplicate WebSocket, IntersectionObserver, CSV blob URL timing
- **Dashboard error handling** — LoginPage 4xx, RunDetailPage chat error, PackDetailPage null state
- **Dashboard a11y** — focus traps on modals, aria-labels on stats, localStorage try-catch
- **Security logging** — `slog.Info`/`slog.Warn` for credential and topic operations
- **Input validation** — array length limits (max 100 items, 128 chars), URL encoding on dynamic links
- **lodash** — CVE-2026-4800, CVE-2026-2950 fixed upstream in 4.18.0; bumped to 4.18.1 as the latest safe release.

#### System Audit Bug Fixes (25 tasks)
- Gateway: Fixed SSRF in marketplace URL validation — added private IP filtering for RFC 1918/loopback/link-local addresses
- Gateway: Hardened public path matching to prevent auth bypass on path variations
- Gateway: Rate limit middleware now runs after API key authentication (was running before, allowing bypass)
- Gateway: Error responses sanitized to prevent internal stack trace leakage
- Scheduler: Fixed per-run mutex for concurrent engine execution
- Scheduler: Fixed reconciler race conditions in timeout handling
- Scheduler: Fixed pending replayer edge cases
- Workflow engine: Fixed stale closure bugs using `useRef` pattern
- Workflow engine: Fixed dependency array triggers in hooks
- Config: Fixed safety policy schema validation
- Memory store: Fixed job store edge cases in concurrent access
- Metrics: Fixed metric registration and labeling
- `bufio.Scanner.Err()` checked after scan loops across codebase

#### Dashboard-to-Backend Integration Bug Fixes
- Transform layer handles API contract mismatches between backend `{scope, data}` wrapper and frontend flat expectations
- Policy bundle detail mapping: parse rules from YAML content instead of hardcoding `rules: []`
- Visual rule builder: use shared `usePolicyBundle()` hook instead of local bypass
- `resolvePublishTargets()`: fixed `secops/` prefix requirement so pack bundles can publish

### Changed

- **core: extracted Unix-timestamp → RFC3339 formatter into `core/infra/timeutil` (task-e396a874)** — 5 inline formatters migrated: `FormatUnixAuto` (handlers_chat.go magnitude cascade) + typed `FromSeconds`/`FromMillis`/`FromMicros`/`FromNanos` for compile-time-known units. Byte-for-byte identical output; empty string on `ts<=0` preserved per site.
- **core: extracted `proto.Clone((*pb.JobRequest))` guard-pattern into `core/protocol/protoutil.CloneJobRequest` (task-625b2ed1)** — 4 inline call sites migrated to one helper with typed ok-check + nil guard. See the paired `Fixed` entry for the latent saga.go:322 nil-deref this closed. JobMetadata clone sites in saga.go not migrated (different type, separate follow-up if drift emerges).
- **gateway: removed packs_compat.go + policy_compat.go (task-a828e179)** — 233 lines of pure-alias shims deleted. Every caller (~40 files) now imports `core/controlplane/gateway/packs` or `core/controlplane/gateway/policybundles` directly and uses the fully-qualified `packs.PascalCase` / `policybundles.PascalCase` shape. `resolveAgentForAudit` moved to `handlers_agents.go`. Internal refactor; no public API change.
- **core: extracted Redis CAS retry loop into `core/infra/redisutil/Retry`** — 4 production call sites (gateway keystore_redis RevokeKey + mcp_approvals Consume/Resolve/SweepExpired) now share a single retry primitive with `WithMaxAttempts`/`WithKeys` options and an `ErrMaxAttemptsExceeded` sentinel. Behavior byte-equivalent. Closes task-c7e419d8.
- **core: unified JobRequest canonicalisation into `core/protocol/reqhash`** — single `Canonical` + `Hash` helper shared by scheduler, gateway, and store; five bare `protojson.Unmarshal` sites in `core/infra/store/job_store.go` now pass `DiscardUnknown: true`. See release notes for the Redis WATCH/MULTI atomic-store decision. Closes task-090ab6af.
- Auth: Login endpoint supports both user credentials and API keys
- Auth: AuthConfig includes `user_auth_enabled` and `saml_enterprise` fields
- Scheduler: Output policy integration in dispatch pipeline
- Workflow engine: Support for 6 new step types alongside existing job/fan-out/condition/delay/approval/notify
- Dashboard: Sidebar navigation consolidated to 9+1 items (removed /context, /pools, /system, /trace, /tools)
- Dashboard: Routes reorganized under new page structure
- Safety kernel: Policy fragments from config service merged with file/URL policy on load/reload

#### CAP v2.5.2 Protocol Integration
- Upgraded CAP protocol dependency from v2.0.19 to v2.5.2 (both `go.mod` and `sdk/go.mod`)
- All NATS-connected services publish `Handshake` on `sys.handshake` at startup for capability discovery (gateway, scheduler, workflow-engine; workers via SDK runtime)
- `SystemAlert` now includes `severity` enum, `error_code_enum`, `source_component`, `details` map, and `trace_id` (deprecated string fields still populated)
- `JobResult` error codes use structured `ErrorCode` enum alongside string `error_code` for backward compatibility
- Bus-layer validation rejects malformed `JobRequest`/`JobResult` messages using CAP SDK helpers (`validation_rejections_total` metric)
- Scheduler handles `BusPacket{Handshake}` in its message switch, updating the worker registry with component capabilities
- Dashboard displays structured error code badges on job detail page and enhanced alert severity in audit log
- Added conformance test fixtures for all 8 CAP packet types with signature verification
- SDK runtime exposes `ValidateJobRequest`, `ValidateJobResult`, `Handshake`, `ComponentRole`, `ErrorCode`, `AlertSeverity` types

### Removed

- Retired the `cordum-enterprise` repo — all enterprise features (SSO/SAML, SCIM, advanced RBAC, SIEM export, legal hold, velocity rules, agent identity) now ship in cordum core behind license entitlements; separate repo archived on GitHub. Closes task-b7c6c2f1. See release notes for full surface list.
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

### Security
- **WebSocket quarantine-redaction fail-closed (task-1d4e6b4c bug #1)** — the filter that strips `ResultPtr` + `ArtifactPtrs` from DENIED `JobResult` packets before broadcasting to WebSocket subscribers previously FAILED OPEN on `proto.Clone` type-assertion failure AND on the defensive `cloned.GetJobResult() == nil` branch, returning the original unredacted packet. Redis-stored result payloads may contain PII, user prompts, secrets, or model outputs; the filter now fails CLOSED: returns nil on any failure, `enqueueBusPacket` drops the broadcast, `cordum_gateway_ws_quarantine_redaction_drops_total` increments, and an error is logged with `job_id` + `trace_id`. The next state-change event arrives in the normal stream cadence.
- Session tokens: Replaced timestamp-based tokens with `crypto/rand` (was only 53 bits entropy)
- HSTS: Added `Strict-Transport-Security` headers
- Brute-force protection: Added login attempt rate limiting
- Password policy: Enforced minimum complexity requirements
- Docker healthchecks: Added health endpoints to all container services
- Kubernetes: Fixed dashboard deployment manifest
- Kubernetes: Added egress network policies
- Redis: Configured persistence for production durability
- Gosec findings mitigated across codebase (G117 suppressions for intentional secret/password fields)
- OIDC host validation fix

### Tests
- Added test coverage for `core/audit/` package: config, datadog, exporter, syslog (4 new test files)
- Added workflow engine tests: loop, parallel, storage, sub-workflow, switch, transform step types
- Added output policy tests: engine output, safety client, protobuf
- Added dashboard component tests: Badge, CollapsibleSection, ComboboxInput, ConfirmDialog, Drawer, HighlightText, TagInput
- Added dashboard hook tests: useApprovals, useAudit, useAuth, useAuthConfig, useCrossTabSync, useDLQ, useJobs, useKeyboardShortcuts, useMemory, useOutputPolicy, useOutputRules, usePacks, usePageTitle, usePermission, usePolicies, useRunStream, useSchemas, useSettings, useSetupStatus, useStatus, useToast, useUrlFilters, useWorkers, useWorkflows
- Added dashboard lib tests: api, audit-filters, audit-report, export, format, logger, pdfExport, policy-yaml, runtime-config, settingsSchemas, status, utils
- Added dashboard state tests: config, toast, ui
- Added dashboard page tests: DLQPage, SettingsLayout, SetupChecklist, SystemHealthTab, UsersTab
- Added API client and transform layer tests

### Dashboard UI Polish Wave (2026-04-25)
- **Soft UI Evolution** — Button/Card/Tabs primitives migrated to `rounded-xl` + `duration-[var(--duration-soft)]` (250ms); `--shadow-soft`, `--shadow-soft-hover`, `--radius: 0.75rem` design tokens consumed at call sites; regression-pinned by `dashboard/src/components/ui/SoftUiEvolution.test.ts`
- **Per-row motion stagger** on Jobs / Audit / Agents tables via `motion.tbody` + `motion.tr` with `staggerChildren: 0.04` and `useReducedMotion`-honoring item variants
- **Staggered motion entry** on PolicyOverviewPage + SimulatorPage matching `HomePage.tsx:317-350` idiom
- **MotionConfig** — global `<MotionConfig reducedMotion="user">` wrapper at `App.tsx:201` so all `motion.*` descendants honor `prefers-reduced-motion`
- **RunDetailPage step-list a11y** — `role="listbox"` parent + `role="option"` items with `tabIndex=0`, `aria-selected`, `aria-label`, `onKeyDown` (Enter + Space, `preventDefault`), focus-visible ring
- **axe-core a11y gate** — automated accessibility test suite (`*.a11y.test.tsx`) covering HomePage, PolicyOverviewPage, SettingsHubPage in light and dark modes; `aria-pressed` added to toggle-state buttons (live-mode, etc.)
- **`useAdminLocks` role gate** — `enabled: useIsAdmin()` short-circuits the 5s `/admin/locks` poll for non-admin users (was emitting 720 `403`s/hour and a silent blank LockInspector card); LockInspector now renders an `EmptyState` admin-required card on `!isAdmin`
- **`useDelegations` test fix** — race-prone unhandled rejection silenced via no-op `mutation.catch(() => {})` immediately after `mutateAsync`; rollback path now asserts `setQueryData(allKey/agentKey, seeded)` explicitly
- **`mapJobRecord` origin refs** — `BackendJobRecord` interface declares `workflow_run_id`, `labels`, and `metadata` as optional fields; `mapJobRecord` forwards them onto the returned `Job` so the JobsPage list `OriginPill` correctly renders Run/Session pills (was always falling through to Direct because the list-mapper stripped the fields that `mapJobDetail` adds back)
- **`backdrop-filter` `@supports` fallback** — Safari `<14` and iOS Safari `<14` now render `.glass-panel` / `.glass-sidebar` / `.glass-header` with opaque `var(--card)` background; PostCSS/Tailwind autoprefixer auto-extended the rule to cover `-webkit-backdrop-filter`
- **GovernanceVerificationPage** at `/govern/verification` (admin-gated via `RequireRole`); routing guard test added so missing `<Route>` registrations fail tsc + vitest
- **`instrument-card` sweep** — 5 Policy Studio routes + ApprovalDetailPage / SettingsMcpPage / RunDetailPage internal info blocks adopted the shared instrument-card primitive
- **Dashboard 12-col Bento Grid** — BundleDetailPage + JobDetailPage + AgentDetailPage refactored to `grid-cols-1 lg:grid-cols-12` with framer-motion staggered tile entry; RunDetailPage explicitly *exempted* (full-viewport 3-pane console shell is non-bento by design — see `dashboard/docs/design-system-audit.md` § DoD-3 exemptions)
- **Brand identity** — favicons + logo refresh

### Strategic Decisions (2026-04-25)
- **Dashboard i18n DEFERRED post-Visa** (task-530874ea) — zero `useTranslation`/`i18next`/`FormattedMessage`/`t(` adoption today; ~1500-key migration cost weighed against zero current external-customer demand and the project_strategic_direction "governance depth over breadth" rail. Follow-up `task-8c4cdcaf` filed in BACKLOG for post-Visa revisit.
- **LLM epic backlogged** (epic-ac495830) — same logic: not the right priority pre-Visa.

### Process Rails (proposed; pending human approval)
- `prop-8cc95268` — DASHBOARD VERIFICATION RAIL: tasks touching `cordum/dashboard/` MUST run `tsc --noEmit` + `npx vitest run` + `npm run build` and paste each summary line into the final `complete_step` note before `complete_task`. Docker-build-success is NOT a substitute (Vite bundles through type errors; the rail closes that loophole).
- `prop-5a162a16` — DASHBOARD QA REJECTION FORMAT: QA must cite the first failing gate and, for vitest failures, the first new failing test as `<describe> > <it> (<path>:<line>)`.

## [v0.3.0] - 2026-01-31
- Protocol/SDK: bump CAP to v2.0.19 across core + SDK modules.
- SDK: `sdk/runtime` now wraps CAP runtime (typed handlers + pointer hydration).
- SDK: add CAP bus helpers for progress/cancel/heartbeats + direct worker subjects.
- Examples: migrate workers to CAP runtime + direct-subject subscriptions.
- Breaking: legacy `sdk/runtime` worker API removed; use `runtime.Agent` + CAP worker helpers.

## [v0.2.0] - 2026-01-26
- Scheduler: add durable saga/compensation handling with reverse-stack rollback for fatal failures.
- Scheduler: add compensation idempotency keys and saga rollback metrics.
- Protocol: align job status handling with CAP v2.0.16 (FAILED_FATAL/FAILED_RETRYABLE).
- Workflow engine: treat FAILED_FATAL as terminal and FAILED_RETRYABLE as retryable.
- Security/docs: updated control-plane docs/wiki for saga semantics and CAP changes.
- Tests: added coverage for saga manager, safety kernel cache/URL validation, protobuf + grpc stubs, and Redis idempotency flows.

## [v0.1.4] - 2026-01-25
- Security: remove default API keys; deployments must supply `CORDUM_API_KEY`.
- Security: fail-closed API auth; enforce `X-Tenant-ID`; require policy signatures when enforcement is enabled.
