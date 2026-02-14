# Packs (Technical + How-To)

This document defines the pack format and installation flow for Cordum.
Packs are installable overlays (workflows + schemas + config/policy fragments) that
extend the platform **without core code changes**.

## Pack goals (v0)

- API-native installs via gateway + config service.
- No arbitrary code execution during install.
- Namespaced resources to avoid collisions.
- Soft uninstall by default (disable routing + policy; keep workflows/schemas).

## Pack bundle format

A pack is either:
- a directory containing `pack.yaml`, or
- a `.tgz` archive whose root contains `pack.yaml` (or a single top-level folder with it).

## Scaffold a pack

Generate a minimal pack skeleton:

```bash
cordumctl pack create my-pack
```

## Marketplace catalogs

Cordum can discover and install packs from catalog JSON files. Catalogs are configured
in the config service under `cfg:system:pack_catalogs`:

Catalog entries may include an optional `image` URL for UI pack cards.

Official catalog source: `https://github.com/cordum-io/cordum-packs` (published to `https://packs.cordum.io`).

The gateway seeds `cfg:system:pack_catalogs` with the official catalog if the
document is missing or empty. Override or disable with:
- `CORDUM_PACK_CATALOG_URL`
- `CORDUM_PACK_CATALOG_ID`
- `CORDUM_PACK_CATALOG_TITLE`
- `CORDUM_PACK_CATALOG_DEFAULT_DISABLED=1`

```json
{
  "catalogs": [
    {
      "id": "official",
      "title": "Cordum Official",
      "url": "https://packs.cordum.io/catalog.json",
      "enabled": true
    }
  ]
}
```

Gateway endpoints:
- `GET /api/v1/marketplace/packs` (merged catalog view + installed status)
- `POST /api/v1/marketplace/install` (install by catalog or URL)

Install by catalog:

```json
{
  "catalog_id": "official",
  "pack_id": "sre-k8s-triage",
  "version": "0.3.1"
}
```

Install by URL (sha256 required):

```json
{
  "url": "https://packs.cordum.io/packs/sre-k8s-triage/0.3.1/pack.tgz",
  "sha256": "<sha256>"
}
```

The gateway downloads the bundle, verifies sha256, and runs the same install flow as
`cordumctl pack install`. Only `http`/`https` URLs are supported.
Direct URL installs must match a pack URL present in an enabled marketplace catalog
and the provided sha256 must match the catalog entry.

### Bundle safety limits (gateway)

To avoid zip-slip and oversized uploads, the gateway enforces:
- Max upload size: 64 MiB
- Max files: 2048
- Max file size: 32 MiB
- Max uncompressed size: 256 MiB
- Absolute paths and `..` segments are rejected during extraction

### Directory layout (recommended)

```
my-pack/
  pack.yaml
  workflows/
    triage.yaml
  schemas/
    IncidentContext.json
  overlays/
    pools.patch.yaml
    timeouts.patch.yaml
    policy.fragment.yaml
  deploy/
    docker-compose.yaml
```

`deploy/` artifacts are informational only. Core does not deploy workers.

Example pack (in this repo):
- `examples/hello-pack` (minimal workflow + schema + overlays)

External reference pack:
- `cordum-packs/packs/mcp-bridge` (MCP stdio bridge + pack)

## pack.yaml schema (v0)

Required fields:

```yaml
apiVersion: cordum.io/v1alpha1
kind: Pack

metadata:
  id: sre-investigator
  version: 0.3.1
  title: SRE Investigator
  description: Incident triage + evidence collection.
  image: https://cdn.simpleicons.org/slack

compatibility:
  protocolVersion: 1
  minCoreVersion: 0.6.0

topics:
  - name: job.sre-investigator.collect.k8s
    requires: ["kubectl", "network:egress"]
    riskTags: ["network"]
    capability: sre.collect.k8s

resources:
  schemas:
    - id: sre-investigator/IncidentContext
      path: schemas/IncidentContext.json
  workflows:
    - id: sre-investigator.triage
      path: workflows/triage.yaml

overlays:
  config:
    - name: pools
      scope: system
      key: pools
      strategy: json_merge_patch
      path: overlays/pools.patch.yaml
    - name: timeouts
      scope: system
      key: timeouts
      strategy: json_merge_patch
      path: overlays/timeouts.patch.yaml
  policy:
    - name: safety
      strategy: bundle_fragment
      path: overlays/policy.fragment.yaml

tests:
  policySimulations:
    - name: allow_collect
      request:
        tenantId: default
        topic: job.sre-investigator.collect.k8s
        capability: sre.collect.k8s
        riskTags: ["network"]
      expectDecision: ALLOW
```

## Naming rules (enforced by installer)

- Pack ID: `^[a-z0-9-]+$`
- Topics: `job.<pack_id>.*`
- Workflow IDs: `<pack_id>.<name>`
- Schema IDs: `<pack_id>/<name>`
- Pool names **created by the pack** must start with `<pack_id>`. Packs may map topics to pre-existing pools.

## Install flow

`cordumctl pack install <path|url>` performs:

0) Acquire locks: `packs:global` + `pack:<id>` (single-writer semantics).
1) Validate `pack.yaml` (namespacing, protocol version).
2) Collision checks:
   - Schema/workflow id exists + different digest -> fail unless `--upgrade`.
3) Register schemas.
4) Upsert workflows.
5) Apply config overlays (json merge patch) into config service.
6) Apply policy fragments into config service bundle.
7) Write pack registry record to `cfg:system:packs`.

### Atomicity + rollback (best-effort)

If any step after writes begin fails, the installer attempts to roll back:
- revert config overlays to the previous snapshot
- restore previous policy fragment values (or delete if newly added)
- delete schemas/workflows created in this attempt; restore previous versions when upgrading

### Policy fragments

Policy fragments are stored in `cfg:system:policy` under `bundles`:

```
cfg:system:policy.data.bundles["<pack_id>/<name>"] = {
  content: "<yaml>",
  version: "<pack_version>",
  sha256: "<digest>",
  installed_at: "<rfc3339>"
}
```

Safety kernel merges file/URL policy with config service fragments on load/reload.
Snapshot hashes are combined (e.g. `baseSnapshot|cfg:<hash>`).

Policy rules may include **remediations** to suggest safer alternatives:

```yaml
rules:
  - id: deny-prod-delete
    match:
      topics: ["job.db.delete"]
    decision: deny
    reason: "Production deletes are blocked"
    remediations:
      - id: archive
        title: "Archive instead of delete"
        summary: "Route to a retention-safe workflow"
        replacement_topic: "job.db.archive"
        replacement_capability: "db.archive"
        add_labels:
          reason: "policy_remediation"
```

Remediations are surfaced on job detail and can be applied via `POST /api/v1/jobs/{id}/remediate`.

Bundle entries may also include `enabled`, `author`, `message`, `created_at`, and `updated_at` (Policy Studio uses these).
When omitted, bundles default to enabled.

Related env vars:
- `SAFETY_POLICY_CONFIG_SCOPE` (default `system`)
- `SAFETY_POLICY_CONFIG_ID` (default `policy`)
- `SAFETY_POLICY_CONFIG_KEY` (default `bundles`)
- `SAFETY_POLICY_CONFIG_DISABLE=1` to disable config service fragments
- `SAFETY_POLICY_RELOAD_INTERVAL` (duration, default 30s)

### Config overlays

Config overlays use **json_merge_patch** semantics:
- `null` deletes a key (used by uninstall).
- Supported top-level keys: `pools`, `timeouts`.

`overlays.config[].key` targets a field inside a config document (`cfg:<scope>:<scope_id>`).
For example, `scope: system` + `scope_id: default` + `key: pools` patches `cfg:system:default.data.pools`.
If `scope_id` is omitted for system scope, the default is `default`.

`pools` patch supports:
- `topics`: map of `topic -> pool(s)`
- `pools`: map of `pool -> {requires: []}`

`timeouts` patch supports:
- `topics`: map of topic-specific timeouts
- `workflows`: map of workflow-specific timeouts

Scheduler reloads `pools` and `timeouts` from config service periodically:
- `SCHEDULER_CONFIG_RELOAD_INTERVAL` (duration, default 30s)

On startup the scheduler bootstraps defaults from `config/pools.yaml` and
`config/timeouts.yaml` into config service if missing.

## Uninstall flow

`cordumctl pack uninstall <id>`:

- Removes config overlays (merge patch deletion).
- Removes policy fragments.
- Marks pack as `DISABLED` in registry.

`--purge` additionally deletes workflows and schemas that the pack installed.

## Upgrade flow

For upgrades, schemas/workflows are upserted; config and policy overlays replace previous values.
Policy fragment keys are stable per pack+name so upgrades overwrite in place.

## CLI commands

```bash
cordumctl pack install ./my-pack
cordumctl pack install https://example.com/my-pack.tgz
cordumctl pack install --inactive ./my-pack
cordumctl pack install --upgrade ./my-pack
cordumctl pack list
cordumctl pack show sre-investigator
cordumctl pack verify sre-investigator
cordumctl pack uninstall sre-investigator
cordumctl pack uninstall --purge sre-investigator
```

Flags:
- `--inactive` installs workflows/schemas but skips pool mappings (pack is INACTIVE).
- `--upgrade` overwrites existing schemas/workflows if digest differs.
- `--force` bypasses `minCoreVersion` validation.
- `--dry-run` prints intent without writing.

## Pack registry

Installed packs are recorded in config service:

```
cfg:system:packs.data.installed["<pack_id>"] = {
  id, version, status, installed_at, resources, overlays, tests, ...
}
```

## How packs use workflow step metadata

Workflows can attach `meta` to steps to pass job metadata:
`pack_id`, `capability`, `risk_tags`, `requires`, `actor_id`, `idempotency_key`.
This maps directly to CAP `JobMetadata` during dispatch.

## Compatibility

- `compatibility.protocolVersion` must match the CAP wire protocol (currently `1`).
- `minCoreVersion` is enforced when the gateway build version is a valid semver.
  Dev/unknown builds skip the check; `--force` always bypasses it.
- Gateway build version is read from `GET /api/v1/status` (`build.version`); include `X-API-Key` and `X-Tenant-ID` when auth is enabled.

---

## Pack Development Workflow

Full lifecycle for creating, testing, and publishing a pack:

```
create → develop → test → build → verify → publish
```

### 1. Scaffold

```bash
cordumctl pack create my-pack
cd my-pack/
```

This generates a skeleton with `pack.yaml`, `workflows/`, `schemas/`, and `overlays/` directories.

### 2. Develop

Add your worker code, workflow definitions, schemas, and policy fragments. Follow the [naming rules](#naming-rules-enforced-by-installer) — topics must match `job.<pack_id>.*`.

### 3. Test Locally

Install the pack against a local Cordum stack:

```bash
# Start infrastructure
export CORDUM_API_KEY="$(openssl rand -hex 32)"
docker compose up -d

# Install pack (dry run first)
cordumctl pack install --dry-run ./my-pack
cordumctl pack install ./my-pack

# Verify pack is installed
cordumctl pack list
cordumctl pack show my-pack
```

### 4. Verify

```bash
cordumctl pack verify my-pack
```

### 5. Build Archive

```bash
tar -czf my-pack.tgz -C my-pack .
sha256sum my-pack.tgz
```

### 6. Publish

See [Marketplace Publishing](#marketplace-publishing) below.

---

## Pack Verification

`cordumctl pack verify <id>` runs the policy simulation tests defined in the
installed pack's `tests.policySimulations` section against the live safety kernel.

How it works:

1. Fetches the pack record from `cfg:system:packs`.
2. For each `policySimulations` entry, sends a `POST /api/v1/policy/simulate` request with the test's topic, capability, risk tags, and other metadata.
3. Compares the returned decision against `expectDecision` (normalized — e.g. `ALLOW`, `DENY`, `REQUIRE_APPROVAL`, `THROTTLE`, `ALLOW_WITH_CONSTRAINTS`).
4. If `pack_id` is not set in the test request, it defaults to the pack's own ID.
5. If `tenantId` is not set, it defaults to `default`.
6. Exits with an error on the first failing simulation.

Schema validation, naming rules, and compatibility checks are performed during
`pack install`, not during `pack verify`.

### Simulation request fields

| Field | Description |
|-------|-------------|
| `tenantId` | Tenant to simulate against (default: `default`) |
| `topic` | NATS topic (e.g. `job.my-pack.echo`) |
| `capability` | Capability string (e.g. `my-pack.echo`) |
| `riskTags` | Risk tag list (e.g. `["network", "write"]`) |
| `requires` | Requirements list (e.g. `["kubectl"]`) |
| `packId` | Pack ID override (defaults to the installed pack ID) |
| `actorId` | Actor identifier for the simulation |
| `actorType` | Actor type (`user`, `service`, etc.) |

Example `pack.yaml` test block:

```yaml
tests:
  policySimulations:
    - name: allow_echo
      request:
        tenantId: default
        topic: job.hello-pack.echo
        capability: hello.echo
      expectDecision: ALLOW
    - name: deny_dangerous
      request:
        topic: job.hello-pack.exec
        capability: exec.shell
        riskTags: ["shell_exec"]
      expectDecision: DENY
```

```bash
$ cordumctl pack verify my-pack
pack my-pack policy simulations passed
```

---

## Testing Packs

### Unit Testing Handlers

Since pack handlers are typed Go functions (`func(runtime.Context, TIn) (TOut, error)`),
you can test them directly without NATS or Redis:

```go
func TestEchoHandler(t *testing.T) {
    input := echoInput{Message: "test", Author: "bot"}
    output, err := handler(runtime.Context{}, input)
    if err != nil {
        t.Fatal(err)
    }
    if output.Message != "test" {
        t.Fatalf("expected 'test', got %q", output.Message)
    }
}
```

### Integration Testing with Docker Compose

Add a `deploy/docker-compose.yaml` to your pack for local testing:

```yaml
services:
  my-worker:
    build: .
    environment:
      - NATS_URL=nats://nats:4222
      - REDIS_URL=redis://:cordum-dev@redis:6379
      - WORKER_ID=my-worker
    depends_on:
      nats:
        condition: service_healthy
    networks:
      - cordum_default

networks:
  cordum_default:
    external: true
```

Run against the main Cordum stack:

```bash
# Start Cordum stack
docker compose up -d

# Start your pack worker (joins the Cordum network)
docker compose -f my-pack/deploy/docker-compose.yaml up -d

# Submit a test job
curl -s -X POST http://localhost:8080/api/v1/jobs \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{"prompt":"hello","topic":"job.my-pack.echo"}'
```

### E2E Smoke Test Pattern

```bash
#!/usr/bin/env bash
set -euo pipefail

# Install pack
cordumctl pack install ./my-pack

# Submit job and capture ID
JOB_ID=$(curl -s -X POST http://localhost:8080/api/v1/jobs \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: default" \
  -H "Content-Type: application/json" \
  -d '{"prompt":"smoke test","topic":"job.my-pack.echo"}' | jq -r '.id')

# Poll for completion (max 30s)
for i in $(seq 1 30); do
  STATUS=$(curl -s http://localhost:8080/api/v1/jobs/$JOB_ID \
    -H "X-API-Key: $CORDUM_API_KEY" \
    -H "X-Tenant-ID: default" | jq -r '.status')
  [ "$STATUS" = "succeeded" ] && echo "PASS" && exit 0
  [ "$STATUS" = "failed" ] && echo "FAIL" && exit 1
  sleep 1
done
echo "TIMEOUT" && exit 1
```

### Policy Simulation Tests

Built into `pack.yaml` under `tests.policySimulations`. Run them with:

```bash
cordumctl pack verify my-pack
```

This evaluates each simulation against the live safety kernel and reports pass/fail.

---

## Pack Configuration

### Declaring Required Config

Packs declare configuration requirements through config overlays. The `overlays.config` section in `pack.yaml` specifies patches applied to the config service:

```yaml
overlays:
  config:
    - name: pools
      scope: system
      key: pools
      strategy: json_merge_patch
      path: overlays/pools.patch.yaml
```

### Config Patch Format

Pool mapping patch (`overlays/pools.patch.yaml`):

```yaml
topics:
  job.my-pack.analyze: my-pack
  job.my-pack.summarize: my-pack
pools:
  my-pack:
    requires: ["network:egress"]
```

Timeout patch (`overlays/timeouts.patch.yaml`):

```yaml
topics:
  job.my-pack.analyze:
    dispatch_timeout: 30s
    execution_timeout: 120s
```

### Default Values

Config patches use JSON Merge Patch semantics — values are set if not already present, and `null` removes a key. The scheduler reloads config periodically (`SCHEDULER_CONFIG_RELOAD_INTERVAL`, default 30s).

### Pool Config Caching

The scheduler's `bootstrapConfig()` is write-once — it caches pool config in Redis key `cfg:system:default`. If your pack's pool mapping isn't picked up after install:

```bash
# Delete the cached config key
docker compose exec redis redis-cli -a cordum-dev DEL cfg:system:default
docker compose restart scheduler
```

See [DOCKER.md](DOCKER.md) for more on this caching behavior.

---

## Worker Registration

Pack workers must register with the platform via heartbeats and topic subscriptions.

### Runtime Lifecycle

Using the Go SDK (`sdk/runtime`):

```go
import (
    "github.com/cordum/cordum/sdk/runtime"
    "github.com/nats-io/nats.go"
)

// 1. Create an agent
agent := &runtime.Agent{
    NATSURL:  "nats://nats:4222",
    RedisURL: "redis://:cordum-dev@redis:6379",
    SenderID: "my-worker",
}

// 2. Register handlers for pack topics
runtime.Register(agent, "job.my-pack.analyze", analyzeHandler)
runtime.Register(agent, "job.my-pack.summarize", summarizeHandler)
// Direct-address handler (for targeted dispatch)
runtime.Register(agent, runtime.DirectSubject("my-worker"), analyzeHandler)

// 3. Start the agent (subscribes to NATS topics)
agent.Start()
defer agent.Close()

// 4. Start heartbeat loop
nc, _ := nats.Connect("nats://nats:4222")
heartbeatFn := func() ([]byte, error) {
    return runtime.HeartbeatPayload(
        "my-worker",   // worker ID
        "my-pack",     // pool name (must match pools.yaml mapping)
        0,             // active jobs
        4,             // max concurrent jobs
        0,             // reserved
    )
}
go runtime.HeartbeatLoop(ctx, nc, heartbeatFn)
```

### Heartbeat Protocol

Workers emit heartbeats on `sys.heartbeat` every 5 seconds (default). The scheduler uses these to:

1. **Track worker liveness** — workers missing 3+ heartbeats are marked offline
2. **Route jobs** — only workers in the matching pool with available capacity receive jobs
3. **Monitor capacity** — `active_jobs` / `max_concurrent` drives the dashboard's agent fleet view

**Critical**: The pool name in `HeartbeatPayload` must match the pool that the pack's topic maps to in `config/pools.yaml` (or the pack's pool overlay).

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://127.0.0.1:4222` | NATS connection URL |
| `REDIS_URL` | `redis://:cordum-dev@127.0.0.1:6379/0` | Redis connection URL |
| `WORKER_ID` | *(required)* | Unique worker identifier |

See [sdk-reference.md](sdk-reference.md) for the full SDK API reference.

---

## Marketplace Publishing

### Publishing Flow

1. **Prepare**: Ensure `pack.yaml` is complete with `metadata.image`, `compatibility`, and `tests`
2. **Build**: Create a `.tgz` archive: `tar -czf my-pack-0.1.0.tgz -C my-pack .`
3. **Compute digest**: `sha256sum my-pack-0.1.0.tgz`
4. **Host**: Upload the `.tgz` to a publicly accessible URL
5. **Register**: Add an entry to your catalog JSON:

```json
{
  "id": "my-pack",
  "title": "My Pack",
  "description": "Does something useful",
  "version": "0.1.0",
  "url": "https://example.com/packs/my-pack-0.1.0.tgz",
  "sha256": "<digest>",
  "image": "https://example.com/my-pack-icon.svg",
  "tags": ["sre", "monitoring"]
}
```

### Official Catalog

The official pack catalog lives at `https://github.com/cordum-io/cordum-packs` and is published to `https://packs.cordum.io/catalog.json`. To submit:

1. Fork `cordum-packs`
2. Add your pack under `packs/<pack-id>/`
3. Update `catalog.json` with your entry
4. Open a pull request

### Version Management

- Pack versions follow semver (`major.minor.patch`)
- The marketplace shows the latest version per pack
- Users can install specific versions via `"version": "0.3.1"` in the install payload
- Old versions remain available at their URLs

### Unpublishing

Remove the entry from the catalog JSON and re-publish. Installed instances are not affected — unpublishing only prevents new installs.

---

## Examples

### hello-worker-go

Located at `examples/hello-worker-go/`. A minimal Go worker demonstrating:

- SDK runtime agent setup
- Topic subscription (`job.hello-pack.echo`)
- Direct-address handler (`runtime.DirectSubject`)
- Heartbeat loop with pool registration (`pool=hello-pack`)
- Graceful shutdown with signal handling

```bash
# Run locally
NATS_URL=nats://localhost:4222 \
REDIS_URL=redis://:cordum-dev@localhost:6379 \
go run ./examples/hello-worker-go

# Or via Docker
docker build -f examples/hello-worker-go/Dockerfile -t hello-worker .
docker run --network cordum_default \
  -e NATS_URL=nats://nats:4222 \
  -e REDIS_URL=redis://:cordum-dev@redis:6379 \
  hello-worker
```

### hello-pack

Located at `examples/hello-pack/`. A minimal pack bundle with:

- `pack.yaml` with topic declarations
- Workflow definition
- JSON schema
- Pool and timeout overlays
- Policy fragment

### Additional Examples

- `examples/python-worker/` — Python worker using NATS client
- `examples/node-worker/` — Node.js worker example
- `examples/demo-guardrails/` — Approval + remediation demo pack

---

## Related docs

- [SDK Reference](sdk-reference.md) — Worker runtime, gateway client, heartbeats, blob store, testing patterns
- [Safety Kernel](safety-kernel.md) — Policy evaluation, fragment merging, overlays, cache, signatures
- [Configuration Reference](configuration-reference.md) — Config service, pools.yaml, env vars master table
- [Dashboard Guide](dashboard-guide.md) — Packs page UI, marketplace browsing, install from UI
- [API Reference](api-reference.md) — REST endpoints for packs, marketplace, policy simulation
- [DOCKER.md](DOCKER.md) — Docker Compose setup, Redis caching, pool config troubleshooting
