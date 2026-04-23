# E2E testing the local TLS stack

`tools/scripts/e2e_test.sh` exercises the full local stack through the
dashboard proxy on `http://localhost:8082` and the gateway on
`https://localhost:8081` when dev certificates are present.

## Prerequisites

- Docker Compose plugin available as `docker compose`.
- Dev certificates generated at `./certs/ca/ca.crt`:
  `go run ./cmd/cordumctl generate-certs --dir ./certs`.
- The default compose stack running with TLS-enabled NATS and Redis:
  `CORDUM_API_KEY=<key> REDIS_PASSWORD=<password> docker compose up -d --build`.
- For the no-skip gate used in CI, enable password auth for the admin session
  checks: set `CORDUM_USER_AUTH_ENABLED=true`, `CORDUM_ADMIN_USERNAME=admin`,
  and a strong `CORDUM_ADMIN_PASSWORD`.
- `CORDUM_API_KEY` exported in the shell running the test.
- `jq`, `curl`, and Go available. The script starts
  `examples/hello-worker-go` with `go run .`. `redis-cli` is optional when
  running next to the Compose stack; when Redis TLS is detected the host
  `redis-cli` probe uses `--tls --cacert` (and client cert/key if present),
  otherwise the script falls back to `docker compose exec redis redis-cli`.

## Command

```bash
export CORDUM_API_KEY="${CORDUM_API_KEY:?set an API key}"
export REDIS_PASSWORD="${REDIS_PASSWORD:-cordum-dev}"
export CORDUM_USER_AUTH_ENABLED=true
export CORDUM_ADMIN_USERNAME=admin
export CORDUM_ADMIN_PASSWORD="${CORDUM_ADMIN_PASSWORD:?set an admin password}"
bash tools/scripts/e2e_test.sh
```

Expected result: all phases report `PASS` and the final summary reports
`Failed: 0`; with admin auth enabled and Redis reachable, the current suite
has zero skipped checks.

## What the setup phase does

- Auto-detects `./certs/ca/ca.crt` and uses `tls://` for NATS plus `rediss://`
  for Redis when present.
- Passes `NATS_TLS_*` and `REDIS_TLS_*` variables through to
  `examples/hello-worker-go`; when `./certs/client/tls.{crt,key}` exists,
  the script defaults both NATS and Redis client cert/key env vars to those
  files.
- Installs `./examples/hello-worker-go/pack` before Phase 4 so
  `job.hello-pack.echo` is present in the topic registry. Missing
  hello-worker readiness or missing Phase 4 job completion is a hard failure,
  not a skip. The readiness gate reads the canonical `/api/v1/workers`
  `items` shape, so a connected worker cannot be hidden by response-shape
  drift.

## Troubleshooting

- **Phase 4 fails before job dispatch**: run
  `cordumctl pack list --gateway https://localhost:8081 --cacert ./certs/ca/ca.crt --api-key "$CORDUM_API_KEY" --tenant "${CORDUM_TENANT_ID:-default}" | grep hello-pack`.
  If absent, rerun
  `cordumctl pack install --gateway https://localhost:8081 --cacert ./certs/ca/ca.crt --api-key "$CORDUM_API_KEY" --tenant "${CORDUM_TENANT_ID:-default}" ./examples/hello-worker-go/pack`.
- **NATS or Redis connection errors**: confirm the script detected
  `./certs/ca/ca.crt`; the worker should log `nats_scheme=tls` and
  `redis_scheme=rediss`.
- **`unknown_topic` response**: inspect the response body's
  `registered_topics`, `truncated`, and `topics_endpoint` fields. The list
  should include `job.hello-pack.echo` after pack install.
- **Gateway TLS errors**: export `CORDUM_TLS_CA=./certs/ca/ca.crt` or rerun
  certificate generation.
