# Changelog

All notable changes to this project will be documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added

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
