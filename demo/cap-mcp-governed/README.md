# cap-mcp-governed — a CAP worker that calls governed Cordum MCP tools

A CAP v2.17.0 worker that, for every job, reads its own job through Cordum's
MCP endpoint, checks platform status, then asks for a governed mutation
(`cordum_create_workflow`) and waits for a human to decide. The worker never
approves or rejects anything itself.

## Sequence

Each job opens its own MCP session (`X-MCP-Session-ID`, random per job) on
`POST <gateway>/mcp/message` and sends, in this order:

1. `initialize` (protocol version `2024-11-05`), then `notifications/initialized`.
2. `resources/read` of `cordum://jobs/<job_id>`, the job it is running.
3. `tools/call` `cordum_status`, a read-only tool.
4. `tools/call` `cordum_create_workflow` with the workflow from the job input
   and `idempotency_key` `cap-mcp-governed:<job_id>`.

If step 4 answers JSON-RPC `-32099` (approval required), the worker reads
`approval_id` from the error data and polls
`GET <gateway>/api/v1/mcp/approvals/{approval_id}`, the first time at once and
then every `CAP_MCP_POLL_INTERVAL`:

- `pending`: keep waiting until the job deadline (`CAP_MCP_JOB_TIMEOUT`).
- `approved`: send `cordum_create_workflow` again with byte-identical
  arguments, exactly once. There is never a third call.
- `rejected`, `expired`, a missing record or any other status: the job fails
  with the matching code below and nothing is retried.
- HTTP `429` (rate limited) on the poll: poll again after `Retry-After`
  seconds (at most 30), or without that header after a doubling backoff that
  starts at twice the poll interval (at most 30s), until the job deadline.

A failed read or status call stops the job before the mutation is requested.
The handler is registered with 0 runtime retries, because a retry would ask
for a new approval.

## Prerequisites

- A Cordum gateway with Redis. Without Redis no approval gate is wired, and
  every approval-required tool fails closed with JSON-RPC `-32097`.
- MCP enabled in the gateway config: `mcp.enabled=true` with `mcp.transport`
  `http` (the default) or `both`. `mcp.enabled` defaults to false, and then
  `POST /mcp/message` answers 503, so every job fails at `initialize` with
  `mcp_unavailable`. An admin can set it through the config API (see
  `docs/mcp-server.md`); the gateway reloads MCP at once:

  ```bash
  curl -sS -X POST http://localhost:8081/api/v1/config \
    -H "X-API-Key: $CORDUM_ADMIN_KEY" -H "X-Tenant-ID: default" \
    -H "Content-Type: application/json" \
    -d '{"mcp": {"enabled": true, "transport": "http"}}'
  ```

  This replaces the whole `mcp` object of the system config, so include any
  `mcp` settings that are already there.
- `mcp.policy_gate_enabled=false`, the default. In that mode a byte-identical
  retry after approval runs the call. With the policy gate on, only an
  `_approval_ref` argument resumes a gated call. This worker does not send
  one, so its retry is gated again and the job fails with `mcp_call_failed`.
- The compat CAP profile (`CORDUM_CAP_PROFILE` unset or `compat`), with
  `CORDUM_SDK_HANDSHAKE` unset or `off` for this worker, because the demo
  agent is unsigned.
- An API key for the worker in the target tenant with `workflows.write`. A key
  without it can still obtain an approval, but the approved call returns a
  403 tool error and the job fails with `mcp_call_failed`.
- An MCP agent identity linked to the worker key's principal in the same
  tenant. The gateway scope-filters every `tools/call`. The worker sends no
  `X-Agent-Id`, so the gateway looks the identity up by the authenticated
  principal ID, in the request tenant only. Without that link
  `cordum_status` is denied with JSON-RPC `-32098`, whatever permissions the
  key has, and the job fails with `mcp_call_failed` before any approval is
  requested. This needs two things:

  - The key itself carries a principal ID, because the worker sends no
    `X-Principal-Id` either. For a key in `CORDUM_API_KEYS` that is the
    `principal_id` field of its JSON entry.
  - An identity created with `POST /api/v1/agents` under the worker's
    `X-Tenant-ID`, with `agent_id` set to that principal ID. That call
    creates the identity and the link in one step. It needs `agents.write`
    or the `admin` role, and the `agent_identity` license entitlement:

    ```bash
    curl -sS -X POST http://localhost:8081/api/v1/agents \
      -H "X-API-Key: $CORDUM_ADMIN_KEY" -H "X-Tenant-ID: default" \
      -H "Content-Type: application/json" \
      -d '{"name": "cap-mcp-governed", "owner": "demo",
           "agent_id": "<worker key principal_id>", "risk_tier": "high",
           "allowed_tools": ["cordum_status", "cordum_create_workflow"]}'
    ```

    `allowed_tools` must admit both tools; an empty list admits none.
    `risk_tier` must be `high` or `critical`: `cordum_status` declares no
    tier, which the scope filter treats as `high`
    (`cordum_create_workflow` is `medium`).
- A route from the topic to the pool in the scheduler's pool map
  (`config/pools.yaml`):

  ```yaml
  topics:
    job.cap-mcp-governed.create-workflow: cap-mcp-governed
  pools:
    cap-mcp-governed:
      requires: []
  ```

## Operator step

While a job waits, a different principal with `jobs.approve` or the `admin`
role resolves the approval, either with
`POST /api/v1/mcp/approvals/{approval_id}/approve` (or `/reject`), or from the
MCP approvals list on the dashboard's `/approvals` page. The principal that
requested the call cannot approve it (`self_approval_denied`). The worker logs
the id it waits for:

```
INFO approval required job_id=<job_id> ... approval_id=<approval_id>
```

## Run

```bash
export CORDUM_GATEWAY_URL=http://localhost:8081
export CORDUM_API_KEY=<worker key with workflows.write>
export CORDUM_TENANT_ID=default
go run ./demo/cap-mcp-governed/worker
```

`http://localhost:8081` is the gateway's default endpoint. For a gateway that
serves TLS, use its `https://` URL and, when its certificate comes from a
private CA, set `CORDUM_TLS_CA=./certs/ca/ca.crt`.

| Variable | Default | Meaning |
|----------|---------|---------|
| `CORDUM_GATEWAY_URL` | required | Gateway base URL. `https`, or `http` only for `localhost`, `::1` or `127.0.0.0/8`. No userinfo, query, fragment or path. |
| `CORDUM_API_KEY` | required | Sent only as the `X-API-Key` header. No whitespace or control characters. Never logged or put in an error. |
| `CORDUM_TENANT_ID` | required | Sent as `X-Tenant-ID`. Matches `[A-Za-z0-9._-]{1,128}`. |
| `CORDUM_TLS_CA` | system roots | Extra PEM CA bundle for the gateway certificate. |
| `CAP_MCP_JOB_TIMEOUT` | `10m` | Deadline for one job, approval wait included. `1s` to `1h`. |
| `CAP_MCP_POLL_INTERVAL` | `2s` | Approval poll interval. `100ms` to `1m`, and shorter than the job timeout. |
| `NATS_URL` | `nats://127.0.0.1:4222` | Bus. `NATS_TOKEN` and `NATS_TLS_CA`, `NATS_TLS_CERT`, `NATS_TLS_KEY`, `NATS_TLS_SERVER_NAME` apply. |
| `REDIS_URL` | `redis://:cordum-dev@127.0.0.1:6379/0` | Job context and result store, shared with Cordum. |

Invalid settings stop the worker at start with an error that names the
variable. The gateway client requires TLS 1.2 or later, honors
`HTTPS_PROXY`/`NO_PROXY`, and does not follow redirects, so the API key is
only sent to the configured gateway.

## Subjects and job input

The handler listens on the pool topic `job.cap-mcp-governed.create-workflow`
and on the worker's direct subject `worker.cap-mcp-governed.jobs`, where
Cordum's scheduler dispatches. The worker advertises pool `cap-mcp-governed`,
capability `cap-mcp-governed.create-workflow` and one job at a time.

The job input carries the workflow in exactly one of two places. Direct form:

```json
{"workflow": {"name": "governed-demo",
              "description": "created by the cap-mcp-governed demo",
              "steps": {"notify": {"type": "notify", "input": {"message": "hello"}}}}}
```

Gateway REST form, as stored for `POST /api/v1/jobs` with a `context` object:

```json
{"topic": "job.cap-mcp-governed.create-workflow", "prompt": "",
 "context": {"workflow": {"name": "governed-demo",
                          "steps": {"notify": {"type": "notify", "input": {"message": "hello"}}}}}}
```

Both forms send the same `cordum_create_workflow` arguments. `name` is
trimmed and must be 1 to 128 bytes without control characters, `steps` must
be a non-empty object, and the encoded arguments must fit in 64 KiB. Input
that does not decode into this shape at all (for example `steps` that is not
an object) is refused by the CAP runtime before the handler runs, with the
runtime's own message.

A succeeded job stores its result at `res:<job_id>`:

```json
{"workflow_id": "<id>", "approval_id": "<approval_id>"}
```

`approval_id` is omitted when the call ran without an approval.

## Error codes

A failed job's `error_message` is `<code>: <fixed sentence>`. It never carries
gateway text, response bodies or the API key.

| Code | Meaning |
|------|---------|
| `invalid_input` | The job id or workflow input failed validation. No request was sent. |
| `mcp_unavailable` | The gateway was unreachable, timed out or answered 5xx, including the approval poll. |
| `mcp_protocol_error` | The response broke the JSON-RPC or MCP contract: wrong `jsonrpc` or id, both or neither of result and error, a body over 1 MiB, a protocol version other than `2024-11-05`, a redirect, or a `-32099` without a valid `approval_id`. |
| `mcp_call_failed` | The gateway refused the request (other 4xx), or a call returned a JSON-RPC error or an `isError` result, or the create result had no `workflow_id`, or the retry after approval did not create the workflow. |
| `mcp_approval_rejected` | An operator rejected the approval. |
| `mcp_approval_expired` | The approval expired before anyone decided. |
| `mcp_approval_not_found` | The approval record does not exist (404). |
| `mcp_approval_timeout` | The job deadline passed while waiting for the decision or during the retry. |
| `mcp_approval_invalid` | The approval record had a status other than pending, approved, rejected or expired. |
| `worker_shutdown` | The worker was stopping (SIGINT or SIGTERM) while the job ran. |

## Limitations

- Unsigned agent, compat CAP profile only.
- `_approval_ref` is not used, so the policy gate must stay off (see
  Prerequisites).
- One job at a time: the handler blocks while it waits for a human.
- Approvals are consume-once. The gateway does not honor `idempotency_key`
  for `cordum_create_workflow`, so the consumed approval is what limits the
  retry to one workflow.
- A retry that is gated again fails the job and can leave a new pending
  approval behind.

## Tests

```bash
go test ./demo/cap-mcp-governed/...
```

The tests deliver jobs over an embedded NATS server to a fake gateway built
from the `core/mcp` wire types, and check the call order, the byte-identical
single retry, every error code and that the worker never calls approve or
reject.
