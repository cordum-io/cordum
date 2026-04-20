# Scope-based MCP preapproval for trusted bots

The default flow for every mutating MCP tool is **human-in-the-loop approval**:
the LLM calls the tool, the gate returns JSON-RPC -32099 with an approval ID, a
human approves in the dashboard, and the LLM retries to execute. See
[mutating-tools.md](./mutating-tools.md) for the full default path.

Scope-based preapproval is the escape hatch for automation identities that
legitimately need to provision without a human click on every call — release
bots, CI/CD orchestrators, scheduled maintenance agents. The feature lets an
admin whitelist a specific `AgentIdentity` to call a specific set of mutating
tools **without** triggering the approval enqueue.

This document covers when to use it, how to configure it, how to audit it, and
how to revoke it.

## Contents

- [When to use it](#when-to-use-it)
- [When NOT to use it](#when-not-to-use-it)
- [Configuring preapproval](#configuring-preapproval)
- [Glob semantics](#glob-semantics)
- [Audit trail](#audit-trail)
- [Alerting on preapproved abuse](#alerting-on-preapproved-abuse)
- [Revocation](#revocation)
- [Recipe: zero-human-touch CI pipeline](#recipe-zero-human-touch-ci-pipeline)

## When to use it

Use `PreapprovedMutatingTools` for identities where:

1. The identity represents a **non-human automation** — no interactive operator
   could review each call in near-real time.
2. The call pattern is **bounded and predictable** — the CI pipeline's
   `cordum_install_pack` calls are the only mutating MCP calls you expect from
   this identity.
3. The blast radius is **containable** — the identity is tenant-scoped,
   capability-scoped, and monitored.

Examples that fit:

- `release-bot` that runs `cordum_install_pack` for signed-off pack versions
  after CI passes.
- `terraform-drift-correcter` that runs `cordum_update_policy_bundle` nightly
  when checked-in YAML drifts from live config.
- `onboarding-provisioner` that runs `cordum_register_agent` when new team
  members join via SCIM.

## When NOT to use it

- **Any human agent identity.** A human logged into Claude Code / Cursor should
  always go through the human-in-the-loop approval flow. Preapproval on a human
  identity means the operator cannot introspect what their AI assistant just
  did before it happened.
- **High-blast-radius tools on a shared identity.** `cordum_revoke_worker_session`
  and `cordum_update_policy_bundle` can brick tenant operations if misused.
  Preapprove these only on tightly-scoped single-purpose identities.
- **Pre-emptively, "just in case".** Every additional entry on the preapproval
  list is a line item the post-incident reviewer must justify. Start empty and
  add an entry only when a specific bot's flow is blocked by the approval step.

## Configuring preapproval

### Via cordumctl

```bash
cordumctl agents update release-bot \
  --preapproved-mutating-tools cordum_install_pack,cordum_create_workflow
```

### Via the REST API

```bash
curl -X PUT https://gateway.example/api/v1/agents/release-bot \
  -H "X-API-Key: $CORDUM_ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "preapproved_mutating_tools": [
      "cordum_install_pack",
      "cordum_create_workflow"
    ]
  }'
```

### Via the MCP tool (admin-approved)

A human admin can use `cordum_set_agent_scope` to grant preapproval — but that
MCP call ITSELF requires a human approval (the write path is admin-gated). This
closes the loop: no bot can escalate its own privileges.

```json
{
  "agent_id": "release-bot",
  "allowed_tools": ["cordum_list_packs", "cordum_install_pack"],
  "preapproved_mutating_tools": ["cordum_install_pack"]
}
```

Writes to `preapproved_mutating_tools` are **admin-only**. The
`handlers_agents.go` path enforces the admin role check before accepting the
field.

## Glob semantics

Each entry in `preapproved_mutating_tools` is either:

- An **exact tool name** — matches only that tool. Example:
  `cordum_install_pack`.
- A **trailing-glob** ending in `*` — matches any tool whose name starts with
  the prefix. Example: `cordum_install_*` matches `cordum_install_pack` but not
  `cordum_uninstall_pack`.

Leading-`*`, interior-`*`, regex patterns, and empty-prefix globs (`*` alone)
are **deliberately refused**. The grammar is narrow so:

- Every preapproval entry is easy to reason about in a post-incident review.
- No regex backtracking / ReDoS risk on the hot path.
- Adding a new mutating tool never accidentally falls inside an existing glob.

## Audit trail

Every preapproved call emits exactly one SIEMEvent:

```
event_type: mcp.tool_invocation
extra:
  tool_name:       cordum_install_pack
  agent_id:        release-bot
  tenant:          acme
  approval_status: preapproved
  duration_ms:     842
  result_size:     256
```

Note the **absence of `approval_id`** — no approval record was created, so
there's nothing to correlate against. The `approval_status=preapproved` field
is the definitive signal for forensics and alerting.

Compare with the normal human-approved flow, which emits FOUR events per call
(enqueue, approve, consume, invocation). The preapproved path writes 1 event —
faster and cheaper, but with less human-visible scrutiny. That trade-off is
why the default is human-in-the-loop.

All events land on the per-tenant Merkle audit chain (task-2497391e), so
preapproved-bypass activity is tamper-evident.

## Alerting on preapproved abuse

Recommended SIEM rules (express in whatever your stack uses —
Datadog / Splunk / Sumo / ClickHouse):

1. **Sudden rate spike.** Any identity whose
   `mcp.tool_invocation{approval_status=preapproved}` count in the last 10
   minutes exceeds 3× its weekly median. Bot identities have stable call rates;
   an exploitation window tends to show up as a burst.

2. **Off-hours activity.** Preapproved calls outside the bot's documented
   working window (e.g. CI runs 09:00-17:00 UTC, alert on preapproved calls at
   03:00).

3. **High-blast-radius tools.** Any preapproved call of
   `cordum_update_policy_bundle` or `cordum_revoke_worker_session` — page an
   on-call engineer unconditionally.

4. **Preapproval list changed.** Watch for
   `audit.action=agent.identity.updated` events where the diff touches
   `preapproved_mutating_tools`. Escalation of the list is a high-trust change
   and should itself be reviewed post-hoc.

Cordum doesn't ship these rules out of the box — they belong in your SIEM. The
events are stable and structured; plumb them through via the existing audit
export (docs/compliance/soc2_mapping.md).

## Revocation

To remove preapproval for an identity:

```bash
# Clear the whole list
cordumctl agents update release-bot --preapproved-mutating-tools ''

# Or drop individual entries via the MCP tool (admin-gated)
cordum_set_agent_scope {
  "agent_id": "release-bot",
  "allowed_tools": [...],
  "preapproved_mutating_tools": []
}
```

The change takes effect on the next call — the preapproval lookup reads the
identity record fresh each time (no caching window).

If the identity itself is compromised, a faster response is:

```bash
cordumctl agents suspend release-bot
```

Suspension disables the identity entirely, whether or not preapproval is set.

## Recipe: zero-human-touch CI pipeline

Goal: a GitHub Actions pipeline that installs a new pack version on merge to
main, with no human interaction.

**Step 1.** Create the bot identity (admin action, one-time):

```bash
cordumctl agents create release-bot \
  --name "Release Bot" \
  --owner acme \
  --risk-tier medium \
  --allowed-tools cordum_list_packs,cordum_install_pack \
  --preapproved-mutating-tools cordum_install_pack
```

**Step 2.** Mint an identity-bound session token:

```bash
cordumctl workers credentials create \
  --identity release-bot \
  --label "github-actions" \
  --ttl 30d > release-bot-token.json
```

**Step 3.** Store the token in GitHub Actions secrets as `CORDUM_TOKEN`.

**Step 4.** In the CI workflow:

```yaml
- name: Install updated pack
  env:
    CORDUM_GATEWAY: https://gateway.acme.cordum.io
    CORDUM_TOKEN: ${{ secrets.CORDUM_TOKEN }}
  run: |
    cordumctl mcp call cordum_install_pack \
      --json '{"pack_id":"acme/webhook","version":"${{ github.sha }}","idempotency_key":"ci-${{ github.run_id }}"}'
```

**Step 5.** Ship audit alerts to on-call:

```yaml
# Datadog monitor (example)
query: >-
  sum(last_10m):count(audit.action:mcp.tool_invocation
    agent_id:release-bot approval_status:preapproved) > 10
message: "release-bot ran >10 preapproved calls in 10m — investigate"
```

The CI pipeline now provisions without a human click per run, but:

- Every call lands on the Merkle audit chain with
  `approval_status=preapproved`.
- An anomalous burst pages an engineer.
- The preapproval list is itself admin-gated — release-bot can't expand its
  own scope.
- `cordumctl agents suspend release-bot` instantly stops the bot if something
  goes wrong.

That's the trust boundary: **automated throughput for the happy path, human
oversight for the list that defines the happy path**.
