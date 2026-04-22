# CORDUM TECHNICAL ROADMAP

## Current State Assessment

### ✅ What's Done (Solid Foundation)
- Core control plane (scheduler, safety kernel, workflow engine, gateway)
- CAP protocol v2.0.11 with 9 releases
- SDKs: Go, Python, Node, C++
- 16 integration packs
- React dashboard
- Professional website
- BUSL-1.1 licensing
- Enterprise repo structure

### ❌ What's Missing (Blocking Adoption)
- No tagged release on `cordum` repo
- No Helm chart
- No "Hello World" tutorial
- packs.cordum.io not deployed
- No CI badges on README
- No Docker Hub images
- Limited example code

---

## PHASE 1: RELEASE READINESS (Days 1-7)
### Theme: "Make it installable"

### Priority 1: Tag v0.1.0 Release ⚡ [Day 1]
```bash
# cordum repo
cd cordum
git tag -a v0.1.0 -m "Initial public release - Safety Kernel, Scheduler, Workflow Engine"
git push origin v0.1.0

# Create GitHub Release with notes:
# - Highlight key features
# - Link to docs
# - Known limitations

# cordum-packs repo
cd cordum-packs
git tag -a v0.1.0 -m "Initial packs release - 16 integration packs"
git push origin v0.1.0
```

### Priority 2: Docker Hub Images [Day 1-2]
```yaml
# Current: Users must build from source
# Target: docker pull cordum/cordum:0.1.0

Action items:
□ Create Docker Hub organization: cordum
□ Set up GitHub Actions for automated builds
□ Push images:
  - cordum/control-plane:0.1.0
  - cordum/dashboard:0.1.0
  - cordum/mcp-bridge:0.1.0
□ Update docker-compose.yml to use Hub images
```

**GitHub Action (.github/workflows/docker.yml):**
```yaml
name: Build and Push Docker Images

on:
  push:
    tags:
      - 'v*'

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Login to Docker Hub
        uses: docker/login-action@v3
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}
      
      - name: Build and push
        uses: docker/build-push-action@v5
        with:
          push: true
          tags: cordum/control-plane:${{ github.ref_name }}
```

### Priority 3: README Improvements [Day 2]
```markdown
Add to cordum README.md:

## Badges (top of file)
![Release](https://img.shields.io/github/v/release/cordum-io/cordum)
![License](https://img.shields.io/badge/license-BUSL--1.1-blue)
![Go Version](https://img.shields.io/github/go-mod/go-version/cordum-io/cordum)
![Docker Pulls](https://img.shields.io/docker/pulls/cordum/control-plane)

## Quick Start (30 seconds)
```bash
curl -fsSL https://get.cordum.io | sh
# or
docker compose up -d
```

## One-liner install
Create install script at get.cordum.io
```

### Priority 4: Deploy packs.cordum.io [Day 2-3]
```bash
# In cordum-packs repo
# Enable GitHub Pages workflow

# Verify publish.yml workflow exists and runs
# Point packs.cordum.io DNS to GitHub Pages

# Test:
curl https://packs.cordum.io/catalog.json
```

### Priority 5: Quick Start Tutorial [Day 3-5]
```markdown
Create: docs/quickstart.md

# Cordum Quick Start (5 minutes)

## Prerequisites
- Docker & Docker Compose
- curl

## Step 1: Start Cordum
```bash
git clone https://github.com/cordum-io/cordum
cd cordum
docker compose up -d
```

## Step 2: Open Dashboard
http://localhost:8082

## Step 3: Create Your First Policy
```yaml
# policy.yaml
rules:
  - id: hello-world
    match:
      topics: ["job.hello.*"]
    decision: allow
```

## Step 4: Submit a Job
```bash
curl -X POST http://localhost:8081/api/v1/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "job.hello.world",
    "context": {"message": "Hello, Cordum!"}
  }'
```

## Step 5: Check the Result
Dashboard → Jobs → See your job succeeded!

## Next Steps
- [Install a Pack](./packs.md)
- [Write a Worker](./workers.md)
- [Configure Policies](./policies.md)
```

---

## PHASE 2: KUBERNETES READY (Days 8-21)
### Theme: "Enterprise deployable"

### Priority 6: Helm Chart [Day 8-14]
```
cordum-helm/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── deployment-control-plane.yaml
│   ├── deployment-dashboard.yaml
│   ├── service.yaml
│   ├── configmap.yaml
│   ├── secret.yaml
│   ├── ingress.yaml
│   ├── serviceaccount.yaml
│   └── _helpers.tpl
└── README.md
```

**Chart.yaml:**
```yaml
apiVersion: v2
name: cordum
description: AI agent orchestration with built-in governance
type: application
version: 0.1.0
appVersion: "0.1.0"
keywords:
  - ai
  - orchestration
  - workflow
  - governance
home: https://cordum.io
sources:
  - https://github.com/cordum-io/cordum
maintainers:
  - name: Yaron
    email: yaron@cordum.io
```

**values.yaml:**
```yaml
replicaCount: 1

image:
  repository: cordum/control-plane
  tag: "0.1.0"
  pullPolicy: IfNotPresent

service:
  type: ClusterIP
  port: 8081

ingress:
  enabled: false
  className: ""
  hosts:
    - host: cordum.local
      paths:
        - path: /
          pathType: Prefix

nats:
  enabled: true  # Deploy NATS with chart
  # Or use external:
  # external:
  #   url: nats://nats.example.com:4222

redis:
  enabled: true  # Deploy Redis with chart
  # Or use external:
  # external:
  #   url: redis://redis.example.com:6379

resources:
  limits:
    cpu: 1000m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi

autoscaling:
  enabled: false
  minReplicas: 1
  maxReplicas: 10
```

**Installation:**
```bash
# Add repo
helm repo add cordum https://charts.cordum.io
helm repo update

# Install
helm install cordum cordum/cordum -n cordum --create-namespace

# With custom values
helm install cordum cordum/cordum -f my-values.yaml
```

### Priority 7: Kubernetes Operator (Optional) [Day 15-21]
```
For managing CRDs like:
- CordumWorkflow
- CordumPack
- CordumPolicy

This is nice-to-have, not critical for launch.
```

---

## PHASE 3: DEVELOPER EXPERIENCE (Days 22-45)
### Theme: "Easy to build on"

### Priority 8: Python Worker Example [Day 22-25]
```python
# examples/python-worker/worker.py
from cordum import Worker, JobContext

worker = Worker(
    pool="my-workers",
    subjects=["job.hello.*"],
    capabilities=["python", "http"]
)

@worker.handler("job.hello.greet")
async def handle_greet(ctx: JobContext):
    name = ctx.input.get("name", "World")
    return {"message": f"Hello, {name}!"}

if __name__ == "__main__":
    worker.run()
```

```
# examples/python-worker/
├── worker.py
├── requirements.txt
├── Dockerfile
├── docker-compose.yml
└── README.md
```

### Priority 9: JavaScript/TypeScript Worker Example [Day 26-28]
```typescript
// examples/node-worker/worker.ts
import { Worker, JobContext } from '@cordum/sdk';

const worker = new Worker({
  pool: 'my-workers',
  subjects: ['job.hello.*'],
  capabilities: ['node', 'http']
});

worker.handle('job.hello.greet', async (ctx: JobContext) => {
  const name = ctx.input.name || 'World';
  return { message: `Hello, ${name}!` };
});

worker.start();
```

### Priority 10: CLI Improvements [Day 29-35]
```bash
# Current CLI gaps to fill:

# Easy init
cordumctl init my-project
# Creates project structure with docker-compose, sample policy, sample workflow

# Pack scaffolding  
cordumctl pack create my-pack
# Creates pack structure with pack.yaml, sample worker

# Local dev mode
cordumctl dev
# Starts local environment with hot reload

# Status dashboard
cordumctl status
# Shows running workers, pending jobs, policy status

# Job submission
cordumctl job submit --topic job.hello.world --input '{"name":"Yaron"}'
cordumctl job status <job-id>
cordumctl job logs <job-id>
```

### Priority 11: VS Code Extension (Nice to Have) [Day 36-45]
```
Features:
- Syntax highlighting for policy YAML
- Workflow visualization
- Job status in sidebar
- Quick commands (submit job, view logs)
```

---

## PHASE 4: PRODUCTION HARDENING (Days 46-60)
### Theme: "Enterprise trustworthy"

### Priority 12: Comprehensive Testing [Day 46-50]
```
Current: Tests exist but coverage unknown

Action:
□ Add coverage badges
□ Target 80%+ coverage on core packages
□ Add integration test suite
□ Add load/stress tests
□ Document test strategy
```

```bash
# Add to CI
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

### Priority 13: Observability Stack [Day 51-55]
```yaml
# Grafana dashboards for:
- Job throughput
- Policy evaluation latency
- Worker health
- Queue depth
- Error rates

# Prometheus alerts for:
- High error rate
- Worker disconnection
- Queue backlog
- Policy evaluation failures
```

Create: `deploy/monitoring/`
```
deploy/monitoring/
├── prometheus/
│   ├── prometheus.yml
│   └── alerts.yml
├── grafana/
│   ├── dashboards/
│   │   └── cordum-overview.json
│   └── provisioning/
└── docker-compose.monitoring.yml
```

### Priority 14: Security Audit [Day 56-60]
```
□ Run gosec on codebase
□ Run trivy on Docker images
□ Document security model
□ Add SECURITY.md with disclosure policy
□ Consider: security audit from third party (paid)
```

---

## PHASE 5: CORDUM CLOUD (Days 61-90)
### Theme: "Revenue ready"

### Priority 15: Multi-Tenant Architecture [Day 61-70]
```
Current: Single tenant
Target: Multi-tenant SaaS

Components:
□ Tenant isolation (namespace per tenant)
□ API key management per tenant
□ Usage metering
□ Billing integration (Stripe)
□ Tenant provisioning automation
```

### Priority 16: Cloud Control Plane [Day 71-80]
```
cloud.cordum.io architecture:

┌─────────────────────────────────────────────┐
│             Cloud Control Plane              │
├─────────────────────────────────────────────┤
│  ┌─────────┐  ┌─────────┐  ┌─────────────┐ │
│  │ Tenant  │  │ Billing │  │ Provisioner │ │
│  │ Manager │  │ Service │  │   Service   │ │
│  └─────────┘  └─────────┘  └─────────────┘ │
├─────────────────────────────────────────────┤
│           Kubernetes Cluster                 │
│  ┌─────────────┐  ┌─────────────┐          │
│  │ Tenant-A NS │  │ Tenant-B NS │  ...     │
│  │  (Cordum)   │  │  (Cordum)   │          │
│  └─────────────┘  └─────────────┘          │
└─────────────────────────────────────────────┘
```

### Priority 17: Onboarding Flow [Day 81-85]
```
User signup flow:
1. Sign up with email/GitHub
2. Create organization
3. Provision dedicated Cordum instance (30 seconds)
4. Quick start wizard in dashboard
5. First job in under 5 minutes
```

### Priority 18: Usage-Based Billing [Day 86-90]
```
Metering:
- Jobs executed
- Policy evaluations  
- Storage used
- API calls

Stripe integration:
- Free tier: 1,000 jobs/month
- Pro: $0.001/job after free tier
- Enterprise: Custom pricing
```

---

## TECHNICAL DEBT TRACKER

| Item | Priority | Effort | Impact |
|------|----------|--------|--------|
| Consistent error wrapping | Low | Low | Medium |
| Logging level audit | Low | Low | Low |
| Config validation (JSON Schema) | Medium | Medium | Medium |
| API docs from protobuf | Medium | Medium | High |
| Database migrations tooling | Medium | Medium | Medium |
| Graceful degradation tests | Low | High | Medium |

---

## DEPENDENCY UPDATES

```bash
# Check for updates regularly
go list -u -m all

# Key dependencies to monitor:
- nats-io/nats.go
- redis/go-redis
- grpc/grpc-go
- protobuf
```

---

## ARCHITECTURE DECISIONS TO DOCUMENT

| Decision | Status | Document |
|----------|--------|----------|
| Why NATS over Kafka | Made | Need ADR |
| Why BUSL-1.1 | Made | Need ADR |
| Why Go over Rust | Made | Need ADR |
| Multi-tenant approach | Pending | Need design doc |
| Secrets management | Pending | Need design doc |

Create: `docs/adr/` (Architecture Decision Records)

---

## QUICK REFERENCE: What to Build When

### Before Launch (Days 1-7)
```
□ Tag v0.1.0
□ Docker Hub images
□ README badges
□ packs.cordum.io live
□ Quick start tutorial
```

### Before ProductHunt (Days 8-30)
```
□ Helm chart
□ Python worker example
□ Node worker example
□ Improved CLI
□ Video demo
```

### Before First Enterprise Customer (Days 31-60)
```
□ 80%+ test coverage
□ Grafana dashboards
□ Security documentation
□ SLA documentation
```

### Before Cordum Cloud Launch (Days 61-90)
```
□ Multi-tenant architecture
□ Billing integration
□ Onboarding flow
□ Usage metering
```

---

## IMMEDIATE NEXT STEPS (Do This Week)

### Monday
```bash
□ Tag v0.1.0 on cordum repo
□ Tag v0.1.0 on cordum-packs repo
□ Create GitHub Releases with notes
```

### Tuesday
```bash
□ Set up Docker Hub org
□ Create GitHub Action for Docker builds
□ Push first images
```

### Wednesday
```bash
□ Add badges to README
□ Deploy packs.cordum.io
□ Verify catalog.json accessible
```

### Thursday
```bash
□ Write quick start tutorial
□ Test quick start on fresh machine
□ Update docs site
```

### Friday
```bash
□ Review & merge all PRs
□ Announce release
□ Start Helm chart work
```
no i mea