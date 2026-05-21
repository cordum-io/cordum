# Edge P0 acceptance evidence

Status values in this document are restricted to **Pass**, **Fail**, or
**Block**. `Block` means the row is intentionally not signed off until the
named EDGE-032 verification step produces fresh evidence; it is not an
incomplete pass.

## Source inventory

- Workspace PRD: `D:\Cordum\PRD.md`
  - §24.7 `Acceptance criteria for P0` lists the P0 completion bullets.
  - §26.1-§26.3 list security threats, mitigations, and production fail
    behavior.
- ADR: `docs/adr/010-edge-p0-architecture-decisions.md`
  - `Decision` captures command-hook, local-agentd, fail-mode, token-storage,
    and OSS/enterprise boundary defaults.
  - `P0 acceptance checklist from PRD 24.7` maps PRD bullets to Moe task
    coverage and gate expectations.
- Backend tests: `TESTING.md#edge-backend-integration-tests`
  - `go test -count=1 ./core/edge/...`
  - `go test -count=1 ./core/controlplane/gateway -run 'Test.*Edge'`
  - `go test -count=3 ./core/controlplane/gateway -run 'Test.*Edge.*(Auth|Tenant|Limit|Redact|Unavailable|Stream|Approval|Export)'`
  - `CORDUM_INTEGRATION=1 go test -tags=integration -count=1 ./core/...`
    only when the documented stack prerequisites are available.
- Fake-hook E2E: `tools/scripts/edge_fake_hook_e2e.sh` and
  `docs/LOCAL_E2E.md#edge-fake-hook-e2e`
  - Required strict output lines: `PASS edge_session_setup`,
    `PASS edge_pretooluse_deny`, `PASS edge_approval_flow`,
    `PASS edge_posttooluse_artifact`, `PASS edge_evidence_export`.
- Security closure: `docs/security/edge-p0-threat-model.md#edge-032-acceptance-checklist`
  - All 11 PRD §26.1 threats are represented.
  - Closed status set: `Implemented`, `Implemented-with-dev-tradeoff`, and
    `Deferred-enterprise-control`.
- Product and runbook docs: `docs/edge/README.md`, `docs/edge/demo.md`,
  `docs/edge/runbook.md`, and `docs/LOCAL_E2E.md`.

## Acceptance matrix

| PRD bullet | Evidence source | Command | Owner | Status |
| --- | --- | --- | --- | --- |
| `cordumctl edge claude` launches Claude Code with generated hook settings. | ADR-010 decision defaults; `docs/edge/cordumctl-edge-claude.md`; EDGE-019/020/021 DONE. | `cordumctl edge claude --dry-run` plus `--settings-output -` against local fake Gateway. | EDGE-032 step 3 | Pass |
| Dashboard shows a live EdgeSession. | EDGE-022/023/024/025/026 DONE; dashboard Edge pages/components; `docs/edge/README.md`. | Dashboard rail plus manual smoke from `dashboard/`. | EDGE-032 steps 6-7 | Pass |
| `PreToolUse` events are stored and streamed. | Gateway events/stream tests from EDGE-028; fake-hook E2E; dashboard timeline. | Backend Edge tests; `bash tools/scripts/edge_fake_hook_e2e.sh`; dashboard smoke. | EDGE-032 steps 4-7 | Pass |
| `Read .env` is denied by policy. | PRD §24.7; policy/classifier/evaluate tests; fake-hook `edge_pretooluse_deny`. | Backend Edge tests; strict fake-hook E2E. | EDGE-032 steps 4-5 | Pass |
| `Edit` can require approval. | Approval store/API tests; fake-hook `edge_approval_flow`; approval drawer from EDGE-025. | Backend Edge tests; strict fake-hook E2E; dashboard smoke. | EDGE-032 steps 4-7 | Pass |
| Approval in dashboard allows the held action after resolution and retry. | EDGE-011/012/012.1/012.2 APIs; EDGE-025 dashboard drawer; fake-hook retry/consume flow; destructive ProvenanceGate requires resolved approval audit evidence, not the requested row alone. | Strict fake-hook E2E; dashboard approval smoke. | EDGE-032 steps 5-7 | Pass |
| `PostToolUse` creates audit events and artifacts. | EDGE-013/014/016; fake-hook `edge_posttooluse_artifact`; export tests. | Backend Edge tests; strict fake-hook E2E. | EDGE-032 steps 4-5, 7 | Pass |
| Session can export evidence bundle. | EDGE-013 export; EDGE-028 export tests; fake-hook `edge_evidence_export`. | Backend Edge tests; strict fake-hook E2E; export smoke. | EDGE-032 steps 4-7 | Pass |
| Logs are structured and redacted. | EDGE-014 observability; `TestEdgeObservabilitySecretLeakMatrix`; `TestWriteEdgeErrorRedactsSecretDetails`; threat model row for raw prompt/tool-output leakage. | Backend Edge tests plus security checklist review. | EDGE-032 steps 4, 9 | Pass |
| Docs and demo script exist. | `docs/edge/*`; `docs/LOCAL_E2E.md`; `tools/scripts/edge_fake_hook_e2e.sh`; EDGE-029/030 DONE. | New-engineer docs/runbook walk-through. | EDGE-032 step 8 | Pass |
| Optional observe-only `edge doctor` shadow-agent diagnostic can report local ungoverned-agent signals without requiring a P0 Shadow Agents dashboard. | `docs/edge/cordumctl-edge-doctor.md`; ADR-010 Shadow Agents scope; EDGE-021 DONE. | `cordumctl edge doctor --json` when CLI binaries/prereqs are available; docs/runbook review. | EDGE-032 steps 3, 8, 10 | Pass |
| No production security requirement is weakened. | PRD §26; ADR-010 security/token storage; threat model acceptance checklist. | Security checklist review and release boundary grep/audit. | EDGE-032 steps 9-10, 12 | Pass |

## Final recommendation and handoff

**Recommendation: GO for P0 acceptance / release readiness.** The acceptance
matrix has `12/12` rows at `Pass`, with no `Fail` rows and no open release gate
in the matrix.

Signoff is based on the P0 contract rather than a single live demo machine:

- CLI/hook setup: real `cordumctl edge claude --dry-run` plus
  `--settings-output -` exited `0` against a local fake Gateway and generated
  command hook settings without printing API keys or hook nonces.
- Backend semantics: the EDGE-028 suite passed fresh and covers session create,
  policy evaluate, allow/deny/approval responses, event stream, approval
  consume/retry, export, auth/tenant denial, redaction, Safety Kernel
  unavailable paths, and Redis unavailable paths.
- Fake-hook E2E: default mode exited `0` with documented safe SKIP semantics.
  Strict live-stack output was not observed on this host because the reachable
  Gateway image returned `404` for `/api/v1/edge/*` and Docker was unavailable.
  Per architect decision `msg-11ec3c3c`, that is acceptable for EDGE-032 when
  paired with the fresh EDGE-028 gate-equivalent evidence. The required strict
  output lines remain documented for live-stack reruns:
  `PASS edge_session_setup`, `PASS edge_pretooluse_deny`,
  `PASS edge_approval_flow`, `PASS edge_posttooluse_artifact`, and
  `PASS edge_evidence_export`.
- Dashboard rail: TypeScript, full Vitest, Vite build, and focused Edge smoke
  all exited `0` from `dashboard/`.
- Security: `docs/security/edge-p0-threat-model.md#edge-032-acceptance-checklist`
  represents all 11 PRD §26.1 threats using the closed statuses
  `Implemented`, `Implemented-with-dev-tradeoff`, and
  `Deferred-enterprise-control`; secret-log lint and security-focused Go tests
  pass.
- Docs/runbook: `docs/edge/README.md`, `docs/edge/demo.md`,
  `docs/edge/runbook.md`, `docs/LOCAL_E2E.md#edge-fake-hook-e2e`, and
  `TESTING.md#edge-backend-integration-tests` are reachable and document exact
  commands, expected outputs, strict-vs-SKIP behavior, real-Claude optionality,
  and no-real-secret handling.

Known non-P0 gaps are explicitly owned and are not release blockers for this P0
signoff:

- EDGE-150: administrator-managed Claude settings / deployment enforcement.
- EDGE-151: hook and agentd binary signing, notarization, and rollout trust.
- EDGE-152: keychain or service-managed credential bootstrap.
- EDGE-100..105: MCP Gateway enforcement.
- EDGE-120..124: LLM Proxy enforcement.
- EDGE-140..144: runtime Shadow Agents detection.

Optional manual demo note: a real Claude Code demo was not run by this worker and
is not required for CI-safe P0 acceptance. The runbook describes it as a manual
operator path; the automated signoff uses synthetic data only.

## Evidence log

### Step 3 — CLI/hook setup dry-run

Commands run from repo root with Go temp/cache rooted under `D:\Cordum\.go-tmp`
and build outputs under `D:\Cordum\.go-tmp\edge032\bin`:

```powershell
go build -p 1 -o D:\Cordum\.go-tmp\edge032\bin\cordumctl.exe ./cmd/cordumctl
go build -p 1 -o D:\Cordum\.go-tmp\edge032\bin\cordum-agentd.exe ./cmd/cordum-agentd
go build -p 1 -o D:\Cordum\.go-tmp\edge032\bin\cordum-hook.exe ./cmd/cordum-hook
```

All three builds exited `0`. Then an in-process local fake Gateway bound to
`127.0.0.1` handled only `/api/v1/edge/sessions` so the dry-run exercised the
real `cordumctl -> cordum-agentd -> generated settings` path without external
network, Docker, real Claude execution, or any real `.env` reads.

Dry-run command shape:

```powershell
D:\Cordum\.go-tmp\edge032\bin\cordumctl.exe edge claude `
  --agentd-path D:\Cordum\.go-tmp\edge032\bin\cordum-agentd.exe `
  --hook-command D:\Cordum\.go-tmp\edge032\bin\cordum-hook.exe `
  --gateway http://127.0.0.1:<fake-gateway-port> `
  --api-key <synthetic-test-key> `
  --tenant tenant-edge032 `
  --principal principal-edge032 `
  --cwd D:\Cordum\cordum `
  --repo cordum `
  --git-branch feature/cordum-edge-p0 `
  --git-sha e3ff0b5d `
  --policy-mode enforce `
  --dashboard-url http://localhost:5173/edge/sessions/sess-edge032-cli `
  --dry-run
```

Result summary:

- Exit code: `0`.
- `api_key_configured=true`; the key value was not printed.
- `tenant_id=tenant-edge032`; `principal_id=principal-edge032`.
- `agentd_url=http://127.0.0.1:<reserved-port>/v1/edge/hooks/claude`.
- `settings_path=D:\Cordum\.go-tmp\cordum-edge-claude-1566473747\settings.json`
  (temporary path reported by dry-run; cleaned up after command return).
- `session_id=sess-edge032-cli`; `execution_id=exec-edge032-cli`.
- `dashboard_url=http://localhost:5173/edge/sessions/sess-edge032-cli`.
- `dry_run=true`; `exit_code=0`; `metadata.platform=windows`.

Generated-settings inspection command used the same flags with
`--dry-run --settings-output -`. Result:

- Exit code: `0`; JSON parsed successfully.
- Env keys present: `CORDUM_AGENTD_URL`, `CORDUM_AGENTD_HOOK_TIMEOUT`,
  `CORDUM_AGENTD_FAIL_CLOSED`, `CORDUM_EDGE_APPROVAL_WAIT_TIMEOUT`,
  `CORDUM_EDGE_EXECUTION_ID`, `CORDUM_EDGE_MODE`, `CORDUM_EDGE_PLATFORM`,
  `CORDUM_EDGE_PRINCIPAL_ID`, `CORDUM_EDGE_SESSION_ID`, `CORDUM_TENANT_ID`.
- Hook events present: `ConfigChange`, `FileChanged`, `PreToolUse`,
  `PostToolUse`, `PostToolUseFailure`, `UserPromptSubmit`.
- Negative checks: output/settings did **not** contain the synthetic API key,
  `CORDUM_AGENTD_HOOK_NONCE`, or `nonce=`.

Fresh backend, E2E, dashboard, docs, and security smoke summaries will be
appended in later step sections before any go/no-go recommendation is made.

### Step 4 — Backend Edge test evidence

Environment for Go commands:

- Repo root: `D:\Cordum\cordum`
- `TEMP`, `TMP`, `GOTMPDIR`: `D:\Cordum\.go-tmp`
- `GOMAXPROCS=2`

Commands from `TESTING.md#edge-backend-integration-tests`:

```powershell
go test -count=1 ./core/edge/...
```

Result: exit `0`.

```text
ok  	github.com/cordum/cordum/core/edge	11.039s
ok  	github.com/cordum/cordum/core/edge/agentd	2.131s
ok  	github.com/cordum/cordum/core/edge/claude	5.517s
```

```powershell
go test -count=1 ./core/controlplane/gateway -run 'Test.*Edge'
```

Result: exit `0`.

```text
ok  	github.com/cordum/cordum/core/controlplane/gateway	9.705s
```

```powershell
go test -count=3 ./core/controlplane/gateway -run 'Test.*Edge.*(Auth|Tenant|Limit|Redact|Unavailable|Stream|Approval|Export)'
```

Result: exit `0`.

```text
ok  	github.com/cordum/cordum/core/controlplane/gateway	17.044s
```

Integration-tag command:

```powershell
CORDUM_INTEGRATION=1 go test -tags=integration -count=1 ./core/...
```

Result: **Block** for this EDGE-032 run, not counted as Pass. The command was
not run because `CORDUM_INTEGRATION` was unset and the Docker server prerequisite
could not be verified: `docker version --format '{{.Server.Version}}'` timed out
after 34 seconds. `where.exe docker` found Docker client binaries, but that is
not enough to satisfy the documented live-stack prerequisites.

First failing test: none in the three package-level backend gates above.

### Step 5 — Fake-hook P0 E2E evidence

Architect decision: chat `msg-11ec3c3c` reframed this gate for the current
non-Docker worker environment. The EDGE-027 fake-hook script's documented SKIP
mode is accepted as the correct live-stack result when Docker/current Gateway
stack prerequisites are unavailable, and the EDGE-028 backend integration suite
is the primary gate-equivalent evidence for the semantics: session create,
evaluate ALLOW/DENY/REQUIRE_APPROVAL, event persistence/streaming, approval
consume/retry, artifact metadata, evidence export, auth/tenant isolation,
redaction, Safety Kernel unavailable behavior, and Redis unavailable behavior.

Step-5 gate rows:

| Gate evidence row | Result | Status |
| --- | --- | --- |
| EDGE-027 fake-hook E2E live-stack | PASS: 5/5 live `make dev-up` PASS lines captured 2026-05-03 from `D:\Cordum\cordum`. Captured under EDGE-039 (task-c7fc618f) following the EDGE-039 / b8afac82 / EDGE-042 fix chain. See "Live-stack PASS lines (2026-05-03)" below for the verbatim run. | Pass |
| EDGE-028 backend integration suite | PASS: miniredis + httptest Gateway suite covers the same acceptance semantics without Docker or external network. | Pass |

Default-mode script probe, rerun after returning the script to HEAD behavior:

```powershell
bash tools/scripts/edge_fake_hook_e2e.sh
```

Result: exit `0` with documented non-destructive skip semantics:

```text
SKIP edge_fake_hook_e2e: https://localhost:8081 reachable but CORDUM_INTEGRATION not set; default mode is non-destructive
EDGE_FAKE_HOOK_DEFAULT_EXIT=0
[edge_fake_hook_e2e] API_BASE=https://localhost:8081
```

Strict live-stack command shape, with `CORDUM_API_KEY` loaded from `.env` but
never printed:

```powershell
CORDUM_INTEGRATION=1 CORDUM_API_KEY=<redacted-from-.env> CORDUM_TENANT_ID=default `
  bash tools/scripts/edge_fake_hook_e2e.sh
```

The strict live-stack run exited `1` because the reachable Gateway was stale and
returned `404` for Edge routes, not because the Edge acceptance semantics failed
in current HEAD:

```text
[edge_fake_hook_e2e] API_BASE=https://localhost:8081
[edge_fake_hook_e2e] edge_session_setup POST /api/v1/edge/sessions -> HTTP 404
FAIL edge_session_setup: create edge session returned HTTP 404; want 201
```

Follow-up probes showed the live Gateway at `https://localhost:8081` was healthy
for pre-Edge routes but was not serving the P0 Edge API surface:

```text
GET /api/v1/status with the local API key -> HTTP 200
GET /api/v1/jobs with the local API key -> HTTP 200
GET /api/v1/edge/sessions with the local API key -> HTTP 404
GET /api/v1/edge/evaluate with the local API key -> HTTP 404
GET /api/v1/edge/events with the local API key -> HTTP 404
POST /api/v1/edge/sessions with the script-equivalent body -> HTTP 404
```

Docker/stack remediation remained unavailable in this worker environment:

```text
docker version --format '{{.Server.Version}}' -> timed out
docker ps --format ... -> timed out
WSL bash: docker command not found in the active distro
```

Fresh EDGE-028 gate-equivalent commands run from `D:\Cordum\cordum` with
`TEMP`, `TMP`, and `GOTMPDIR` rooted at `D:\Cordum\.go-tmp` and `GOMAXPROCS=2`:

```powershell
go test -p 1 -count=1 ./core/edge/...
```

Result: exit `0`.

```text
ok  	github.com/cordum/cordum/core/edge	7.991s
ok  	github.com/cordum/cordum/core/edge/agentd	2.132s
ok  	github.com/cordum/cordum/core/edge/claude	5.833s
```

```powershell
go test -p 1 -count=1 ./core/controlplane/gateway -run 'Test.*Edge'
```

Result: exit `0`.

```text
ok  	github.com/cordum/cordum/core/controlplane/gateway	9.687s
```

```powershell
go test -p 1 -count=3 ./core/controlplane/gateway -run 'Test.*Edge.*(Auth|Tenant|Limit|Redact|Unavailable|Stream|Approval|Export)'
```

Result: exit `0`.

```text
ok  	github.com/cordum/cordum/core/controlplane/gateway	17.261s
```

First failing test in step 5: none in the EDGE-028 gate-equivalent suite.

Live-stack PASS lines (2026-05-03):

After EDGE-039 (task-c7fc618f) landed the agentd binding, principal-id
propagation, evidence-event-id, and Gateway approval-auto-consume fixes (commits
`be748127`, `1760c2c2`, `67bc82d5`, `b8afac82`, plus this task's evaluator and
approval-consume changes), the strict live-stack run from `D:\Cordum\cordum`
against a fresh `make dev-up` stack with `cordum-edge-pack` installed under
`tenant=default` produced all 5 required PASS lines verbatim and in order:

```text
PASS edge_session_setup
PASS edge_pretooluse_deny
PASS edge_approval_flow
PASS edge_posttooluse_artifact
PASS edge_evidence_export
```

Run command (`CORDUM_API_KEY` loaded from `.env`, never printed; synthetic
fixture paths only — no real `.env` file is ever read):

```powershell
CORDUM_INTEGRATION=1 bash tools/scripts/edge_fake_hook_e2e.sh
```

Run-specific identifiers from this capture (other runs will have fresh UUIDs):
session_id `91f6e885-f071-4ba3-8eec-a52f60bee5fd`, execution_id
`80ca7825-510a-4a90-abaa-758ad1153d90`, approval_ref
`edge_appr_al4e3TxNFNKOX4WRITmiHYnZ`. Gateway events listing returned the DENY
event for `edge_pretooluse_deny`, the export endpoint
`POST /api/v1/edge/sessions/{id}/export` returned `HTTP 200` with the bundle,
and negative redaction sanity checks (no literal `OPENAI_API_KEY`,
`AWS_SECRET_ACCESS_KEY`, or `BEGIN PRIVATE KEY` markers anywhere in events,
hook stdout, or export bundle) all passed.

### Step 6 — Dashboard rail and Edge smoke

Dashboard commands were run from `D:\Cordum\cordum\dashboard`.

Project rail command 1:

```powershell
node ./node_modules/typescript/bin/tsc --noEmit
```

Result: exit `0`.

```text
DASHBOARD_TSC_EXIT=0
```

Project rail command 2:

```powershell
npx vitest run
```

Result: exit `0`; no failed tests, so there is no regression versus the
branch-point baseline.

```text
Test Files 222 passed (222)
Tests 1797 passed (1797)
Duration 55.83s
DASHBOARD_VITEST_EXIT=0
```

Vitest emitted jsdom environment warnings for unimplemented canvas `getContext()`
and `window.scrollTo()` during teardown/log flushing, but the process exit code
was `0` and all tests passed.

Project rail command 3:

```powershell
npm run build
```

Result: exit `0`.

```text
> cordum-dashboard-v2@2.0.0 build
> tsc -b && vite build
✓ 3308 modules transformed.
✓ built in 712ms
DASHBOARD_BUILD_EXIT=0
```

Focused Edge dashboard smoke command:

```powershell
npx vitest run src/pages/EdgeSessionsPage.test.tsx src/pages/EdgeSessionDetailPage.test.tsx src/components/edge/EdgeEventInspector.test.tsx src/components/edge/EdgeApprovalsDrawer.test.tsx src/components/edge/EdgeArtifactsPanel.test.tsx src/hooks/useEdgeSessions.test.ts src/api/transform.test.ts
```

Result: exit `0`.

```text
Test Files 7 passed (7)
Tests 109 passed (109)
Duration 2.87s
DASHBOARD_EDGE_SMOKE_EXIT=0
```

Smoke coverage summary:

- Edge Sessions list: summary cards, empty/loading/error states, session rows,
  risk counts, policy/search filters, and row navigation to detail.
- Edge Session detail: session metadata, chronological event timeline, decision
  and kind filters, and event inspector open/close behavior.
- Redacted event inspector: redacted input surface, hashes, approval reference,
  and artifact pointers without raw payload display.
- Approval drawer: approval request rows and approve/reject action wiring scoped
  to the current Edge session/principal.
- Artifacts panel and evidence export link/API path: artifact pointer rendering,
  missing-artifact handling, export mapping, and export mutation/query invalidation.

This step proves the dashboard build/test rail and component-level Edge surfaces.
Live cross-surface session/execution/event ID tracing remains for step 7.

### Step 7 — Acceptance flow cross-check

Because the live Docker/Gateway stack remained unavailable for strict script
mode, this cross-check traces the same synthetic flow across the committed hook,
agentd, Gateway, dashboard, and export evidence surfaces using focused automated
tests plus the dashboard smoke evidence from step 6.

Focused Gateway acceptance-flow command:

```powershell
go test -p 1 -count=1 ./core/controlplane/gateway -run 'TestGatewayEdge(EndToEndEvaluateApprovalStreamAndExport|RedactionRoundTripAcrossEventsApprovalsAndExport)' -v
```

Result: exit `0`.

```text
=== RUN   TestGatewayEdgeEndToEndEvaluateApprovalStreamAndExport
--- PASS: TestGatewayEdgeEndToEndEvaluateApprovalStreamAndExport (0.08s)
=== RUN   TestGatewayEdgeRedactionRoundTripAcrossEventsApprovalsAndExport
--- PASS: TestGatewayEdgeRedactionRoundTripAcrossEventsApprovalsAndExport (0.06s)
PASS
ok  	github.com/cordum/cordum/core/controlplane/gateway	0.381s
```

Gateway trace summary:

- Session and execution IDs originate from the created Edge session/execution and
  are reused by every evaluate/event/export assertion.
- PreToolUse policy decision events are persisted and streamed with exact event
  IDs, decisions, rule IDs, and the session policy snapshot.
- Approval-required edit flow returns `REQUIRE_APPROVAL`, a generated
  `approval_ref`, dashboard approval URL shape, matching action/input hashes,
  and `manual_approval` / `approve_then_retry` guidance.
- Operator approval consumes exactly once: first retry returns `ALLOW` with the
  same approval/action/input hashes; duplicate retry returns `DENY` with
  `request_new_approval` guidance.
- Destructive action provenance requires the resolved approval audit event
  (`EventEdgeApprovalResolved` / `edge.approval_resolved`) for the same
  tenant/ref/hash; an approval-requested row alone is not sufficient evidence.
- PostToolUse-style artifact event stores metadata-only artifact pointers; the
  export bundle includes session/execution IDs, ordered event IDs, approval
  issue/approve/consume timestamps, resolver identity, and missing-artifact
  manifest metadata without raw artifact bodies.
- Redaction round-trip test injects synthetic markers and asserts stored events,
  approval detail, artifact manifests, and export response contain redaction
  markers/hashes but never literal synthetic secrets.

Focused hook/agentd command:

```powershell
go test -p 1 -count=1 ./core/edge/claude -run 'Test(MapHookInputReadSecretCarriesSecretsTag|MapHookInputEditWriteCapability|MapHookInputPostToolUseSuccess|MapEdgeDecisionToHookOutputPreToolUseDeny|MapEdgeDecisionToHookOutputPreToolUseRequireApprovalIsImmediateDenyWithApprovalRef|MapEdgeDecisionToHookOutputPostToolUseBlockDoesNotClaimPrevention|RunPreToolUseDenyWritesDenyReasonForClaude|RunPostToolUseBlockProvidesFeedbackWithoutClaimingPrevention)' -v
```

Result: exit `0`.

```text
=== RUN   TestMapEdgeDecisionToHookOutputPreToolUseDeny
--- PASS: TestMapEdgeDecisionToHookOutputPreToolUseDeny (0.00s)
=== RUN   TestMapEdgeDecisionToHookOutputPreToolUseRequireApprovalIsImmediateDenyWithApprovalRef
--- PASS: TestMapEdgeDecisionToHookOutputPreToolUseRequireApprovalIsImmediateDenyWithApprovalRef (0.00s)
=== RUN   TestMapEdgeDecisionToHookOutputPostToolUseBlockDoesNotClaimPrevention
--- PASS: TestMapEdgeDecisionToHookOutputPostToolUseBlockDoesNotClaimPrevention (0.00s)
=== RUN   TestMapHookInputReadSecretCarriesSecretsTag
--- PASS: TestMapHookInputReadSecretCarriesSecretsTag (0.00s)
=== RUN   TestMapHookInputEditWriteCapability
--- PASS: TestMapHookInputEditWriteCapability (0.00s)
=== RUN   TestMapHookInputPostToolUseSuccess
--- PASS: TestMapHookInputPostToolUseSuccess (0.00s)
=== RUN   TestRunPreToolUseDenyWritesDenyReasonForClaude
--- PASS: TestRunPreToolUseDenyWritesDenyReasonForClaude (0.00s)
=== RUN   TestRunPostToolUseBlockProvidesFeedbackWithoutClaimingPrevention
--- PASS: TestRunPostToolUseBlockProvidesFeedbackWithoutClaimingPrevention (0.00s)
PASS
ok  	github.com/cordum/cordum/core/edge/claude	0.028s
```

Focused agentd evidence command:

```powershell
go test -p 1 -count=1 ./core/edge/agentd -run 'Test(BuildDecisionEvidenceEventAttachesArtifactPointers|RecordDecisionEvidenceWritesSanitizedDecisionEvent)' -v
```

Result: exit `0`.

```text
=== RUN   TestRecordDecisionEvidenceWritesSanitizedDecisionEvent
--- PASS: TestRecordDecisionEvidenceWritesSanitizedDecisionEvent (0.00s)
=== RUN   TestBuildDecisionEvidenceEventAttachesArtifactPointers
--- PASS: TestBuildDecisionEvidenceEventAttachesArtifactPointers (0.00s)
PASS
ok  	github.com/cordum/cordum/core/edge/agentd	0.026s
```

Dashboard surface evidence is the step-6 focused smoke: Edge Sessions list/detail,
redacted inspector, approval drawer, artifacts panel, export hook/API mapping,
and transform-layer export bundle mapping all passed in `7` files / `109` tests.

Acceptance-flow conclusion: the synthetic `Read .env` path is denied without
raw secret display, the synthetic edit path produces approval guidance and a
single-use approval consume on retry, and the synthetic PostToolUse/artifact path
lands metadata-only artifact pointers in events/export. The same
`session_id`/`execution_id`/event IDs are asserted across Gateway event pages,
stream packets, approval records, dashboard-facing hook data, and export bundle
fixtures. No real Claude process, external network, or real secret payload was
required.

### Step 8 — Docs and demo runbook reachability

I walked the new-engineer documentation path from the top-level Edge overview
through the P0 demo and operator runbook, then cross-checked the local E2E
script contract and CLI diagnostic docs. No product docs needed wording changes;
this section records the reachability evidence only.

Path existence check:

```powershell
docs/edge/README.md
docs/edge/demo.md
docs/edge/runbook.md
docs/LOCAL_E2E.md
docs/edge/cordumctl-edge-claude.md
docs/edge/cordumctl-edge-doctor.md
docs/demo-edge-claude-spike.md
tools/scripts/edge_fake_hook_e2e.sh
```

Result: all paths exist.

Runbook coverage found:

- `docs/edge/README.md` explains the P0 governed path
  `Claude Code -> cordum-hook -> cordum-agentd -> Gateway /api/v1/edge/*`,
  the Edge data hierarchy, artifact pointer/redaction boundaries, OSS vs
  enterprise enforcement, and links to the demo and operator paths.
- `docs/edge/demo.md` has prerequisites, an automated fake-hook E2E path,
  optional/manual real-Claude steps, expected deny/approval/artifact/export
  proof, screenshot/GIF checklist, cleanup, and troubleshooting links.
- `docs/edge/runbook.md` has operating rules, fast triage, common failures,
  recovery playbooks, and a go/no-go checklist. It explicitly says synthetic
  demo data only and that real Claude Code is manual/optional.
- `docs/LOCAL_E2E.md#edge-fake-hook-e2e` documents prerequisites, environment
  variables, strict-mode PASS lines, default SKIP semantics, and the direct
  Gateway bypass fallback for hosts that cannot build/run hook + agentd.
- `docs/edge/cordumctl-edge-claude.md` documents `--dry-run` /
  `--settings-output` so CI/new-engineer validation does not require real
  Claude launch.
- `docs/edge/cordumctl-edge-doctor.md` documents local diagnostics, JSON output,
  policy-mode implications, troubleshooting, and that secrets are not printed.
- `docs/demo-edge-claude-spike.md` remains a historical EDGE-000 spike runbook,
  not the current P0 acceptance path, and includes its own no-secret evidence
  rule.

Doc contract checks:

```powershell
Select-String docs/LOCAL_E2E.md for:
  PASS edge_session_setup
  PASS edge_pretooluse_deny
  PASS edge_approval_flow
  PASS edge_posttooluse_artifact
  PASS edge_evidence_export
  SKIP edge_fake_hook_e2e
  CORDUM_INTEGRATION
```

Result: all seven strings present.

```powershell
Select-String docs/edge/demo.md for:
  manual and optional
  Do not paste real secrets
Select-String docs/edge/runbook.md for:
  Real Claude Code is manual/optional
  Default mode should SKIP safely
Select-String docs/edge/cordumctl-edge-claude.md for:
  --dry-run
Select-String docs/edge/cordumctl-edge-doctor.md for:
  secrets are not printed
```

Result: all strings present.

Script syntax check:

```powershell
bash -n tools/scripts/edge_fake_hook_e2e.sh
```

Result: exit `0`.

Secret-example check:

```powershell
Select-String -Path docs/edge/*.md,docs/LOCAL_E2E.md `
  -Pattern 'CORDUM_API_KEY=([^<\s][^\s]*)'
```

Result: no concrete `CORDUM_API_KEY` values in the checked docs; examples use
placeholders or instruct operators to load local environment values without
printing them.

Step-8 conclusion: a new engineer can follow the documented path without a real
Claude requirement for CI validation, without real-secret examples, with clear
strict-vs-SKIP fake-hook semantics, and with troubleshooting for Gateway/auth,
agentd/hook startup, policy mismatch, approvals, dashboard stream, export, and
incident handling.

### Step 8 — Docs and demo runbook reachability

Docs audited as a new engineer from the repo root:

- `docs/edge/README.md`
- `docs/LOCAL_E2E.md#edge-fake-hook-e2e`
- `docs/edge/demo.md`
- `docs/edge/runbook.md`
- `docs/demo-edge-claude-spike.md`
- `docs/demo-edge-claude.md`
- `TESTING.md#edge-backend-integration-tests`
- `docs/security/edge-p0-threat-model.md#edge-032-acceptance-checklist`

Reachability checks:

```powershell
# file existence for the docs above
# required phrase audit for SKIP/PASS semantics, integration env vars,
# fake-hook path, real-Claude optionality, secret hygiene, and wrapper boundary
# relative Markdown link resolution for README/demo/runbook/LOCAL_E2E/spike docs
```

Results:

```text
docs/edge/README.md exists=True
docs/edge/demo.md exists=True
docs/edge/runbook.md exists=True
docs/LOCAL_E2E.md exists=True
docs/demo-edge-claude-spike.md exists=True
docs/demo-edge-claude.md exists=True
TESTING.md exists=True
docs/security/edge-p0-threat-model.md exists=True
DOC_LINK_MISSING_COUNT 0
```

Required operator/readiness content verified:

- Exact fake-hook commands and PASS lines appear in `docs/LOCAL_E2E.md` and
  `docs/edge/demo.md`.
- Default SKIP semantics and strict integration behavior are documented,
  including `CORDUM_INTEGRATION`, `CORDUM_EDGE_E2E_START_STACK`, timeout,
  temp retention, and bypass-mode environment variables.
- New-engineer path separates CI-safe fake-hook automation from optional/manual
  real Claude Code; CI does not require a Claude install.
- Prerequisites cover running stack/Gateway, API key and tenant, `curl`, `jq`,
  demo policy overlay, hook/agentd binaries, `openssl`, and Docker only when
  stack startup is requested.
- Expected output, exit codes, troubleshooting, `cordumctl edge doctor`, and
  go/no-go checklist are reachable from the runbook.
- Docs repeat the security boundary: synthetic data only, do not print API keys
  or hook nonces, wrapper is not enterprise fleet enforcement, and raw prompts,
  transcripts, tool payloads, command output, provider tokens, and `.env` bytes
  must not appear in evidence.

No doc wording gaps required edits outside this evidence document.

### Step 9 — Security checklist signoff

Security source reviewed: `docs/security/edge-p0-threat-model.md`, including
Gateway posture, dashboard posture, hook/agentd fail-closed behavior, redaction,
audit/artifact/retention posture, the PRD §26.1 threat table, known-gaps catalog,
adversarial review notes, and the EDGE-032 acceptance checklist.

I updated the dashboard posture section to reflect the current P0 routed Edge
Sessions list/detail pages and EDGE-032 dashboard rail evidence, replacing the
older EDGE-031 note that page routes did not yet exist. The known-gaps catalog no
longer lists dashboard page-level security review as a deferred gap because
EDGE-023/024/025/026 plus the EDGE-032 dashboard rail and focused smoke cover the
P0 dashboard surface.

Security validation commands:

```powershell
go test -p 1 -count=1 ./core/edge/... -run 'Test.*(Redact|Secret|Tenant|FailMode)'
```

Result: exit `0`.

```text
ok  	github.com/cordum/cordum/core/edge	0.084s
ok  	github.com/cordum/cordum/core/edge/agentd	0.044s
ok  	github.com/cordum/cordum/core/edge/claude	0.038s
```

```powershell
bash tools/scripts/lint_no_secret_log.sh
```

Result: exit `0`.

```text
OK: No raw secret logging found in scanned files
SECRET_LOG_LINT_EXIT=0
```

Threat-model structural checks:

```text
THREAT_ROWS 11
STATUS_COUNTS {'Deferred-enterprise-control': 4, 'Implemented-with-dev-tradeoff': 2, 'Implemented': 5}
BAD_STATUS_COUNT 0
OPEN_GAP_MATCH_COUNT 0
DASHBOARD_STALE_GAP_MATCH_COUNT 0
```

Security cross-check summary:

- All 11 PRD §26.1 threats are represented and each status is in the closed set:
  `Implemented`, `Implemented-with-dev-tradeoff`, or
  `Deferred-enterprise-control`.
- Deferred controls have owners and follow-up IDs: EDGE-150 managed deployment,
  EDGE-151 binary signing/notarization, EDGE-152 keychain/service bootstrap,
  EDGE-100..105 MCP Gateway, EDGE-120..124 LLM Proxy, and EDGE-140..144 runtime
  detection.
- Gateway auth/tenant/body/TLS posture remains intact: `/api/v1/edge/*` stays
  under existing auth, `X-Tenant-ID` isolation, body bounds, rate limiting, and
  documented Gateway TLS deployment settings.
- Redaction-before-persistence and export posture remain intact: Redis events
  store redacted summaries/hashes/artifact pointers, export bundles are
  metadata/manifests by default, and secret-log lint plus redaction tests pass.
- Dashboard posture is current: routed P0 Edge pages use the shared auth client,
  sanitized transforms, narrowed event-stream cache entries, and the dashboard
  rail/focused smoke from step 6.
- Wrapper-vs-enterprise language remains honest: the developer wrapper is the
  adoption/demo path; enterprise bypass prevention depends on managed settings,
  endpoint controls, binary trust, and service bootstrap.

### Step 10 — Release-readiness boundary check

Board boundary check using Moe task inventory:

- P1 MCP Gateway tasks EDGE-100, EDGE-101, EDGE-102, EDGE-103, EDGE-104, and
  EDGE-105 are all `PLANNING`, unassigned, and have `planStepCount=0`.
- P2 LLM Proxy tasks EDGE-120, EDGE-121, EDGE-122, EDGE-123, and EDGE-124 are
  all `PLANNING`, unassigned, and have `planStepCount=0`.
- P3 Shadow/runtime tasks EDGE-140, EDGE-141, EDGE-142, EDGE-143, and EDGE-144
  are all `PLANNING`, unassigned, and have `planStepCount=0`.
- Enterprise hardening follow-ups EDGE-150, EDGE-151, and EDGE-152 are also
  `PLANNING`, unassigned, and tracked as deferred controls in the threat model.

Shadow dashboard boundary commands:

```powershell
rg -n 'Shadow Agents|ShadowAgents|shadow agents|shadow-agent|ShadowAgent' dashboard/src -g '*.tsx' -g '*.ts'
rg -n 'path="/.*shadow|to="/.*shadow|Shadow' dashboard/src/App.tsx dashboard/src/components dashboard/src/pages -g '*.tsx' -g '*.ts'
```

Results:

```text
FIRST_EXIT=1
SECOND_EXIT=0
```

Interpretation: no `Shadow Agents` / `ShadowAgent` / runtime-agent dashboard tab
exists in the dashboard source. The second grep finds pre-existing Policy Bundle
shadow-evaluation UI (`BundleShadowTab`, `ShadowImpactPanel`, policy shadow API
hooks), which is policy-bundle comparison functionality and not the P0 Shadow
Agents/runtime detector surface.

Raw event payload boundary command:

```powershell
rg -n 'RawPayload|raw_payload|tool_input|tool_result|transcript|raw_prompt|raw_output' core/edge/model.go core/edge/store_redis.go core/edge/export.go core/edge/approval_store.go core/controlplane/gateway/handlers_edge_events.go core/controlplane/gateway/handlers_edge_evaluate.go
```

Result: hits are limited to artifact type constants and Gateway request fields
accepted so the handlers can reject raw/transcript payloads before persistence;
`core/edge/model.go`, Redis store, approval store, and export bundle schemas do
not persist raw prompt/tool/transcript bodies. This is backed by step-7/9 tests
and secret-log lint.

Optional edge doctor diagnostic:

```powershell
go build -p 1 -o D:\Cordum\.go-tmp\edge032\bin\cordumctl.exe ./cmd/cordumctl
D:\Cordum\.go-tmp\edge032\bin\cordumctl.exe edge doctor --json
```

Result: build exit `0`; doctor emitted structured JSON and exit code `1` because
this host lacks a valid live Gateway/API key/agentd demo environment. The command
still proves the optional local diagnostic is present and reports concrete
fixes without a P0 Shadow Agents dashboard:

```text
summary: fail=6 ok=2 skip=4 warn=0
EDGE_DOCTOR_EXIT=1
policyMode=enforce
```

Known deferred gaps remain owned by follow-up task IDs in the threat model:
EDGE-150 managed settings deployment, EDGE-151 hook/agentd binary integrity,
EDGE-152 keychain/service bootstrap, EDGE-100..105 MCP Gateway, EDGE-120..124
LLM Proxy, and EDGE-140..144 runtime detection. No boundary breach found.

### Step 12 — Adversarial review

The dedicated `adversarial-self-review` skill is not installed in this worker's
available skill list, so this review applies the same skeptical checks manually.

Adversarial questions and outcomes:

- Could the matrix be green while a release gate is still blocked? No. A parser
  over the acceptance matrix returned `MATRIX_ROWS 12` and
  `MATRIX_COUNTS {'Pass': 12}`.
- Could the evidence doc be relying on forbidden status wording? No. The status
  grep returned `PENDING_PARTIAL_MATCH_COUNT 0`.
- Could the final recommendation be missing a hard gate's output? No. The
  structural audit found all required backend commands, dashboard rail lines,
  fake-hook strict PASS-line names, fake-hook SKIP result, runbook/security
  links, and known-gap owner IDs: `REQUIRED_EVIDENCE_MISSING_COUNT 0`.
- Could the fake-hook result be overstated? No. The recommendation explicitly
  says strict live-stack output was not observed on this host and ties signoff to
  the architect-approved SKIP-mode contract plus fresh EDGE-028 gate-equivalent
  evidence.
- Could the dashboard rail be stale? The document includes the exact step-6
  results: `DASHBOARD_TSC_EXIT=0`, `Test Files 222 passed (222)`,
  `Tests 1797 passed (1797)`, `DASHBOARD_VITEST_EXIT=0`,
  `DASHBOARD_BUILD_EXIT=0`, and `DASHBOARD_EDGE_SMOKE_EXIT=0`.
- Could security rows still contain an open unowned gap? No. The threat mapping
  parser returned `THREAT_ROWS 11`,
  `THREAT_STATUS_COUNTS {'Deferred-enterprise-control': 4,
  'Implemented-with-dev-tradeoff': 2, 'Implemented': 5}`, and
  `THREAT_BAD_STATUS_COUNT 0`.
- Could P1/P2/P3 have started before P0 acceptance? Moe inventory confirmed
  EDGE-100..105, EDGE-120..124, and EDGE-140..144 remain `PLANNING`,
  unassigned, with `planStepCount=0`.
- Could a P0 Shadow Agents dashboard tab have slipped in? No. The dashboard grep
  for `Shadow Agents|ShadowAgents|shadow agents|shadow-agent|ShadowAgent`
  returned no matches (`SHADOW_AGENT_GREP_EXIT=1`).
- Could raw prompt/tool/transcript payloads be persisted in Edge model/store
  schemas? The raw-payload grep found only Gateway request fields used for
  rejection plus artifact type constants; model/store/export schemas do not
  persist raw prompt/tool/transcript bodies.

Adversarial review commands:

```powershell
# structural parser over docs/edge/p0-acceptance-evidence.md and
# docs/security/edge-p0-threat-model.md
python <inline-adversarial-doc-check>

rg -n 'Shadow Agents|ShadowAgents|shadow agents|shadow-agent|ShadowAgent' `
  dashboard/src -g '*.tsx' -g '*.ts'

rg -n 'RawPayload|raw_payload|tool_input|tool_result|transcript|raw_prompt|raw_output' `
  core/edge/model.go core/edge/store_redis.go core/edge/export.go `
  core/edge/approval_store.go `
  core/controlplane/gateway/handlers_edge_events.go `
  core/controlplane/gateway/handlers_edge_evaluate.go

git diff --check -- docs/edge/p0-acceptance-evidence.md `
  docs/security/edge-p0-threat-model.md
```

Result summary:

```text
PENDING_PARTIAL_MATCH_COUNT 0
MATRIX_ROWS 12
MATRIX_COUNTS {'Pass': 12}
REQUIRED_EVIDENCE_MISSING_COUNT 0
THREAT_ROWS 11
THREAT_STATUS_COUNTS {'Deferred-enterprise-control': 4, 'Implemented-with-dev-tradeoff': 2, 'Implemented': 5}
THREAT_BAD_STATUS_COUNT 0
ADVERSARIAL_DOC_CHECK=PASS
SHADOW_AGENT_GREP_EXIT=1
RAW_PAYLOAD_GREP_EXIT=0
git diff --check: exit 0
```

Conclusion: no matrix row was greened only by a non-nil or unverified claim; the
known SKIP/live-stack limitation is documented honestly; no real secrets, real
Claude requirement, external network requirement, raw event payload leak, or
P1/P2/P3 boundary breach was found.
