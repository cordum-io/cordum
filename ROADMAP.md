# Cordum Roadmap

> **Last Updated:** January 27, 2026

This roadmap outlines our vision for Cordum's evolution. Priorities may shift based on community feedback and production learnings.

## Current Focus: v0.9.0 → v1.0.0 (Q1-Q2 2026)

The path to v1.0.0 focuses on **production hardening** and **API stability**.

### Stability & Reliability
- [x] Zero memory leaks over 72h continuous operation
- [x] 99.96%+ uptime in production deployments
- [x] Scheduler reconciler for timeouts/deadlines
- [x] Pending job replayer for stalled/missed dispatches
- [x] Dead-letter queue (DLQ) capture + retry/inspection endpoints
- [x] Saga-based compensation rollback for workflows
- [ ] Complete API documentation with OpenAPI spec
- [ ] Comprehensive error handling guide
- [ ] Disaster recovery playbook

### Performance
- [x] 15k ops/sec policy evaluation throughput
- [x] <5ms p99 end-to-end latency
- [x] gRPC API option (20% latency reduction target)
- [x] Policy caching layer (2x throughput target)
- [ ] ARM64 optimization (15% efficiency target)

### Enterprise Features
- [x] SSO/SAML integration
- [x] User/password authentication (separate from API keys)
- [x] Advanced RBAC
- [x] Audit export (JSON, CSV, SIEM)
- [ ] Air-gapped deployment guide
- [ ] FIPS 140-2 compliance mode

---

## Q1 2026: Production Readiness

### Goals
- ✅ **v1.0.0 Release Candidate**
- ✅ **External Security Audit**
- ✅ **100% API Coverage Tests**

### Features

#### Safety Kernel Enhancements
- [x] **Policy hot-reload** - Update policies without restart
- [x] **Policy simulation mode** - Test changes before apply
- [x] **Policy versioning** - Track and rollback policy changes
- [ ] **Constraint templates** - Reusable constraint patterns

#### Workflow Engine Improvements
- [x] **Parallel step execution** - Run independent steps concurrently (40% faster)
- [x] **Conditional branching** - If/else logic in workflows
- [x] **Loop constructs** - For-each over datasets
- [x] **Approval steps** - Human-in-the-loop workflow gating
- [x] **Delay/timer steps** - Scheduled waits and retries
- [x] **Notify steps** - Emit system alerts from workflows
- [ ] **Workflow templates** - Parameterized workflow definitions

#### Observability
- [ ] **Distributed tracing** - OpenTelemetry integration
- [ ] **Detailed metrics** - Extended Prometheus metrics
- [ ] **Log aggregation** - ELK/Loki integration guide
- [ ] **Performance profiling** - Built-in pprof endpoints

### Documentation
- [ ] Architecture deep-dive series
- [ ] Migration guide (from Temporal, Airflow)
- [ ] Best practices guide
- [ ] Troubleshooting cookbook

---

## Q2 2026: Scale & Ecosystem

### Goals
- 🎯 **v1.0.0 GA Release**
- 🎯 **100+ Production Adopters**
- 🎯 **Public Pack Registry**

### Features

#### Distributed Scheduler
- [ ] **Multi-region support** - Deploy across regions
- [ ] **Sharded job queue** - Horizontal scaling
- [ ] **Worker affinity** - Sticky routing for stateful jobs
- [ ] **Auto-scaling** - Dynamic worker pool management

#### Pack Ecosystem
- [ ] **Public pack registry** - Discover and share packs
- [x] **Pack marketplace** - Curated pack collection
- [x] **Pack templates** - Scaffolding tool for new packs
- [x] **Pack install/uninstall with overlays** - Config/policy/schema/workflow merges
- [ ] **Pack testing framework** - Automated pack validation

#### Developer Experience
- [ ] **VS Code extension** - Syntax highlighting, debugging
- [x] **Local dev mode** - Simplified single-node setup
- [ ] **Interactive CLI** - Better command-line UX
- [ ] **Workflow debugger** - Step-through execution

### Integrations
- [ ] **Terraform provider** - Infrastructure as code
- [ ] **Kubernetes operator** - Native K8s deployment
- [ ] **Cloud provider SDKs** - AWS, GCP, Azure helpers
- [ ] **Popular SaaS integrations** - Slack, PagerDuty, etc.

---

## Q3 2026: Intelligence & Automation

### Goals
- 🎯 **v1.1.0 Release**
- 🎯 **ML-Powered Features**
- 🎯 **Self-Healing Workflows**

### Features

#### Intelligent Scheduling
- [ ] **Predictive scheduling** - ML-based resource prediction
- [ ] **Adaptive rate limiting** - Self-tuning based on load
- [ ] **Anomaly detection** - Automatic failure pattern detection
- [ ] **Cost optimization** - Minimize cloud costs automatically

#### Self-Healing
- [ ] **Automatic retry strategies** - Learn from failure patterns
- [ ] **Circuit breaker patterns** - Prevent cascade failures
- [ ] **Automatic rollback** - Revert on policy violations
- [ ] **Health check automation** - Auto-disable unhealthy workers

#### Advanced Policies
- [ ] **ML-assisted policy authoring** - Suggest policies from logs
- [ ] **Policy conflict detection** - Find contradictory rules
- [ ] **Policy impact analysis** - Predict effects before deploy
- [ ] **Compliance templates** - SOC2, HIPAA, PCI presets

---

## Q4 2026: Global Scale

### Goals
- 🎯 **v1.2.0 Release**
- 🎯 **Geo-Distributed Deployment**
- 🎯 **1M+ Jobs/Day Deployments**

### Features

#### Global Distribution
- [ ] **Multi-datacenter replication** - Active-active clusters
- [ ] **Edge computing support** - Run closer to data sources
- [ ] **Latency-based routing** - Route to nearest region
- [ ] **Data residency controls** - GDPR/compliance requirements

#### Massive Scale
- [ ] **Sharded event streams** - Handle millions of events/sec
- [ ] **Tiered storage** - Archive old workflows cost-effectively
- [ ] **Query optimization** - Fast search over billions of jobs
- [ ] **Capacity planning** - Predict resource needs

#### Enterprise Governance
- [ ] **Multi-tenancy** - Isolated environments per tenant
- [ ] **Chargeback/showback** - Cost allocation reporting
- [ ] **Compliance dashboards** - Real-time compliance status
- [ ] **Custom SLA enforcement** - Automated SLA tracking

---

## Future (2027+)

### Research & Innovation

#### Experimental Features
- **Quantum-resistant crypto** - Prepare for post-quantum world
- **Serverless workers** - FaaS integration for elastic scaling
- **Blockchain integration** - Immutable audit trail options
- **AI policy authoring** - Natural language to policy DSL

#### Platform Evolution
- **Plugin architecture** - Custom components without forking
- **GraphQL subscriptions** - Real-time data push
- **Mobile SDK** - iOS/Android workflow management
- **No-code workflow builder** - Visual workflow designer

---

## Community Priorities

Vote on features at: https://github.com/cordum-io/cordum/discussions/categories/feature-requests

**Top Community Requests:**
1. ⭐ Policy hot-reload (Q1 2026)
2. ⭐ VS Code extension (Q2 2026)
3. ⭐ Terraform provider (Q2 2026)
4. ⭐ Workflow templates (Q1 2026)
5. ⭐ Pack registry (Q2 2026)

---

## Deprecations & Breaking Changes

### v1.0.0 Breaking Changes
- ❌ **Old API endpoints** - /v0/* deprecated, use /v1/*
- ❌ **Legacy pack format** - Migrate to new pack schema
- ❌ **Insecure defaults** - TLS required, auth enforced

### Migration Support
- 📖 **Migration guide** - Step-by-step upgrade instructions
- 🛠️ **Migration tools** - Automated conversion scripts
- 🆘 **Migration support** - Dedicated Slack channel

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
| Production Adopters | 15+ | 100+ | 500+ |
| Jobs Processed (Total) | 500M+ | 10B+ | 100B+ |
| Throughput (ops/sec) | 15k | 25k | 50k |
| Latency (p99) | 4.2ms | 3.0ms | 2.0ms |
| Uptime | 99.96% | 99.99% | 99.99% |
| GitHub Stars | TBD | 1000+ | 5000+ |
| Community Contributors | TBD | 50+ | 200+ |

---

## Questions?

- 💬 **GitHub Discussions:** https://github.com/cordum-io/cordum/discussions
- 📧 **Email:** roadmap@cordum.io
- 🐦 **Twitter:** @cordum_io

---

**Last updated:** January 2026
**Next review:** April 2026
