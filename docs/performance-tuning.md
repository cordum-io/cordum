# Performance Tuning Guide

Recommendations for optimizing Cordum gateway throughput in production deployments.

---

## Rate Limiter

The gateway rate limiter uses a **single Redis Lua script** per request (atomic INCR + conditional EXPIRE in a 1-second sliding window). This is already optimal — no WATCH/MULTI serialization.

**Tuning:**
- `API_RATE_LIMIT_RPS` — requests per second per API key (default: 2000, or tier limit)
- `API_RATE_LIMIT_BURST` — burst capacity (default: 2× RPS)
- Use `REDIS_RATE_LIMIT=false` to fall back to in-memory token bucket (lower latency, not distributed)

## Status Endpoint Cache

The `/api/v1/status` endpoint is polled by the dashboard every 5-10 seconds. Each call previously made 3+ Redis round-trips (PING, worker snapshot, pipeline job counts). A **2-second in-memory cache** now eliminates redundant Redis calls.

**Behavior:**
- First request within the TTL window fetches fresh data from Redis/NATS
- Subsequent requests within 2s return the cached response (`X-Cache: HIT` header)
- Cache auto-expires after TTL; also invalidated on state changes

**Tuning:**
- The cache TTL is hardcoded at 2 seconds — a good balance between freshness and throughput
- For high-polling dashboards (many browser tabs), this eliminates O(N) Redis calls where N = concurrent dashboard clients

## GOMAXPROCS

Go's runtime auto-detects CPU count from the OS, but in containers this can over-count (it sees the host's CPUs, not the cgroup limit). This causes excessive goroutine scheduling overhead.

**Recommendation:** Set `GOMAXPROCS` to match the container's CPU limit.

```yaml
# Helm values.yaml
gateway:
  goMaxProcs: "4"   # Match your CPU limit (e.g. 4000m → 4)
  resources:
    limits:
      cpu: 4000m
```

Alternatively, use [automaxprocs](https://github.com/uber-go/automaxprocs) as a Go dependency (reads cgroup limits automatically). The `GOMAXPROCS` env var is the simpler, dependency-free approach.

## Redis Connection Pooling

The gateway uses a single `redis.Client` per store (job store, user store, key store, RBAC store, etc.). Each client maintains its own connection pool.

**Tuning:**
- Redis connection pool size defaults to 10× GOMAXPROCS. Override with `REDIS_POOL_SIZE` if needed.
- Monitor Redis `connected_clients` — if it's near `maxclients`, increase the Redis server limit or reduce gateway replicas.

## Scaling Horizontally

When adding gateway replicas:
- Rate limiting is distributed via Redis — all replicas share the same counters
- Status cache is per-replica (not shared) — each replica caches independently
- Worker snapshots are read from a shared Redis key written by the scheduler
- WebSocket connections are per-replica — clients reconnect on pod restarts

**Expected scaling:** Near-linear up to the point where Redis becomes the bottleneck. With a dedicated Redis instance, 5 replicas should achieve 2000-3000 req/s aggregate at typical API workloads.

## Profiling

To profile the gateway under load:

```bash
# Enable pprof (add to gateway startup or set GATEWAY_PPROF_ADDR)
# Then capture a 30-second CPU profile:
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# Goroutine profile:
go tool pprof http://localhost:6060/debug/pprof/goroutine

# Heap profile:
go tool pprof http://localhost:6060/debug/pprof/heap
```

## Checklist

| Setting | Default | Recommended |
|---------|---------|-------------|
| `GOMAXPROCS` | auto (host CPUs) | Match CPU limit |
| `API_RATE_LIMIT_RPS` | 2000 | Tier limit or higher |
| `API_RATE_LIMIT_BURST` | 2× RPS | 2× RPS |
| `REDIS_RATE_LIMIT` | true (Redis) | true for distributed |
| Gateway replicas | 1 | 3-5 for production |
| Redis `maxclients` | 10000 | Monitor connected_clients |
