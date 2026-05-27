# Cordum Quickstart

One command, ~3 minutes on first run, ~30 seconds afterwards. You end
with a fully-configured stack, a gateway API key, TLS certificates, a
running dashboard, and a post-deploy smoke test that proves an approval
workflow flows end-to-end.

## Fastest path

```bash
git clone https://github.com/cordum-io/cordum.git
cd cordum
./tools/scripts/quickstart.sh
```

That's it. The script:

1. Verifies Docker is running and Compose v2 is available.
2. Creates `.env` if missing and generates a `CORDUM_API_KEY` (32-byte hex)
   and a `REDIS_PASSWORD` (16-byte hex) using `openssl` /
   `/dev/urandom` / `python -c 'secrets.token_hex'` — whichever is
   available.
3. Generates the TLS certificate bundle under `./certs/` via
   `cordumctl generate-certs` (or `go run ./cmd/cordumctl` as a
   fallback).
4. Warns if any of ports 4222, 6379, 8081, 8082, 9080, 9092, 9093,
   50051, 50400 are already in use on the host.
5. Runs `docker compose up -d --build`, streaming progress.
6. Polls `https://localhost:8081/api/v1/status` until
   `nats.connected=true` and `redis.ok=true` (120-second timeout).
7. Verifies the config service responded (`/api/v1/config` 200).
8. Runs `tools/scripts/platform_smoke.sh` against the live stack —
   creates an approval-gated workflow, starts a run, approves the gate,
   and checks the run reaches `succeeded`.
9. Prints a status box with dashboard URL, gateway URL, login, and the
   list of exposed ports.

## What you get at the end

| Resource | Address |
|----------|---------|
| Dashboard | http://localhost:8082 |
| Gateway (HTTPS) | https://localhost:8081 |
| Gateway (gRPC) | localhost:9080 |
| Workflow Engine health | http://localhost:9093/readyz |
| Gateway metrics | http://localhost:9092 |
| Safety Kernel gRPC | localhost:50051 |
| Context Engine gRPC | localhost:50400 |
| NATS | localhost:4222 |
| Redis (TLS) | localhost:6379 |

**Dashboard login:** user `admin`, default password `ChangeMe123!` (also in
`.env` as `CORDUM_ADMIN_PASSWORD`). Rotate it before exposing the stack beyond
localhost by setting a new `CORDUM_ADMIN_PASSWORD` in `.env` (policy: ≥12
chars, with an uppercase letter, a digit, and a special character) and
restarting the gateway.

**API key:** in `.env` as `CORDUM_API_KEY`. Attach to every request:

```bash
source .env && export CORDUM_API_KEY
curl -sS --cacert ./certs/ca/ca.crt \
  -H "X-API-Key: $CORDUM_API_KEY" -H "X-Tenant-ID: default" \
  https://localhost:8081/api/v1/status | jq
```

## Prerequisites

| | Minimum | Recommended |
|---|---|---|
| Docker Desktop | v4.0+ | v4.30+ |
| Docker Engine + Compose v2 (Linux) | 20.10 + 2.0 | current |
| RAM allocated to Docker | 4 GB | 8 GB |
| Free disk | 5 GB | 10 GB |
| `go` | 1.24 (for cert generation only) | 1.25 |
| `curl`, `openssl` | any | any |
| `jq` | — | for dashboard-free API exploration |

### Platform notes

- **Windows** — run under **Git Bash**, **MSYS2**, or **WSL2**. The
  script uses Unix shell syntax (`/dev/null`, forward slashes). Docker
  Desktop's WSL2 backend is supported.
- **macOS** — Docker Desktop is the supported path (both Intel and Apple
  Silicon).
- **Linux** — Docker Engine + the `docker compose` plugin (the `v2`
  Python `docker-compose` binary also works but is discouraged).

## Useful flags

```bash
# Tear the previous stack down first (clean deploy).
./tools/scripts/quickstart.sh --clean

# Skip the Docker build and reuse existing images.
./tools/scripts/quickstart.sh --skip-build

# Skip the smoke test (script exits after the stack is healthy).
./tools/scripts/quickstart.sh --skip-smoke

# Wait longer for health readiness on slow hosts.
./tools/scripts/quickstart.sh --health-timeout 300

# Capture compose status, service logs, and an env snapshot to a dir.
./tools/scripts/quickstart.sh --artifacts-dir ./deploy-artifacts
```

Environment overrides the script honors:

| Variable | Purpose |
|----------|---------|
| `CORDUM_API_KEY` | Reuse a specific API key instead of generating one. |
| `CORDUM_TENANT_ID` | Default tenant (`default`). |
| `CORDUM_ORG_ID` | Defaults to `CORDUM_TENANT_ID`. |
| `REDIS_PASSWORD` | Reuse a specific Redis password instead of generating one. |
| `CORDUM_SKIP_BUILD=1` | Same as `--skip-build`. |
| `CORDUM_COMPOSE_FILES` | Space-separated list of compose files to chain. |
| `CORDUM_ALLOW_ENTERPRISE=1` | Allow the enterprise compose override (OSS-only by default). |

## Under the hood

- `.env` is read by `docker compose` automatically. Everything in it is
  picked up by the services.
- TLS everywhere: the stack uses `rediss://` for Redis, `tls://` for
  NATS, `https://` for the gateway. The CA lives at
  `./certs/ca/ca.crt`; use it in clients (`curl --cacert`,
  `CORDUM_TLS_CA` env var, SDK `TLSOptions.CACertPath`).
- Self-signed certs: your browser shows a warning on the dashboard. Dev
  only — generate real certs before production.
- `platform_smoke.sh` exercises the real governance path
  (workflow → approval-gate job → approval → run succeeds). If it
  passes, the stack is wired end-to-end.

## Teardown

```bash
docker compose down          # keep volumes (quick restart)
docker compose down -v       # wipe NATS + Redis volumes
```

To start over completely, also delete `.env` and `./certs/` — the next
`quickstart.sh` run will regenerate both.

## Troubleshooting

| Issue | Fix |
|-------|-----|
| `missing dependency: docker` | Install Docker Desktop / Engine and make sure it's on `PATH`. |
| `cannot connect to the Docker daemon` | Start Docker Desktop, or `sudo systemctl start docker` on Linux. |
| `warning: port 8081 (api-gateway http) is already in use` | Stop the conflicting service (`lsof -i :8081` / `ss -ltnp \| grep 8081`) or change the host port in `docker-compose.yml`. |
| `Go 1.24+ required` | Upgrade Go (https://go.dev/dl/) — only needed for the first-run cert generation. |
| `health check timed out` | Re-run with `--health-timeout 300`; inspect `docker compose logs api-gateway` for the real error. |
| TLS / certificate errors in the browser | Accept the self-signed cert for local dev, or delete `./certs/` and re-run to regenerate. |
| Dashboard empty | Normal on a fresh install — run the smoke test (step 8 above) or create a workflow from the dashboard UI. |
| Docker out of memory | Allocate ≥ 4 GB RAM to Docker Desktop; reduce parallel builds with `COMPOSE_PARALLEL_LIMIT=1`. |

More troubleshooting scenarios: [troubleshooting.md](troubleshooting.md).

## Manual walkthrough

If you want to see every API call the stack goes through (instead of
letting `quickstart.sh` do it), use the detailed step-by-step in
[cordumctl.md](cordumctl.md) and
[workflow-step-types.md](workflow-step-types.md) — those docs walk the
same path using the REST API and `cordumctl` subcommands.

## Next steps

- **Install the hello pack** — a minimal, self-contained example pack
  with a Go worker that echoes input. See
  [examples/hello-pack/README.md](../examples/hello-pack/README.md).
- **Explore the dashboard** — http://localhost:8082 for jobs,
  approvals, workflows, audit trail.
- **Read the concepts** — [docs/system_overview.md](system_overview.md),
  [docs/safety-kernel.md](safety-kernel.md),
  [docs/output-safety.md](output-safety.md).
- **Make it yours** — add rules in `config/safety.yaml` and watch them
  hot-reload every 5 seconds (see
  [`SAFETY_POLICY_RELOAD_INTERVAL`](safety-kernel.md#hot-reload)).
