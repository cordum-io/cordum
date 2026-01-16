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
cordumctl pack install ./my-pack --inactive
cordumctl pack install ./my-pack --upgrade
cordumctl pack list
cordumctl pack show sre-investigator
cordumctl pack verify sre-investigator
cordumctl pack uninstall sre-investigator
cordumctl pack uninstall sre-investigator --purge
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
- Gateway build version is read from `GET /api/v1/status` (`build.version`).
