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
