# Hello Worker (Go)

Minimal CAP worker that listens on `job.hello-pack.echo` and writes results to Redis.

## Run

From the repo root:

```bash
cd examples/hello-worker-go

# Uses the local stack defaults
export NATS_URL=nats://localhost:4222
export REDIS_URL=redis://localhost:6379/0

# Optional overrides
# export CORDUM_POOL=hello-pack
# export CORDUM_SUBJECT=job.hello-pack.echo

go run .
```

## What it does

- Reads the workflow input from `ctx:<job_id>` in Redis
- Writes the output to `res:<job_id>` so the workflow engine can inline results
- Returns a CAP `JobResult` with the result pointer
