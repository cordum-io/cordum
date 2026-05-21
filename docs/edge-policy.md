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

## URL network egress DNS/SSRF gate

`URLGate` is the action-layer guard for governed URL actions before they reach
approval or allow decisions. It uses the shared `HostResolver` abstraction (not
ad hoc DNS) so tests can inject deterministic answers and production can use
the platform resolver path.

For non-literal hostnames, the gate resolves the host and denies the action if
any answer is loopback, RFC1918/private, link-local, IPv6 ULA, unspecified,
multicast, or a known cloud metadata literal. Known exfiltration hosts, paste
write destinations, prompt-exfil query signatures, and literal metadata/private
IPs are denied before DNS so those deterministic reasons are preserved.

Resolver uncertainty fails closed for governed URL actions:

- resolver errors, empty answer sets, and malformed addresses return a stable
  resolver-denial reason rather than allowing the action;
- successful answers use a bounded short-TTL cache and singleflight so attacker
  controlled host cardinality cannot grow memory without limit;
- resolver errors are not cached, and TTL expiry revalidates the host before a
  later decision.

This policy-time check reduces SSRF and rebinding exposure but does not by
itself close the socket-connect TOCTOU window. Transport code that opens the
outbound connection should pin the selected resolved address or revalidate that
address at connect time instead of performing an unrelated second lookup.

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

### ProvenanceGate: resolved approval audit evidence

For destructive action-gate decisions, a backend `approved` approval is
necessary but not sufficient. After the mutation gate validates the stored
`EdgeApproval`, `ProvenanceGate` verifies the tenant audit chain and requires a
canonical resolved approval event:

- event type `EventEdgeApprovalResolved` / `edge.approval_resolved`;
- decision `approved` or `approve`;
- exact tenant match;
- exact `approval_ref` and `action_hash` match in bounded audit `extra`.

`EventEdgeApprovalRequested` / `edge.approval_requested` rows are request
lifecycle context only. A requested-only row, wrong tenant, wrong ref, wrong
hash, rejected/expired decision, malformed event JSON, Redis/audit verifier
outage, or compromised hash/HMAC/linkage is an evidence gap and the gate fails
closed. The failure is reported with bounded reason codes such as
`audit_evidence_missing`, `audit_chain_compromised`, or
`audit_chain_verifier_unavailable`; raw prompts, tool payloads, transcripts,
approval secrets, and command output are never embedded in the audit evidence.

The provenance check uses the approval window (`CreatedAt` through max of
`ResolvedAt`, `ConsumedAt`, and now), caps the window to the shared audit verify
spread, and caps Redis stream work with the same audit verification limit used
by the chain verifier. Busy tenants therefore get bounded verification instead
of an unbounded stream scan, and retention-trimmed history is accepted only when
the in-window resolved approval evidence is still present.

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

## Strict-mode e2e gate requires a 2-key gateway stack

`tools/scripts/edge_fake_hook_e2e.sh` in strict mode (`CORDUM_INTEGRATION=1`)
exercises the full approval lifecycle: each gate issues a
`/api/v1/edge/evaluate` POST as the REQUESTER principal, then a
`/api/v1/edge/approvals/{ref}/{approve,reject}` POST as the APPROVER
principal. The gateway's `edgeApprovalRequesterMatchesResolver` +
`identitiesOverlap` (`core/controlplane/gateway/helpers.go:1428-1434`) match
on `sha256(api_key)[:4]` regardless of any `X-Principal-Id` header override,
so a single shared API key trips `self_approval_denied` (HTTP 403) before
the gate can complete.

To run the strict-mode gate locally you need TWO distinct API keys: a
REQUESTER (`CORDUM_API_KEY`, the existing single-key env) and an APPROVER
(`CORDUM_APPROVER_API_KEY`, the script's new env var). The approver key
must be admin-registered in the gateway via `CORDUM_API_KEYS` JSON. The
docker-compose default deployment intentionally ships a single key for
adoption-friction reduction and cannot satisfy strict mode — production
operators do not run this gate.

CI provisions the 2-key stack via:

- `.github/workflows/edge-fake-hook-e2e.yml` — generates two random keys
  per run, asserts sha256-prefix distinctness, registers the approver via
  `CORDUM_API_KEYS=[{"key":"<hex>","role":"admin","tenant":"default", ...}]`.
- `docker-compose.ci.yml` — workflow-only override that adds the
  `CORDUM_API_KEYS` env passthrough to the `api-gateway` service. The
  default `docker-compose.yml` is unchanged; the override is layered with
  `docker compose -f docker-compose.yml -f docker-compose.ci.yml up -d`.

`CORDUM_APPROVER_API_KEY` defaults to `CORDUM_API_KEY` for backward
compatibility — existing single-key callers still see the explicit
`self_approval_denied` failure with a directed remediation message rather
than a silent regression. See the script's `ENVIRONMENT` block for the full
contract.

### Pack-load contract

The script's `gate_pretooluse_deny` requires the `cordum-edge-pack` policy
overlay (`examples/cordum-edge-pack/overlays/policy.fragment.yaml`) to be
loaded on the gateway before the strict-PASS assertion runs. A fresh
`docker compose up` deployment has no overlay loaded by default — the
Safety Kernel's policy loader (`core/controlplane/safetykernel/kernel.go`
`loadFragments`) reads fragments only from `configSvc`/Redis, not from
disk, so mounting the YAML into the container has no effect. The pack must
be installed via the production install path (`POST /api/v1/packs/install`,
`core/controlplane/gateway/handlers_packs.go` `installPackFromDir`) so the
gateway writes the overlay into `configSvc` where `loadFragments` finds it.

CI bootstraps the pack between gateway-health and the script run by calling
`cordumctl pack install` from the workflow runner (no extra compose
services required — the gateway handles install in-process):

```
docker compose -f docker-compose.yml -f docker-compose.ci.yml up -d --build
# wait for gateway /api/v1/status
go run ./cmd/cordumctl pack install ./examples/cordum-edge-pack
# wait for GET /api/v1/packs/cordum-edge-pack → 200
bash tools/scripts/edge_fake_hook_e2e.sh
```

The pack is unsigned in-tree; the gateway accepts unsigned packs in default
mode (`CORDUM_GATEWAY_PACK_STRICT` unset → false). Operators running the
strict-mode gate against a hardened gateway with
`CORDUM_GATEWAY_PACK_STRICT=true` must sign the pack first (see
`docs/pack.md` for the signing workflow).

With the pack installed, the workflow asserts all 7 PASS lines exit-0 (see
`.github/workflows/edge-fake-hook-e2e.yml` "Assert 7 PASS lines" step):

```
PASS edge_session_setup
PASS edge_pretooluse_deny
PASS edge_approval_flow
PASS edge_approval_rejected
PASS edge_approval_expired
PASS edge_posttooluse_artifact
PASS edge_evidence_export
```

On any failure the workflow uploads `edge-fake-hook-e2e.log` and
`docker-compose-edge-fake-hook-e2e.log` as the `edge-fake-hook-e2e-artifacts`
artifact for triage.
