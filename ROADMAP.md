# Cordum Roadmap

> **Last Updated:** February 13, 2026

This roadmap outlines our vision for Cordum's evolution. Priorities may shift based on community feedback and production learnings.

## Current Focus: v0.9.0 → v1.0.0 (Q1-Q2 2026)

The path to v1.0.0 focuses on **production hardening** and **API stability**.

### Stability & Reliability
- [ ] Zero memory leaks over 72h continuous operation (no endurance test yet)
- [x] Scheduler reconciler for timeouts/deadlines
- [x] Pending job replayer for stalled/missed dispatches
- [x] Dead-letter queue (DLQ) capture + retry/inspection endpoints
- [x] Saga-based compensation rollback for workflows
- [x] Complete API documentation with OpenAPI spec
- [x] Comprehensive error handling guide
- [x] Disaster recovery playbook

### Performance
- [ ] 15k ops/sec policy evaluation throughput (target — no benchmark yet)
- [ ] <5ms p99 end-to-end latency (target — no benchmark yet)
- [x] gRPC API option
- [x] Policy caching layer
- [ ] ARM64 optimization (15% efficiency target)

### Enterprise Features
- [x] OIDC/SSO integration
- [ ] SAML support
- [x] User/password authentication (separate from API keys)
- [x] Basic role-based access (admin/user)
- [ ] Advanced RBAC (resource-level permissions, inheritance)
- [x] Audit event capture
- [ ] Audit export (JSON, CSV, SIEM)
- [ ] Air-gapped deployment guide
- [ ] FIPS 140-2 compliance mode

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

---

## In Progress — Q1 2026

### Output Policy + MCP Server (10 tasks)
- [x] Output policy gRPC contract (`output_policy.proto`)
- [x] Safety kernel output scanners (content patterns, detectors)
- [x] Scheduler output safety client integration
- [ ] MCP server stdio + HTTP/SSE modes
- [ ] MCP tools and resources catalog
- [ ] Dashboard output quarantine UX
- [ ] Dashboard remediation drawer

### Workflow Step Types (6 tasks)
- [x] **Switch** — multi-branch condition evaluation
- [x] **Transform** — inline expression evaluation with `${ }` syntax
- [ ] **Parallel** — concurrent branch execution
- [ ] **Loop** — iterative execution with break conditions
- [ ] **Storage** — read/write workflow context paths
- [ ] **Sub-workflow** — nested workflow invocation

### Dashboard Feature Gaps (11 tasks)
- [x] Workflow run deletion (single + bulk)
- [x] Policy snapshot capture with name/label
- [x] Policy explain UI
- [ ] Memory panel for job context
- [ ] Job submit drawer enhancements
- [ ] Workflow builder improvements
- [ ] Settings MCP configuration page

### Documentation Gaps (10 tasks)
- [x] Output policy operator guide (`docs/output-policy.md`)
- [x] Workflow step types reference (`docs/workflow-step-types.md`)
- [x] API reference (`docs/api-reference.md`)
- [x] Safety kernel deep reference (`docs/safety-kernel.md`)
- [x] MCP server guide (`docs/mcp-server.md`)
- [x] Scheduler internals (`docs/scheduler-internals.md`)
- [x] Dashboard guide (`docs/dashboard-guide.md`)
- [x] Configuration reference (`docs/configuration-reference.md`)
- [x] CLI reference (`docs/cli-reference.md`)
- [x] Architecture Decision Records (`docs/adr/`)

---

## Q1 2026: Remaining Production Readiness

### Safety Kernel Enhancements
- [x] **Policy hot-reload** — update policies without restart
- [x] **Policy simulation mode** — test changes before apply
- [x] **Policy versioning** — track and rollback policy changes
- [ ] **Constraint templates** — reusable constraint patterns

### Workflow Engine Improvements
- [x] **Fan-out step execution** — for_each over datasets with parallel dispatch
- [x] **Conditional branching** — if/else logic in workflows
- [x] **Approval steps** — human-in-the-loop workflow gating
- [x] **Delay/timer steps** — scheduled waits and retries
- [x] **Notify steps** — emit system alerts from workflows
- [x] **Switch steps** — multi-branch condition routing
- [x] **Transform steps** — inline expression evaluation
- [ ] **Loop constructs** — iterative loops within workflows
- [ ] **Workflow templates** — parameterized workflow definitions

### Observability
- [ ] **Distributed tracing** — OpenTelemetry integration
- [ ] **Detailed metrics** — extended Prometheus metrics
- [ ] **Log aggregation** — ELK/Loki integration guide
- [ ] **Performance profiling** — built-in pprof endpoints

### Documentation
- [x] Architecture deep-dive (ADRs)
- [ ] Migration guide (from Temporal, Airflow)
- [ ] Best practices guide
- [x] Troubleshooting cookbook

---

## Q2 2026: Scale & Ecosystem

### Goals
- 🎯 **v1.0.0 GA Release**
- 🎯 **100+ Production Adopters**
- 🎯 **Public Pack Registry**

### Features

#### Distributed Scheduler
- [ ] **Multi-region support** — deploy across regions
- [ ] **Sharded job queue** — horizontal scaling
- [ ] **Worker affinity** — sticky routing for stateful jobs
- [ ] **Auto-scaling** — dynamic worker pool management

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

**Last updated:** February 2026
**Next review:** April 2026
