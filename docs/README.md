# Cordum Docs

Documentation for Cordum — the safety-first agent orchestration platform.

All API calls require `X-API-Key` and `X-Tenant-ID` headers (set `CORDUM_API_KEY`
and `CORDUM_TENANT_ID` in your shell before running the scripts).

---

## Quick Start

Fastest path to a working local stack:

```bash
# from the repo root
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
./tools/scripts/quickstart.sh
```

Docker Compose loads `.env` automatically; the helper scripts read environment
variables from your shell.

---

## Getting Started

| Doc | Description |
|-----|-------------|
| [install.md](install.md) | Install options and prerequisites |
| [getting_started.md](getting_started.md) | Quickstart walkthrough |
| [quickstart.md](quickstart.md) | Hello-world tutorial |
| [DOCKER.md](DOCKER.md) | Docker Compose setup — service inventory, networking, health checks, env vars, volumes, troubleshooting |
| [helm.md](helm.md) | Kubernetes Helm install guide |
| [LOCAL_E2E.md](LOCAL_E2E.md) | Local end-to-end test walkthrough |
| [cordumctl.md](cordumctl.md) | CLI reference (`cordumctl up`, `cordumctl pack`, etc.) |
| [faq.md](faq.md) | Common questions and answers |

## User Guides

| Doc | Description |
|-----|-------------|
| [dashboard-guide.md](dashboard-guide.md) | Dashboard feature guide — all pages, workflows, keyboard shortcuts |
| [cordumctl.md](cordumctl.md) | CLI reference and command catalog |
| [cli-reference.md](cli-reference.md) | Full cordumctl command reference — flags, examples, env vars |
| [demo-guardrails.md](demo-guardrails.md) | Guardrails demo walkthrough with GIF recording |
| [demo-mock-bank.md](demo-mock-bank.md) | Mock bank governance demo walkthrough |

## Tutorials

| Doc | Description |
|-----|-------------|
| [tutorials/langchain-guard.md](tutorials/langchain-guard.md) | LangChain + Cordum safety guard tutorial |
| [demo-guardrails.md](demo-guardrails.md) | Approval + remediation demo |
| [demo-mock-bank.md](demo-mock-bank.md) | Mock bank governance demo |

## Operator Guides

| Doc | Description |
|-----|-------------|
| [configuration.md](configuration.md) | Config files and environment variables overview |
| [configuration-reference.md](configuration-reference.md) | Complete config schema reference — system.yaml fields, overlay system, env vars master table |
| [output-policy.md](output-policy.md) | Output safety scanning operator guide — rules, scanners, quarantine runbook |
| [production.md](production.md) | Production readiness guide — checklist, DR procedures, incident runbooks, scaling guide, monitoring alerts, security hardening |
| [guides/production-deployment.md](guides/production-deployment.md) | Production deployment guide — Docker Compose, K8s, Helm with TLS |
| [guides/tls-setup.md](guides/tls-setup.md) | TLS setup guide — architecture, cert generation, env vars, troubleshooting |
| [production-gate.md](production-gate.md) | Production gate script and verification |
| [DOCKER.md](DOCKER.md) | Docker Compose deployment — volumes, networking, health checks, env vars, multi-platform, troubleshooting |
| [helm.md](helm.md) | Kubernetes Helm deployment |
| [k8s-deployment.md](k8s-deployment.md) | Detailed Kubernetes deployment guide — base manifests, production overlay, TLS, clustering, monitoring, scaling, backups |
| [troubleshooting.md](troubleshooting.md) | Troubleshooting guide — common issues, error diagnosis, debug commands |
| [SCHEDULER_POOL_SPEC.md](SCHEDULER_POOL_SPEC.md) | Pool routing specification |

## Architecture

| Doc | Description |
|-----|-------------|
| [system_overview.md](system_overview.md) | Architecture, data flow, and service topology |
| [safety-kernel.md](safety-kernel.md) | Safety kernel deep reference — input policy, MCP filters, overlays, cache, signatures, remediations, gRPC/TLS |
| [scheduler-internals.md](scheduler-internals.md) | Scheduler engine internals — state machine, output policy, reconciler, saga, routing, circuit breaker |
| [workflow-step-types.md](workflow-step-types.md) | Workflow step type reference — job, fan-out, condition, delay, approval, switch, parallel, loop, transform, storage, sub-workflow |
| [output-policy.md](output-policy.md) | Output policy architecture — two-phase scanning, quarantine flow |
| [AGENT_PROTOCOL.md](AGENT_PROTOCOL.md) | CAP bus protocol and pointer semantics |
| [CORE.md](CORE.md) | Core libraries technical reference |
| [adr/](adr/) | Architecture Decision Records (ADRs) |
| [adr/001-safety-before-dispatch.md](adr/001-safety-before-dispatch.md) | ADR: Policy-before-dispatch guarantee, <5ms p99 |
| [adr/002-context-pointers.md](adr/002-context-pointers.md) | ADR: Context pointers vs inline payloads on bus |
| [adr/003-redis-nats-split.md](adr/003-redis-nats-split.md) | ADR: Redis as state store + NATS as message bus |
| [adr/004-inline-vs-dispatch-steps.md](adr/004-inline-vs-dispatch-steps.md) | ADR: Workflow inline vs dispatch step types |
| [adr/005-output-policy-architecture.md](adr/005-output-policy-architecture.md) | ADR: Two-phase output policy architecture |
| [adr/006-circuit-breaker-safety.md](adr/006-circuit-breaker-safety.md) | ADR: Circuit breaker on Safety Kernel client |
| [adr/007-dashboard-state-management.md](adr/007-dashboard-state-management.md) | ADR: Zustand + React Query state management |

## API Reference

| Doc | Description |
|-----|-------------|
| [api-reference.md](api-reference.md) | Comprehensive REST endpoint reference — gateway routes, schemas, auth, errors, examples |
| [api.md](api.md) | REST/gRPC overview |
| [grpc-services.md](grpc-services.md) | gRPC service reference — CordumApi, ContextEngine, OutputPolicyService, SafetyKernel, health |
| [mcp-server.md](mcp-server.md) | MCP server modes (stdio + HTTP/SSE) and Claude integration setup |
| [mcp-tools-reference.md](mcp-tools-reference.md) | MCP tool catalog — schemas, error codes, JSON-RPC examples |
| [mcp-resources-reference.md](mcp-resources-reference.md) | MCP resource catalog — URI templates, pagination, response examples |
| [websocket-streaming.md](websocket-streaming.md) | WebSocket streaming protocol — global/per-job streams, auth, events, reconnection, client examples |
| `make openapi` | Generate OpenAPI specs from protobufs in `docs/api/openapi/` |

## Packs

| Doc | Description |
|-----|-------------|
| [pack.md](pack.md) | Pack format, development workflow, testing, marketplace publishing, worker registration, policy fragments |
| `cmd/cordumctl` | CLI with `cordumctl pack` subcommands |
| `cordum-packs` | Official pack bundles + catalog at `https://packs.cordum.io` |

## Examples

| Example | Description |
|---------|-------------|
| `examples/hello-pack` | Minimal pack bundle |
| `examples/hello-worker-go` | Go worker consuming `job.hello-pack.echo` |
| `examples/python-worker` | Python worker example |
| `examples/node-worker` | Node.js worker example |
| `examples/demo-guardrails` | Approval + remediation demo pack |

## Development

| Doc | Description |
|-----|-------------|
| [sdk-reference.md](sdk-reference.md) | SDK reference — gateway client, worker runtime, heartbeats, blob store, testing patterns |
| [CORE.md](CORE.md) | Core libraries reference (safety, workflow, scheduler, bus, store) |
| [backend_capabilities.md](backend_capabilities.md) | Feature coverage matrix |
| [backend_feature_matrix.md](backend_feature_matrix.md) | Feature/test matrix |
| [dashboard/README.md](dashboard/README.md) | Dashboard developer runbook |
| [repo_split.md](repo_split.md) | Repo boundaries (core vs enterprise vs tools) |
| [enterprise.md](enterprise.md) | Enterprise overview and repo links |
| `make coverage` | Coverage reports (core target >= 80%) |

## Scripts

| Script | Description |
|--------|-------------|
| `tools/scripts/quickstart.sh` | One-command local stack + smoke test |
| `tools/scripts/e2e_install_workflow.sh` | Install + approval workflow E2E test |
| `tools/scripts/demo_guardrails_run.sh` | Guardrails demo runner |
| `tools/scripts/demo_mock_bank.sh` | Mock bank demo runner |
| `tools/scripts/platform_smoke.sh` | Platform smoke test |
| `tools/scripts/install.sh` | Installer script for local or hosted one-liner |
| `cordumctl up` | One-command local stack launcher |

---

## Roadmap

See [../ROADMAP.md](../ROADMAP.md) for the full feature roadmap — completed milestones, active epics, and planned work.

## Changelog

See [../CHANGELOG.md](../CHANGELOG.md) for a detailed log of all changes by version — follows [Keep a Changelog](https://keepachangelog.com/) format.

---

Internal engineering notes and planning docs live in a private tooling repo to keep
the core repo public-facing.
