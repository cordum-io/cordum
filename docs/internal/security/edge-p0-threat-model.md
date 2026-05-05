# Edge P0 threat model

Last reviewed: 2026-05-02.

This document is the P0 security closure checklist for the Cordum Edge Claude
Code path. It is anchored to the 2026-05-02 sweep; re-review this file during
EDGE-032 final acceptance and whenever a P0 Edge task changes hook, agentd,
Gateway, Safety Kernel, approval, audit, artifact, or dashboard behavior.

## Scope

In scope for this P0 review:

- `cordum claude` / `cordumctl edge claude` wrapper behavior and generated
  Claude settings.
- `cordum-hook` command hooks and the local `cordum-agentd` boundary.
- Gateway `/api/v1/edge/*` APIs, Safety Kernel evaluate decisions, approvals,
  redaction, audit/observability, artifact pointers, evidence export, and Edge
  Sessions dashboard read-only review.
- Local fake-hook E2E and package-level backend regression suites used as
  security evidence.

Out of scope for P0 closure:

- **P1 MCP Gateway:** MCP enforcement is documented as future scope; this review
  does not claim Cordum fronts every MCP tool path in P0.
- **P2 LLM Proxy:** model-provider proxy enforcement and provider-token
  mediation are future scope; P0 only documents the template/proxy placeholder.
- **P3 Shadow/runtime detection:** full scanners, stores, remediation, runtime
  sidecars, and dashboard surfaces remain deferred. P0 only allows opt-in
  observe-mode diagnostics.

## Assumptions

- Production-shaped P0 uses command hooks, not the disposable EDGE-000 HTTP
  hook spike.
- The local path is: Claude Code hook -> `cordum-hook` -> local
  `cordum-agentd` -> Gateway Edge APIs -> Safety Kernel evaluate -> approvals /
  audit / artifact-pointer evidence.
- Events persist redacted summaries, hashes, stable IDs, decisions, and artifact
  pointers; raw prompts, raw tool outputs, transcripts, and secrets are not
  persisted by default.
- Enterprise enforcement requires managed settings plus controlled
  rollout/bootstrap. Wrapper-only behavior is a local developer convenience, not
  enterprise bypass prevention.

## Policy hierarchy and invariants posture

EDGE-053 adds the three-tier policy hierarchy that Edge and workflow evidence
now rely on: **Job → Workflow → Global**, with Global Invariants as an
uncrossable security floor.

- Global fragments without `tier` remain backward-compatible global policy.
- Workflow overrides live on workflow definitions as `policy_override` YAML and
  are loaded into the unified policy authority as synthetic
  `workflow/{id}/policy` bundles.
- Job overrides do not add a new persistence layer. Workflow jobs and Edge
  sessions carry `policy.attachment_id` labels (`job/{id}/policy` or
  `session/{id}/policy`) and Safety Kernel evaluate resolves scope from those
  labels before falling back to job/session IDs.
- Evaluation order is: Global Invariant DENY / approval / throttle first, then
  Job rules, then Workflow rules, then Global rules, then the most-specific
  scoped default decision. Invariant ALLOW rules remain fallback defaults and do
  not override a workflow/job/global DENY.
- Edge audit evidence records the producing tier as `tier:
  global|workflow|job` alongside `rule_id` and `policy_snapshot`; SIEM extras
  carry the same bounded tier field.
- Agentd safe-allow cache keys include the global snapshot plus workflow/job
  scoped snapshot identifiers, so a per-job allow cannot be replayed in another
  workflow/job scope. Empty scoped snapshots keep the previous global-only cache
  key shape.

Security consequence: a workflow or job owner can narrow or locally relax policy
within its scope, but cannot cross the Global Invariants floor. The integration
test `TestEDGE053_TierPrecedenceIntegration` pins the GlobalOnly, Workflow
override, Job override, Invariant DENY, and scoped-default cases against one
Safety Kernel snapshot.

## Gateway auth, tenant, rate-limit, and transport posture

The Edge Gateway surface is not a bypass around the existing API posture:

- `core/controlplane/gateway/gateway.go:972-978` wires the HTTP middleware chain
  as logging -> CORS -> rate limit -> API key/JWT auth -> read audit -> tenant
  middleware -> max-body middleware -> router. Rate limiting runs before auth so
  invalid-key brute force is IP-limited, and body bounds run before handlers.
- `core/controlplane/gateway/gateway.go:1243-1261` registers every
  `/api/v1/edge/*` route under the same mux, and
  `core/controlplane/gateway/gateway.go:1541-1546` includes `/api/v1/edge` in
  tenant-scoped prefixes.
- Edge handlers explicitly call `requireEdgePermissionOrRole`,
  `edgeStoreOrUnavailable`, and `edgeTenantFromRequest`: sessions
  (`handlers_edge_sessions.go:85`, `:236`, `:266`, `:294`, `:326`, `:381`,
  `:477`, `:505`, tenant helper at `:569`), events
  (`handlers_edge_events.go:89`, `:148`, `:345`, `:382`), evaluate
  (`handlers_edge_evaluate.go:119`, context at `:248`), approvals
  (`handlers_edge_approvals.go:29`, `:54`, `:90`, `:204`), export
  (`handlers_edge_export.go:77`), and standardized error envelopes
  (`handlers_edge_errors.go:67`, `:159`).
- EDGE-028 regression evidence covers missing auth/missing tenant/cross-tenant
  denial and bounded-body failures without orphan Redis writes:
  `TestGatewayEdgeExportRequiresAuthTenantAndDeniesCrossTenant`,
  `TestGatewayEdgeApprovalRoutesRequireAuthAndTenant`,
  `TestGatewayEdgeEvaluateRequiresAuthTenantAndRejectsMalformedRequests`,
  `TestGatewayEdgeEventRoutesDenyCrossTenantWithoutLeakingIDs`,
  `TestGatewayEdgeEventWriteRejectsBodyOverMaxBytesWithoutOrphanKeys`, and
  `TestGatewayEdgeEvaluateRejectsBodyOverMaxBytesWithoutOrphanKeys`.
- TLS is a Gateway deployment/transport setting, not a separate Edge handler
  path. `GATEWAY_HTTP_TLS_CERT` / `GATEWAY_HTTP_TLS_KEY` are documented in
  `docs/configuration-reference.md:798-800`, and production Docker guidance
  requires TLS in `docs/DOCKER.md:554` and `docs/DOCKER.md:586`. EDGE-031 makes
  no Gateway transport change and does not weaken API key/JWT/X-Tenant-ID
  requirements.

## Dashboard Edge posture

The EDGE-032 re-review supersedes the earlier EDGE-031 pre-dashboard note: the
P0 Edge dashboard route and component surfaces are now present and were verified
without changing dashboard files in this acceptance task.

- `dashboard/src/api/client.ts` centralizes HTTP auth headers. Edge fetches
  reuse `get`/`post` from this client, so `X-API-Key`, `X-Tenant-ID`,
  `X-Principal-Id`, and `X-Principal-Role` are preserved for Edge HTTP API
  calls.
- `dashboard/src/lib/constants.ts` declares the Edge session, execution,
  approval, evaluate, event, and batch-event API paths.
- `dashboard/src/hooks/useEdgeSessions.ts` calls the Edge sessions, events,
  executions, approvals, wait, and export APIs through the shared authenticated
  client, and keys React Query caches by Edge session/execution/approval IDs.
- `dashboard/src/api/types.ts` explicitly bans raw prompts, raw tool payloads,
  transcripts, command output, tokens, Authorization headers, and signed URLs
  from Edge dashboard state.
- `dashboard/src/api/transform.ts` drops unsafe Edge keys such as
  `raw_payload`, `raw_prompt`, `tool_input`, `tool_result`, `transcript`,
  `authorization`, `token`, `secret`, `password`, and `signed_url`, then maps
  Edge events, approvals, and export bundles through sanitized helpers.
- `dashboard/src/hooks/useEventStream.ts` narrows live Edge WebSocket cache
  entries to identifiers, decisions, hashes, artifact pointers, and redacted
  summaries, then invalidates Edge query keys without copying raw frames into
  React Query.
- `dashboard/src/App.tsx` routes `/edge/sessions` and
  `/edge/sessions/:sessionId` to the Edge Sessions list/detail pages.
  `dashboard/src/pages/EdgeSessionsPage.tsx` renders the session list; the
  detail page composes `EdgeEventInspector`, `EdgeApprovalsDrawer`, and
  `EdgeArtifactsPanel`.
- EDGE-032 dashboard rail evidence passed on 2026-05-02:
  `node ./node_modules/typescript/bin/tsc --noEmit` exit 0;
  `npx vitest run` exit 0 with `222` files / `1797` tests passed;
  `npm run build` exit 0; focused Edge dashboard smoke exit 0 with `7` files /
  `109` tests passed.

The dashboard is an evidence/review surface for P0. It must not claim
wrapper-only enterprise enforcement, and it must continue to render only
redacted summaries, IDs, hashes, decisions, approval metadata, and artifact
pointer metadata.

## Hook and agentd fail-closed / token-handling posture

Hook/agentd review maps to threats 1, 2, 3, 4, 6, 7, and 8:

- `cmd/cordum-hook/main.go:42-70` accepts only documented Claude command-hook
  subcommands and routes them through `claude.Run`; stdout/stderr separation is
  enforced by `cmd/cordum-hook/main.go:73-85` and tested by
  `TestRunCLIDelegatesClaudeHookAndKeepsStdoutJSONOnly`.
- The hook enforces one bounded stdin JSON object, a hook timeout below Claude
  Code's 5s command-hook deadline, and sanitized stderr errors in
  `core/edge/claude/runner.go:27-62`, `:82-120`, and
  `core/edge/claude/hook_input_test.go:14`.
- P0 uses a local-only loopback agentd HTTP boundary rather than the Week 0 HTTP
  hook spike. `core/edge/agentd/config.go:204-228` rejects non-loopback binds,
  `core/edge/agentd/local_server.go:143-156` requires the nonce on each hook
  request, and `core/edge/agentd/local_server.go:470-475` generates 32 bytes of
  entropy when no trusted launcher nonce is supplied. P0 does not claim a Unix
  socket/named-pipe listener; `docs/edge/cordum-agentd.md:151-175` documents the
  current loopback+nonce boundary and future socket preference.
- Hook-to-agentd nonce handling is runtime-only: `core/edge/claude/runner.go:65-73`
  reads `CORDUM_AGENTD_HOOK_NONCE`, `core/edge/claude/agentd_client.go:92-114`
  strips legacy URL nonces and fails closed when a URL nonce lacks runtime env,
  and `core/edge/claude/agentd_client.go:117-164` sends
  `X-Cordum-Agentd-Nonce` and rejects unknown agentd decisions.
- Dev and managed settings avoid long-lived secrets:
  `core/edge/claude/settings.go:82-104` emits command-hook settings, not HTTP
  hooks; `core/edge/claude/settings.go:150-180` rejects sensitive extra env
  keys and strips nonce query parameters; `core/edge/claude/managed_settings.go:36-80`
  renders enterprise managed settings with managed hooks/MCP, disabled bypass
  mode, and no persisted hook nonce.
- Agentd state persistence scrubs transient secrets and nonce-like metadata in
  `core/edge/agentd/state_store.go:196-225`; startup and state errors are
  covered by `TestRunCLIRedactsSecretsFromStartupErrors`,
  `TestRunCLIRedactsNonceFromStartupErrors`, and
  `TestFileStateStoreDropsSecretLikeMetadataKeys`.
- Fail-closed mode is centralized in `core/edge/agentd/fail_modes.go:70-160`:
  enterprise-strict and workflow-required paths deny on any governance miss,
  approval retries never consume without a fresh decision, enforce mode denies
  risky/unknown actions, and observe mode records degraded evidence rather than
  a false allow. Evidence includes
  `TestApplyFailModeMatrixCoversObserveEnforceEnterpriseStrict`,
  `TestApplyFailModeApprovalRetryAlwaysDenies`,
  `TestRunEnterpriseStrictDeniesMalformedAgentdResponse`,
  `TestRunStrictModeDeniesWhenAgentdUnavailable`,
  `TestRunStrictModeDeniesWhenAgentdTimesOut`, and the EDGE-028
  `TestGatewayEdgeEvaluateSafetyUnavailableVariantsPersistNoFalseAllow`.

## Redaction, audit, artifact, and retention posture

This review maps to threats 4, 5, 6, and 10:

- Raw Claude hook payloads are confined to the local hook/agentd boundary:
  `docs/edge/cordum-hook.md:55` states the hook does not persist or log raw
  payloads, `docs/edge/claude-hook-mapper.md:81-140` documents redacted
  `InputRedacted` / `InputHash` / `ActionHash`, and
  `docs/edge/cordum-agentd.md:49-55` says agentd forwards only bounded redacted
  metadata to Gateway evaluate.
- Redis events and approvals carry redacted summaries, hashes, and artifact
  pointers rather than raw tool payloads: `core/edge/approval_store.go:25-44`
  explicitly stores only hashes/snapshots/redacted metadata, and
  `core/edge/artifact.go:8-24` attaches opaque, same-tenant artifact pointers
  without dereferencing artifact bodies.
- Evidence export is metadata + manifest only. `core/edge/export.go:92-105`
  defines `SessionExportBundle` as redacted evidence, `core/edge/export.go:121-140`
  defaults artifact bodies off, and `core/edge/export.go:143-150` enforces
  tenant-scoped export queries.
- Audit and structured logs are bounded and redacted:
  `core/edge/observability.go:250-270` forbids raw command, prompt, path, full
  URLs, request bodies, errors, labels, and `InputRedacted` maps in log attrs;
  `core/edge/observability.go:362-372` maps policy decisions / denials /
  approval requests to SIEM events with safe extras only; and
  `core/edge/observability.go:735-740` keeps approval audit extras bounded and
  excludes raw reasons. `core/controlplane/gateway/handlers_edge_errors.go:67-72`
  pins sanitized Edge error envelopes.
- Retention defaults are conservative for P0: `PRD.md:2282-2286` lists
  `CORDUM_EDGE_SESSION_TTL=168h`, `CORDUM_EDGE_HEARTBEAT_TTL=30s`, and
  `CORDUM_EDGE_ARTIFACT_RETENTION=standard`; `core/edge/model.go:155-161`
  restricts retention classes to `short`, `standard`, and `audit`; missing
  artifact export entries distinguish TTL expiry / never-written / tenant
  mismatch instead of fetching or embedding bodies.
- Evidence tests include EDGE-004/013/014/027/028 coverage:
  `TestRedactValueRedactsSensitiveKeysRecursively`,
  `TestRedactBytesAndInvalidJSONNeverEchoRawInput`,
  `TestEventMarshalCarriesOnlyArtifactPointerMetadata`,
  `TestSessionExportAssemblerRecordsMissingArtifactsWithoutLeakingContent`,
  `TestEdgeObservabilitySecretLeakMatrix`,
  `TestWriteEdgeErrorRedactsSecretDetails`,
  `TestGatewayEdgeRedactionRoundTripAcrossEventsApprovalsAndExport`, and the
  EDGE-027 fake-hook E2E gates (`edge_pretooluse_deny`,
  `edge_posttooluse_artifact`, `edge_evidence_export`).
- The EDGE-014 redaction-gap fixes are treated as an actual discovered
  mitigation, not just paper evidence: the secret-leak matrix and Edge error
  redaction tests now pin the prior failure modes so future logging/error
  changes have to break tests before leaking token-shaped values.

## Threat mapping

The table below enumerates all PRD §26.1 threats verbatim. Status values are
restricted to the final closed set: `Implemented`,
`Implemented-with-dev-tradeoff`, or `Deferred-enterprise-control`. Any newly
discovered unowned mitigation gap must be assigned an owner and follow-up before
EDGE-032 can pass.

| Threat | Implemented mitigation | Code paths | Evidence | Residual risk | Status |
| --- | --- | --- | --- | --- | --- |
| Developer deletes local hook config. | P0 dev settings use command hooks; enterprise templates set `allowManagedHooksOnly`, disable bypass mode, force managed MCP, and add `ConfigChange`/`FileChanged` command hooks so governed settings changes are observed and can block in enterprise-strict. | `core/edge/claude/settings.go:79`; `core/edge/claude/managed_settings.go:36`; `core/edge/claude/managed_settings.go:60`; `core/edge/claude/runner.go:295`; `docs/edge/managed-settings-template.md:5` | `core/edge/claude/settings_test.go:75 TestDevSettingsRendersNonceOutsideURL`; `core/edge/claude/managed_settings_test.go:180 TestManagedSettingsFixturesAreSyntheticAndParseable`; `core/edge/claude/config_file_hooks_test.go:9 TestRunConfigChangeForwardsToAgentdAndBlocksOnlyInEnterpriseStrict` | Wrapper/local settings are user-editable. Fleet enforcement depends on managed settings deployment, which is a deferred enterprise rollout control. | Deferred-enterprise-control |
| Developer runs raw claude instead of cordum claude. | Local wrapper provides the governed path; enterprise-managed settings are the real bypass-prevention control by forcing managed hooks/MCP and disabling bypass permissions mode. The threat model does not claim wrapper-only enforcement. | `core/edge/claude/managed_settings.go:60`; `docs/edge/managed-settings-template.md:7`; `docs/adr/010-edge-p0-architecture-decisions.md:93`; `PRD.md:1319` | `core/edge/claude/managed_settings_test.go:110 TestManagedSettingsRendersNonceOutsideURL`; `core/edge/claude/managed_settings_test.go:180 TestManagedSettingsFixturesAreSyntheticAndParseable` | A developer can still launch raw Claude in unmanaged local-dev environments. Preventing that is enterprise managed-settings / device-management scope. | Deferred-enterprise-control |
| Hook binary is replaced with malicious binary. | The P0 architecture requires command hooks and local `cordum-agentd`, but binary signing/notarization and fleet binary integrity enforcement are not implemented in P0. | `cmd/cordum-hook/main.go:42`; `docs/adr/010-edge-p0-architecture-decisions.md:95`; `PRD.md:2709` | `cmd/cordum-hook/main_test.go:26 TestRunCLIDelegatesClaudeHookAndKeepsStdoutJSONOnly`; deferred control tracked in known-gaps catalog below. | A same-user or admin attacker who can replace the binary can bypass or falsify the hook until enterprise signing/notarization and managed deployment are added. | Deferred-enterprise-control |
| Hook token leaked through settings/logs. | Long-lived secrets are not written to generated settings; agentd loopback nonce is runtime-only via `CORDUM_AGENTD_HOOK_NONCE` and sent as `X-Cordum-Agentd-Nonce`; settings validation rejects sensitive keys/values; logs/errors/events use shared redaction. | `core/edge/claude/settings.go:82`; `core/edge/claude/settings.go:155`; `core/edge/claude/settings.go:178`; `core/edge/claude/agentd_client.go:92`; `core/edge/claude/agentd_client.go:130`; `core/edge/agentd/local_server.go:148`; `core/edge/redaction.go:409` | `core/edge/claude/settings_test.go:75 TestDevSettingsRendersNonceOutsideURL`; `core/edge/claude/settings_test.go:102 TestDevSettingsOmitsAgentdNonceVariants`; `core/edge/claude/agentd_client_test.go:69 TestRunAuthenticatesViaHeaderFromEnv`; `cmd/cordum-agentd/main_test.go:91 TestRunCLIRedactsNonceFromStartupErrors`; `core/edge/observability_test.go:1293 TestEdgeObservabilitySecretLeakMatrix` | Local process environment can be visible to same-user process inspectors; ADR-010 treats that as a local-dev tradeoff until keychain/service bootstrap exists. | Implemented-with-dev-tradeoff |
| Agent tries to read secrets. | The classifier marks secret paths (`.env`, `secrets`, SSH/AWS/key material) with secret risk; policy/evaluate maps those tags to deny/approval outcomes before tool execution. | `core/edge/classifier.go:490`; `core/edge/policy_templates.go:70`; `core/controlplane/gateway/handlers_edge_evaluate.go:125`; `core/controlplane/gateway/handlers_edge_evaluate.go:512` | `core/edge/classifier_test.go:12 TestClassifyEventDeterministicTable`; `core/edge/classifier_test.go:432 TestClassifyEventDoesNotLeakSecretValuesIntoLabels`; `core/controlplane/gateway/edge_evaluate_test.go:1473 TestGatewayEdgeEvaluateAppliesDemoPolicySimulationFixtures`; `tools/scripts/edge_fake_hook_e2e.sh` gate `edge_pretooluse_deny` | Coverage is for governed Claude hook events and Gateway evaluate requests; raw OS reads outside governed tools are out of P0 runtime scope. | Implemented |
| Agent tries destructive shell command. | Shell classification detects destructive commands and narrow safe-shell allowlist disqualifies metacharacter composition/substitution bypasses; evaluate maps destructive/high-risk actions to deny/approval. | `core/edge/classifier.go:517`; `core/edge/classifier.go:563`; `core/edge/classifier.go:570`; `core/controlplane/gateway/handlers_edge_evaluate.go:498`; `core/controlplane/gateway/handlers_edge_evaluate.go:512` | `core/edge/classifier_test.go:623 TestClassifyShellAdversarialBypassCases`; `core/controlplane/gateway/edge_evaluate_test.go:1473 TestGatewayEdgeEvaluateAppliesDemoPolicySimulationFixtures`; `core/edge/policy_templates_test.go:198 TestEdgePolicySimulationFixturesEvaluateAgainstDemoPolicy` | Prevents destructive shell in governed Claude hook flow; it does not sandbox arbitrary local processes. | Implemented |
| Agent bypasses MCP and uses shell/network directly. | P0 command hooks classify Claude `Bash`/tool actions, including network and unknown shell risk; managed settings can force managed MCP and hooks. Full MCP Gateway, LLM Proxy, and runtime shadow detection are explicitly later phases. | `core/edge/classifier.go:563`; `core/edge/claude/managed_settings.go:60`; `docs/adr/010-edge-p0-architecture-decisions.md:86`; `docs/edge/managed-settings-template.md:7` | `core/edge/classifier_test.go:623 TestClassifyShellAdversarialBypassCases`; `core/edge/claude/managed_settings_test.go:180 TestManagedSettingsFixturesAreSyntheticAndParseable`; `docs/adr/010-edge-p0-architecture-decisions.md:122` | Direct shell/network activity outside Claude hooks, and non-Cordum MCP/LLM paths, require P1/P2/P3 enterprise controls. | Deferred-enterprise-control |
| Gateway unavailable causes fail-open in strict environment. | Hook/agentd and Gateway evaluate fail-mode matrices deny governed actions in enterprise-strict and deny risky/unknown actions on Safety/Gateway unavailability; observe mode may allow but records degraded evidence rather than a false allow. | `core/edge/agentd/fail_modes.go:70`; `core/edge/agentd/fail_modes.go:118`; `core/edge/claude/runner.go:53`; `core/controlplane/gateway/handlers_edge_evaluate.go:130`; `core/controlplane/gateway/handlers_edge_evaluate.go:441`; `core/controlplane/gateway/handlers_edge_evaluate.go:478` | `core/edge/claude/fail_modes_test.go:53 TestRunEnterpriseStrictDeniesMalformedAgentdResponse`; `core/edge/agentd/app_test.go:301 TestRunReturnsFailClosedAfterHeartbeatFailuresInStrictMode`; `core/controlplane/gateway/edge_evaluate_test.go:995 TestGatewayEdgeEvaluateSafetyUnavailableByPolicyMode`; `core/controlplane/gateway/edge_evaluate_test.go:1059 TestGatewayEdgeEvaluateSafetyUnavailableVariantsPersistNoFalseAllow` | Observe mode remains intentionally permissive and must be labeled local/dev; enterprise-strict is the required strict control. | Implemented |
| Cross-tenant event access. | All Edge routes are registered under the shared Gateway middleware chain with API auth, rate limit, tenant middleware, and body limit; `/api/v1/edge` is tenant-scoped; stores and export queries require tenant IDs before reads. | `core/controlplane/gateway/gateway.go:972`; `core/controlplane/gateway/gateway.go:1243`; `core/controlplane/gateway/gateway.go:1541`; `core/controlplane/gateway/handlers_edge_events.go:89`; `core/edge/export.go:143` | `core/controlplane/gateway/edge_events_test.go:95 TestGatewayEdgeEventRoutesDenyCrossTenantWithoutLeakingIDs`; `core/controlplane/gateway/edge_routes_test.go:544 TestGatewayEdgeExportRequiresAuthTenantAndDeniesCrossTenant`; `core/controlplane/gateway/edge_stream_test.go:122 TestEdgeEventStreamTenantFilteringAndBusPacketRegression`; `core/controlplane/gateway/auth_regression_test.go:633 TestCrossTenantHeaderSpoofing_Blocked` | Tenant isolation relies on existing Gateway auth/tenant middleware and Redis key discipline; future new Edge routes must stay under the same prefix/guards. | Implemented |
| Raw prompt/tool output leaks secrets into logs/artifacts. | Hook mapper redacts before mapped requests; `RawPayload` stays in-memory at hook/agentd boundary; Redis events carry redacted input and hashes; export bundles are metadata/manifest only and artifact bodies default off; observability attrs bound and redact secrets. | `docs/edge/claude-hook-mapper.md:81`; `core/edge/claude/agentd_client.go:21`; `core/edge/redaction.go:428`; `core/edge/export.go:92`; `core/edge/export.go:137`; `core/edge/observability.go:258` | `core/controlplane/gateway/edge_routes_test.go:324 TestGatewayEdgeRedactionRoundTripAcrossEventsApprovalsAndExport`; `core/edge/observability_test.go:1293 TestEdgeObservabilitySecretLeakMatrix`; `core/edge/export_test.go:274 TestSessionExportAssemblerRecordsMissingArtifactsWithoutLeakingContent`; `core/edge/artifact_test.go:201 TestEventMarshalCarriesOnlyArtifactPointerMetadata`; `core/controlplane/gateway/handlers_edge_errors_test.go:196 TestWriteEdgeErrorRedactsSecretDetails` | EDGE-014 found and fixed redaction gaps in observability/error paths; those tests now pin the mitigation. If a future feature opts into artifact bodies, it needs a separate enterprise/strict review. P0 export is redacted metadata + pointers. | Implemented |
| Shadow-agent detector over-collects private developer data. | P0 intentionally ships no full Shadow Agents scanner/store/dashboard. Any local signal is opt-in observe-only, bounded, privacy-sensitive, and non-enforcing; full runtime detection is P3. | `PRD.md:2017`; `PRD.md:2040`; `PRD.md:2049`; `docs/adr/010-edge-p0-architecture-decisions.md:86`; `docs/adr/010-edge-p0-architecture-decisions.md:100`; `PRD.md:2754` | `docs/adr/010-edge-p0-architecture-decisions.md:86` scope decision; PRD §18.2 observe-only constraints. | P0 does not provide enforcement against ungoverned agents beyond optional local diagnostics; full detector privacy review remains P3. | Implemented-with-dev-tradeoff |

## Evidence command index

Evidence outputs for the 2026-05-02 closure run are captured verbatim in
`docs/security/edge-p0-threat-model-evidence-20260502.txt`. On this Windows/MSYS
host the Go commands ran with `TEMP`, `TMP`, and `GOTMPDIR` set to
`D:\Cordum\.go-tmp` and `GOMAXPROCS=2` to avoid known host temp/memory pressure;
the command strings below are the runnable security gates.

| ID | Command | 2026-05-02 result | Primary coverage |
| --- | --- | --- | --- |
| E1 | `go test -count=1 ./core/edge/... -run 'Test.*(Redact\|Secret\|Tenant\|FailMode)'` | PASS: `core/edge` 0.119s, `core/edge/agentd` 0.054s, `core/edge/claude` 0.037s. | Redaction, secret classifiers, tenant-safe stores/export, agentd/hook fail modes. |
| E2 | `go test -count=3 ./core/controlplane/gateway -run 'Test.*Edge.*(Auth\|Tenant\|Limit\|Redact\|Unavailable\|Stream\|Approval\|Export)'` | PASS: `core/controlplane/gateway` 16.256s across three runs. | Gateway Edge auth/tenant/body-limit/redaction/Safety-unavailable/stream/approval/export regressions from EDGE-028. |
| E3 | `bash tools/scripts/edge_fake_hook_e2e.sh` | EXIT 0: local non-integration run emitted `SKIP edge_fake_hook_e2e: https://localhost:8081 reachable but CORDUM_INTEGRATION not set; default mode is non-destructive`. EDGE-027 (`task-aa00876a`) is DONE and defines strict PASS lines for `edge_session_setup`, `edge_pretooluse_deny`, `edge_approval_flow`, `edge_posttooluse_artifact`, and `edge_evidence_export` when `CORDUM_INTEGRATION=1` plus a test API key are supplied. | Full fake-hook E2E command path and script contract; local host did not have integration credentials. |
| E4 | `bash tools/scripts/lint_no_secret_log.sh` | PASS: `OK: No raw secret logging found in scanned files`. | Static docs/script scan for raw secret logging patterns. |

Threat-to-command map:

| PRD §26.1 threat | Evidence commands |
| --- | --- |
| Developer deletes local hook config. | E1 managed-settings/config-change hook tests; E3 fake-hook script contract for governed hook path. |
| Developer runs raw claude instead of cordum claude. | E1 managed-settings tests; known-gap table maps enterprise enforcement to EDGE-150. |
| Hook binary is replaced with malicious binary. | E3 hook binary path contract; known-gap table maps binary integrity to EDGE-151. |
| Hook token leaked through settings/logs. | E1 redaction/nonce/settings tests; E4 secret-log lint. |
| Agent tries to read secrets. | E1 classifier/secret tests; E2 Gateway Edge evaluate/body/redaction regressions; E3 fake-hook `edge_pretooluse_deny` contract. |
| Agent tries destructive shell command. | E1 classifier/fail-mode tests; E2 Gateway Edge evaluate regressions. |
| Agent bypasses MCP and uses shell/network directly. | E1 classifier/managed-settings tests; known-gap table maps MCP/LLM/runtime enforcement to P1/P2/P3 tasks. |
| Gateway unavailable causes fail-open in strict environment. | E1 agentd/claude fail-mode tests; E2 Safety Kernel unavailable / no-false-allow regressions. |
| Cross-tenant event access. | E1 tenant-safe store/export tests; E2 Gateway Edge auth/tenant/stream/export regressions. |
| Raw prompt/tool output leaks secrets into logs/artifacts. | E1 redaction/artifact/export tests; E2 redaction round-trip/export regressions; E4 static secret-log lint. |
| Shadow-agent detector over-collects private developer data. | Scope/evidence is ADR-010/PRD documentation plus known-gap table; P0 ships no full detector and P3 owns runtime detection. |

## Known gaps catalog

The rows below are the deliberately **not-P0** controls. Wrapper-only behavior
is not enterprise enforcement; every deferred enterprise claim must close through
managed settings, controlled binary rollout, device/service bootstrap, or later
roadmap enforcement.

| Gap | Current P0 status | Owner | Follow-up task | Closure criterion |
| --- | --- | --- | --- | --- |
| Managed settings deployment automation. | EDGE-020 produced the template and docs, including platform rollout notes, but it does not install managed settings through Jamf/Kandji, Intune/Group Policy, Linux `/etc/claude-code`, or a server-managed settings channel. | Enterprise platform / device-management owner. | EDGE-020 template baseline (`task-04574a18`, DONE) plus EDGE-150 managed deployment automation (`task-ebed169a`, PLANNING). | A managed-device rollout installs the template, verifies drift, and can prove that local/user settings cannot remove Cordum hooks or MCP restrictions. |
| `allowManagedHooksOnly` / `disableBypassPermissionsMode` support. | The generated managed-settings template sets `allowManagedHooksOnly: true`, `allowManagedMcpServersOnly: true`, `allowedHttpHookUrls: []`, and `disableBypassPermissionsMode: "disable"`; tests assert those fields and reject weakening settings. This is template support, not fleet enforcement. | Enterprise platform owner with Claude Code managed-settings administrator. | EDGE-020 (`task-04574a18`) for template generation; EDGE-150 (`task-ebed169a`) for deployment/verification. | Admin-managed Claude Code settings are deployed and verified on endpoints, with no persisted hook nonce, API key, or bearer token in settings. |
| Hook / agentd binary signing and notarization. | P0 has command hooks, redaction, and fail-closed behavior, but no release-time signature, notarization, or endpoint binary-integrity verification. | Release engineering / security owner. | EDGE-151 hook and agentd binary signing/notarization (`task-909be4cb`, PLANNING). | Cordum releases sign/notarize or otherwise verify `cordum-hook`, `cordum-agentd`, and wrapper-installed artifacts before activation, with CI checks for tampered synthetic artifacts. |
| OS keychain and service bootstrap for hook nonce / local credentials. | ADR-010 accepts local-dev runtime env as a P0 tradeoff: the nonce is not written to Claude settings, but same-user process inspection may see local environment data. | Agentd / CLI platform owner. | EDGE-152 agentd keychain and service bootstrap hardening (`task-00320a80`, PLANNING); EDGE-019 wrapper task (`task-81f8e11d`, PLANNING) remains the adoption path. | Supported platforms inject nonce/credentials through a service manager or keychain/credential-store path without writing secrets to settings, shell history, logs, or persistent state. |
| Runtime bypass detection. | P0 ships governed hook/agentd evidence and optional observe-only diagnostics; it does not scan arbitrary local processes, shell/network activity, or ungoverned agents. | Runtime security / Shadow Agents owner. | EDGE-140 (`task-74ac5153`), EDGE-141 (`task-06aaab74`), EDGE-142 (`task-4cd8299f`), EDGE-143 (`task-de50a293`), and EDGE-144 (`task-f2bf3c65`) remain P3 follow-ups. | Runtime detector is opt-in, privacy-reviewed, and can report/remediate ungoverned agent activity without over-collecting developer data. |
| MCP Gateway enforcement. | P0 managed settings can force the `cordum-edge` managed MCP entry, but the full Cordum MCP Gateway is out of P0 enforcement scope. | MCP Gateway owner. | EDGE-100 (`task-0ffcac35`), EDGE-101 (`task-fb11aa72`), EDGE-102 (`task-032e01fa`), EDGE-103 (`task-968d6646`), EDGE-104 (`task-9351f243`), and EDGE-105 (`task-a04699dc`) remain P1 follow-ups. | MCP traffic is mediated by the Gateway with tenant auth, audit, policy hooks, and bypass-resistant managed settings. |
| LLM Proxy enforcement. | P0 templates include `ANTHROPIC_BASE_URL` placeholder support, but provider-token mediation and model-provider policy enforcement are P2, not P0. | LLM Proxy / provider-governance owner. | EDGE-120 (`task-b4d53633`), EDGE-121 (`task-24a7fe60`), EDGE-122 (`task-ce77541d`), EDGE-123 (`task-7a2ab379`), and EDGE-124 (`task-10bf1a83`) remain P2 follow-ups. | Provider traffic flows through a policy/audit proxy with token mediation, tenant isolation, and redacted evidence. |
| Agentd nonce-in-query compatibility removal. | P0 strips legacy URL nonce values and requires runtime nonce env for hook calls, but the compatibility cleanup is tracked separately so old docs/scripts can be retired safely. | Agentd / hook protocol owner. | EDGE-017.4.1 (`task-3d754b38`, PLANNING). | Hook/agentd docs, settings, and scripts no longer accept nonce-in-query compatibility paths and tests prove header-only nonce auth. |

## Adversarial review notes

Manual adversarial review was performed on 2026-05-02 because the
`adversarial-self-review` skill was not installed in this agent environment.
Each row asks how an attacker still succeeds and verifies the status is still
honest:

| Threat | Attacker challenge | Review result |
| --- | --- | --- |
| Developer deletes local hook config. | A local developer deletes user/project settings or edits `.claude/settings.local.json`. | Still deferred to enterprise controls. The mitigation is not wrapper-only; managed settings deployment is owned by EDGE-150, while P0 only observes config/file changes when hooks remain installed. |
| Developer runs raw claude instead of cordum claude. | A developer launches Claude Code without the wrapper or from a clean profile. | Still deferred to enterprise controls. Raw local launch is possible without administrator-managed Claude Code settings; EDGE-150 owns fleet enforcement. |
| Hook binary is replaced with malicious binary. | Same-user or local-admin write access replaces `cordum-hook` / `cordum-agentd`. | Still deferred to enterprise controls. P0 has no signing/notarization; EDGE-151 owns binary integrity before activation. |
| Hook token leaked through settings/logs. | A same-user process reads shell environment or crash/startup logs echo nonce-like data. | Status remains `Implemented-with-dev-tradeoff`: settings/log/state tests pin redaction and no settings nonce persistence, but OS keychain/service bootstrap is deferred to EDGE-152. |
| Agent tries to read secrets. | The model asks for `.env`, `.ssh`, `.aws`, or credential-like paths through governed tools. | Status remains `Implemented`: `core/edge/classifier.go:490-498` includes the `/.env` containment match and related secret-path checks, and E1/E2/E3 map those classifications to policy denial/approval evidence. Raw OS reads outside governed Claude tools remain out of P0 runtime scope and are separately covered by bypass gaps. |
| Agent tries destructive shell command. | The model wraps destructive shell commands in substitution, metacharacters, or apparently safe prefixes. | Status remains `Implemented`: classifier adversarial cases and Safety evaluate regressions cover safe-shell allowlist inversion and destructive/risky decisions. P0 does not claim an OS sandbox. |
| Agent bypasses MCP and uses shell/network directly. | The model or user invokes non-Cordum MCP, raw network tools, or local shells outside the hook path. | Still deferred to enterprise controls. P0 hooks cover governed Claude tool actions; P1 MCP Gateway, P2 LLM Proxy, P3 runtime detection, and managed settings own wider bypass resistance. |
| Gateway unavailable causes fail-open in strict environment. | Safety Kernel, Gateway, or agentd is unavailable, times out, or returns malformed/future decisions. | Status remains `Implemented`: E1/E2 cover enterprise-strict and Gateway/Safety unavailable paths, including no persisted false-allow decisions. Observe mode is intentionally permissive and labeled as local/dev. |
| Cross-tenant event access. | Caller spoofs tenant headers, uses another tenant's session IDs, or subscribes to stream packets. | Status remains `Implemented`: E2 covers auth/tenant/export/stream denial and sanitized not-found envelopes; future Edge routes must stay under the same Gateway middleware/prefix discipline. |
| Raw prompt/tool output leaks secrets into logs/artifacts. | Raw tool input, prompt text, token-shaped strings, signed URLs, or artifact bodies leak through logs, error details, exports, or dashboards. | Status remains `Implemented`: EDGE-014 fixed prior redaction gaps, E1/E2/E4 pin redaction/error/export/log lint, and export remains metadata/pointers unless a future feature gets a separate review. |
| Shadow-agent detector over-collects private developer data. | Runtime detection scans process lists, files, prompts, or network metadata too broadly. | Status remains `Implemented-with-dev-tradeoff` for P0 scope only: P0 ships no full detector; P3 tasks own privacy-reviewed runtime detection before any enforcement claim. |

## EDGE-032 acceptance checklist

EDGE-032 final acceptance must use this section as the security closure anchor:

- [x] All 11 PRD §26.1 threats are represented verbatim in the threat table.
- [x] Threat statuses are in the final closed set only:
  `Implemented` (5 rows), `Implemented-with-dev-tradeoff` (2 rows), and
  `Deferred-enterprise-control` (4 rows).
- [x] Every deferred enterprise-control row has a corresponding owner and
  follow-up in the known-gaps catalog.
- [x] Evidence commands E1-E4 have captured 2026-05-02 results in
  `docs/security/edge-p0-threat-model-evidence-20260502.txt`.
- [x] Gateway auth, X-Tenant-ID isolation, body bounds, rate limits, and TLS
  deployment posture are reviewed without weakening API key/JWT requirements.
- [x] Dashboard review includes the routed Edge Sessions list/detail pages,
  redacted event inspector, approval drawer, artifacts panel, export API
  mapping, shared auth/tenant client behavior, and EDGE-032 dashboard rail
  evidence (`tsc`, `vitest`, and `build` all exit 0).
- [x] Hook/agentd strict-mode failure behavior, token/nonce handling, redaction,
  audit, artifact retention, and known enterprise rollout gaps are documented.
- [x] P1 MCP Gateway, P2 LLM Proxy, and P3 Shadow/runtime detection remain out
  of P0 enforcement scope and are mapped to their parked follow-up tasks.
- [x] Security acceptance for `task-8c3f8581` / EDGE-032 should link this
  checklist plus the evidence artifact before the roadmap expands beyond P0.
