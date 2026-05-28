# multi-topic-worker-go

Canonical pattern for a Cordum worker that handles **more than one topic** from
the same process.

## Why this exists

`examples/hello-worker-go` is the minimal "one handler, one topic" template. It
registers its single handler on the topic subject AND on the per-worker direct
subject (`worker.<id>.jobs`). That works because there is only one handler
type to invoke.

The naive copy-paste for a worker with multiple typed handlers is unsafe:

```go
runtime.Register(agent, "job.app.a", handlerA)                       // Handler[InA, OutA]
runtime.Register(agent, "job.app.b", handlerB)                       // Handler[InB, OutB]
runtime.Register(agent, runtime.DirectSubject(id), handlerA)         // ← bug
```

The last line silently binds `handlerA` to every job the scheduler dispatches
via the direct subject — regardless of `JobRequest.Topic`. A workflow's
`job.app.b` step runs `handlerA`, succeeds with garbage output (handlerA happens
to JSON-encode *something*), and the next step fails on parse.

## The fix

One topic-aware **dispatcher** registered on every subject — both topic
subjects and the direct subject. The dispatcher reads `ctx.Job.Topic` and
routes to the right typed handler internally.

See [`main.go`](./main.go) — `makeDispatcher` + `invokeTyped` are the entire
pattern in ~40 lines.

## Adding a new topic

Three edits:

1. Define typed `In` / `Out` structs and a `Handler[In, Out]` for the new
   topic.
2. Add a `case "job.app.new":` arm to `makeDispatcher`.
3. Add the topic constant + a `runtime.Register(agent, topicNew, dispatcher)`
   in `main`.

The topic-aware dispatcher takes care of the direct-subject path automatically.

## Running locally

```bash
go build -o bin/multi-topic-worker ./examples/multi-topic-worker-go

WORKER_ID=multi-topic-worker \
NATS_URL=nats://127.0.0.1:4222 \
REDIS_URL=redis://:cordum-dev@127.0.0.1:6379/0 \
  ./bin/multi-topic-worker
```

For the TLS-enabled stack started by `make dev-up`, set the same
`NATS_TLS_*` / `REDIS_TLS_*` envs that `examples/hello-worker-go` uses
(`tls://nats:4222`, `rediss://:${REDIS_PASSWORD}@redis:6379`, plus the cert
paths under `/etc/cordum/tls`).

## Submitting jobs

Each topic's input shape matches its typed `In` struct:

```bash
curl -X POST http://localhost:8081/api/v1/jobs \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d '{"topic":"job.multi-topic.upper","context":{"text":"hello"}}'
# → {"text":"HELLO"}

curl -X POST http://localhost:8081/api/v1/jobs \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d '{"topic":"job.multi-topic.add","context":{"a":2,"b":3}}'
# → {"sum":5}

curl -X POST http://localhost:8081/api/v1/jobs \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d '{"topic":"job.multi-topic.tag","context":{"items":["a","b"],"tag":"x"}}'
# → {"tagged":["x:a","x:b"]}
```
