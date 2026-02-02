# Cordum Wiki

Welcome to the Cordum documentation hub. This wiki is the fastest path from
"zero" to a secure, running control plane.

## Start here

- [Installation](Installation) - choose quickstart, Docker Compose, or Helm.
- [Quickstart](Quickstart) - 5-minute local workflow demo.
- [Security](Security) - production hardening essentials.
- [Configuration](Configuration) - env vars, config files, and overrides.

## What is Cordum?

Cordum is a governance-first control plane for autonomous workflows. It
intercepts agent intent, evaluates against a Safety Kernel, and only dispatches
approved jobs to workers. The result is deterministic, auditable execution even
when the agent is non-deterministic.

All API requests require `X-API-Key` and `X-Tenant-ID` headers.

## Key concepts

- **Workflows** define the steps your agents can execute.
- **Jobs** are concrete executions of workflow steps.
- **Safety Kernel** enforces policy decisions (allow/deny/approve/remediate).
- **Packs + Workers** deliver capabilities that can be safely dispatched.
- **Tenants** isolate workloads and data (every request requires a tenant).

## Architecture at a glance

- **API Gateway**: HTTP + gRPC entrypoint, auth, rate limits.
- **Scheduler**: evaluates jobs and enforces timeouts/retries.
- **Safety Kernel**: policy evaluation and approval workflow.
- **NATS JetStream**: durable bus for job dispatch.
- **Redis**: state, workflow definitions, config, pointers.

See [Architecture](Architecture) for the full data flow.

## Where to go next

- [CLI](CLI) - `cordumctl` reference.
- [Packs and Workers](Packs-and-Workers) - build and install capabilities.
- [Dashboard](Dashboard) - UI setup and configuration.
- [Operations](Operations) - metrics, runbooks, scaling.
- [Troubleshooting](Troubleshooting) - common errors and fixes.
- [Release and Upgrades](Release-and-Upgrades) - upgrade guidance.
