# Cordum Docs

This folder contains the public documentation for Cordum core.

## Getting started

- `docs/getting_started.md` - quickstart walkthrough
- `docs/DOCKER.md` - docker compose + env setup
- `docs/LOCAL_E2E.md` - local end-to-end walkthrough
- `tools/scripts/platform_smoke.sh` - smoke test (create/run/approve/delete workflow)

## Architecture

- `docs/system_overview.md` - architecture and data flow
- `docs/CORE.MD` - deep technical reference
- `docs/AGENT_PROTOCOL.md` - bus + pointer semantics

## API and configuration

- `docs/api.md` - REST/gRPC overview
- `docs/configuration.md` - config files + env vars

## Packs

- `docs/pack.md` - pack format + install/uninstall rules
- `cmd/cordumctl` - CLI with `cordumctl pack` subcommands

## Operations

- `docs/SCHEDULER_POOL_SPEC.md` - pool routing config
- `docs/backend_capabilities.md` - feature coverage
- `docs/backend_feature_matrix.md` - feature/test matrix

## Dashboards

- `docs/dashboard/README.md` - dashboard runbook

## Enterprise

- `docs/enterprise.md` - enterprise overview and repo links

## Repo layout

- `docs/repo_split.md` - repo boundaries (core vs enterprise vs tools)
- `docs/faq.md` - common questions

## Engineering notes

Internal engineering notes and planning docs live in `cordum-tools/docs/internal` to keep
the core repo public-facing.
