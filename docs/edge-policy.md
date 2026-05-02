# Cordum Edge policy templates

Cordum Edge uses the existing Safety Kernel policy evaluator for coding-agent
actions. EDGE-008/009 normalize raw Claude Code hook input into deterministic
policy inputs before evaluation:

- **Topic:** `job.edge.action`
- **Capability:** classifier-owned category such as `exec.shell`, `file.read`,
  `file.write`, or `edge.unknown`
- **Risk tags:** classifier-owned tags such as `test`, `build`, `secrets`,
  `destructive`, `write`, `git`, `network`, and `unknown`
- **Labels:** bounded labels such as `hook.tool_name`, `command.class`,
  `command.family`, `path.class`, and `unknown.impact`

The `job.edge.action` topic is a Safety Kernel compatibility namespace. It is
not a Cordum Job topic, job progress event, queue name, or worker-dispatch
contract. Edge actions remain `EdgeSession -> AgentExecution ->
AgentActionEvent` evidence records.

## Classifier mapping

| Action type | Capability | Risk tags | Key labels | Policy behavior in demo fragment |
|---|---:|---|---|---|
| Bash `npm test`, `npm run test`, `go test`, `pytest`, `vitest` | `exec.shell` | `exec`, `test` | `hook.tool_name=Bash`, `command.class=safe`, `command.family=test` | Allow via `claude-code.allow-safe-build-test` |
| Bash `npm run build`, `go build`, `make build` | `exec.shell` | `exec`, `build` | `hook.tool_name=Bash`, `command.class=safe`, `command.family=build` | Allow via `claude-code.allow-safe-build-test` |
| Bash recursive delete such as `rm -rf` | `exec.shell` | `destructive`, `exec`, `filesystem` | `command.class=destructive`, `command.family=filesystem_delete` | Deny via `claude-code.deny-destructive-shell` |
| Claude `Read` of `.env`, keys, tokens, credentials | `file.read` | `filesystem`, `read`, `secrets` | `hook.tool_name=Read`, `path.class=secret` | Deny via `claude-code.deny-secret-reads` |
| Claude `Edit`/`Write`/`MultiEdit` source file | `file.write` | `filesystem`, `source_code`, `write` | `hook.tool_name=Edit`, `path.class=source_code` | Require approval via `claude-code.require-approval-for-edits` |
| Bash `git push ...` | `exec.shell` | `deploy`, `git`, `network` | `command.class=deploy`, `command.family=git_push` | Require approval via `claude-code.require-approval-for-vcs-push` |
| Bash `curl`, `wget`, `ssh`, `nc` network egress | `exec.shell` | `exec`, `network` | `command.class=network`, `command.family=network_egress` | Require approval via `claude-code.require-approval-for-network` |
| Unknown high-impact hook action | `edge.unknown` | `destructive`, `review_required`, `unknown` | `unknown.impact=high` | Deny via `claude-code.deny-unknown-high-risk` |

The policy fragments do not match raw nested hook fields such as
`tool_input.command`. The Gateway/classifier owns raw parsing and redaction; the
Safety Kernel sees normalized metadata and bounded redacted input only.

## Redacted fixture example

`examples/cordum-edge-pack/fixtures/policy-simulations.json` carries synthetic,
redacted Edge events. A shortened example:

```json
{
  "name": "read_dotenv",
  "event": {
    "event_id": "evt-edge-sim-read-dotenv",
    "session_id": "sess-edge-sim-demo",
    "execution_id": "exec-edge-sim-demo",
    "tenant_id": "tenant-edge-demo",
    "principal_id": "principal-edge-demo",
    "layer": "hook",
    "kind": "hook.pre_tool_use",
    "agent_product": "claude-code",
    "tool_name": "Read",
    "input_redacted": {
      "file_path": ".env"
    },
    "decision": "RECORDED",
    "status": "ok"
  },
  "expected_decision": "DENY",
  "expected_rule_id": "claude-code.deny-secret-reads",
  "expected_approval_required": false
}
```

Do not place real `.env` contents, credentials, tokens, raw hook payloads,
transcripts, or tool results in fixtures or docs.

## Demo vs production-oriented fragments

- `examples/cordum-edge-pack/overlays/policy.fragment.yaml` is demo-oriented:
  it denies secret reads and destructive shell commands, requires approval for
  file edits, git push, and generic network egress, and allows safe local
  tests/builds.
- `examples/cordum-edge-pack/overlays/policy.production.fragment.yaml` is
  narrower: it keeps deny-by-default behavior for secrets, destructive shell,
  and unknown high-risk actions; requires approval for source-code edits, git
  push, and generic network egress; and allows safe local tests/builds with
  explicit constraints.

The production-oriented fragment is not a complete enterprise enforcement
boundary. Managed Claude settings, `cordum-agentd`, short-lived tokens,
OS/tenant controls, audit retention, and tenant-specific policy review are
still required for enterprise deployment.

## Approval retry and optional inline wait contract

EDGE-012 defines how an Edge action that requires human approval is run. The
default UX is immediate `REQUIRE_APPROVAL` + retry coordinates; an opt-in
inline wait is available for local/demo callers.

### Default flow: immediate deny + approve-then-retry

`POST /api/v1/edge/evaluate` for an action that the policy gates with
`require_human` returns `decision=REQUIRE_APPROVAL` immediately along with
the coordinates a caller needs to retry once a human approves:

| Response field | Purpose |
|---|---|
| `approval_ref` | Server-generated ID with prefix `edge_appr_`. The caller passes this back in a retry request to consume the approval. |
| `approval_url` | Dashboard path of the form `/edge/approvals/<approval_ref>` for human reviewers. |
| `action_hash` | `sha256:<hex>` over the canonical action (tenant, session, execution, principal, layer, kind, tool, action_name, capability, risk_tags, labels, input_hash, policy_snapshot). Server-derived; client-supplied hashes are NOT trusted. |
| `input_hash` | `sha256:<hex>` over the redacted input, set by the EDGE-004 redactor. |
| `policy_snapshot` | The Safety Kernel snapshot identifier the approval is bound to. |
| `wait_strategy` | `manual_approval` — caller blocks the action, surfaces the approval to a human. |
| `wait_after` | `approve_then_retry` — once the human approves, the caller re-issues the same evaluate body with `approval_ref` populated. |
| `terminal_message` | Concise hook/agentd terminal copy: "approval required. This action was not run. Approval: edge_appr_…. Approve it in Cordum, then retry the command." |

The decision event for the request is persisted with the redacted input and
hashes. The pending `EdgeApproval` is enqueued with the same
`tenant/session/execution/action_hash/policy_snapshot` tuple, so repeated
evaluates of the same action reuse the same approval rather than spamming new
ones.

### Retry: consume-once via the same evaluate endpoint

Once the approval is approved (via the dashboard or API), the caller re-issues
`POST /api/v1/edge/evaluate` with the same body plus an `approval_ref` field.
The gateway:

1. Recomputes `action_hash` against the **fresh** safety policy_snapshot.
2. Loads the stored `EdgeApproval` and switches on its status:
   - `approved` + matching `action_hash` + matching `policy_snapshot` →
     `ALLOW` once. The store atomically marks `consumed_at` under WATCH/MULTI;
     the response carries `decision=ALLOW`, `permission_decision=allow`,
     `exit_code=0`, and the consumed approval's hashes for traceability.
   - `approved` + mismatched action_hash or policy_snapshot →
     `DENY` "approval action or policy snapshot mismatch; request a new approval".
     The original approval is **not** consumed and may still be claimed by a
     valid retry.
   - `approved` + already consumed → `DENY` "approval already consumed; request
     a new approval".
   - `rejected` → `DENY` echoing `approval.resolution_reason`.
   - `expired`/`invalidated` → `DENY` "approval expired; request a new approval".
   - `pending` → `REQUIRE_APPROVAL` with the same `approval_ref` (still waiting).

The CAS uses the **stored** `session_id`, `execution_id`, and `event_id` from
the original approval — the retry's freshly-appended evidence event has a new
`event_id` that the approval was never bound to. This is intentional: the
binding to the original event is part of the action's identity.

If the fresh safety decision is anything other than `REQUIRE_APPROVAL`
(`ALLOW`, `DENY`, `THROTTLE`, `CONSTRAIN`), the approval is not consumed: a
fresh `DENY` must win over a stale approval, and a fresh `ALLOW` does not
need one. The approval's lifecycle continues until explicitly resolved or it
expires.

### Optional inline wait (opt-in, demo only)

For local agentd or interactive demo callers that prefer a single blocking
RPC to poll-and-retry, `/api/v1/edge/evaluate` and a standalone
`POST /api/v1/edge/approvals/{approval_ref}/wait` endpoint accept opt-in
inline-wait fields:

- `wait_for_approval: true` (evaluate request only) — after enqueuing or
  resolving the approval, the handler bound-waits for the approval to leave
  Pending, then routes through the same consume-once CAS.
- `approval_wait_timeout_ms` (evaluate or `/wait` body) — caller-requested
  wait budget. Server clamps to a 5-minute maximum and uses 30 seconds when
  omitted or non-positive. Non-positive values fall back to the default; values
  larger than the cap are silently clamped.

The wait helper polls the EDGE-011 store every 250 ms with `context.WithTimeout`
and `time.NewTicker`, both released via `defer` so timeout, request cancellation,
and store errors all exit without leaking goroutines or tickers.

For `wait_for_approval=true` on `/api/v1/edge/evaluate`, a resolved wait routes
through the same consume helper (approved → consume → `ALLOW`, rejected →
`DENY`, expired → `DENY`). If the wait times out while the approval is still
pending, evaluate returns `DENY` timeout guidance, keeps the action blocked, and
includes the existing `approval_ref`, `wait_after=approve_then_retry`, and
terminal copy telling the caller that the action was not run, the approval is
still pending, and the caller should approve it in Cordum and then retry. The
pending approval remains unconsumed.

The standalone `POST /api/v1/edge/approvals/{approval_ref}/wait` endpoint is
observation-only: it returns the resolved `EdgeApproval`, or the still-pending
`EdgeApproval` if its bounded timeout elapses. It never consumes an approval or
changes an evaluate decision by itself.

Inline wait is **not** required by browser/dashboard approval UX. Production
hooks and agentd should default to `wait_for_approval: false` and treat the
inline-wait affordance as a local-development convenience.

### What this contract does not do

- Approvals do **not** create Cordum Jobs. The action remains evidence in the
  `EdgeSession → AgentExecution → AgentActionEvent` log; the approval is a
  separate `EdgeApproval` record in the Edge approval store.
- A consumed approval is single-use. There is no "approve a class of actions"
  or "approve for the next 5 minutes" — each action retry computes a fresh
  action_hash against the fresh policy_snapshot and consumes (at most) one
  matching approved record.
- Approvals do not bypass tenant isolation. `GetApproval`, `ApproveApproval`,
  `RejectApproval`, `ClaimApproval`, and the wait helper are all
  tenant-scoped; cross-tenant requests get 403/404 without metadata leakage.
- The default deny + retry UX is the **production** path. Inline wait is a
  local/demo convenience; production deployments should use the standard
  approve-then-retry flow with the dashboard or `/api/v1/edge/approvals/...`
  resolution endpoints.

## Test coverage

The Edge policy examples are executable fixtures, not static samples:

- `core/edge/policy_templates_test.go` parses both fragments with
  `config.ParseSafetyPolicy`, validates critical rule IDs, verifies fixture
  normalization via `ClassifyEvent -> MapEventToPolicyCheckRequest`, and
  evaluates all cases with `policybundles.EvaluatePolicyCheck`.
- `core/controlplane/gateway/edge_evaluate_test.go` sends representative
  fixtures through `/api/v1/edge/evaluate` with a deterministic policy-backed
  Safety Kernel fake and asserts response decisions, rule IDs, persisted Edge
  events, and absence of synthetic `job_id`.
