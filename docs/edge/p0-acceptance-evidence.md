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
| Dashboard shows a live EdgeSession. | EDGE-022/023/024/025/026 DONE; dashboard Edge pages/components; `docs/edge/README.md`. | Dashboard rail plus manual smoke from `dashboard/`. | EDGE-032 steps 6-7 | Block |
| `PreToolUse` events are stored and streamed. | Gateway events/stream tests from EDGE-028; fake-hook E2E; dashboard timeline. | Backend Edge tests; `bash tools/scripts/edge_fake_hook_e2e.sh`; dashboard smoke. | EDGE-032 steps 4-7 | Block |
| `Read .env` is denied by policy. | PRD §24.7; policy/classifier/evaluate tests; fake-hook `edge_pretooluse_deny`. | Backend Edge tests; strict fake-hook E2E. | EDGE-032 steps 4-5 | Block |
| `Edit` can require approval. | Approval store/API tests; fake-hook `edge_approval_flow`; approval drawer from EDGE-025. | Backend Edge tests; strict fake-hook E2E; dashboard smoke. | EDGE-032 steps 4-7 | Block |
| Approval in dashboard allows the approval-requested action. | EDGE-011/012/012.1/012.2 APIs; EDGE-025 dashboard drawer; fake-hook retry/consume flow. | Strict fake-hook E2E; dashboard approval smoke. | EDGE-032 steps 5-7 | Block |
| `PostToolUse` creates audit events and artifacts. | EDGE-013/014/016; fake-hook `edge_posttooluse_artifact`; export tests. | Backend Edge tests; strict fake-hook E2E. | EDGE-032 steps 4-5, 7 | Block |
| Session can export evidence bundle. | EDGE-013 export; EDGE-028 export tests; fake-hook `edge_evidence_export`. | Backend Edge tests; strict fake-hook E2E; export smoke. | EDGE-032 steps 4-7 | Block |
| Logs are structured and redacted. | EDGE-014 observability; `TestEdgeObservabilitySecretLeakMatrix`; `TestWriteEdgeErrorRedactsSecretDetails`; threat model row for raw prompt/tool-output leakage. | Backend Edge tests plus security checklist review. | EDGE-032 steps 4, 9 | Block |
| Docs and demo script exist. | `docs/edge/*`; `docs/LOCAL_E2E.md`; `tools/scripts/edge_fake_hook_e2e.sh`; EDGE-029/030 DONE. | New-engineer docs/runbook walk-through. | EDGE-032 step 8 | Block |
| Optional observe-only `edge doctor` shadow-agent diagnostic can report local ungoverned-agent signals without requiring a P0 Shadow Agents dashboard. | `docs/edge/cordumctl-edge-doctor.md`; ADR-010 Shadow Agents scope; EDGE-021 DONE. | `cordumctl edge doctor --json` when CLI binaries/prereqs are available; docs/runbook review. | EDGE-032 steps 3, 8, 10 | Block |
| No production security requirement is weakened. | PRD §26; ADR-010 security/token storage; threat model acceptance checklist. | Security checklist review and release boundary grep/audit. | EDGE-032 steps 9-10, 12 | Block |

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
| EDGE-027 fake-hook E2E live-stack | SKIP: Docker/current live stack unavailable; `https://localhost:8081` was reachable but served a stale Gateway image without `/api/v1/edge/*`, and Docker server checks timed out. This follows the EDGE-027 SKIP-mode contract for non-integration environments. | Pass |
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
Required strict live-stack PASS lines were not observed because the live stack
was stale; per architect decision they are optional follow-up evidence once
Docker/current Gateway is available, not a P0 ship blocker. No real `.env` file
was read by the E2E fixture; only synthetic fixture paths were used, and no API
key value was printed.
