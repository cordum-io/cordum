# Operations

## Health checks

- Gateway: `GET /api/v1/status`
- Workflow engine: `http://localhost:9093/health`

## Smoke tests

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
CORDUM_TENANT_ID=${CORDUM_TENANT_ID:-default} \
bash ./tools/scripts/platform_smoke.sh
```

If your filesystem is mounted `noexec`, prefix the script with `bash` as above.

## Metrics

- Gateway: `:9092/metrics`
- Scheduler: `:9090/metrics`

In production, metrics bind to loopback unless you set:
`GATEWAY_METRICS_PUBLIC=1` or `SCHEDULER_METRICS_PUBLIC=1`.

## Logs

All services log to stdout/stderr. Aggregate with your preferred log collector.

## Config auto-bootstrap

On first startup, the gateway and scheduler create the `system/default` config
document in Redis with minimal safety and rate-limit defaults. This is idempotent
and safe to run on upgrades.

**Helm:** A post-install/post-upgrade Job verifies the config endpoint returns `200`.
Disable with `--set configBootstrap.enabled=false`.

**Troubleshooting empty settings page:**
1. Check `GET /api/v1/config` returns `200` (not `404` or `500`).
2. If `404`, restart the gateway pod to trigger auto-bootstrap.
3. If Redis is unreachable, fix connectivity — the bootstrap is non-fatal on startup
   but the config will be empty until Redis is available.
4. Manually seed config: `POST /api/v1/config -d '{"safetyStance":"balanced"}'`.

## Scaling notes

Tags: scaling, scheduler, availability

- Gateway: horizontally scalable behind a service/load balancer.
- Scheduler: horizontally scalable; NATS queue groups + Redis locks gate dispatch/reconciler/replay work.
- Safety Kernel: can be replicated for gRPC throughput.
- NATS + Redis: use HA deployments with persistence in production.
