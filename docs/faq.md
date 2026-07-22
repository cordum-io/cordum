# FAQ

## Does Cordum ship workers?

No. Cordum is a control plane only. Workers live outside this repo and connect
via NATS topics using CAP v2 wire contracts.

## Can I run workflows without workers?

Yes. The platform supports "vanilla workflows" (create/run/approve/delete)
without external packs installed. Steps that require workers will remain
pending until a worker subscribes.

## Where do packs live?

Packs are installable overlays (workflows, schemas, config, policy). They are
installed via `cordumctl pack` or the gateway APIs. Worker binaries for packs
are deployed separately.

## What is stored in Redis?

Job metadata, workflow runs, config snapshots, DLQ entries, and pointers to
context/results/artifacts are stored in Redis. See `docs/system_overview.md` for
details.

## What license is Cordum core under?

The core repo (`cordum`) is licensed under BUSL-1.1. It is free to self-host
and use internally, but it does not permit a competing hosted/managed offering.
See `LICENSE` for the Change Date and terms.
