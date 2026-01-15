# Cordum Docs

This folder contains the public documentation for Cordum core.

## Getting started

- `docs/getting_started.md` - quickstart walkthrough
- `docs/DOCKER.md` - docker compose + env setup
- `docs/LOCAL_E2E.md` - local end-to-end walkthrough
- `tools/scripts/platform_smoke.sh` - smoke test (create/run/approve/delete workflow)
- `cmd/cordumctl/cordumctl up` - one-command local stack launcher

## Architecture

- `docs/system_overview.md` - architecture and data flow
- `docs/CORE.MD` - deep technical reference
- `docs/AGENT_PROTOCOL.md` - bus + pointer semantics

## API and configuration

- `docs/api.md` - REST/gRPC overview
- `make openapi` - generate OpenAPI specs from protobufs in `docs/api/openapi`
- `docs/configuration.md` - config files + env vars

## Packs

- `docs/pack.md` - pack format + install/uninstall rules
- `cmd/cordumctl` - CLI with `cordumctl pack` subcommands
- `cordum-packs` - official pack bundles + catalog published to `https://packs.cordum.io`

## Examples

- `examples/hello-pack` - minimal pack bundle
- `examples/hello-worker-go` - Go worker consuming `job.hello-pack.echo`
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
