# Cordum Docs

This folder contains the public documentation for Cordum core.
All API calls require `X-API-Key` and `X-Tenant-ID` headers (set `CORDUM_API_KEY`
and `CORDUM_TENANT_ID` in your shell before running the scripts).

## Start here (newcomers)

Fastest path to a working local stack:

```bash
# from the repo root
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
./tools/scripts/quickstart.sh
```

Want the guardrails demo?

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
./tools/scripts/demo_guardrails_run.sh
```

Want the mock bank demo?

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
./tools/scripts/demo_mock_bank.sh
```

## Getting started

- `docs/install.md` - install options and prerequisites
- `docs/getting_started.md` - quickstart walkthrough
- `docs/quickstart.md` - hello world tutorial
- `docs/DOCKER.md` - docker compose + env setup
- `docs/helm.md` - Helm install guide
- `docs/LOCAL_E2E.md` - local end-to-end walkthrough
- `docs/cordumctl.md` - CLI reference
- `docs/demo-guardrails.md` - guardrails demo walkthrough + GIF recording
- `docs/demo-mock-bank.md` - mock bank governance demo walkthrough
- `tools/scripts/quickstart.sh` - one-command local stack + smoke test
- `tools/scripts/demo_guardrails_run.sh` - guardrails demo runner
- `tools/scripts/demo_mock_bank.sh` - mock bank demo runner
- `tools/scripts/platform_smoke.sh` - smoke test (create/run/approve/delete workflow)
- `cordumctl up` - one-command local stack launcher (`go run ./cmd/cordumctl up` or `./bin/cordumctl up` after `make build SERVICE=cordumctl`)
- `tools/scripts/install.sh` - installer script for local or hosted one-liner

## Architecture

- `docs/system_overview.md` - architecture and data flow
- `docs/CORE.MD` - deep technical reference
- `docs/AGENT_PROTOCOL.md` - bus + pointer semantics

## API and configuration

- `docs/api.md` - REST/gRPC overview
- `make openapi` - generate OpenAPI specs from protobufs in `docs/api/openapi` (UI at `docs/api/openapi/`)
- `docs/configuration.md` - config files + env vars

## Packs

- `docs/pack.md` - pack format + install/uninstall rules
- `cmd/cordumctl` - CLI with `cordumctl pack` subcommands
- `cordum-packs` - official pack bundles + catalog published to `https://packs.cordum.io`

## Examples

- `examples/hello-pack` - minimal pack bundle
- `examples/hello-worker-go` - Go worker consuming `job.hello-pack.echo`
- `examples/python-worker` - Python worker example for `job.hello-pack.echo`
- `examples/node-worker` - Node worker example for `job.hello-pack.echo`
- `examples/demo-guardrails` - approval + remediation demo pack
- `cordum-packs/packs/mcp-bridge` - MCP stdio bridge + pack (packs monorepo)

## Operations

- `docs/SCHEDULER_POOL_SPEC.md` - pool routing config
- `docs/backend_capabilities.md` - feature coverage
- `docs/backend_feature_matrix.md` - feature/test matrix
- `docs/production.md` - production readiness checklist
- `make coverage` / `make coverage-core` - coverage reports (core target >= 80%)

## Dashboards

- `docs/dashboard/README.md` - dashboard runbook

## Enterprise

- `docs/enterprise.md` - enterprise overview and repo links

## Repo layout

- `docs/repo_split.md` - repo boundaries (core vs enterprise vs tools)
- `docs/faq.md` - common questions

## Engineering notes

Internal engineering notes and planning docs live in a private tooling repo to keep
the core repo public-facing.
