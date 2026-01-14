# Cordum - Deterministic Control Plane for Autonomous Workflows

[![License: BUSL-1.1](https://img.shields.io/badge/license-BUSL--1.1-blue)](LICENSE)
[![Release](https://img.shields.io/github/v/release/cordum-io/cordum?sort=semver)](https://github.com/cordum-io/cordum/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/cordum-io/cordum)](go.mod)
[![Docker Compose](https://img.shields.io/badge/compose-ready-0f766e)](docker-compose.yml)
[![Docs](https://img.shields.io/badge/docs-cordum--docs-0ea5e9)](docs/README.md)
![CI](https://github.com/cordum-io/cordum/workflows/CI/badge.svg)
![CodeQL](https://github.com/cordum-io/cordum/workflows/CodeQL/badge.svg)
[![Website](https://img.shields.io/badge/website-cordum.io-blue)](https://cordum.io)
[![WebsiteDocs](https://img.shields.io/badge/docs-cordum.io%2Fdocs-0ea5e9)](https://cordum.io/docs)

Cordum (cordum.io) is a platform-only control plane for autonomous workflows and external workers.
It uses NATS for the bus, Redis for state and payload pointers, and CAP v2 wire contracts for jobs,
results, and heartbeats. Workers and product packs live outside this repo.

See the full product docs at [Cordum](https://cordum.io) (or the local `docs/README.md`).

## Getting started (1 minute)

![Getting started](docs/assets/getting-started.gif)

1. `./cmd/cordumctl/cordumctl up`
2. Open `http://localhost:8082` (dashboard).
3. Run `./tools/scripts/platform_smoke.sh`.

## Feature highlights

- Workflow engine with retries/backoff, approvals, timeouts, delays, and crash-safe state.
- Least-loaded scheduling with capability-aware pool routing.
- Policy-before-dispatch (ALLOW/DENY/REQUIRE_APPROVAL/CONSTRAINTS).
- Pack overlays for workflows, schemas, and policy/config fragments.
- Durable job bus on NATS JetStream with Redis-backed pointers and auditability.
- API + CLI for workflows, runs, policy bundles, schemas, packs, locks, and artifacts.

## Architecture (current code)

Core services:
- API gateway: HTTP/WS + gRPC for jobs, workflows/runs, approvals, config, policy, DLQ, schemas, locks, artifacts, traces, packs.
- Scheduler: safety gate, routing, job state, reconciler timeouts.
- Safety kernel: policy check/evaluate/explain/simulate; file policy + config-service fragments.
- Workflow engine: Redis-backed workflows/runs with fan-out, approvals, retries/backoff, delay/notify/condition steps, reruns, timeline.
- Context engine (optional): gRPC helper for context windows and memory in Redis.
- Dashboard (optional): React UI served via Nginx; connects to `/api/v1` and `/api/v1/stream`.

Control plane flow (simplified):

```
Clients/UI
   |
   v
API Gateway  --->  Redis (runs, jobs, pointers, config, policy, DLQ)
   |
   v
Scheduler  --->  Safety Kernel (policy decision)
   |
   v
NATS (JetStream bus)  --->  External workers (your code)
```

Protocol:
- Bus and safety types are CAP v2 (`github.com/cordum-io/cap/v2`) via aliases in `core/protocol/pb/v1`.
- API/Context protos live in `core/protocol/proto/v1`; generated Go types live in `core/protocol/pb/v1` and `sdk/gen/go/cordum/v1`.

SDK:
- Public Go SDK lives under `sdk/` (module `github.com/cordum/cordum/sdk`), including generated protos,
  a minimal gateway client, and a CAP worker runtime (`sdk/runtime`).

## Why Cordum?

Cordum is built for teams that need deterministic automation and policy control.

| Capability | Cordum | Typical workflow engines |
| --- | --- | --- |
| Policy-before-dispatch | Built-in | External/custom |
| Approval gates | Built-in | Manual |
| Scheduling | Least-loaded + pool routing | Queue-based |
| Pack overlays | Built-in | Plugins/scripts |

## Quickstart (Docker)

Requirements: Docker/Compose, curl, jq.

```bash
./cmd/cordumctl/cordumctl up
```

Or manually:

```bash
docker compose build
docker compose up -d
```

Dashboard (optional): `http://localhost:8082` (uses `CORDUM_API_KEY`).

Platform smoke (create workflow + run + approve + delete):
```bash
./tools/scripts/platform_smoke.sh
```

CLI smoke (cordumctl):
```bash
./tools/scripts/cordumctl_smoke.sh
```

## Examples

- `examples/hello-pack` - minimal pack (workflow + schema + policy/config overlays)
- `examples/hello-worker-go` - Go worker that consumes `job.hello-pack.echo`
- `cordum-packs/packs/mcp-bridge` - MCP stdio bridge + pack (packs monorepo)

## Docs

Start here:
- `docs/README.md` (docs index)
- `docs/system_overview.md` (architecture + data flow)
- `docs/CORE.MD` (deep technical reference)

Key guides:
- `docs/DOCKER.md` (compose + environment)
- `docs/AGENT_PROTOCOL.md` (bus + pointer semantics)
- `docs/pack.md` (pack format + install flow)
- `docs/LOCAL_E2E.md` (local e2e walkthrough)

Resources:
- `CONTRIBUTING.md`
- `SECURITY.md`
- `SUPPORT.md`
- `LICENSE`

## Repositories

- `cordum`: core control plane (this repo)
- `cordum-enterprise`: enterprise binaries (license check + enterprise auth provider)
- `cordum-packs`: official pack bundles + worker projects
- `cap`: protocol contracts and SDKs (`github.com/cordum-io/cap/v2`)

## Enterprise

Enterprise features are delivered by the `cordum-enterprise` repo and require a signed license.
Enterprise-only capabilities (available in enterprise binaries):
- Enterprise auth provider (multi-tenant API keys + RBAC)
- License enforcement on enterprise binaries

Enterprise offering includes (roadmap and commercial program):
- SSO/SAML integration
- SIEM and audit log export
- Dedicated support + SLA
- Custom pack development
- Managed or on-prem deployment assistance

## Development and tests

- Go toolchain: `go 1.24` (module-specified). Use local cache to avoid permission issues:
  `GOCACHE=$(pwd)/.cache/go-build go test ./...`
- Proto changes: edit `core/protocol/proto/v1`, then run `make proto`.
- Docker images: `docker compose build` rebuilds all services.
- Binaries: `make build` (all), or `make build SERVICE=cordum-scheduler` (one).
- Container image: `make docker SERVICE=cordum-scheduler` (uses the root Dockerfile).
- Smoke: `make smoke` (runs `tools/scripts/platform_smoke.sh`).
- Integration tests: `make test-integration` (opt-in, tagged).

## Observability

- Scheduler: `:9090/metrics`
- API gateway: `:9092/metrics`
- Workflow engine health: `:9093/health`

## Reset state (local)

- Wipe Redis (jobs + ctx/res + memory + workflows + config + DLQ):
  `docker compose exec redis redis-cli FLUSHALL`
- Wipe JetStream state too: `docker compose down -v` (removes `nats_data`) and then `docker compose up -d`

## License

Licensed under the Business Source License 1.1 (BUSL-1.1). Free for
self-hosted and internal use, but not for competing hosted/managed offerings.
See `LICENSE` for details and the Change Date.
