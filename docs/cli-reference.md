# CLI Reference — cordumctl

Complete command reference for `cordumctl`, the Cordum control-plane CLI.

For REST API endpoints, see [api-reference.md](api-reference.md).
For pack format details, see [pack.md](pack.md).
For configuration, see [configuration-reference.md](configuration-reference.md).

---

## Global Flags

Every command accepts these flags. Environment variables are checked when a flag
is not provided on the command line.

| Flag | Env Variable | Default | Description |
|------|-------------|---------|-------------|
| `--gateway` | `CORDUM_GATEWAY` | `https://localhost:8081` | Gateway base URL |
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
| `approval step` | Approve or reject a workflow step |
| `approval job` | Approve or reject a job |
| `dlq retry` | Retry a dead-letter job |
| `pack create` | Scaffold a new pack |
| `pack install` | Install a pack |
| `pack uninstall` | Uninstall a pack |
| `pack list` | List installed packs |
| `pack show` | Show pack details |
| `pack verify` | Run pack policy simulation tests |

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

### `approval step <workflow_id> <run_id> <step_id>`

Approve or reject a workflow step that requires human approval.

| Flag | Description |
|------|-------------|
| `--approve` | Approve the step |
| `--reject` | Reject the step |

```bash
cordumctl approval step wf-123 run-456 step-789 --approve
cordumctl approval step wf-123 run-456 step-789 --reject
```

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
