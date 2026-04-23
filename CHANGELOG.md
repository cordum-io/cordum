# Changelog

All notable changes to this project will be documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/).

## [Unreleased]

### Added

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

### Fixed

#### Critical
- **NATS reconnect** — Safety kernel and scheduler re-subscribe to `sys.config.changed` on NATS reconnect. Previously degraded silently to 30s polling on network partition.
- **Config scope corruption** — `SetWithRetry` now deep-merges config updates, preserving existing keys. Policy bundles no longer silently wiped by pools config pushes. Startup migration moves stale bundles to correct scope.
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
- **lodash** bumped to 4.18.1 (CVE-2026-4800, CVE-2026-2950)

### Changed

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

- Removed the legacy OpenAPI sidecars `docs/api/openapi/cordum-rest.yaml`
  and `docs/api/openapi/cordum.swagger.json`. `docs/api/openapi/cordum-api.yaml`
  is now the single canonical OpenAPI 3 spec, `make openapi` is a pure
  Redocly validation pass, and the local/public Swagger UI wrappers now load
  only that canonical spec. Also removed the legacy prefixed MCP transport
  aliases `/api/v1/mcp/{sse,message,status}`; MCP transport is now exposed
  only at `/mcp/{sse,message,status}` while MCP governance REST endpoints
  remain under `/api/v1/mcp/*`. See
  [`docs/cleanup/openapi-legacy-audit.md`](docs/cleanup/openapi-legacy-audit.md)
  `Audit re-verification 2026-04-23` for the ground-truth timeline.

### Added

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

### Changed
- Auth: Login endpoint supports both user credentials and API keys
- Auth: AuthConfig includes `user_auth_enabled` and `saml_enterprise` fields
- Scheduler: Output policy integration in dispatch pipeline
- Workflow engine: Support for 6 new step types alongside existing job/fan-out/condition/delay/approval/notify
- Dashboard: Sidebar navigation consolidated to 9+1 items (removed /context, /pools, /system, /trace, /tools)
- Dashboard: Routes reorganized under new page structure
- Safety kernel: Policy fragments from config service merged with file/URL policy on load/reload

### Fixed

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

### Security
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
- Dashboard: disable API key storage in localStorage (opt-in embed via `CORDUM_DASHBOARD_EMBED_API_KEY`).
- Breaking: clients must send `X-Tenant-ID` on all `/api/*` requests.
