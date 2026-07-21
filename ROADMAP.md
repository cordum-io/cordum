# Cordum Roadmap

> **Last Updated:** June 24, 2026

This roadmap outlines our vision for Cordum's evolution. Priorities may shift based on community feedback and production learnings.

## Current Focus: v0.9.0 → v1.0.0 (Q1-Q2 2026)

The path to v1.0.0 focuses on **production hardening** and **API stability**. Backend services, horizontal scaling, and documentation are complete. Remaining work is dashboard UX gaps, observability, and enterprise features.

### Stability & Reliability
- [x] Scheduler reconciler for timeouts/deadlines
- [x] Pending job replayer for stalled/missed dispatches
- [x] Dead-letter queue (DLQ) capture + retry/inspection endpoints
- [x] Saga-based compensation rollback for workflows
- [x] Complete API documentation with OpenAPI spec
- [x] Comprehensive error handling guide
- [x] Disaster recovery playbook
- [x] Horizontal scaling (2-6 replicas of every service)
- [ ] Zero memory leaks over 72h continuous operation (no endurance test yet)

### Performance
- [x] gRPC API option
- [x] Policy caching layer
- [x] Redis connection pool tuning for multi-replica
- [ ] 15k ops/sec policy evaluation throughput (target — no benchmark yet)
- [ ] <5ms p99 end-to-end latency (target — no benchmark yet)
- [ ] ARM64 optimization (15% efficiency target)

### Enterprise Features
- [x] OIDC/SSO integration (with JWKS Redis cache + refresh jitter)
- [x] User/password authentication (separate from API keys)
- [x] Basic role-based access (admin/user)
- [x] Audit event capture (with NATS-backed durable buffer)
- [ ] SAML support
- [ ] Advanced RBAC (resource-level permissions, inheritance)
- [ ] Audit export (JSON, CSV, SIEM)
- [ ] Air-gapped deployment guide
- [ ] FIPS 140-2 compliance mode

---

## Agent Governance: GitHub Copilot (Total Copilot Governance)

> **Goal:** govern **every Copilot chat turn and every action** (code edits, terminal,
> tool calls) with real enforcement — secrets never reach the model provider, every
> interaction is audited, and dangerous actions are blocked or routed to human
> approval. Built as a defense-in-depth stack on Cordum Edge (the same
> `cordum-hook → cordum-agentd → safety kernel` path used for Claude Code).
>
> **Enforcement model:** *cooperative* layers (hooks) govern everything an honest
> client routes through them and give a full audit trail; the *mandatory* layers
> (LLM proxy, OS sandbox) hold even against active bypass. A demo / trusted-team
> deployment needs only the cooperative + proxy layers; the sandbox and fleet
> rollout are production hardening.

### Shipped
- [x] **Phase 0 — Hook contract validation** — confirmed VS Code Copilot Agent Hooks (`UserPromptSubmit` / `PreToolUse` / `PostToolUse`) match Claude Code's contract, so Cordum's Edge stack is directly reusable with near-zero new surface.
- [x] **Phase 1 — Copilot Edge hook adapter** (PR #371) — `cordum-hook copilot <event>` governs **every chat** (`UserPromptSubmit`) and **every tool action** (`PreToolUse`, blockable) through agentd → safety kernel; `AdapterCopilotHook` attribution; managed hook-settings generator. Reuses classifier / redaction / approval / fail-closed unchanged.
- [x] **Phase 2 — LLM-proxy ingest** (PR #372) — `core/edge/llmingest` + `POST /api/v1/edge/llm/events`: an enterprise LLM proxy routes every model request/response through Cordum for **mandatory redaction + audit** (secrets never reach the provider, even if a hook is bypassed). Records each turn as a `layer=llm` event and returns a per-event `record` / `redact` advisory plus secret finding *types*. Block/deny reuses the layer-agnostic `POST /api/v1/edge/evaluate`. Disabled by default (`CORDUM_EDGE_LLM_INGEST_ENABLED`). See `docs/edge/llm-proxy-governance.md`.
- [x] **Copilot MCP integration** (PR #370) — `copilot.Store` (Redis) + ingestion tap + session-id threading so Copilot's use of Cordum's own MCP tools is recorded into Copilot sessions.

### In progress / needed
- [ ] **Phase 3 — MCP broker hardening** — govern Copilot's *external* tool calls (GitHub/Jira/AWS): scope-filter fronted-upstream `tools/list`, make per-identity authz on upstream `tools/call` mandatory (independent of `policy_gate_enabled`), per-request multi-tenant/multi-upstream selection, add `AllowedUpstreams` to `AgentIdentity`. _Needed only if Copilot uses Cordum-brokered MCP servers._
- [ ] **Phase 4 — CAP governed execution** — effectful work Copilot delegates runs as a CAP job/workflow under safety-before-dispatch + output policy/quarantine + audit. Mostly reuse; land PR #370 and document the pattern. _Needed only if Copilot triggers server-side jobs/deploys._
- [ ] **Phase 5 — OS / devcontainer sandbox** — kernel-level block of native file/terminal that hooks can only observe (agentd / eBPF / Tetragon via the `runtime-sidecar` adapter + `core/edge/runtimeingest`). The no-escape backstop. _Needed only to defend against active bypass or for strict compliance._
- [ ] **Phase 6 — Per-developer identity + fleet rollout** — per-dev SSO identity (OIDC → auto-provisioned agent identity, no shared key), MDM-managed distribution of the hook + LLM-proxy CA + MCP config (extended `cordumctl edge managed-settings`), hosted HA gateway + SIEM export. _Needed for org-wide / fleet scale._

### Deployment guidance
- **Demo / trusted internal team:** Phases 1 + 2 are the complete story (every chat + action governed, audited, secrets redacted). Add 3/4 only if MCP / CAP are in the scenario; the sandbox and fleet rollout are roadmap, not required.
- **Production at scale / adversarial threat model:** add Phase 5 (mandatory exec/FS enforcement) and Phase 6 (per-dev identity + MDM rollout).
- Reference docs: `docs/edge/llm-proxy-governance.md`, `docs/edge/managed-settings-deploy.md`, `docs/edge/environment-variables.md`.

---

## Completed — Q1 2026

### Dashboard Full Rebuild (215 tasks across 12 epics)
- [x] **Foundation & AppShell** — sidebar navigation, routing, command palette (Cmd+K), theme system
- [x] **Command Center (Overview)** — metrics dashboard, system health, recent activity
- [x] **Agent Fleet** — worker pool management, heartbeat monitoring, status badges
- [x] **Jobs** — job list with filters, detail view, state machine visualization, submit drawer, artifacts panel
- [x] **Workflows** — workflow builder, DAG canvas, run visualization, node config panel, step type nodes
- [x] **Safety Policies** — policy studio, visual rule builder, bundle editor, output rules tab
- [x] **Approvals** — approval queue with badge count, approve/reject actions
- [x] **Audit Trail** — audit log with filters, export, search
- [x] **Dead Letter Queue** — DLQ page with retry/inspect, badge count
- [x] **Packs** — pack catalog, install/uninstall, marketplace browser
- [x] **Settings** — system health tab, users management, API key management, MCP config
- [x] **Schemas** — schema registry, validation, detail views

### Security & Production Readiness (16 tasks)
- [x] **SSRF mitigation** — private IP filtering in marketplace URL validation
- [x] **Auth hardening** — public path whitelist, session token entropy (crypto/rand)
- [x] **Rate limit fix** — moved rate limiter after auth middleware
- [x] **HSTS headers** — Strict-Transport-Security on all responses
- [x] **Egress network policy** — Kubernetes NetworkPolicy for outbound traffic
- [x] **Redis persistence** — AOF + RDB backup configuration
- [x] **K8s dashboard fix** — production overlay corrections
- [x] **Tenant isolation** — memory store cross-tenant protection
- [x] **Docker health checks** — health probes for all containers
- [x] **Error sanitization** — strip internal details from error responses
- [x] **Password policy** — minimum complexity requirements
- [x] **Brute-force protection** — login attempt rate limiting

### Horizontal Scaling & High Availability (30 tasks)
- [x] **Multi-replica coordination** — all 7 services run 2-6 replicas with Redis distributed locks, NATS queue groups, graceful shutdown
- [x] **Distributed state** — rate limiter, circuit breakers, delay timers, caches, audit buffer migrated from in-memory to Redis/NATS
- [x] **K8s production manifests** — HPA, PodDisruptionBudgets, session affinity, Redis cluster ops
- [x] **HA Docker overlay** — `docker-compose.ha.yaml` with 2-replica topology
- [x] **Validation & gate** — Gate 19 acceptance suite (no duplicate dispatch, no drift, failover)

### MCP Server
- [x] **Stdio transport** — newline-delimited JSON-RPC over stdin/stdout
- [x] **HTTP/SSE transport** — HTTP POST + Server-Sent Events with session management
- [x] **Tools catalog** — 6 tools (submit/cancel job, trigger workflow, approve/reject, query policy)
- [x] **Resources catalog** — 7 resources (jobs, workflows, runs, audit, health, policies)

### Input Safety Fail Modes
- [x] **Configurable fail modes** — `open` (allow through) and `closed` (requeue/quarantine) for input and output safety
- [x] **Dashboard settings** — InputSafetySettings and OutputSafetySettings pages
- [x] **Metrics instrumentation** — `cordum_input_fail_open_total` and `cordum_output_policy_skipped_total` counters

### CAP Protocol & Go SDK
- [x] **CAP v2.5.2 integration** — Handshake, ErrorCode enum, AlertSeverity, MetricsHook
- [x] **Go Worker SDK** — `sdk/runtime/` with typed handler registration, TLS blob store, heartbeat, panic recovery, ECDSA verification

### Bug Fixes — System Audit (25 tasks)
- [x] Concurrency fixes in scheduler engine (per-run mutex)
- [x] Error handling gaps in gateway and workflow engine
- [x] Resource leak fixes (context cancellation, defer patterns)
- [x] JSON encoding issues in API responses
- [x] Policy bundle mapping fixes (YAML content parsing)
- [x] Dashboard-to-backend integration bugs (transform layer, API contract)

### Missing Backend Endpoints (3 tasks)
- [x] **API Key CRUD** — GET/POST/DELETE /auth/keys
- [x] **User CRUD** — GET/PUT/DELETE /users + password change
- [x] **Config shape alignment** — backend {scope,data} wrapper → frontend flat transform

### Workflow Step Types (6 tasks)
- [x] **Switch** — multi-branch condition evaluation
- [x] **Transform** — inline expression evaluation with `${ }` syntax
- [x] **Parallel** — concurrent branch execution (all/any/n_of_m strategies)
- [x] **Loop** — iterative execution with break conditions (while/until/fixed count)
- [x] **Storage** — read/write/delete workflow context paths
- [x] **Sub-workflow** — nested workflow invocation (input/output mapping, circular detection)

### Documentation (22 tasks)
- [x] Output policy operator guide (`docs/output-policy.md`)
- [x] Workflow step types reference (`docs/workflow-step-types.md`)
- [x] API reference (`docs/api-reference.md`) + OpenAPI spec
- [x] Safety kernel deep reference (`docs/safety-kernel.md`)
- [x] MCP server guide (`docs/mcp-server.md`)
- [x] Scheduler internals (`docs/scheduler-internals.md`)
- [x] Dashboard guide (`docs/dashboard-guide.md`)
- [x] Configuration reference (`docs/configuration-reference.md`)
- [x] CLI reference (`docs/cli-reference.md`)
- [x] Architecture Decision Records (`docs/adr/` — 7 ADRs)
- [x] gRPC services reference (`docs/grpc-services.md`)
- [x] K8s deployment guide (`docs/k8s-deployment.md`)
- [x] SDK reference (`docs/sdk-reference.md`)
- [x] WebSocket streaming protocol (`docs/websocket-streaming.md`)
- [x] Production guide with DR/incident runbooks (`docs/production.md`)
- [x] Pack development guide (`docs/pack.md`)
- [x] Docker guide (`docs/DOCKER.md`)
- [x] Troubleshooting cookbook (`docs/troubleshooting.md`)
- [x] CHANGELOG (`CHANGELOG.md`)

---

## In Progress — Q1 2026

### Output Policy Dashboard
- [x] Output policy gRPC contract (`output_policy.proto`)
- [x] Safety kernel output scanners (content patterns, detectors)
- [x] Scheduler output safety client integration
- [ ] Dashboard output quarantine UX — quarantined job list, detail view, release/delete actions
- [ ] Dashboard remediation drawer — review quarantined output, apply redaction, re-approve

### Dashboard Feature Gaps
- [x] Workflow run deletion (single + bulk)
- [x] Policy snapshot capture with name/label
- [x] Policy explain UI
- [ ] Memory panel for job context — view/edit context window in job detail
- [ ] Job submit drawer enhancements — template selection, validation preview
- [ ] Workflow builder improvements — copy/paste nodes, undo/redo, mini-map
- [ ] Settings MCP configuration page — configure MCP server from dashboard

---

## Remaining for v1.0.0

### Safety Kernel Enhancements
- [x] **Policy hot-reload** — update policies without restart
- [x] **Policy simulation mode** — test changes before apply
- [x] **Policy versioning** — track and rollback policy changes
- [ ] **Constraint templates** — reusable constraint patterns

### Workflow Engine Improvements
- [x] **Fan-out step execution** — for_each over datasets with parallel dispatch
- [x] **Conditional branching** — if/else logic in workflows
- [x] **Approval steps** — human-in-the-loop workflow gating
- [x] **Delay/timer steps** — scheduled waits and retries (durable Redis-backed timers)
- [x] **Notify steps** — emit system alerts from workflows
- [x] **Switch steps** — multi-branch condition routing
- [x] **Transform steps** — inline expression evaluation
- [x] **Loop constructs** — iterative loops within workflows (while/until/fixed count)
- [ ] **Workflow templates** — parameterized workflow definitions

### Observability
- [ ] **Distributed tracing** — OpenTelemetry integration
- [ ] **Detailed metrics** — extended Prometheus metrics
- [ ] **Log aggregation** — ELK/Loki integration guide
- [ ] **Performance profiling** — built-in pprof endpoints

### Documentation
- [x] Architecture deep-dive (ADRs)
- [x] Troubleshooting cookbook
- [ ] Migration guide (from Temporal, Airflow)
- [ ] Best practices guide

---

## Q2 2026: Scale & Ecosystem

### Goals
- 🎯 **v1.0.0 GA Release**
- 🎯 **100+ Production Adopters**
- 🎯 **Public Pack Registry**

### Features

#### Distributed Scheduler
- [ ] **Multi-region support** — deploy across regions
- [ ] **Sharded job queue** — partitioned streams for higher throughput
- [x] **Worker affinity** — sticky routing via `preferred_worker_id` label
- [ ] **Auto-scaling** — dynamic worker pool management (HPA-driven)

#### Pack Ecosystem
- [ ] **Public pack registry** — discover and share packs
- [x] **Pack marketplace** — curated pack collection
- [ ] **Pack templates** — scaffolding tool for new packs
- [x] **Pack install/uninstall with overlays** — config/policy/schema/workflow merges
- [ ] **Pack testing framework** — automated pack validation

#### Developer Experience
- [ ] **VS Code extension** — syntax highlighting, debugging
- [x] **Local dev mode** — simplified single-node setup
- [ ] **Interactive CLI** — better command-line UX
- [ ] **Workflow debugger** — step-through execution

### Integrations
- [ ] **Terraform provider** — infrastructure as code
- [ ] **Kubernetes operator** — native K8s deployment
- [ ] **Cloud provider SDKs** — AWS, GCP, Azure helpers
- [ ] **Popular SaaS integrations** — Slack, PagerDuty, etc.

---

## Q3 2026: Intelligence & Automation

### Goals
- 🎯 **v1.1.0 Release**
- 🎯 **ML-Powered Features**
- 🎯 **Self-Healing Workflows**

### Features

#### Intelligent Scheduling
- [ ] **Predictive scheduling** — ML-based resource prediction
- [ ] **Adaptive rate limiting** — self-tuning based on load
- [ ] **Anomaly detection** — automatic failure pattern detection
- [ ] **Cost optimization** — minimize cloud costs automatically

#### Self-Healing
- [ ] **Automatic retry strategies** — learn from failure patterns
- [x] **Circuit breaker patterns** — prevent cascade failures
- [ ] **Automatic rollback** — revert on policy violations
- [ ] **Health check automation** — auto-disable unhealthy workers

#### Advanced Policies
- [ ] **ML-assisted policy authoring** — suggest policies from logs
- [ ] **Policy conflict detection** — find contradictory rules
- [ ] **Policy impact analysis** — predict effects before deploy
- [ ] **Compliance templates** — SOC2, HIPAA, PCI presets

---

## Q4 2026: Global Scale

### Goals
- 🎯 **v1.2.0 Release**
- 🎯 **Geo-Distributed Deployment**
- 🎯 **1M+ Jobs/Day Deployments**

### Features

#### Global Distribution
- [ ] **Multi-datacenter replication** — active-active clusters
- [ ] **Edge computing support** — run closer to data sources
- [ ] **Latency-based routing** — route to nearest region
- [ ] **Data residency controls** — GDPR/compliance requirements

#### Massive Scale
- [ ] **Sharded event streams** — handle millions of events/sec
- [ ] **Tiered storage** — archive old workflows cost-effectively
- [ ] **Query optimization** — fast search over billions of jobs
- [ ] **Capacity planning** — predict resource needs

#### Enterprise Governance
- [ ] **Multi-tenancy** — isolated environments per tenant
- [ ] **Chargeback/showback** — cost allocation reporting
- [ ] **Compliance dashboards** — real-time compliance status
- [ ] **Custom SLA enforcement** — automated SLA tracking

---

## Future (2027+)

### Research & Innovation

#### Experimental Features
- **Quantum-resistant crypto** — prepare for post-quantum world
- **Serverless workers** — FaaS integration for elastic scaling
- **Blockchain integration** — immutable audit trail options
- **AI policy authoring** — natural language to policy DSL

#### Platform Evolution
- **Plugin architecture** — custom components without forking
- **GraphQL subscriptions** — real-time data push
- **Mobile SDK** — iOS/Android workflow management
- **No-code workflow builder** — visual workflow designer

---

## Community Priorities

Vote on features at: https://github.com/cordum-io/cordum/discussions/categories/feature-requests

**Top Community Requests:**
1. ⭐ Policy hot-reload (done)
2. ⭐ VS Code extension (Q2 2026)
3. ⭐ Terraform provider (Q2 2026)
4. ⭐ Workflow templates (Q1 2026)
5. ⭐ Pack registry (Q2 2026)

---

## Deprecations & Breaking Changes

### v1.0.0 Breaking Changes
- ❌ **Old API endpoints** — /v0/* deprecated, use /v1/*
- ❌ **Legacy pack format** — migrate to new pack schema
- ❌ **Insecure defaults** — TLS required, auth enforced

### Migration Support
- 📖 **Migration guide** — step-by-step upgrade instructions
- 🛠️ **Migration tools** — automated conversion scripts
- 🆘 **Migration support** — dedicated Slack channel

---

## Release Schedule

### Versioning
- **Major (1.0.0):** Breaking changes, annually
- **Minor (1.1.0):** New features, quarterly
- **Patch (1.0.1):** Bug fixes, as needed

### Support Policy
- **Current version:** Full support
- **Previous minor:** Security fixes for 6 months
- **Older versions:** Community support only

---

## How to Influence the Roadmap

1. **Star features** you want in GitHub Discussions
2. **Submit RFCs** for major features
3. **Contribute code** for features you need
4. **Share use cases** that inform priorities
5. **Become a sponsor** for prioritized support

---

## Success Metrics

We track these metrics to measure progress:

| Metric | Current | Q2 2026 Goal | Q4 2026 Goal |
|--------|---------|--------------|--------------|
| Production Adopters | 0 (pre-v1.0) | 100+ | 500+ |
| Jobs Processed (Total) | N/A (pre-v1.0) | 10B+ | 100B+ |
| Throughput (ops/sec) | untested | 25k | 50k |
| Latency (p99) | untested | 3.0ms | 2.0ms |
| Uptime | N/A | 99.99% | 99.99% |
| GitHub Stars | TBD | 1000+ | 5000+ |
| Community Contributors | TBD | 50+ | 200+ |

---

## Questions?

- 💬 **GitHub Discussions:** https://github.com/cordum-io/cordum/discussions
- 📧 **Email:** roadmap@cordum.io
- 🐦 **Twitter:** @cordum_io

---

**Last updated:** June 24, 2026
**Next review:** July 2026
