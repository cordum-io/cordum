# CLI Reference — cordumctl

Complete command reference for `cordumctl`, the Cordum control-plane CLI.

For REST API endpoints, see [api-reference.md](api-reference.md).
For pack format details, see [pack.md](pack.md).
For configuration, see [configuration-reference.md](configuration-reference.md).
For the Cordum Edge Claude hook binary, see [edge/cordum-hook.md](edge/cordum-hook.md).
For Cordum Edge Claude settings generator behavior, see [edge/cordumctl-edge-claude.md](edge/cordumctl-edge-claude.md).

---

## Global Flags

Every command accepts these flags. Environment variables are checked when a flag
is not provided on the command line.

| Flag | Env Variable | Default | Description |
|------|-------------|---------|-------------|
| `--gateway` | `CORDUM_GATEWAY` | `http://localhost:8081` | Gateway base URL |
| `--api-key` | `CORDUM_API_KEY` | *(none)* | API authentication key |
| `--tenant` | `CORDUM_TENANT_ID` | `default` | Tenant ID |
| `--cacert` | `CORDUM_TLS_CA` | *(none)* | CA certificate for TLS verification |
| `--insecure` | `CORDUM_TLS_INSECURE` | `false` | Skip TLS verification (dev/debug only) |

```bash
# Flags take precedence over env vars
cordumctl status --gateway https://prod:8081 --api-key $KEY --cacert ./certs/ca/ca.crt
```

---

## Command Summary

| Command | Description |
|---------|-------------|
| `init` | Scaffold a new Cordum project |
| `generate-certs` | Generate TLS certificates (CA, server, client) |
| `up` | Start production stack via Docker Compose |
| `dev` | Start development stack via Docker Compose |
| `status` | Show gateway health and version |
| `job submit` | Submit a job |
| `job status` | Get job status |
| `job logs` | Get job result or error |
| `workflow create` | Create a workflow from JSON |
| `workflow delete` | Delete a workflow |
| `run start` | Start a workflow run |
| `run delete` | Delete a workflow run |
| `run timeline` | Get run timeline events |
| `approval job` | Approve or reject a job |
| `dlq retry` | Retry a dead-letter job |
| `topic list` | List canonical topic registrations |
| `topic create` | Register or update a topic |
| `topic delete` | Delete a topic registration |
| `worker credential list` | List worker credentials |
| `worker credential create` | Create or rotate a worker credential |
| `worker credential revoke` | Revoke a worker credential |
| `pack create` | Scaffold a new pack |
| `pack install` | Install a pack |
| `pack uninstall` | Uninstall a pack |
| `pack list` | List installed packs |
| `pack show` | Show pack details |
| `pack verify` | Run pack policy simulation tests |

---

> **Planned Edge command:** `edge claude --settings-output` is intentionally
> omitted from the shipped command table until EDGE-019 lands. The underlying
> Claude settings generator helpers exist; the final `cordumctl` command name
> remains gated on EDGE-019.

---

## Project Initialization

### `init <dir>`

Scaffold a new Cordum project with Docker Compose, config files, and a sample
workflow.

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | `false` | Overwrite existing files |

**Files created:**

```
<dir>/
├── docker-compose.yml
├── config/
│   ├── pools.yaml
│   ├── timeouts.yaml
│   └── safety.yaml
├── workflows/
│   └── hello.json
└── README.md
```

**Example:**

```bash
cordumctl init my-project
cd my-project
```

---

## TLS Certificate Generation

### `generate-certs`

Generate a full TLS certificate chain: CA certificate, server certificate
(with SANs for all Cordum services), and client certificate.

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `./certs` | Output directory |
| `--force` | `false` | Overwrite existing certificates |
| `--days` | `365` | Certificate validity in days |

Certificates use EC P-256 keys with PKCS8 encoding.

```bash
# Generate into default ./certs directory
cordumctl generate-certs

# Custom output directory
cordumctl generate-certs --dir /path/to/certs

# Regenerate expired certificates
cordumctl generate-certs --force --days 730
```

**Output structure:**

```
certs/
├── ca/
│   ├── ca.crt          # CA certificate
│   └── ca.key          # CA private key
├── server/
│   ├── tls.crt         # Server certificate (SANs: localhost, service names)
│   └── tls.key         # Server private key
└── client/
    ├── tls.crt         # Client certificate
    └── tls.key         # Client private key
```

`cordumctl up` and `cordumctl dev` auto-generate certificates if `certs/ca/ca.crt`
does not exist. Use `generate-certs --force` to regenerate manually.

For full TLS documentation, see [guides/tls-setup.md](guides/tls-setup.md).

---

## Stack Management

### `up`

Start the production Cordum stack. Requires `CORDUM_API_KEY` (from environment,
flag, or `.env` file).

| Flag | Default | Description |
|------|---------|-------------|
| `--file` | `docker-compose.yml` | Compose file path |
| `--build` | `true` | Build images before starting |
| `--detach` | `true` | Run in background |

The command also sets `COMPOSE_HTTP_TIMEOUT` and `DOCKER_CLIENT_TIMEOUT` to
1800 seconds if not already set.

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
cordumctl up
```

### `dev`

Start the stack in development mode.

| Flag | Default | Description |
|------|---------|-------------|
| `--file` | `docker-compose.yml` | Compose file path |
| `--build` | `true` | Build images before starting |
| `--detach` | `false` | Run in background |

```bash
cordumctl dev            # foreground with logs
cordumctl dev --detach   # background
```

### `status`

Print gateway health and version information.

```bash
cordumctl status
# {"status":"ok","version":"0.12.0",...}
```

---

## Job Management

### `job submit`

Submit a job to a topic. Either `--prompt` or `--input` is required.

| Flag | Default | Description |
|------|---------|-------------|
| `--topic` | *(required)* | Job topic (e.g. `job.my-pack.echo`) |
| `--prompt` | | Job prompt text |
| `--input` | | Input JSON (file path or inline) |
| `--idempotency-key` | | Deduplication key |
| `--capability` | | Job capability |
| `--pack-id` | | Pack ID |
| `--labels` | | Labels as JSON object |
| `--risk-tags` | | Comma-separated risk tags |
| `--requires` | | Comma-separated requirements |
| `--org` | | Organization/tenant ID |
| `--actor-id` | | Actor ID |
| `--actor-type` | | Actor type (`human` or `service`) |
| `--json` | `false` | Output full JSON response |

```bash
# Simple prompt
cordumctl job submit --topic job.hello-pack.echo --prompt "Hello world"

# With input file and labels
cordumctl job submit \
  --topic job.my-pack.process \
  --input ./input.json \
  --labels '{"env":"staging"}' \
  --risk-tags "pii,financial"

# Full JSON output
cordumctl job submit --topic job.hello-pack.echo --prompt "test" --json
```

### `job status <job_id>`

Get the status of a job.

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output full job JSON instead of just state |

```bash
cordumctl job status job-abc123
# running

cordumctl job status job-abc123 --json
# {"id":"job-abc123","status":"running",...}
```

### `job logs <job_id>`

Get the result or error message of a completed job.

```bash
cordumctl job logs job-abc123
# {"result":"Hello world!"}
```

---

## Workflow Management

### `workflow create --file <file>`

Create a new workflow from a JSON definition file.

| Flag | Default | Description |
|------|---------|-------------|
| `--file` | *(required)* | Path to workflow JSON file |

```bash
cordumctl workflow create --file workflows/my-pipeline.json
# workflow-xyz789
```

### `workflow delete <workflow_id>`

Delete a workflow by ID.

```bash
cordumctl workflow delete workflow-xyz789
```

---

## Run Management

### `run start <workflow_id>`

Start a new run of a workflow.

| Flag | Default | Description |
|------|---------|-------------|
| `--input` | | Input JSON file path or inline JSON |
| `--dry-run` | `false` | Start in simulation mode |
| `--idempotency-key` | | Deduplication key |

```bash
# Start with input
cordumctl run start workflow-xyz789 --input '{"key":"value"}'

# Dry run
cordumctl run start workflow-xyz789 --dry-run
```

### `run delete <run_id>`

Delete a workflow run.

```bash
cordumctl run delete run-abc123
```

### `run timeline <run_id>`

Get the full timeline/audit trail of a run.

```bash
cordumctl run timeline run-abc123
# [{"event":"step_started","step":"step1","at":"2026-01-15T10:00:00Z"},...]
```

---

## Approvals

### `approval job <job_id>`

Approve or reject a job pending approval.

| Flag | Description |
|------|-------------|
| `--approve` | Approve the job |
| `--reject` | Reject the job |

```bash
cordumctl approval job job-abc123 --approve
```

---

## Dead Letter Queue

### `dlq retry <job_id>`

Retry a job that ended up in the dead-letter queue.

```bash
cordumctl dlq retry job-dead456
```

---

## Pack Management

Packs bundle topics, schemas, workflows, config overlays, and policy fragments
into installable units. See [pack.md](pack.md) for the full format specification.

### `pack create <pack_id>`

Scaffold a new pack directory with template files.

| Flag | Default | Description |
|------|---------|-------------|
| `--dir` | `<pack_id>` | Output directory |
| `--force` | `false` | Overwrite existing files |

Pack IDs must match `[a-z0-9-]+`.

```bash
cordumctl pack create my-agent
```

**Files created:**

```
my-agent/
├── pack.yaml                     # Pack manifest
├── README.md
├── schemas/EchoInput.json        # Sample input schema
├── workflows/echo.yaml           # Sample workflow
└── overlays/
    ├── pools.patch.yaml          # Pool config overlay
    ├── timeouts.patch.yaml       # Timeout config overlay
    └── policy.fragment.yaml      # Policy fragment
```

### `pack install <path|url>`

Install a pack from a local directory, `.tgz` archive, or HTTPS URL.

This registers pack topics in the canonical topic registry after config overlays
are applied, so installed packs pass submit-time unknown-topic validation.

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Print planned changes without applying |
| `--force` | `false` | Skip core version compatibility check |
| `--upgrade` | `false` | Overwrite existing resources (schemas/workflows) |
| `--inactive` | `false` | Install without pool mappings |

**Validation checks:**
- Pack manifest syntax
- Protocol version compatibility
- Core version range (unless `--force`)
- Topic/schema/workflow namespacing

```bash
# Install from directory
cordumctl pack install ./my-agent

# Install from tarball
cordumctl pack install my-agent-1.0.0.tgz

# Install from URL
cordumctl pack install https://packs.cordum.io/my-agent/1.0.0.tgz

# Dry run
cordumctl pack install ./my-agent --dry-run

# Upgrade existing
cordumctl pack install ./my-agent --upgrade
```

### `pack uninstall <pack_id>`

Remove an installed pack.

This also removes the pack's topic registrations from the canonical topic registry.

| Flag | Default | Description |
|------|---------|-------------|
| `--purge` | `false` | Also delete pack's workflows and schemas |

```bash
cordumctl pack uninstall my-agent
cordumctl pack uninstall my-agent --purge
```

### `pack list`

List all installed packs.

```bash
cordumctl pack list
# my-agent    1.0.0    ACTIVE
# demo-guard  0.2.1    INACTIVE
```

### `pack show <pack_id>`

Show detailed information about an installed pack (JSON output).

```bash
cordumctl pack show my-agent
# {"id":"my-agent","version":"1.0.0","status":"ACTIVE",...}
```

### `pack verify <pack_id>`

Run policy simulation tests defined in the pack manifest.

```bash
cordumctl pack verify my-agent
# PASS: test-allow-echo
# PASS: test-deny-dangerous
# 2/2 tests passed
```

---

## Environment Variables

Complete list of environment variables used by `cordumctl` and the services
started with `up`/`dev`.

### CLI Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_GATEWAY` | `http://localhost:8081` | Gateway base URL |
| `CORDUM_API_KEY` | *(none)* | API authentication key |
| `CORDUM_TENANT_ID` | `default` | Tenant ID |

### Docker Compose Variables

These are used by `cordumctl up` and `cordumctl dev`:

| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_API_KEY` | *(required)* | API key for all services |
| `CORDUM_VERSION` | `latest` | Docker image version tag |
| `CORDUM_TENANT_ID` | `default` | Default tenant ID |
| `REDIS_PASSWORD` | *(required)* | Redis password (generate with `openssl rand -hex 32`) |
| `CORDUM_API_BASE_URL` | | Dashboard API base URL |
| `CORDUM_PRINCIPAL_ID` | | Dashboard principal ID |
| `CORDUM_PRINCIPAL_ROLE` | | Dashboard principal role |
| `COMPOSE_HTTP_TIMEOUT` | `1800` | Docker Compose HTTP timeout (seconds) |
| `DOCKER_CLIENT_TIMEOUT` | `1800` | Docker client timeout (seconds) |

---

## Pack File Limits

| Limit | Value |
|-------|-------|
| Maximum files per pack | 2,048 |
| Maximum file size | 32 MB |
| Maximum uncompressed size | 256 MB |
| Supported formats | Directory or `.tgz` archive |

---

## Topic Management

Manage the canonical topic registry used by the gateway, scheduler, and dashboard.

### `cordumctl topic list`

List registered topics with pool mapping, schema bindings, registry status, and active
worker count.

```bash
cordumctl topic list
```

### `cordumctl topic create <name>`

Register a topic or update an existing registration.

| Flag | Default | Description |
|------|---------|-------------|
| `--pool` | *(required unless `--status disabled`)* | Worker pool name |
| `--input-schema` | | Input schema ID |
| `--output-schema` | | Output schema ID |
| `--pack-id` | | Owning pack ID |
| `--requires` | | Comma-separated capability requirements |
| `--risk-tags` | | Comma-separated risk tags |
| `--status` | `active` | Topic status: `active`, `deprecated`, or `disabled` |

```bash
cordumctl topic create job.my-pack.process \
  --pool my-pack \
  --input-schema my-pack/ProcessInput \
  --output-schema my-pack/ProcessResult \
  --pack-id my-pack \
  --requires docker,network:egress \
  --risk-tags external-api

# Disabled topics may omit the pool during staged rollout
cordumctl topic create job.my-pack.legacy --status disabled
```

### `cordumctl topic delete <name>`

Delete a topic registration from the canonical registry.

```bash
cordumctl topic delete job.my-pack.legacy
```

---

## Worker Credential Management

Provision worker credentials used by scheduler attestation mode.

### `cordumctl worker credential list`

List issued credentials, including allowlists and revocation state.

```bash
cordumctl worker credential list
```

### `cordumctl worker credential create --worker-id <worker_id>`

Create a new worker credential or rotate an existing one. The plaintext token is
printed once and must be stored immediately.

| Flag | Default | Description |
|------|---------|-------------|
| `--worker-id` | *(required)* | Worker identity to provision |
| `--allowed-pools` | | Comma-separated allowed pools |
| `--allowed-topics` | | Comma-separated allowed topics |

```bash
cordumctl worker credential create \
  --worker-id external-worker-01 \
  --allowed-pools my-pack \
  --allowed-topics job.my-pack.process
```

### `cordumctl worker credential revoke --worker-id <worker_id>`

Revoke an issued worker credential.

```bash
cordumctl worker credential revoke --worker-id external-worker-01
```

---

## Pool Management

Manage worker pools dynamically without restarting services.

### `cordumctl pool list`

List all pools with worker counts and utilization.

```bash
cordumctl pool list
```

### `cordumctl pool get <name>`

Get detailed pool information as JSON.

```bash
cordumctl pool get gpu-batch
```

### `cordumctl pool create <name>`

Create a new worker pool.

| Flag | Default | Description |
|------|---------|-------------|
| `--requires` | | Comma-separated capability requirements |
| `--description` | | Pool description |

```bash
cordumctl pool create gpu-batch --requires gpu,docker --description "GPU batch pool"
```

### `cordumctl pool update <name>`

Update pool configuration. Only provided flags are changed.

| Flag | Default | Description |
|------|---------|-------------|
| `--requires` | | Comma-separated capability requirements |
| `--description` | | Pool description |

```bash
cordumctl pool update gpu-batch --description "Updated GPU pool"
```

### `cordumctl pool delete <name>`

Delete a pool. Fails if the pool has active topic mappings unless `--force`.

| Flag | Default | Description |
|------|---------|-------------|
| `--force` | false | Force delete even with active topic mappings |

```bash
cordumctl pool delete gpu-batch --force
```

### `cordumctl pool drain <name>`

Start draining a pool. New jobs stop routing to this pool; in-flight jobs
complete normally. The pool auto-transitions to inactive when done.

| Flag | Default | Description |
|------|---------|-------------|
| `--timeout` | 300 | Drain timeout in seconds |

```bash
cordumctl pool drain gpu-batch --timeout 600
```

### `cordumctl pool topic add <pool> <topic>`

Add a topic-to-pool mapping.

```bash
cordumctl pool topic add gpu-batch job.ml.train
```

### `cordumctl pool topic remove <pool> <topic>`

Remove a topic-to-pool mapping.

```bash
cordumctl pool topic remove gpu-batch job.ml.train
```

---

## Operator Commands (Diagnostic + Governance)

These subcommands ship as additions to the main `cordumctl` binary and
the `cordumctl-governance-helper` companion. Run `--help` on each for
the full flag schema.

### `cordumctl doctor`

Operator health-check tool that probes every dependency (gateway,
scheduler, safety kernel, context engine, NATS, Redis, MCP) plus the
local config and credential store. Prints a structured report with
green / yellow / red status per probe.

```bash
cordumctl doctor                          # check all probes
cordumctl doctor --json                   # machine-readable output
cordumctl doctor --probe gateway,redis    # subset
cordumctl doctor --output report.txt      # write to file
```

Used in CI as `tools/scripts/doctor_integration_test.sh`.

### `cordumctl audit`

Audit chain operations — verify, replay, export.

```bash
cordumctl audit verify --since 24h        # verify chain integrity over a window
cordumctl audit replay --job <job_id>     # replay a job's audit timeline locally
cordumctl audit export --backend datadog  # one-shot export to a configured backend
```

See [audit.md](audit.md) for event types + SIEM exporter
configuration.

### `cordumctl governance`

Governance helpers (delivered by the `cordumctl-governance-helper`
binary at `cmd/cordumctl-governance-helper/`). Wraps the
`/api/v1/governance/*` REST surface for scripted use.

```bash
cordumctl governance health                   # overall policy decision pipeline health
cordumctl governance decisions --since 1h     # list decisions over a window
cordumctl governance approvals analytics      # approval rate + bottleneck analytics
```

See [api-reference.md](api-reference.md) § Governance for the
underlying REST endpoints.

### `cordumctl evals`

Evaluations CRUD + run. Drives the offline evaluation framework
(separate from runtime context-window assembly — see
[context-engine.md](context-engine.md)).

```bash
cordumctl evals datasets list
cordumctl evals datasets create --file dataset.yaml
cordumctl evals run --dataset <id> --bundle <bundle_id>
cordumctl evals run show <run_id>
```

Dashboard surface: `EvalsPage`, `EvalDatasetDetailPage`,
`EvalRunDetailPage`.

### `cordumctl mcp`

MCP server lifecycle + tool approval flow. Wraps
`/api/v1/mcp/*` endpoints.

```bash
cordumctl mcp tools list
cordumctl mcp approvals list                  # outstanding tool approvals
cordumctl mcp approvals approve <id>
cordumctl mcp approvals reject <id> --reason "reason"
```

See [mcp-server.md](mcp-server.md), [mcp-tools-reference.md](mcp-tools-reference.md),
and [mcp-resources-reference.md](mcp-resources-reference.md).

### `cordumctl delegation-keygen`

Generates a delegation key pair for A2A delegation token signing.

```bash
cordumctl delegation-keygen --out ./keys/    # writes ed25519 private + public pair
cordumctl delegation-keygen --rotate         # rotate; preserves prior public key for verification window
```

Companion to the delegation system documented in
[delegation.md](delegation.md). Tokens are re-verified at
dispatch time per the boundary-hardening described in
[CORE.md § 3](CORE.md#3-scheduler-that-is-purely-config‑driven-no-hardcoded-core-topics).

---

## Service Ports (Default Stack)

| Service | Port | Protocol |
|---------|------|----------|
| API Gateway (HTTP) | 8080 | HTTP |
| API Gateway (admin) | 8081 | HTTP |
| API Gateway (metrics) | 9092 | HTTP |
| Dashboard | 8082 | HTTP |
| Safety Kernel (gRPC) | 50051 | gRPC |
| Context Engine (gRPC) | 50070 | gRPC |
| Workflow Engine (metrics) | 9093 | HTTP |
| NATS | 4222 | NATS |
| Redis | 6379 | Redis |

---

## See Also

- [api-reference.md](api-reference.md) — REST endpoint reference
- [configuration-reference.md](configuration-reference.md) — Config file reference
- [guides/tls-setup.md](guides/tls-setup.md) — TLS setup and troubleshooting
- [pack.md](pack.md) — Pack format specification
- [DOCKER.md](DOCKER.md) — Docker Compose deployment
- [quickstart.md](quickstart.md) — Getting started tutorial
