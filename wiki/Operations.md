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

## Scaling notes

- Gateway: horizontally scalable behind a service/load balancer.
- Scheduler: run a single active instance unless you implement leader locks.
- Safety Kernel: can be replicated for gRPC throughput.
- NATS + Redis: use HA deployments with persistence in production.
