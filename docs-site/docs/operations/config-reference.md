---
sidebar_position: 5
title: "Configuration Reference (Full)"
slug: /operations/config-reference
---

# Configuration Reference

Complete reference for all Cordum configuration files, environment variables, and the config overlay system.

For a quick-start overview, see [configuration.md](/operations/configuration-guide).

---

## Table of Contents

1. [Overview](#overview)
2. [system.yaml — System Configuration](#systemyaml--system-configuration)
3. [Config Overlay System](#config-overlay-system)
4. [pools.yaml — Worker Pool Routing](#poolsyaml--worker-pool-routing)
5. [timeouts.yaml — Timeout Configuration](#timeoutsyaml--timeout-configuration)
6. [safety.yaml — Safety Policy](#safetyyaml--safety-policy)
7. [output_scanners.yaml — Output Scanner Patterns](#output_scannersyaml--output-scanner-patterns)
8. [Environment Variables Master Table](#environment-variables-master-table)
9. [Cross-References](#cross-references)

---

## Overview

Cordum uses three configuration layers:

1. **YAML config files** — mounted into containers from `config/`
2. **Environment variables** — per-service settings, secrets, addresses
3. **Config overlay system** — runtime config stored in Redis, merged by scope hierarchy

### Config Files

| File | Purpose | Validated |
|------|---------|-----------|
| `config/pools.yaml` | Topic-to-pool routing, pool capability requirements | Yes (JSON Schema) |
| `config/timeouts.yaml` | Per-topic and per-workflow timeouts, reconciler settings | Yes (JSON Schema) |
| `config/safety.yaml` | Safety kernel input/output rules, MCP allow/deny lists | Yes (JSON Schema) |
| `config/output_scanners.yaml` | Output content scanner regex patterns (secret, PII, injection) | No |
| `config/system.yaml` | System-wide config (budgets, rate limits, models, SLOs) — stored via config service | No |
| `config/nats.conf` | NATS server config (JetStream `sync_interval`) | N/A |

The control plane validates pools, timeouts, and safety files against embedded JSON schemas in `core/infra/config/schema/`. Invalid configs return errors; for timeouts, the system falls back to defaults.

### Config Loading Order

1. YAML files loaded from paths specified by env vars (or defaults)
2. On startup, `bootstrapConfig()` writes file-based pools/timeouts into the Redis config service
3. Runtime overlay from Redis config service takes precedence over files
4. Env vars override specific settings (e.g., `OUTPUT_POLICY_ENABLED` overrides safety.yaml)

---

## system.yaml — System Configuration

`config/system.yaml` is **not** mounted by default in Docker Compose. It is a payload for the config service — store it via `POST /api/v1/config` or let packs write fragments.

### safety

Controls system-wide safety defaults. These supplement the rule-based policy in `safety.yaml`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `pii_detection_enabled` | bool | `true` | Enable PII detection in inputs |
| `pii_action` | string | `"block"` | Action on PII detection: `block`, `redact`, `warn` |
| `pii_types_to_detect` | string[] | `["email","phone"]` | PII categories to scan for |
| `injection_detection` | bool | `true` | Enable prompt injection detection |
| `injection_sensitivity` | string | `"high"` | Sensitivity level: `low`, `medium`, `high` |
| `content_filter_enabled` | bool | `true` | Enable content category filtering |
| `blocked_categories` | string[] | `["hate_speech","sexual_content"]` | Blocked content categories |
| `anomaly_detection` | bool | `false` | Enable anomaly detection |
| `allowed_topics` | string[] | `[]` | Allowlisted topics (empty = all allowed) |
| `denied_topics` | string[] | `[]` | Denylisted topics |

### budget

Cost control and attribution settings.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `daily_limit_usd` | float | `1000.0` | Daily spend limit in USD |
| `monthly_limit_usd` | float | `10000.0` | Monthly spend limit |
| `per_job_max_usd` | float | `5.0` | Maximum cost per single job |
| `per_workflow_max_usd` | float | `50.0` | Maximum cost per workflow run |
| `alert_at_percent` | int[] | `[50,75,90,100]` | Alert at these % of limit |
| `action_at_limit` | string | `"throttle"` | Action when limit hit: `throttle`, `deny`, `alert` |
| `cost_attribution_enabled` | bool | `true` | Enable per-tenant cost tracking |
| `cost_centers` | string[] | `[]` | Cost center tags for attribution |

### rate_limits

System-level budget rate limits enforced by the scheduler. These are independent from gateway-level API rate limiting (`API_RATE_LIMIT_RPS` env var), which is enforced by the api-gateway middleware before requests reach the scheduler.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `requests_per_minute` | int | `120000` | Sustained throughput limit (2000 req/sec) |
| `requests_per_hour` | int | `7200000` | Hourly throughput limit |
| `burst_size` | int | `4000` | Token bucket burst — peak spike capacity before throttling |
| `concurrent_jobs` | int | `10000` | Max concurrent jobs across all tenants |
| `concurrent_workflows` | int | `5` | Max concurrent workflows |
| `queue_size` | int | `5000` | Max pending queue depth |

### retry

Default retry policy for jobs (overridable per-topic in `timeouts.yaml`).

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_retries` | int | `3` | Maximum retry attempts |
| `initial_backoff` | duration | `1s` | Initial backoff delay |
| `max_backoff` | duration | `30s` | Maximum backoff delay |
| `backoff_multiplier` | float | `2.0` | Exponential backoff multiplier |
| `retryable_errors` | string[] | `["network_error","timeout"]` | Error types that trigger retry |
| `non_retryable_errors` | string[] | `["bad_request"]` | Error types that skip retry |

### resources

Resource allocation defaults.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `default_priority` | string | `"interactive"` | Default job priority |
| `max_timeout_seconds` | int | `300` | Maximum allowed timeout |
| `default_timeout_seconds` | int | `60` | Default job timeout |
| `max_parallel_steps` | int | `10` | Max parallel workflow steps |
| `preemption_enabled` | bool | `true` | Allow job preemption |
| `preemption_grace_period` | int | `30` | Seconds before preemption |

### models

Allowed LLM model configuration.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allowed_models` | string[] | `["gpt-4","llama-3","claude-3"]` | Permitted model identifiers |
| `default_model` | string | `"gpt-4"` | Default model for jobs |
| `fallback_models` | string[] | `["llama-3"]` | Models to try if primary unavailable |

### context

Context engine retrieval settings.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allowed_memory_ids` | string[] | `["repo:*","kb:*"]` | Allowed memory ID patterns |
| `denied_memory_ids` | string[] | `[]` | Denied memory ID patterns |
| `max_context_tokens` | int | `4000` | Max tokens to retrieve |
| `max_retrieved_chunks` | int | `10` | Max chunks per retrieval |
| `cross_tenant_access` | bool | `false` | Allow cross-tenant context access |
| `allowed_connectors` | string[] | `["github","slack"]` | Permitted connector types |
| `redaction_policies` | object | `{}` | Config field defined but not yet consumed at runtime |

### slo

Service-level objective configuration.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `target_p95_latency_ms` | int | `1000` | Target p95 latency in milliseconds |
| `error_rate_budget` | float | `0.01` | Error rate budget (1%) |
| `timeout_seconds` | int | `60` | SLO evaluation window timeout |
| `critical` | bool | `false` | Mark as critical service |

### experiment (NOT YET IMPLEMENTED)

> Struct exists in code but no runtime code reads these fields.

```yaml
experiment:
  enabled: false
  name: ""
  buckets: []
```

### integrations (NOT YET IMPLEMENTED)

> Struct exists in code but no runtime code reads these fields.

```yaml
integrations:
  github:
    enabled: false
    connection_id: ""
    allowed_teams: []
    allowed_scopes: []
  gitlab:     # same structure
  slack:      # same structure
  jira:       # same structure
```

### observability (NOT YET IMPLEMENTED)

> No backing code or struct exists.

```yaml
observability:
  otel:
    enabled: false
    endpoint: ""
    protocol: "grpc"    # grpc | http
    headers: {}
    resource_attributes: {}
  grafana:
    base_url: ""
    dashboards:
      system_overview: ""
      workflow_performance: ""
```

### alerting (NOT YET IMPLEMENTED)

> No backing code or struct exists.

```yaml
alerting:
  pagerduty:
    enabled: false
    integration_key: ""
    severity: "critical"
  slack:
    enabled: false
    webhook_url: ""
    severity: "error"
```

---

## Config Overlay System

The config service stores configuration fragments in Redis, organized by scope hierarchy. Lower scopes override higher ones.

### Scope Hierarchy

```
system (global defaults)
  └── org (organization overrides)
       └── team (team overrides)
            └── workflow (workflow-specific)
                 └── step (step-specific)
```

### Redis Key Format

```
cfg:{scope}:{scope_id}
```

Examples:
- `cfg:system:default` — system-wide config (pools, timeouts, pack catalogs)
- `cfg:system:policy` — policy bundle fragments from packs
- `cfg:system:packs` — installed pack registry
- `cfg:system:pack_catalogs` — marketplace catalog definitions
- `cfg:org:acme-corp` — organization-level overrides
- `cfg:team:platform` — team-level overrides
- `cfg:workflow:my-workflow` — workflow-specific config

### Document Structure

Each config document in Redis is a JSON object:

```json
{
  "scope": "system",
  "scope_id": "default",
  "data": {
    "pools": { ... },
    "timeouts": { ... },
    "_poolsFileHash": "sha256...",
    "_timeoutsFileHash": "sha256..."
  },
  "revision": 3,
  "updated_at": "2026-01-15T10:30:00Z",
  "meta": {}
}
```

### bootstrapConfig() Behavior

On scheduler startup, `bootstrapConfig()` syncs file-based config into Redis:

1. Reads `cfg:system:default` from Redis
2. For pools and timeouts:
   - If the key **does not exist** in Redis, writes the file-based config (creates key)
   - If the key **exists**, compares SHA-256 hashes of the file content
   - If hashes **differ**, updates Redis with new file content (file wins)
   - If hashes **match**, no-op
3. This means dashboard/API changes to pools/timeouts persist until the file changes

### Config Reload

Config changes propagate to all replicas through two mechanisms:

1. **NATS notification (immediate)** — When `PUT /api/v1/config` writes to Redis, the API gateway publishes a lightweight notification to `sys.config.changed` (broadcast, empty queue group). All scheduler replicas subscribe and reload config from Redis immediately on receipt.

2. **Polling fallback (30s)** — Each scheduler replica polls Redis for config changes on a configurable interval. This catches any notifications missed due to transient NATS issues.

- **Env var**: `SCHEDULER_CONFIG_RELOAD_INTERVAL` (default `30s`)
- **NATS subject**: `sys.config.changed` — broadcast to all replicas
- On each reload (notification or poll), it reads `cfg:system:default` and compares hashes
- If pools changed: updates routing table live
- If timeouts changed: updates reconciler timeouts live

> **Note**: The NATS message is a notification only — it does not contain the config data itself. Replicas always reload from Redis to ensure consistency.

### Resetting Cached Config

To force a config reload from files:

```bash
# Delete the Redis config key
redis-cli DEL cfg:system:default

# The next scheduler tick (or restart) will re-bootstrap from files
```

### Effective Config Resolution

The config service merges scopes top-down. For a given request context:

```
effective = merge(system, org, team, workflow, step)
```

Each scope's `data` map shallow-merges into the result. Keys in lower scopes override higher scopes.

### API Endpoints

- `GET /api/v1/config?scope={scope}&scope_id={id}` — read a config document
- `PUT /api/v1/config` — write/update a config document
- `GET /api/v1/config/effective?scope={scope}&scope_id={id}` — get merged effective config

### Fresh Install Behavior

On fresh installs, no `cfg:system:default` key exists in Redis. When the dashboard
requests `GET /api/v1/config` (which defaults to `scope=system&scope_id=default`),
the gateway returns `200 {}` — an empty JSON object. The dashboard renders its
built-in defaults (safety stance, rate limits, retention days, etc.) until an admin
saves settings via the Settings page or `POST /api/v1/config`.

No manual config seeding is required. Non-default scope queries (e.g.,
`?scope=org&scope_id=acme`) still return `404` if the config document does not exist.

---

## pools.yaml — Worker Pool Routing

Defines how job topics are routed to worker pools.

### Example

```yaml
topics:
  "job.default":         ["general"]
  "job.hello-pack.echo": ["hello-pack"]
  "job.code-review":     ["code-review", "general"]   # fallback order
  "job.compliance.*":    ["compliance"]

pools:
  general:
    requires: []
  hello-pack:
    requires: []
  code-review:
    requires: ["code.read", "code.write"]
  compliance:
    requires: ["compliance.review", "data.access"]
```

### Topics Section

Maps NATS subject patterns to ordered lists of pool names.

| Field | Type | Description |
|-------|------|-------------|
| `topics` | map[string]string[] | Topic pattern → ordered list of eligible pool names |

- Topics use exact match or NATS wildcard patterns
- The list ordering defines **fallback priority** — first pool with capacity wins
- Worker pool name must match the pool a worker heartbeats as

### Pools Section

Defines pool profiles and capability requirements.

| Field | Type | Description |
|-------|------|-------------|
| `pools` | map[string]PoolDef | Pool name → pool definition |
| `pools.*.requires` | string[] | Capabilities a worker must declare to join this pool |

### Routing Algorithm

1. Scheduler receives a job with topic (e.g., `job.code-review`)
2. Looks up topic in `topics` map → gets pool list `["code-review", "general"]`
3. For each pool in order:
   a. Checks if pool has workers with required capabilities (`requires` list)
   b. Checks if pool has capacity (workers available)
   c. First match wins — job dispatched to that pool
4. If no pool matches → job stays in pending state for reconciler

### Schema

Validated against `core/infra/config/schema/pools.schema.json`.

---

## timeouts.yaml — Timeout Configuration

Controls per-topic timeouts, per-workflow timeouts, and reconciler settings.

### Example

```yaml
reconciler:
  dispatch_timeout_seconds: 300    # 5 min for pending→dispatched
  running_timeout_seconds: 900     # 15 min default for running jobs
  scan_interval_seconds: 30        # check every 30s

topics:
  "job.compliance.review":
    timeout_seconds: 600           # 10 min timeout
    max_retries: 5
  "job.quick-check":
    timeout_seconds: 30
    max_retries: 1

workflows:
  "long-pipeline":
    child_timeout_seconds: 1800    # 30 min per step
    total_timeout_seconds: 7200    # 2 hr total
    max_retries: 2
```

### Reconciler Section

Controls how the scheduler detects and handles stalled jobs.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `dispatch_timeout_seconds` | int | `300` (5m) | Max time for pending → dispatched transition |
| `running_timeout_seconds` | int | `900` (15m) | Max time for dispatched → completed transition. Per-topic overrides available via `topics.<topic>.running_timeout_seconds`. |
| `scan_interval_seconds` | int | `30` | How often reconciler scans for stale jobs |

### Topics Section

Per-topic timeout overrides.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `topics.*.timeout_seconds` | int | (reconciler default) | Job execution timeout for this topic |
| `topics.*.max_retries` | int | `0` | Max retries for this topic |

### Workflows Section

Per-workflow timeout overrides.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `workflows.*.child_timeout_seconds` | int | (reconciler default) | Timeout per child step |
| `workflows.*.total_timeout_seconds` | int | (none) | Total workflow run timeout |
| `workflows.*.max_retries` | int | `0` | Max retries per step |

### Schema

Validated against `core/infra/config/schema/timeouts.schema.json`.

---

## safety.yaml — Safety Policy

Defines safety kernel input rules, output rules, and MCP (Model Context Protocol) configuration.

For full details on the safety kernel, see [safety-kernel.md](/concepts/safety-kernel). For output policy, see [output-policy.md](/concepts/output-policy).

### Example

```yaml
version: "1"
rules:
  - id: fraud-review
    match:
      capabilities: ["bank.transfer"]
      risk_tags: ["financial", "high_value"]
    decision: require_approval
    reason: "Financial transactions require human approval"

  - id: auto-allow-validators
    match:
      capabilities: ["validate.*"]
    decision: allow
    reason: "Read-only validation is always safe"

output_policy:
  enabled: false
  fail_mode: open          # open = allow on scanner error, closed = deny

output_rules:
  - id: secret_leak
    match:
      detectors: ["secret_leak"]
    decision: quarantine
    reason: "Potential secret in output"

  - id: pii
    match:
      detectors: ["pii"]
    decision: redact
    reason: "PII detected — redacting"

tenants:
  acme-corp:
    mcp:
      allow: ["github", "slack"]
      deny: ["*"]
  default:
    mcp:
      allow: ["*"]
      deny: []
```

### Rules Section (Input Policy)

| Field | Type | Description |
|-------|------|-------------|
| `rules[].id` | string | Unique rule identifier |
| `rules[].match.capabilities` | string[] | Capability patterns to match (supports `*` wildcard) |
| `rules[].match.risk_tags` | string[] | Risk tag patterns to match |
| `rules[].match.metadata` | map | Key-value metadata conditions |
| `rules[].decision` | string | `allow`, `deny`, `require_approval`, `throttle` |
| `rules[].reason` | string | Human-readable reason |
| `rules[].throttle_duration` | duration | Required if decision is `throttle` |

Rules are evaluated top-to-bottom; first match wins.

### Velocity Rule Fragments

Velocity rules are regular `rules[]` entries stored as dedicated policy bundle
fragments at `cfg:system:policy -> bundles -> velocity/{id}`. They do **not**
change the safety-kernel evaluator; they only add managed rule fragments that
use the existing `velocity` block on input rules.

Example fragment:

```yaml
version: "1"
rules:
  - id: login-burst
    match:
      topics: ["job.auth.login"]
      tenants: ["default"]
      risk_tags: ["auth"]
    velocity:
      max_requests: 3
      window_seconds: 60
      key: tenant
    decision: require_approval
    reason: "Repeated login attempts require review"
```

| Field | Type | Description |
|-------|------|-------------|
| `rules[].velocity.max_requests` | int | Requests allowed inside the sliding window before the rule fires |
| `rules[].velocity.window_seconds` | int | Sliding-window size in seconds (`1` to `86400`) |
| `rules[].velocity.key` | string | Bucket key expression (`tenant`, `topic`, `actor_id`, `actor_type`, `capability`, `pack_id`, or `labels.<key>`; compound keys use `:`) |

### Default Decision

The `default_decision` field at the top of `safety.yaml` controls what happens when no input rule matches a job. The production default is `deny` (fail-closed), meaning unmatched jobs are rejected. To whitelist specific topics, add `decision: allow` rules.

```yaml
# Fail-closed: unmatched jobs are denied
default_decision: deny
```

### Output Policy Section

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `output_policy.enabled` | bool | `false` | Enable output scanning |
| `output_policy.fail_mode` | string | `"closed"` | `open` = allow on scanner error, `closed` = quarantine on scanner error (recommended for production) |

### Output Rules Section

| Field | Type | Description |
|-------|------|-------------|
| `output_rules[].id` | string | Unique rule identifier |
| `output_rules[].match.topics` | string[] | Topic patterns |
| `output_rules[].match.capabilities` | string[] | Capability patterns |
| `output_rules[].match.risk_tags` | string[] | Risk tag patterns |
| `output_rules[].match.content_patterns` | string[] | Regex patterns for content matching |
| `output_rules[].match.detectors` | string[] | Scanner detector names (`secret_leak`, `pii`, `injection`) |
| `output_rules[].match.max_output_bytes` | int | Maximum output size in bytes |
| `output_rules[].decision` | string | `allow`, `deny`, `quarantine`, `redact` |
| `output_rules[].reason` | string | Human-readable reason |

### Tenants Section

Per-tenant MCP tool access control.

| Field | Type | Description |
|-------|------|-------------|
| `tenants.*.mcp.allow` | string[] | Allowed MCP tool/resource patterns |
| `tenants.*.mcp.deny` | string[] | Denied MCP tool/resource patterns |

### Schema

Validated against `core/infra/config/schema/safety_policy.schema.json`.

---

## output_scanners.yaml — Output Scanner Patterns

Defines regex-based content scanners for output policy enforcement. Loaded by the safety kernel when `OUTPUT_POLICY_ENABLED=true`.

### Example

```yaml
scanners:
  secret:
    patterns:
      - name: aws_access_key
        regex: "AKIA[0-9A-Z]{16}"
        severity: critical
        confidence: high
      - name: github_token
        regex: "gh[ps]_[A-Za-z0-9_]{36,}"
        severity: critical
        confidence: high
      - name: generic_api_key
        regex: "(?i)(api[_-]?key|apikey|secret[_-]?key)\\s*[:=]\\s*['\"]?[A-Za-z0-9/+=]{20,}"
        severity: high
        confidence: medium
  pii:
    patterns:
      - name: email_address
        regex: "[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}"
        severity: medium
        confidence: high
      - name: ssn
        regex: "\\b\\d{3}-\\d{2}-\\d{4}\\b"
        severity: critical
        confidence: high
  injection:
    patterns:
      - name: prompt_injection
        regex: "(?i)(ignore previous|disregard|forget all|system prompt)"
        severity: high
        confidence: medium
```

### Scanner Definition

| Field | Type | Description |
|-------|------|-------------|
| `scanners` | map[string]Scanner | Scanner name → scanner definition |
| `scanners.*.patterns` | Pattern[] | List of regex patterns |
| `scanners.*.patterns[].name` | string | Pattern identifier |
| `scanners.*.patterns[].regex` | string | Go-compatible regex pattern |
| `scanners.*.patterns[].severity` | string | `critical`, `high`, `medium`, `low` |
| `scanners.*.patterns[].confidence` | string | `high`, `medium`, `low` |
| `scanners.*.patterns[].context_required` | bool | Whether surrounding context needed for match |

### Env Var

| Variable | Default | Description |
|----------|---------|-------------|
| `OUTPUT_SCANNERS_PATH` | `config/output_scanners.yaml` | Path to scanner definitions file |

---

## Environment Variables Master Table

### Global / Shared

| Variable | Default | Required | Description |
|----------|---------|----------|-------------|
| `CORDUM_ENV` | — | No | Set to `production` or `prod` for strict security defaults |
| `CORDUM_PRODUCTION` | `false` | No | Alternative: set to `true` for production mode |
| `CORDUM_TLS_MIN_VERSION` | `1.2` (dev), `1.3` (prod) | No | Minimum TLS version: `1.2` or `1.3` |
| `CORDUM_LOG_FORMAT` | `text` | No | Log format: `json` or `text` |
| `CORDUM_GRPC_REFLECTION` | — | No | Set to `1` to enable gRPC reflection (dev only) |
| `NATS_URL` | `nats://localhost:4222` | Yes | NATS server URL |
| `REDIS_URL` | `redis://localhost:6379` | Yes | Redis URL (Compose: `redis://:${REDIS_PASSWORD}@redis:6379` — password required) |
| `NATS_USE_JETSTREAM` | `0` | No | Enable NATS JetStream: `0` or `1` |
| `POOL_CONFIG_PATH` | `config/pools.yaml` | No | Path to pools config |
| `TIMEOUT_CONFIG_PATH` | `config/timeouts.yaml` | No | Path to timeouts config. **Production mode**: if explicitly set and the file cannot be loaded or parsed, the scheduler exits with an error. In dev mode, falls back to built-in defaults with a warning. |
| `SAFETY_POLICY_PATH` | `config/safety.yaml` | No | Path to safety policy |
| `SAFETY_KERNEL_ADDR` | `localhost:50051` | No | Safety kernel gRPC address |
| `CONTEXT_ENGINE_ADDR` | `:50070` | No | Context engine gRPC address |
| `OUTPUT_POLICY_ENABLED` | `false` | No | Enable output policy scanning: `true`, `1` |
| `CORDUM_TENANT_ID` | — | No | Default tenant ID for SDK/MCP clients |
| `CORDUM_INSTANCE_ID` | `os.Hostname()` | No | Override pod name used in Prometheus `pod` label. Defaults to hostname; falls back to `"unknown"` |

> **Prometheus pod label**: All Cordum metrics include a `pod` const label (`os.Hostname()` or `CORDUM_INSTANCE_ID`) so Prometheus can distinguish replicas in HA deployments. Use `sum by (pod) (cordum_scheduler_jobs_received_total)` for per-replica breakdown.

### Licensing

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_LICENSE_FILE` | — | Path to license JSON file. If not set, checks `~/.cordum/license.json` and `/etc/cordum/license.json` |
| `CORDUM_LICENSE_TOKEN` | — | License token (base64-encoded or raw JSON). Alternative to file-based licensing |
| `CORDUM_LICENSE_PUBLIC_KEY` | embedded | Base64-encoded Ed25519 public key for signature verification |
| `CORDUM_LICENSE_PUBLIC_KEY_PATH` | — | Path to public key file (alternative to inline) |

No license = Community tier (3 workers, 3 concurrent jobs, 500 RPS, 7-day audit retention). Invalid or expired licenses degrade to Community — Cordum never crashes or blocks startup due to licensing.

### Telemetry

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_TELEMETRY_MODE` | `anonymous` | Telemetry mode: `off` (no collection), `local_only` (collect but don't report), `anonymous` (collect and report aggregate stats) |
| `CORDUM_TELEMETRY_ENDPOINT` | `https://telemetry.cordum.io/v1/report` | HTTPS endpoint for anonymous telemetry reports |

Telemetry is independent from licensing. It never collects PII, prompts, secrets, or job content. Operators can opt out at any time via `CORDUM_TELEMETRY_MODE=off` or `POST /api/v1/telemetry/consent`.

### NATS TLS

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_TLS_CA` | — | CA certificate path for NATS TLS |
| `NATS_TLS_CERT` | — | Client certificate path |
| `NATS_TLS_KEY` | — | Client private key path |
| `NATS_TLS_INSECURE` | — | Skip TLS verification |
| `NATS_TLS_SERVER_NAME` | — | TLS server name override |

### NATS JetStream

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_JS_ACK_WAIT` | `10m` | JetStream ack wait duration |
| `NATS_JS_MAX_AGE` | `7d` | JetStream message max age |
| `NATS_JS_REPLICAS` | `1` | JetStream stream replication factor |

### Redis TLS

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_TLS_CA` | — | CA certificate path for Redis TLS |
| `REDIS_TLS_CERT` | — | Client certificate path |
| `REDIS_TLS_KEY` | — | Client private key path |
| `REDIS_TLS_INSECURE` | — | Skip TLS verification |
| `REDIS_TLS_SERVER_NAME` | — | TLS server name override |
| `REDIS_CLUSTER_ADDRESSES` | — | Comma-separated cluster seeds (host:port) |

### Redis Data TTL

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_DATA_TTL_SECONDS` | — | Data TTL in seconds (takes precedence) |
| `REDIS_DATA_TTL` | — | Data TTL as Go duration (e.g., `24h`) |

### Redis Connection Pool

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_POOL_SIZE` | `20` | Max connections per Redis node. Each service replica opens up to this many connections. |
| `REDIS_MIN_IDLE_CONNS` | `5` | Minimum idle connections kept warm per Redis node. Reduces cold-start latency for bursty traffic. |

**Sizing guidance**: With N service replicas × P pool size × M Redis nodes, total connections ≈ N×P×M. For example, 3 scheduler replicas × 50 pool × 1 Redis = 150 connections. Redis default `maxclients` is 10000, so pool sizes up to 100 are safe for typical deployments. The scheduler benefits from higher pool sizes (recommend 50) due to concurrent job dispatch; other services can use the default 20.

Invalid values (non-numeric, zero, negative) are silently replaced with defaults and a warning is logged.

### Gateway

| Variable | Default | Description |
|----------|---------|-------------|
| `GATEWAY_GRPC_ADDR` | `:50051` | gRPC listen address |
| `GATEWAY_HTTP_ADDR` | `:8080` | HTTP listen address |
| `GATEWAY_METRICS_ADDR` | `:9090` | Metrics listen address |
| `GATEWAY_METRICS_PUBLIC` | — | Set to `1` for non-loopback metrics in production |
| `GATEWAY_HTTP_TLS_CERT` | — | HTTP TLS certificate path |
| `GATEWAY_HTTP_TLS_KEY` | — | HTTP TLS private key path |
| `GRPC_TLS_CERT` | — | gRPC TLS certificate path |
| `GRPC_TLS_KEY` | — | gRPC TLS private key path |
| `GATEWAY_MAX_JOB_PAYLOAD_BYTES` | `2097152` (2 MB) | Max job submission payload size in bytes |
| `GATEWAY_MAX_BODY_BYTES` | `1048576` (1 MB) | Max HTTP request body size in bytes |
| `GATEWAY_MAX_JSON_BODY_BYTES` | — | Max JSON request body size |
| `TENANT_ID` | — | Single-tenant default ID |
| `ARTIFACT_MAX_BYTES` | — | Max artifact upload/download size |
| `WORKFLOW_FOREACH_MAX_ITEMS` | — | Max items in workflow for-each expansion |
| `POLICY_CHECK_FAIL_MODE` | `closed` | Behavior when Safety Kernel is unreachable during policy evaluation (both gateway submit-time and scheduler dispatch-time). `closed` (default): reject the job. `open`: allow with warning log. |

### Gateway — API Keys

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_API_KEY` | — | Single API key |
| `API_KEY` | — | Fallback if `CORDUM_API_KEY` not set |
| `CORDUM_API_KEYS` | — | Multiple keys: comma-separated or JSON array |
| `CORDUM_API_KEYS_PATH` | — | Path to keys file (reloads on change) |
| `CORDUM_ALLOW_INSECURE_NO_AUTH` | — | Set to `1` for no-auth mode (dev only) |
| `CORDUM_ALLOW_HEADER_PRINCIPAL` | — | Set to `true` for header-based principal (disabled in production) |

### Gateway — Rate Limiting

| Variable | Default | Description |
|----------|---------|-------------|
| `API_RATE_LIMIT_RPS` | `2000` | Per-tenant rate limit (requests/sec) |
| `API_RATE_LIMIT_BURST` | `4000` | Per-tenant burst size |
| `API_PUBLIC_RATE_LIMIT_RPS` | `20` | Public (unauthenticated) rate limit |
| `API_PUBLIC_RATE_LIMIT_BURST` | `40` | Public burst size |
| `REDIS_RATE_LIMIT` | `true` | Enable Redis-backed distributed rate limiting. When `true`, rate limits are enforced globally across all gateway replicas via Redis sliding-window counters (key format: `cordum:rl:{key}:{unix_second}`). When `false` or Redis unavailable, falls back to per-process in-memory token buckets (effective limit = N × configured limit with N replicas). |

> **Horizontal scaling note**: With multiple gateway replicas, Redis-backed rate
> limiting (`REDIS_RATE_LIMIT=true`) is strongly recommended. Without it, each
> replica maintains its own in-memory token bucket, so the effective rate limit
> is multiplied by the number of replicas.

### Gateway — CORS

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_ALLOWED_ORIGINS` | — | Allowed CORS origins |
| `CORDUM_CORS_ALLOW_ORIGINS` | — | Alias for allowed origins |
| `CORS_ALLOW_ORIGINS` | — | Alias for allowed origins |

### Gateway — JWT Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_JWT_HMAC_SECRET` | — | HMAC secret for JWT signing |
| `CORDUM_JWT_PUBLIC_KEY` | — | RSA/EC public key (PEM) for JWT verification |
| `CORDUM_JWT_PUBLIC_KEY_PATH` | — | Path to public key file |
| `CORDUM_JWT_ISSUER` | — | Expected JWT issuer |
| `CORDUM_JWT_AUDIENCE` | — | Expected JWT audience |
| `CORDUM_JWT_DEFAULT_ROLE` | — | Default role for JWT tokens without role claim |
| `CORDUM_JWT_CLOCK_SKEW` | — | Allowed clock skew (e.g., `30s`) |
| `CORDUM_JWT_REQUIRED` | — | Set to `true` to require JWT for all requests |

### Gateway — OIDC Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_OIDC_ISSUER` | — | OIDC issuer URL |
| `CORDUM_OIDC_AUDIENCE` | — | Expected OIDC audience |
| `CORDUM_OIDC_CLAIM_TENANT` | — | JWT claim for tenant ID |
| `CORDUM_OIDC_CLAIM_ROLE` | — | JWT claim for user role |
| `CORDUM_OIDC_GROUPS_CLAIM` | `groups` | JWT/OIDC claim containing IdP group names for group-to-role mapping |
| `CORDUM_OIDC_GROUP_ROLE_MAPPING` | `{}` | JSON object mapping IdP group names to Cordum roles (`admin`, `operator`, `viewer`) |
| `CORDUM_OIDC_ALLOWED_ALGS` | — | Comma-separated allowed algorithms |
| `CORDUM_OIDC_JWKS_REFRESH_INTERVAL` | — | JWKS refresh interval (e.g., `1h`) |
| `CORDUM_OIDC_ISSUER_ALLOWLIST` | — | Comma-separated allowed issuers |
| `CORDUM_OIDC_ALLOW_PRIVATE` | — | Allow private/loopback issuer URLs |
| `CORDUM_OIDC_ALLOW_HTTP` | — | Allow HTTP (non-TLS) issuer URLs |

**HA note — JWKS coordination**: When running multiple gateway replicas, the OIDC provider
automatically coordinates JWKS fetches via Redis. The first replica to refresh fetches from
the IdP and writes the JWKS to `cordum:auth:jwks:<issuerHash>` (TTL 1h). Other replicas read
from this cache, reducing IdP load from N requests to 1 per refresh cycle. Each replica also
applies random jitter (0–30s initial, 0–15s per tick) to prevent thundering-herd requests.
If Redis is unavailable, replicas fall back to direct IdP fetches (same behavior as single-replica).

### Gateway — OIDC Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_OIDC_ENABLED` | `false` | Enable OIDC JWT validation for bearer tokens |
| `CORDUM_OIDC_ISSUER` | — | OpenID Connect issuer URL used for discovery |
| `CORDUM_OIDC_AUDIENCE` | — | Expected audience for bearer-token validation; browser callback validation uses `CORDUM_OIDC_CLIENT_ID` |
| `CORDUM_OIDC_CLAIM_TENANT` | `org_id` | Claim name used to resolve the Cordum tenant |
| `CORDUM_OIDC_CLAIM_ROLE` | `cordum_role` | Claim name used to resolve the Cordum role |
| `CORDUM_OIDC_GROUPS_CLAIM` | `groups` | Claim containing Okta/OIDC group names; when non-empty, mapped groups win over `cordum_role` |
| `CORDUM_OIDC_GROUP_ROLE_MAPPING` | `{}` | JSON object mapping IdP group names to Cordum roles, for example `{"cordum-admins":"admin"}` |
| `CORDUM_OIDC_CLIENT_ID` | — | Enable browser OIDC SSO with this client ID |
| `CORDUM_OIDC_CLIENT_SECRET` | — | Client secret used during the authorization-code exchange |
| `CORDUM_OIDC_REDIRECT_URI` | — | Absolute callback URL registered with the IdP (typically `https://<gateway>/api/v1/auth/sso/oidc/callback`) |
| `CORDUM_OIDC_SCOPES` | `openid,profile,email` | Comma-separated scopes requested during login |
| `CORDUM_OIDC_STATE_TTL` | `10m` | TTL for OIDC state / nonce tracking entries stored in Redis |
| `CORDUM_OIDC_ALLOWED_ALGS` | `RS256,RS384,RS512,ES256,ES384,ES512` | Restrict accepted signing algorithms |
| `CORDUM_OIDC_JWKS_REFRESH_INTERVAL` | `6h` | Background refresh interval for the issuer JWKS cache |
| `OIDC_JWKS_REFRESH_COOLDOWN` | `1m` | Minimum time between on-demand unknown-`kid` refresh attempts |
| `CORDUM_OIDC_ISSUER_ALLOWLIST` | — | Optional comma-separated allowlist of issuer hosts/domains |
| `CORDUM_OIDC_ALLOW_PRIVATE` | `false` in production | Allow private-network issuer hosts in production |
| `CORDUM_OIDC_ALLOW_HTTP` | `false` in production | Allow plain HTTP issuer / redirect URLs in production |
| `CORDUM_AUTH_REDIRECT_URL` | `<ui-origin>/login` | Post-auth redirect target used after OIDC or SAML completes |
| `CORDUM_AUTH_SESSION_TTL` | `24h` | Browser/session token TTL for password, OIDC, and SAML sign-ins |

**Helm / Compose note**: The Helm chart exposes these under `auth.oidc.*`.

### Gateway — SAML Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_SAML_ENABLED` | `false` | Enable the SAML service-provider endpoints on the gateway |
| `CORDUM_SAML_IDP_METADATA_URL` | — | Remote IdP metadata URL the gateway should fetch on startup |
| `CORDUM_SAML_IDP_METADATA` | — | Inline IdP metadata XML (use instead of the URL for air-gapped installs) |
| `CORDUM_SAML_BASE_URL` | `http://localhost:8081` | External gateway base URL used to publish metadata, ACS, and login endpoints |
| `CORDUM_SAML_CERT_PATH` | — | PEM certificate path for the service-provider signing / TLS cert |
| `CORDUM_SAML_KEY_PATH` | — | PEM private-key path paired with `CORDUM_SAML_CERT_PATH` |
| `CORDUM_SAML_ENTITY_ID` | metadata URL | Explicit SAML entity ID override for the service provider |
| `CORDUM_SAML_BINDING` | `redirect` | SP-initiated binding used for the login request (`redirect` or `post`) |
| `CORDUM_SAML_RESPONSE_BINDING` | `post` | Expected ACS response binding (`post` or `redirect`) |
| `CORDUM_SAML_ALLOW_IDP_INITIATED` | `false` | Allow IdP-initiated SSO responses with no stored RelayState |
| `CORDUM_SAML_STATE_TTL` | `10m` | TTL for SAML RelayState/request tracking entries stored in Redis |
| `CORDUM_AUTH_REDIRECT_URL` | `<ui-origin>/login` | Post-auth redirect target used after the ACS callback completes |
| `CORDUM_AUTH_SESSION_TTL` | `24h` | Browser/session token TTL for password, OIDC, and SAML sign-ins |

**Helm / Compose note**: The Helm chart exposes these under `auth.saml.*`, and `docker-compose.yml` includes the same gateway variables as commented examples for local development.

### Gateway — SCIM Provisioning

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_SCIM_BEARER_TOKEN` | — | Shared bearer token required by all SCIM 2.0 provisioning endpoints under `/api/v1/scim/v2/*` |

SCIM provisioning is additionally gated by the `SCIM` license entitlement. When the entitlement is disabled, discovery, user, and group routes return `403 tier_limit_exceeded` even if a bearer token is configured.

If `CORDUM_SCIM_BEARER_TOKEN` is unset, Cordum can generate and store a Redis-backed SCIM token through the admin settings API (`POST /api/v1/scim/settings/token`) and the dashboard page at `/settings/scim`. If the env var is set, that value is used unless an operator later creates a Redis-managed override.

SCIM response locations and the dashboard-published endpoint URL are derived from the external gateway base URL (`CORDUM_API_BASE_URL`, `CORDUM_API_BASE`, or `CORDUM_SAML_BASE_URL`).

**Helm note**: The Helm chart exposes these under `auth.scim.*`, including `auth.scim.existingSecret` for referencing an existing Kubernetes secret instead of placing the bearer token inline.

### Gateway — Advanced RBAC

Advanced RBAC provides role hierarchy with permission-based access control, gated by the `RBAC` license entitlement (Enterprise plan). When the entitlement is disabled, the gateway falls back to basic role string matching (admin/operator/viewer).

RBAC roles are stored in Redis (key prefix `rbac:role:`). Default roles (admin, operator, viewer) are bootstrapped on startup if not present.

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_RBAC_ROLE_DEFS` | — | JSON array of custom role definitions to seed on startup (optional) |

**Dashboard**: The roles management tab at `/settings/users` shows built-in and custom roles. Custom role creation/editing requires the RBAC entitlement.

**API**: Role management endpoints at `/api/v1/auth/roles` (see [API Reference](/api-reference/full-reference)).

### Gateway — User Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_USER_AUTH_ENABLED` | `false` | Enable user/password auth (Redis-backed) |
| `CORDUM_ADMIN_USERNAME` | `admin` | Default admin username |
| `CORDUM_ADMIN_PASSWORD` | — | Admin password (creates user on first startup) |
| `CORDUM_ADMIN_EMAIL` | — | Optional admin email |

### Gateway — Pack Marketplace

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_PACK_CATALOG_URL` | (built-in) | Official catalog URL |
| `CORDUM_PACK_CATALOG_ID` | (auto) | Catalog ID |
| `CORDUM_PACK_CATALOG_TITLE` | (auto) | Catalog display title |
| `CORDUM_PACK_CATALOG_DEFAULT_DISABLED` | — | Set to `1` to disable default catalog |
| `CORDUM_MARKETPLACE_ALLOW_HTTP` | — | Set to `1` for HTTP marketplace URLs |
| `CORDUM_MARKETPLACE_HTTP_TIMEOUT` | — | Fetch timeout (e.g., `15s`) |

### Scheduler

| Variable | Default | Description |
|----------|---------|-------------|
| `SCHEDULER_METRICS_ADDR` | `:9090` | Metrics listen address |
| `SCHEDULER_METRICS_PUBLIC` | — | Set to `1` for non-loopback metrics in production |
| `SCHEDULER_CONFIG_RELOAD_INTERVAL` | `30s` | Config overlay reload interval |
| `JOB_META_TTL` | — | Job metadata TTL (Go duration, e.g., `48h`) |
| `JOB_META_TTL_SECONDS` | — | Job metadata TTL in seconds (takes precedence) |
| `WORKER_SNAPSHOT_INTERVAL` | — | Worker state snapshot interval |
| `OUTPUT_POLICY_ENABLED` | `false` | Enable output policy: `true`, `1` |
| `POLICY_CHECK_FAIL_MODE` | `closed` | Behavior when safety kernel is unreachable during pre-dispatch input policy checks. `closed` (default): requeue with backoff. `open`: allow through with warning log and metric. See [safety-kernel.md](/concepts/safety-kernel) for risk implications. |

### Gateway + Scheduler — Boundary Hardening

These flags control the canonical topic registry, schema enforcement, worker attestation,
and readiness gating described in ADR 009.

| Variable | Default | Type | Service | Description |
|----------|---------|------|---------|-------------|
| `SCHEMA_ENFORCEMENT` | `warn` | string (`off`, `warn`, `enforce`) | gateway + scheduler | Controls how registered topic schemas are enforced. The gateway uses it at submit time for `POST /api/v1/jobs`; the scheduler uses the same mode before dispatch. `warn` logs violations and continues, `enforce` rejects/failed-jobs on schema mismatch, `off` skips schema validation. |
| `WORKER_ATTESTATION` | `off` | string (`off`, `warn`, `enforce`) | scheduler | Controls whether scheduler heartbeat processing requires a valid worker credential token. `warn` accepts the heartbeat but logs attestation failures; `enforce` rejects unattested heartbeats; `off` skips attestation checks. |
| `WORKER_READINESS_REQUIRED` | `false` | bool | scheduler | When `true`, scheduling only considers workers that have recently advertised matching `ready_topics` in their handshake. When `false`, workers without readiness data remain eligible for backward compatibility. |
| `WORKER_READINESS_TTL` | `60s` | duration | scheduler | Freshness window for handshake readiness state. After this TTL expires, the worker heartbeat may still be present, but readiness gating treats the worker as not ready until it handshakes again. Invalid or non-positive values fall back to `60s` with a warning log. |

### Workflow Engine

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKFLOW_ENGINE_HTTP_ADDR` | — | HTTP listen address |
| `WORKFLOW_ENGINE_SCAN_INTERVAL` | — | Run scan interval |
| `WORKFLOW_ENGINE_RUN_SCAN_LIMIT` | — | Max runs to scan per tick |
| `WORKFLOW_FOREACH_MAX_ITEMS` | — | Max items in for-each expansion |

### Safety Kernel

| Variable | Default | Description |
|----------|---------|-------------|
| `SAFETY_KERNEL_ADDR` | `localhost:50051` | gRPC listen address |
| `SAFETY_POLICY_PATH` | `config/safety.yaml` | Path to safety policy file |
| `SAFETY_POLICY_URL` | — | Load policy from URL instead of file |
| `SAFETY_POLICY_URL_ALLOWLIST` | — | Comma-separated allowed hostnames for URL loading |
| `SAFETY_POLICY_URL_ALLOW_PRIVATE` | — | Allow private/loopback policy URLs (not recommended) |
| `SAFETY_POLICY_MAX_BYTES` | — | Max policy file size |
| `SAFETY_KERNEL_TLS_CERT` | — | TLS server certificate |
| `SAFETY_KERNEL_TLS_KEY` | — | TLS server private key |
| `SAFETY_KERNEL_TLS_CA` | — | Client TLS CA (for mTLS) |
| `SAFETY_KERNEL_TLS_REQUIRED` | — | Require TLS for kernel connections |
| `SAFETY_KERNEL_INSECURE` | — | Skip TLS verification (dev only) |
| `SAFETY_DECISION_CACHE_TTL` | — | Decision cache TTL (e.g., `5s`, `250ms`) |
| `OUTPUT_SCANNERS_PATH` | `config/output_scanners.yaml` | Path to scanner patterns file |

### Safety Kernel — Policy Signature Verification

| Variable | Default | Description |
|----------|---------|-------------|
| `SAFETY_POLICY_PUBLIC_KEY` | — | Public key for signature verification (PEM) |
| `SAFETY_POLICY_SIGNATURE` | — | Inline signature |
| `SAFETY_POLICY_SIGNATURE_PATH` | — | Path to signature file |
| `SAFETY_POLICY_SIGNATURE_REQUIRED` | — | Require valid signature |

### Safety Kernel — Policy Reload / Overlays

| Variable | Default | Description |
|----------|---------|-------------|
| `SAFETY_POLICY_RELOAD_INTERVAL` | — | Policy file reload interval |
| `SAFETY_POLICY_CONFIG_SCOPE` | — | Config service scope for overlay |
| `SAFETY_POLICY_CONFIG_ID` | — | Config service scope ID |
| `SAFETY_POLICY_CONFIG_KEY` | — | Config service data key |
| `SAFETY_POLICY_CONFIG_DISABLE` | — | Disable config service overlay |

### Context Engine

| Variable | Default | Description |
|----------|---------|-------------|
| `CONTEXT_ENGINE_ADDR` | `:50070` | gRPC listen address |
| `CONTEXT_ENGINE_TLS_CERT` | — | TLS server certificate |
| `CONTEXT_ENGINE_TLS_KEY` | — | TLS server private key |
| `CONTEXT_ENGINE_TLS_CA` | — | Client TLS CA (for connections to engine) |
| `CONTEXT_ENGINE_TLS_REQUIRED` | — | Require TLS connections |
| `CONTEXT_ENGINE_INSECURE` | — | Skip TLS verification |
| `CONTEXT_ENGINE_MAX_ENTRY_BYTES` | — | Max size per context entry |
| `CONTEXT_ENGINE_MAX_CHUNK_SCAN` | — | Max chunks to scan per retrieval |

### MCP Server

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_API_KEY` | — | API key for gateway-backed MCP handlers |
| `CORDUM_TENANT_ID` | — | Tenant ID for MCP bridge/resource operations |
| `MCP_TRANSPORT` | `stdio` | Transport mode: `stdio` (default) or `http` |
| `MCP_HTTP_ADDR` | `:8090` | HTTP listen address (only used when `MCP_TRANSPORT=http`) |

**HA note — HTTP transport**: Set `MCP_TRANSPORT=http` to enable HTTP mode, which exposes
`/sse` (SSE stream) and `/message` (POST JSON-RPC) endpoints. This allows running multiple
MCP server replicas behind a load balancer. The default `stdio` mode supports only a single
instance and is intended for local CLI integrations.

### Audit Export

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_AUDIT_EXPORT_TYPE` | — | Export type: `webhook`, `syslog`, `datadog`, `cloudwatch` |
| `CORDUM_AUDIT_EXPORT_WEBHOOK_URL` | — | Webhook endpoint URL |
| `CORDUM_AUDIT_EXPORT_WEBHOOK_SECRET` | — | Webhook HMAC signing secret |
| `CORDUM_AUDIT_EXPORT_SYSLOG_ADDR` | — | Syslog server address |
| `CORDUM_AUDIT_EXPORT_DD_API_KEY` | — | Datadog API key |
| `CORDUM_AUDIT_EXPORT_DD_SITE` | — | Datadog site (e.g., `datadoghq.com`) |
| `CORDUM_AUDIT_EXPORT_DD_TAGS` | — | Datadog tags (comma-separated) |
| `CORDUM_AUDIT_EXPORT_CW_LOG_GROUP` | — | CloudWatch log group |
| `CORDUM_AUDIT_EXPORT_CW_LOG_STREAM` | — | CloudWatch log stream |
| `AWS_REGION` | — | AWS region for CloudWatch |
| `AWS_ACCESS_KEY_ID` | — | AWS credentials |
| `AWS_SECRET_ACCESS_KEY` | — | AWS credentials |
| `AUDIT_TRANSPORT` | `buffer` | Audit transport: `buffer` (in-memory) or `nats` (NATS-backed, recommended for multi-replica) |
| `CORDUM_AUDIT_BUFFER_SIZE` | `1000` | In-memory audit buffer size (events) |
| `CORDUM_AUDIT_EXPORT_MAX_RETRIES` | `3` | Max retries before dropping a batch |

#### NATS-Backed Audit Pipeline

When `AUDIT_TRANSPORT=nats`, audit events are published to the NATS subject `sys.audit.export` instead of being buffered in per-process memory. A consumer subscribes with queue group `audit-exporters` so exactly one replica handles each event. This provides:

- **Crash resilience** — events survive process restarts when JetStream is enabled (at-least-once delivery)
- **Stateless replicas** — audit events are no longer tied to the process that generated them
- **Automatic fallback** — if NATS publish fails, events fall back to the local in-memory buffer

The consumer calls the configured SIEM exporter (`CORDUM_AUDIT_EXPORT_TYPE`) for each event. Failed exports trigger NATS redelivery (nak). Malformed messages are acked to prevent poison pill loops.

> **Note**: For production HA deployments, enable JetStream on the `sys` stream to get durable audit delivery. Without JetStream, audit events use core NATS (at-most-once).

### DLQ

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_DLQ_ENTRY_TTL_DAYS` | — | DLQ entry TTL in days |

### Worker SDK

| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://localhost:4222` | NATS URL for worker connections |
| `WORKER_ID` | — | Explicit worker ID (auto-generated if not set) |

### CLI TLS

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_TLS_CA` | — | CA certificate path for CLI TLS verification |
| `CORDUM_TLS_INSECURE` | — | Set to `1` to skip TLS verification (dev/debug only) |

### Dashboard

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_API_UPSTREAM_SCHEME` | `http` | Set to `https` when gateway serves TLS |
| `CORDUM_DASHBOARD_EMBED_API_KEY` | — | Embed API key in dashboard (dev only) |

### Docker Compose Helpers

| Variable | Default | Description |
|----------|---------|-------------|
| `COMPOSE_HTTP_TIMEOUT` | — | Docker Compose HTTP timeout |
| `DOCKER_CLIENT_TIMEOUT` | — | Docker client timeout |

---

## Cross-References

- [configuration.md](/operations/configuration-guide) — Quick-start config overview
- guides/tls-setup.md — TLS setup and troubleshooting
- [safety-kernel.md](/concepts/safety-kernel) — Safety kernel architecture and evaluation
- [output-policy.md](/concepts/output-policy) — Output scanning and quarantine system
- [DOCKER.md](/getting-started/docker) — Docker Compose deployment and NATS JetStream durability
- [mcp-server.md](/api-reference/mcp-server) — MCP server configuration
- [api-reference.md](/api-reference/full-reference) — REST API documentation
- [horizontal-scaling.md](/operations/horizontal-scaling) — Multi-replica deployment, Redis lock keys, NATS subject matrix
