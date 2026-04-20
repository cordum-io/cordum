# Topic Registry

The topic registry is the authoritative map from a Cordum topic name
(`job.foo`, `pack.stripe.charge_refund`, ...) to the metadata the
scheduler needs to dispatch a job on that topic: which pool serves it,
which input/output schemas validate its payloads, which risk tags
apply, and which pack owns it.

Prior to Phase 2 this mapping lived in a boot-time YAML
(`config/pools.yaml`). In K8s and Helm deployments that model forces a
restart every time a pack is installed or upgraded, and every replica
needs the same YAML mounted at the same path. Phase 2 replaces that
with a Redis-backed dynamic registry populated by
`cordumctl pack install` and consumed by the gateway, scheduler, and
dashboard at runtime.

## Why dynamic

- **Hot install**: `cordumctl pack install stripe.tgz` makes the
  pack's topics dispatchable on the next request, with no restart.
- **Multi-replica consistency**: the registry is a single Redis
  document accessed through `configsvc`, so every gateway and
  scheduler replica sees the same state without synchronising local
  files.
- **K8s + Helm**: Helm charts no longer bake topic lists into
  ConfigMaps — they just install packs and the registry fills in at
  runtime. Rollbacks uninstall packs, which unregister their topics.

## Data model

Each entry is a `topicregistry.Registration` stored at configsvc
scope `system`, ID `topics`:

| Field | Purpose |
|---|---|
| `name` | Canonical topic name (e.g. `job.foo`). Key in the map. |
| `pool` | Worker pool that serves the topic. |
| `input_schema_id` / `output_schema_id` | Schema IDs for payload validation. |
| `pack_id` | Owning pack. Used by pack uninstall to delete the pack's topics. |
| `requires` | Capabilities a worker must advertise to receive jobs on this topic. |
| `risk_tags` | Compliance/audit tags attached to jobs on this topic. |
| `status` | `active` / `deprecated` / `disabled`. |
| `risk_tag_deriver` | Optional name of a built-in server-side risk-tag deriver. |

## REST API

All three endpoints live under `/api/v1/topics`.

```
GET    /api/v1/topics              # admin|operator|viewer
POST   /api/v1/topics              # admin
DELETE /api/v1/topics/{name}       # admin
```

`GET` returns `{items: [...], registry_empty: bool}`. Each item
carries the registration plus an `active_worker_count` computed from
the scheduler's runtime snapshot. When `registry_empty` is `true` and
no legacy topics exist, the deployment has never registered a topic
— the dashboard uses this to surface the "install a pack to add
topics" CTA.

`POST` registers or updates a topic. Admin-only. Uses the configsvc
SetWithRetry path so concurrent admin writes are serialised on the
document's revision rather than a raw HSETNX.

`DELETE /api/v1/topics/{name}` removes a single topic. Intended for
operator break-glass only; the normal lifecycle is
`cordumctl pack install` / `pack uninstall`.

## Pack install flow

`cordumctl pack install <pack.tgz>` uploads the pack bundle to
`/api/v1/packs/install`. The gateway's handler (`installPackFromDir`
in `core/controlplane/gateway/handlers_packs.go`):

1. Parses the pack manifest.
2. Applies any policy bundle fragments the pack ships.
3. Calls `s.topicRegistry.SetMany(ctx, regs)` where `regs` is the
   list of Registrations built from the manifest's `topics` array.
4. Issues a pack-scoped worker credential.
5. Updates the pack registry document.
6. Publishes `configChanged("system","topics")` so consumers refresh.
7. Emits a `topic_registered` SIEM event per topic (see below).

If any of steps 4–6 fails, step 3's writes are rolled back via
`DeleteMany(registeredTopicNames)` and the other pack-lifecycle
mutations unwind too. The scheduler never sees a partial installation.

## Pack uninstall flow

`cordumctl pack uninstall <pack-id>` hits `/api/v1/packs/{id}/uninstall`.
The handler deletes the pack's topics via
`s.topicRegistry.DeleteMany(ctx, names)`, publishes the same config-
change event, and emits a `topic_unregistered` SIEM event per topic.
The uninstall order is: topics → worker credential → pack registry
doc → policy bundle fragments.

## Collision handling

The registry is keyed by topic name across all tenants. A pack that
tries to register a topic name already owned by a different pack
fails on configsvc's SetWithRetry path with a revision conflict —
the installer is expected to either (a) choose a disambiguating name
(`stripe.charge_refund` rather than `charge_refund`) or (b) ask the
owning pack's author to rename first. The error surfaces as HTTP 409.

## cordumctl

```
$ cordumctl topic list
NAME                       POOL       PACK           STATUS
job.foo                    default    —              active
pack.stripe.charge_refund  payments   stripe/v2      active
pack.demo.echo             default    demo-echo/1    active

$ cordumctl topic list --pack stripe/v2
NAME                       POOL       PACK           STATUS
pack.stripe.charge_refund  payments   stripe/v2      active

$ cordumctl topic list --json
{"items":[...],"registry_empty":false}
```

Filters (`--pack`, `--pool`) apply client-side; the gateway returns
the full list and `cordumctl` filters locally.

## Legacy YAML migration

Deployments that previously ran with `config/pools.yaml`-driven
topics get an automatic one-time migration. The first call that
reads the registry finds the canonical doc empty and invokes
`migrateFromLegacyPools()` in
`core/controlplane/topicregistry/service.go`, which builds
Registrations from the YAML and persists them through the normal
SetWithRetry path. After migration the YAML is advisory only — it
can be deleted on the next rollout without breaking dispatch.

There is no feature flag (e.g. `CORDUM_TOPICS_SOURCE`) because the
scheduler reads per-request from the registry rather than caching a
YAML-driven map at boot; the migration closes the gap on first read
without operator action. A flag can be added in a follow-up if ops
need to pin the source explicitly during a cutover window.

## Audit events

Every registration and unregistration emits a SIEMEvent through the
gateway's audit exporter:

| EventType | When | Severity | Extra fields |
|---|---|---|---|
| `topic_registered` | After a successful `SetMany` during pack install | `INFO` | `pack_id`, `topic_name`, `actor_id` |
| `topic_unregistered` | After a successful `DeleteMany` during pack uninstall | `MEDIUM` | `pack_id`, `topic_name`, `actor_id` |

Events land in the tenant audit chain via the Merkle chainer (see
`docs/deployment/audit-chain.md`) — so a compliance reviewer can
reconstruct the full lifecycle of a topic, including which admin
added or removed it. Uninstall is `MEDIUM` severity because the
operation removes a previously-available dispatch path; alerting on
this event is expected in production deployments.

## Failure modes

| Symptom | Likely cause | Remediation |
|---|---|---|
| `GET /api/v1/topics` returns `503 topic registry unavailable` | Gateway booted without a configsvc client | Check `CORDUM_REDIS_URL`; the topic registry shares configsvc's Redis. |
| `POST` returns `409` | Two admins simultaneously editing the same topic | Retry; `SetWithRetry` makes sequential writes succeed. |
| Pack install fails with a topic error | Name collision with a different pack | Rename the topic in the pack manifest, rebuild, retry. |
| Dashboard Topics page empty after install | Stale React Query cache | Reload; 5-second poll picks up fresh data otherwise. |
| Scheduler dispatches fail with "topic not in registry" | Pack uninstalled while jobs were in flight | Expected — the topic is deliberately gone. Re-install the pack to restore. |
