# a2a-governed — A2A turns governed by Cordum, end to end

This demo runs the CAP A2A bridge (`cap-a2a-bridge`) and its example worker
against a Cordum stack. Every A2A turn becomes a Cordum job that the scheduler
and the safety kernel govern. `integration_test.sh` drives the demo, and the
`demo-a2a-governed-e2e` workflow runs it in CI.

## What it proves

| Output line | What it checks |
|-------------|----------------|
| `PASS allowed_turn_input_required_cordum_allow` | An allowed first turn ends `TASK_STATE_INPUT_REQUIRED`. Its Cordum job is `SUCCEEDED`, and `GET /api/v1/jobs/{id}/decisions` holds an `ALLOW` decision by rule `a2a-governed-allow`. |
| `PASS resumed_turn_completed_cordum_allow` | The interrupted task, resumed with a second message, ends `TASK_STATE_COMPLETED` with one `itinerary` artifact. The second job has its own `ALLOW` decision. |
| `PASS denied_topic_rejected_cordum_denied` | A turn on the denied topic ends `TASK_STATE_REJECTED`. Its Cordum job is `DENIED` by rule `a2a-governed-denied`, and it is missing from `GET /api/v1/workers/a2a-demo-worker/jobs`. |
| `PASS final_invariants` | The worker and both bridges still run, the task is still `COMPLETED`, and none of the three credentials appears in any saved artifact. |

Then the script prints `A2A GOVERNED DEMO OK` once. The workflow fails unless
the marker appears exactly once and there are exactly 4 `PASS` lines.

## Topology

- Cordum runs as the default `docker compose` services, with no profile and no
  license. The scheduler uses the compose defaults `CORDUM_CAP_PROFILE=compat`
  and `CORDUM_SDK_HANDSHAKE=off`.
- On the host, the script runs:
  - one example worker, `a2a-demo-worker`, in pool `a2a-governed` on topic
    `job.a2a-governed.turns`;
  - two bridges, because one bridge serves one topic. The turns bridge listens
    on `https://localhost:8443`, and the denied bridge on
    `https://localhost:8444`.
- Every hop uses TLS:
  - the bridges serve HTTPS with `certs/server`;
  - the bridges and the worker reach NATS at `tls://localhost:4222` with the
    `certs/client` pair, because the compose NATS requires client
    certificates;
  - they reach Redis at `rediss://localhost:6379`.

  `CAP_A2A_DEV_INSECURE` is never set, and the script unsets it.
- curl sends JSON-RPC `SendMessage` and `GetTask` to the URL in each bridge's
  agent card, with the card's `protocolVersion` as the `A2A-Version` header.

## Identity

- The bridge's `CAP_A2A_API_KEYS_FILE` holds only the SHA-256 of
  `CAP_A2A_CLIENT_API_KEY`, mapped to tenant `default` and principal
  `a2a-demo-client`. The client sends the key as `Authorization: Bearer`.
- The script computes each Cordum job id itself, using the
  [idempotency key](https://github.com/cordum-io/cap/blob/3216b28090a869c37f595d36755385adf80cb500/spec/interop/a2a-mapping.md#31-idempotency-key)
  of the CAP mapping. Its inputs are tenant, principal, task ref (empty on a
  new task) and `messageId`. The job must exist under exactly that id, so
  the lookup proves which identity the bridge bound to the turn.
- Each job's `workflow_id` is the A2A task id.

## The pack

`pack/` registers 2 topics, 1 pool and 2 policy rules:

- Topics `job.a2a-governed.turns` and `job.a2a-governed.denied`. Both must be
  registered, because the scheduler fails a job on an unregistered topic as
  "unknown topic" before the safety check. So an unregistered denied topic
  would end `FAILED`, not `DENIED`.
- Both topics route to pool `a2a-governed`. So only the deny rule keeps the
  denied job away from the worker.
- Rules `a2a-governed-denied` (deny), then `a2a-governed-allow` (allow). The
  safety kernel applies the first rule that matches.

The script installs the pack with `cordumctl pack install --upgrade`. Then it
waits until `POST /api/v1/policy/evaluate` returns both rules.

## Limitations

This demo runs only on Cordum's compat profile:

- **Admission.** The bridge publishes raw `sys.job.submit` packets with its own
  `sender_id`. The scheduler admits them only while `CORDUM_SDK_HANDSHAKE` is
  `off` or `warn` (`verifyReservedServiceAuthority` in
  [engine_active_authority.go](../../core/controlplane/scheduler/engine_active_authority.go)).
  Under `enforce` and on CAP-PRODUCTION it rejects them.
- **Input.** Each turn's input travels as a legacy `context_ptr` to
  `ctx:<job_id>` in Redis. The scheduler reads that pointer only off the
  production profile
  ([CAP mapping §8](https://github.com/cordum-io/cap/blob/3216b28090a869c37f595d36755385adf80cb500/spec/interop/a2a-mapping.md#8-bridge-placement-and-admission)).
- **Unsigned packets.** Compat publishes unsigned packets, so the bridges and
  the worker run with `CAP_A2A_DEV_UNSIGNED=1` and without
  `CAP_A2A_RESULT_KEYS_DIR`. Trust therefore rests on network position. Any
  client that can connect to NATS can publish job requests or job results on
  these subjects; here, that is any holder of a `certs/client` certificate.
- **Output safety.** The bridge maps the worker's `JobResult` from
  `sys.job.result` and reads `res:<job_id>` itself
  ([egress.go](https://github.com/cordum-io/cap/blob/3216b28090a869c37f595d36755385adf80cb500/internal/a2abridge/egress.go#L102-L123)).
  It does not wait for Cordum's output-safety verdict, so the A2A task does
  not reflect that verdict.
- **No gateway front.** Fronting the bridge with the Cordum gateway is a
  follow-up. It is not available yet.

The bridge's own settings and limits are in the CAP
[A2A bridge README](https://github.com/cordum-io/cap/blob/3216b28090a869c37f595d36755385adf80cb500/examples/a2a-bridge/README.md).

## Run it locally

You need Linux or WSL (Git Bash is not supported), Docker, Go, jq, curl,
openssl and python3. Ports 8443 and 8444 must be free. From the repo root:

```bash
CAP_PIN="$(sed -n 's/^ *ref: *\([0-9a-f]\{40\}\) *$/\1/p' .github/workflows/demo-a2a-governed-e2e.yml)"
git clone https://github.com/cordum-io/cap.git ../cap && git -C ../cap checkout --detach "$CAP_PIN"
go build -C ../cap -trimpath -o "$PWD/bin/cap-a2a-bridge" ./cmd/cap-a2a-bridge
go build -C ../cap -trimpath -o "$PWD/bin/cap-a2a-worker" ./examples/a2a-bridge/worker
go build -o bin/cordumctl ./cmd/cordumctl
./bin/cordumctl generate-certs --dir ./certs && chmod -R a+rX ./certs
export CORDUM_API_KEY="$(openssl rand -hex 32)" CAP_A2A_CLIENT_API_KEY="$(openssl rand -hex 32)" \
  REDIS_PASSWORD="$(openssl rand -hex 32)"
docker compose up -d --build
CORDUM_INTEGRATION=1 CORDUM_TLS_CA="$PWD/certs/ca/ca.crt" bash demo/a2a-governed/integration_test.sh
docker compose down -v
```

- Exit codes: 0 means pass, 1 means an assertion failed, and 2 means a
  prerequisite is missing.
- The script saves every response and process log to `A2A_ARTIFACT_DIR`
  (default: a new temp directory) and prints that path on exit.
- You can rerun it on the same stack, because every run uses new message ids.

## CI

[`demo-a2a-governed-e2e.yml`](../../.github/workflows/demo-a2a-governed-e2e.yml)
runs:

- nightly;
- on demand;
- on pushes and pull requests to `main` that change the workflow or this
  directory.

It mints the three credentials with `openssl rand`, masks them, and redacts
them from failure artifacts. `tools/scripts/workflow_credential_guard.py`
checks that masking.

**Bumping the CAP pin.** The workflow's "Check out the pinned CAP" step pins
`cordum-io/cap` at a full commit SHA. To bump it:

1. Replace that `ref:` with a commit on CAP `main` that still has
   `cmd/cap-a2a-bridge` and `examples/a2a-bridge/worker`.
2. Update the comment above the `ref:`.
3. Run the demo locally against the new pin.
