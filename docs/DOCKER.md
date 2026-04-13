# Docker Compose Quickstart (platform only)

This repo ships the control-plane stack plus an optional dashboard UI. Compose builds the platform binaries and runs:

Prereqs: Docker + Docker Compose. The smoke test script requires `curl` and `jq`.

- Infra: `nats`, `redis`
- Control plane: `cordum-api-gateway`, `cordum-scheduler`, `cordum-safety-kernel`, `cordum-workflow-engine`
- Optional: `cordum-context-engine` (generic memory helper)
- Optional UI: `cordum-dashboard` (React UI served by a lightweight static server)

---

## 1. Service Inventory

| Service | Dockerfile | Ports | Health Endpoint | Depends On |
|---------|-----------|-------|-----------------|------------|
| `nats` | *(image: nats:2.10-alpine)* | 4222 | `:8222/healthz` | — |
| `redis` | *(image: redis:7-alpine)* | 6379 | `redis-cli ping` (TLS) | — |
| `context-engine` | `Dockerfile` (SERVICE=cordum-context-engine) | 50070 (gRPC) | `nc -z localhost 50070` | redis |
| `safety-kernel` | `Dockerfile` (SERVICE=cordum-safety-kernel) | 50051 (gRPC) | `nc -z localhost 50051` | nats |
| `scheduler` | `Dockerfile` (SERVICE=cordum-scheduler) | 9090 (metrics) | `GET /health` on :9090 | nats, redis, safety-kernel |
| `api-gateway` | `Dockerfile` (SERVICE=cordum-api-gateway) | 8080 (HTTP), 8081 (health), 9092 (metrics) | `GET /health` on :8081 (HTTPS) | nats, redis, scheduler, safety-kernel |
| `workflow-engine` | `Dockerfile` (SERVICE=cordum-workflow-engine) | 9093 (HTTP) | `GET /health` on :9093 | nats, redis, scheduler |
| `dashboard` | `dashboard/Dockerfile` | 8082→8080 (nginx) | `GET /healthz` on :8080 | api-gateway |

The root `Dockerfile` is a multi-service image — the `SERVICE` build arg selects which binary under `cmd/` to compile. The dashboard has its own Dockerfile using a Node builder + nginx runtime.

---

## 2. Bring Up the Stack

```bash
# 1. Generate an API key (required by the gateway)
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default

# 2. Build all images
docker compose build

# 3. Start all services
docker compose up -d

# 4. Verify everything is healthy
docker compose ps
```

Docker Compose automatically loads `.env`. The helper scripts read environment
variables from your shell, so keep the `export` lines when running scripts.

### Use GHCR Images (Release Builds)

```bash
export CORDUM_VERSION=v0.9.7
docker compose -f docker-compose.release.yml pull
docker compose -f docker-compose.release.yml up -d
```

Release images:
- `ghcr.io/cordum-io/cordum/api-gateway:<version>`
- `ghcr.io/cordum-io/cordum/scheduler:<version>`
- `ghcr.io/cordum-io/cordum/safety-kernel:<version>`
- `ghcr.io/cordum-io/cordum/workflow-engine:<version>`
- `ghcr.io/cordum-io/cordum/context-engine:<version>`
- `ghcr.io/cordum-io/cordum/dashboard:<version>`

### Smoke Test (No Workers Required)

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
CORDUM_TENANT_ID=${CORDUM_TENANT_ID:-default} \
bash ./tools/scripts/platform_smoke.sh
```

---

## 3. Volume Mounts

### Named Volumes

| Volume | Mount Point | Purpose | Persistence |
|--------|-------------|---------|-------------|
| `redis_data` | `/data` in redis | AOF + RDB persistence | Survives `docker compose down`; removed by `docker compose down -v` |
| `nats_data` | `/data` in nats | JetStream file store | Survives `docker compose down`; removed by `docker compose down -v` |

### Config Bind Mounts

| Host Path | Container Path | Service | Purpose |
|-----------|---------------|---------|---------|
| `config/nats.conf` | `/etc/nats/nats.conf` | nats | NATS server config (JetStream, auth, `sync_interval`) |
| `config/safety.yaml` | `/etc/cordum/safety.yaml` | safety-kernel | Safety policy rules |
| `config/pools.yaml` | `/etc/cordum/pools.yaml` | scheduler | Worker pool→topic mapping |
| `config/timeouts.yaml` | `/etc/cordum/timeouts.yaml` | scheduler | Job timeout configuration |

All config mounts are `:ro` (read-only).

### Backup and Restore

```bash
# Back up Redis data
docker compose exec redis redis-cli -a "${REDIS_PASSWORD}" BGSAVE
docker cp "$(docker compose ps -q redis)":/data/dump.rdb ./backup/

# Back up NATS JetStream
docker compose stop nats
docker cp "$(docker compose ps -q nats)":/data ./backup/nats-data/
docker compose start nats

# Restore Redis
docker compose stop redis
docker cp ./backup/dump.rdb "$(docker compose ps -q redis)":/data/
docker compose start redis
```

---

## 4. Network Topology

All services share the default `cordum` Docker network. No service exposes ports beyond those listed.

```
                    ┌─────────────┐
                    │  Dashboard  │ :8082
                    │  (nginx)    │
                    └──────┬──────┘
                           │ HTTP
                    ┌──────▼──────┐
     ┌──────────────│ API Gateway │──────────────┐
     │              │  :8080/8081 │              │
     │              └──┬───┬───┬──┘              │
     │                 │   │   │                 │
     │     gRPC :50051 │   │   │ NATS pub/sub   │
     │    ┌────────────┘   │   └────────────┐   │
     │    │                │                │   │
┌────▼────▼───┐    ┌──────▼──────┐   ┌──────▼───┐
│Safety Kernel│    │  Scheduler  │   │   NATS   │
│   :50051    │    │   :9090     │   │  :4222   │
└──────┬──────┘    └──┬───┬─────┘   └──────────┘
       │              │   │
       │     ┌────────┘   │
       │     │            │
  ┌────▼─────▼──┐   ┌────▼────────────┐
  │    Redis    │   │ Workflow Engine  │
  │   :6379    │   │     :9093        │
  └────────────┘   └─────────────────┘

  Context Engine (:50070) ← Redis only
```

**Communication patterns:**
- **Gateway → Safety Kernel**: gRPC for policy evaluation
- **Gateway → Redis**: Job state, config, sessions
- **Gateway → NATS**: Job submission, event streaming
- **Scheduler → NATS**: Job dispatch, heartbeat consumption
- **Scheduler → Redis**: Job state, worker tracking
- **Scheduler → Safety Kernel**: gRPC for output policy checks
- **Workflow Engine → NATS**: Step execution, event bus
- **Workflow Engine → Redis**: Run state persistence
- **Safety Kernel → NATS**: Policy event subscription
- **Safety Kernel → Redis**: Policy bundle loading from config service
- **Context Engine → Redis**: Memory storage
- **Dashboard → Gateway**: HTTP API + WebSocket stream

**Port exposure rationale:**
- Only the gateway (`:8080`/`:8081`) and dashboard (`:8082`) need external access
- Infrastructure ports (Redis `:6379`, NATS `:4222`) are exposed for local debugging; in production, remove these from `ports:`
- Metrics ports (`:9090`, `:9092`, `:9093`) are for Prometheus scraping

---

## 5. Health Checks

Every service defines a health check in `docker-compose.yml`:

```yaml
healthcheck:
  test: <command>
  interval: 10s      # Check every 10 seconds
  timeout: 3s        # Fail if check takes > 3s
  retries: 3         # Unhealthy after 3 consecutive failures
  start_period: 10s  # Grace period after container start
```

### Health Check Commands per Service

| Service | Command | What It Checks |
|---------|---------|---------------|
| nats | `wget -qO- http://localhost:8222/healthz \|\| exit 1` | NATS HTTP monitoring healthz endpoint (requires `:8222` enabled in `nats.conf`) |
| redis | `redis-cli --tls --cacert <ca> -a <pass> ping` | Redis responds to PING over TLS |
| context-engine | `nc -z localhost 50070` | gRPC port open |
| safety-kernel | `nc -z localhost 50051` | gRPC port open |
| scheduler | `wget --spider -q http://127.0.0.1:9090/health` | Dedicated health HTTP endpoint |
| api-gateway | `wget --spider -q --no-check-certificate https://127.0.0.1:8081/health` | Dedicated health HTTPS endpoint |
| workflow-engine | `wget --spider -q http://127.0.0.1:9093/health` | Dedicated health HTTP endpoint |
| dashboard | `curl -f http://127.0.0.1:8080/healthz` | Nginx healthz endpoint |

### Verifying Health Manually

```bash
# Check all services at once
docker compose ps --format "table {{.Name}}\t{{.Status}}"

# Check a specific service
docker inspect --format='{{.State.Health.Status}}' cordum-api-gateway-1

# View recent health check logs
docker inspect --format='{{range .State.Health.Log}}{{.Output}}{{end}}' cordum-redis-1

# Hit gateway health endpoint directly
curl -s http://localhost:8081/health | jq .

# Hit gateway status endpoint (detailed)
curl -s -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  http://localhost:8080/api/v1/status | jq .
```

### Tuning Health Checks for Slow Machines

If services fail health checks during startup (common on CI or low-resource hosts), increase `start_period`:

```yaml
healthcheck:
  start_period: 30s  # Give more time for Go binary to compile and start
```

---

## 6. Environment Variables Reference

### Infrastructure Services

#### NATS
| Variable | Default | Description |
|----------|---------|-------------|
| *(configured via nats.conf)* | — | JetStream settings, auth, sync_interval |

#### Redis
| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_PASSWORD` | *(required)* | Redis AUTH password (generate with `openssl rand -hex 32`) |

### Control Plane Services

#### API Gateway
| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_API_KEY` | *(required)* | Primary API key for authentication |
| `CORDUM_API_KEYS` | — | Comma-separated or JSON array of multiple keys |
| `CORDUM_API_KEYS_PATH` | — | File path for hot-reloadable API keys |
| `NATS_URL` | `nats://nats:4222` | NATS connection URL |
| `NATS_USE_JETSTREAM` | `1` | Enable JetStream for durable messaging |
| `REDIS_URL` | `redis://:$REDIS_PASSWORD@redis:6379` | Redis connection URL |
| `SAFETY_KERNEL_ADDR` | `safety-kernel:50051` | gRPC address of safety kernel |
| `TENANT_ID` | `default` | Default tenant ID |
| `API_RATE_LIMIT_RPS` | `2000` | Requests per second limit |
| `API_RATE_LIMIT_BURST` | `4000` | Burst capacity |
| `REDIS_DATA_TTL` | `24h` | TTL for cached data in Redis |
| `JOB_META_TTL` | `168h` | TTL for job metadata |
| `CORDUM_USER_AUTH_ENABLED` | `false` | Enable user/password authentication |
| `CORDUM_ADMIN_USERNAME` | `admin` | Initial admin username |
| `CORDUM_ADMIN_PASSWORD` | — | Initial admin password (required if auth enabled) |
| `CORDUM_ADMIN_EMAIL` | — | Initial admin email |
| `CORDUM_ALLOW_INSECURE_NO_AUTH` | — | Skip auth (dev only, blocked in production) |
| `CORDUM_ENV` | — | Set to `production` for production mode |
| `GATEWAY_HTTP_TLS_CERT` | — | Path to HTTP TLS certificate |
| `GATEWAY_HTTP_TLS_KEY` | — | Path to HTTP TLS private key |
| `GRPC_TLS_CERT` | — | Path to gRPC TLS certificate |
| `GRPC_TLS_KEY` | — | Path to gRPC TLS private key |
| `GATEWAY_METRICS_PUBLIC` | — | Set to `1` to expose metrics publicly in production |

#### Scheduler
| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://nats:4222` | NATS connection URL |
| `NATS_USE_JETSTREAM` | `1` | Enable JetStream |
| `REDIS_URL` | `redis://:$REDIS_PASSWORD@redis:6379` | Redis connection URL |
| `SAFETY_KERNEL_ADDR` | `safety-kernel:50051` | gRPC address of safety kernel |
| `POOL_CONFIG_PATH` | `/etc/cordum/pools.yaml` | Worker pool configuration |
| `TIMEOUT_CONFIG_PATH` | `/etc/cordum/timeouts.yaml` | Job timeout configuration. In production (`CORDUM_ENV=production`), load/parse failures are fatal. |
| `JOB_META_TTL` | `168h` | TTL for job metadata |
| `WORKER_SNAPSHOT_INTERVAL` | `5s` | How often to snapshot worker state |

#### Safety Kernel
| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://nats:4222` | NATS connection URL |
| `REDIS_URL` | `redis://:$REDIS_PASSWORD@redis:6379` | Redis connection URL |
| `SAFETY_KERNEL_ADDR` | `:50051` | Listen address for gRPC |
| `SAFETY_POLICY_PATH` | `/etc/cordum/safety.yaml` | Safety policy file path |

#### Workflow Engine
| Variable | Default | Description |
|----------|---------|-------------|
| `NATS_URL` | `nats://nats:4222` | NATS connection URL |
| `NATS_USE_JETSTREAM` | `1` | Enable JetStream |
| `REDIS_URL` | `redis://:$REDIS_PASSWORD@redis:6379` | Redis connection URL |
| `WORKFLOW_ENGINE_HTTP_ADDR` | `:9093` | HTTP listen address |
| `WORKFLOW_ENGINE_SCAN_INTERVAL` | `5s` | How often to scan for pending runs |
| `WORKFLOW_ENGINE_RUN_SCAN_LIMIT` | `200` | Max runs to process per scan |

#### Context Engine
| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_URL` | `redis://:$REDIS_PASSWORD@redis:6379` | Redis connection URL |
| `CONTEXT_ENGINE_ADDR` | `:50070` | Listen address for gRPC |

#### Dashboard
| Variable | Default | Description |
|----------|---------|-------------|
| `CORDUM_API_BASE_URL` | — | Override gateway URL (auto-detected if empty) |
| `CORDUM_API_KEY` | — | API key for embedded auth |
| `CORDUM_DASHBOARD_EMBED_API_KEY` | `false` | Inject API key into dashboard config |
| `CORDUM_TENANT_ID` | `default` | Tenant ID for dashboard requests |
| `CORDUM_PRINCIPAL_ID` | — | Override principal identity |
| `CORDUM_PRINCIPAL_ROLE` | — | Override principal role |

---

## 7. `.dockerignore` Requirements

### Root `.dockerignore` (Go services)

The root `.dockerignore` must exclude dashboard artifacts to keep the Go build context small:

```
bin
.git
.cache
.gocache
.gomodcache
vendor
node_modules
**/node_modules
dashboard/node_modules
dashboard/dist
*.exe
*.swp
*.tmp
*.log
.moe
```

### `dashboard/.dockerignore`

The dashboard has its own `.dockerignore` because its build context is `./dashboard`:

```
node_modules
dist
.git
*.swp
*.tmp
*.log
*.zip
```

**Critical**: If `dashboard/.dockerignore` is missing or doesn't exclude `node_modules`, the dashboard Docker build will fail or produce a multi-GB context. The `COPY . .` step copies everything in the build context into the builder stage.

---

## 8. Common Issues

### Dashboard Build Fails — `node_modules` in Build Context

**Symptom**: Dashboard Docker build takes forever or runs out of disk.

**Fix**: Ensure `dashboard/.dockerignore` exists and contains `node_modules`:
```bash
echo "node_modules" >> dashboard/.dockerignore
```

### MSYS Path Mangling (Windows/Git Bash)

**Symptom**: `docker exec` commands fail with paths like `C:/Program Files/Git/...` instead of `/usr/local/bin/...`.

**Fix**: Prefix commands with `MSYS_NO_PATHCONV=1`:
```bash
MSYS_NO_PATHCONV=1 docker exec cordum-redis-1 redis-cli -a "$REDIS_PASSWORD" ping
```

### Port Conflicts

**Symptom**: `Bind for 0.0.0.0:8080 failed: port is already allocated`.

**Fix**: Stop the conflicting process or remap ports in `docker-compose.yml`:
```yaml
ports:
  - "18080:8080"  # Map to alternate host port
```

### Redis Connection Refused on Startup

**Symptom**: Services fail to start with `redis: connection refused`.

**Cause**: Services start before Redis is healthy. Compose uses `depends_on` with `condition: service_healthy`, but if Redis health check is misconfigured this can fail.

**Fix**: Verify Redis is healthy:
```bash
docker compose ps redis
docker compose logs redis
```

### Pool Config Cached in Redis

**Symptom**: Changes to `config/pools.yaml` aren't picked up.

**Cause**: `bootstrapConfig()` is write-once — it caches the pool config in Redis key `cfg:system:default`.

**Fix**: Delete the cached key and restart:
```bash
docker compose exec redis redis-cli -a "$REDIS_PASSWORD" DEL cfg:system:default
docker compose restart scheduler
```

### Gateway Refuses to Start — Missing API Key

**Symptom**: `error: CORDUM_API_KEY is not set`.

**Fix**: Generate and export an API key before starting:
```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
docker compose up -d
```

### NATS JetStream Not Enabled

**Symptom**: Scheduler or gateway logs show `jetstream not enabled`.

**Fix**: Verify `config/nats.conf` has JetStream enabled:
```
jetstream {
  store_dir: /data
  max_mem: 256MB
  max_file: 1GB
}
```

---

## 9. Development Workflow

### Viewing Logs

```bash
# All services
docker compose logs -f

# Specific service
docker compose logs -f api-gateway

# Last 50 lines
docker compose logs --tail 50 scheduler
```

### Rebuilding a Single Service

```bash
docker compose build api-gateway
docker compose up -d api-gateway
```

### Running Tests Against Docker Services

```bash
# Run Go tests that connect to local Redis/NATS
REDIS_URL=redis://:$REDIS_PASSWORD@localhost:6379 \
NATS_URL=nats://localhost:4222 \
go test ./core/... -count=1

# Run the platform smoke test
bash ./tools/scripts/platform_smoke.sh
```

### Hot Reload (Development)

The Go services don't support hot reload inside Docker. For rapid iteration:

1. Run infrastructure in Docker: `docker compose up -d nats redis`
2. Run Go services locally with `go run ./cmd/cordum-api-gateway`
3. Point local services at Docker infra: `NATS_URL=nats://localhost:4222 REDIS_URL=redis://:$REDIS_PASSWORD@localhost:6379`

For the dashboard, run `npm run dev` in `dashboard/` and configure `VITE_API_URL=http://localhost:8080/api/v1`.

---

## 10. Resource Requirements

### Minimum (Local Development)

| Resource | Requirement |
|----------|-------------|
| RAM | 4 GB available for Docker |
| CPU | 2 cores |
| Disk | 5 GB (images + volumes) |

### Recommended (Smoke Tests + Dashboard)

| Resource | Requirement |
|----------|-------------|
| RAM | 8 GB available for Docker |
| CPU | 4 cores |
| Disk | 10 GB |

Go image builds are CPU-intensive (compilation). First build takes 3-5 minutes; subsequent builds use the module cache.

---

## 11. Multi-Platform Notes

### Windows (MSYS / Git Bash)

- Always prefix `docker exec` with `MSYS_NO_PATHCONV=1`
- `jq` is not available by default — install via `pacman -S mingw-w64-x86_64-jq` or use `grep`/`sed` for JSON extraction
- Line endings: ensure `.sh` scripts have LF endings (Git config: `core.autocrlf=input`)
- Docker Desktop must have WSL 2 backend enabled for best performance

### macOS (Docker Desktop)

- Allocate at least 4 GB RAM in Docker Desktop → Settings → Resources
- File sharing performance: use VirtioFS (Docker Desktop 4.25+) for faster bind mounts
- First build is slower due to emulation if on Apple Silicon (images are `linux/amd64`)

### Linux (Native Docker)

- No special configuration needed
- For rootless Docker, ensure the user is in the `docker` group
- BuildKit is enabled by default in Docker 23+

---

## 12. Tear Down

```bash
# Stop all services (keep data volumes)
docker compose down

# Stop and remove all data (JetStream + Redis persistence)
docker compose down -v

# Remove built images too
docker compose down -v --rmi local
```

---

## Production Safety Checklist

Before deploying to production, verify these critical configuration settings:

- [ ] `config/safety.yaml` has `default_decision: deny` (fail-closed — unmatched jobs are denied)
- [ ] `config/safety.yaml` has `output_policy.fail_mode: closed` (quarantine on scanner error)
- [ ] `config/timeouts.yaml` has `running_timeout_seconds` set appropriately for your workload (default: 900s / 15 min)
- [ ] Timeout values are consistent across `config/timeouts.yaml`, K8s ConfigMaps, and Helm values
- [ ] `CORDUM_ENV=production` is set (enforces TLS, disables no-auth mode)
- [ ] API keys are generated with `openssl rand -hex 32` (not weak/default values)
- [ ] Redis password is set and not empty
- [ ] `CORDUM_ALLOW_INSECURE_NO_AUTH` is **not** set

---

## API Key Setup

The gateway requires an API key (or JWT) by default.
Compose now requires `CORDUM_API_KEY` to be set before startup.
Production mode (`CORDUM_ENV=production` or `CORDUM_PRODUCTION=true`) always fails to start without API keys configured.
For local-only testing, you can opt out by setting `CORDUM_ALLOW_INSECURE_NO_AUTH=1` (not allowed in production).

To override:

```bash
cp .env.example .env
# generate a key (requires openssl)
export CORDUM_API_KEY="$(openssl rand -hex 32)"
# set a tenant for requests
export CORDUM_TENANT_ID=default
```

HTTP requests must include `X-API-Key` and `X-Tenant-ID`; gRPC uses metadata `x-api-key`.
The default tenant is `TENANT_ID` (defaults to `default` in compose).
WebSocket stream auth uses `Sec-WebSocket-Protocol: cordum-api-key, <base64url>` plus `?tenant_id=<tenant>` (the dashboard handles this automatically).

The default Compose stack embeds the API key into the dashboard config for local
development (`CORDUM_DASHBOARD_EMBED_API_KEY=true`). Remove that variable in
shared environments to require manual auth.

Production mode (`CORDUM_ENV=production`) requires TLS for HTTP/gRPC and for Redis/NATS clients.
Metrics endpoints bind to loopback in production unless you set `GATEWAY_METRICS_PUBLIC=1`.

For multiple API keys, set `CORDUM_API_KEYS` (comma-separated or JSON). Example:

```
CORDUM_API_KEYS=key-a,key-b
```

API keys support JSON metadata for roles/tenants/expiry, for example:

```
CORDUM_API_KEYS='[{"key":"k1","role":"admin","tenant":"default","expires_at":"2030-01-01T00:00:00Z"}]'
```

To rotate keys without a restart, set `CORDUM_API_KEYS_PATH` to a file with the
same content; the gateway reloads on change.

Enterprise deployments (multi-tenant keys, RBAC, SSO, SIEM export) are configured in the enterprise repo.

## Config Mounts

Compose mounts:
- `config/pools.yaml`
- `config/timeouts.yaml`
- `config/safety.yaml`
- `config/nats.conf` (NATS server config; tune `sync_interval` for JetStream durability)

To adjust JetStream durability for local/dev, edit `config/nats.conf` and set
`sync_interval` (lower values improve crash durability at the cost of throughput).

If you install policy bundles via packs, the safety kernel must have `REDIS_URL`
set so it can load policy fragments from the config service (compose does this
by default).
