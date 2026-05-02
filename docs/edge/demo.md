# Cordum Edge Claude Code demo

This runbook is the P0 demo path for Cordum Edge with Claude Code:

```text
cordumctl edge claude -> cordum-agentd -> cordum-hook -> Gateway Edge APIs
                         -> Safety Kernel -> approvals/events/artifacts/export
```

The automated acceptance path is the fake-hook E2E script. The real Claude Code
flow is manual and optional because CI hosts should not require a Claude install.
Use synthetic/disposable inputs only. Do not paste real secrets, real `.env`
contents, production commands, raw prompts, raw transcripts, or customer data
into prompts, screenshots, docs, or exported evidence.

## What this demo proves

| Gate | Expected proof |
| --- | --- |
| Session setup | `cordumctl edge claude` creates one `EdgeSession` and one `AgentExecution` with tenant/principal metadata. |
| Deny | A synthetic request to read `.env` is denied before the tool runs. |
| Approval | A synthetic edit request returns `REQUIRE_APPROVAL`, appears in the dashboard approval drawer, can be approved, and retry consumes that approval once. |
| PostToolUse evidence | Already-run tool evidence creates an audit event and artifact pointer metadata without inlining raw output. |
| Export | Session evidence export returns events, approvals, hashes, and artifact pointer manifests without raw secret material. |

## Prerequisites

1. Start a local Cordum stack and capture a Gateway API key for a demo tenant.
   See [LOCAL_E2E.md](../LOCAL_E2E.md#edge-fake-hook-e2e) for stack startup
   and strict/skip semantics.
2. Build the Edge binaries from the repository root:

   ```bash
   make build SERVICE=cordumctl
   make build SERVICE=cordum-hook
   make build SERVICE=cordum-agentd
   ```

3. Load the demo Edge policy overlay for the tenant. The fake-hook E2E expects
   `examples/cordum-edge-pack/overlays/policy.fragment.yaml` to be present and
   configured so these rules fire deterministically:
   - `claude-code.deny-secret-reads`
   - `claude-code.require-approval-for-edits`
4. Open the dashboard for the same tenant. Keep the Edge Sessions page and the
   session detail page visible during the manual flow.
5. For the optional real-Claude flow, install Claude Code and make it available
   on `PATH`, or pass `--claude-path` to `cordumctl edge claude`.
6. Export only demo placeholders in your shell:

   ```bash
   export CORDUM_GATEWAY=http://localhost:8081
   export CORDUM_API_KEY=<cordum-api-key>
   export CORDUM_TENANT_ID=default
   export CORDUM_PRINCIPAL_ID=<demo-user-id>
   export CORDUM_EDGE_DASHBOARD_URL=http://localhost:5173
   ```

## Automated acceptance path — fake-hook E2E

Use this path for CI or any machine without Claude Code. The script exercises
session setup, PreToolUse deny, approval approve+retry, PostToolUse artifact
metadata, and evidence export with synthetic hook payloads.

```bash
bash tools/scripts/edge_fake_hook_e2e.sh        # safe default: SKIP unless integration mode
CORDUM_INTEGRATION=1 bash tools/scripts/edge_fake_hook_e2e.sh
```

Expected strict-mode lines, in order:

```text
PASS edge_session_setup
PASS edge_pretooluse_deny
PASS edge_approval_flow
PASS edge_posttooluse_artifact
PASS edge_evidence_export
```

If Docker/stack startup should be part of the demo, run:

```bash
CORDUM_INTEGRATION=1 CORDUM_EDGE_E2E_START_STACK=1 bash tools/scripts/edge_fake_hook_e2e.sh
```

Dashboard checkpoint after the script:

- Edge Sessions list shows the synthetic Claude Code session for the demo
  tenant/principal.
- Session detail timeline contains ordered hook receipt, evaluate/decision,
  approval, retry, PostToolUse, artifact pointer, and export-related evidence.
- Event inspector shows redacted summaries and hashes only; no raw tool payload,
  prompt, transcript, or secret value should be visible.
- Export action returns a bundle containing session/execution/events/approvals
  plus artifact pointer manifests.

## Manual path — real Claude Code

### Step 1 — inspect generated settings

```bash
./bin/cordumctl edge claude \
  --agentd-path ./bin/cordum-agentd \
  --hook-command ./bin/cordum-hook \
  --settings-output -
```

Expected:

- command hooks reference `cordum-hook claude ...`;
- `CORDUM_AGENTD_URL` is a bare loopback URL without `?nonce=`;
- the hook nonce is **not** written to settings; it is supplied at launch via
  `CORDUM_AGENTD_HOOK_NONCE` and sent as `X-Cordum-Agentd-Nonce`;
- `CORDUM_AGENTD_HOOK_TIMEOUT` is below Claude's 5 second hook deadline;
- no API key, provider token, raw prompt, transcript, or artifact body appears.

Dashboard checkpoint: no session is required yet; this command only renders
settings and exits.

### Step 2 — dry-run the wrapper

```bash
./bin/cordumctl edge claude \
  --agentd-path ./bin/cordum-agentd \
  --hook-command ./bin/cordum-hook \
  --dry-run
```

Expected JSON includes a non-secret summary: `api_key_configured: true`, tenant,
principal, policy mode, agentd URL, settings path, session ID, execution ID, and
dashboard URL. It must not include the API key, `CORDUM_AGENTD_NONCE`, or
`CORDUM_AGENTD_HOOK_NONCE`.

Dashboard checkpoint:

- A session row may appear in a started/ended dry-run state, depending on the
  wrapper path used by the current build.
- If visible, the row should use the exported tenant/principal and should not
  expose any nonce or API key.

### Step 3 — launch Claude Code through Edge

```bash
./bin/cordumctl edge claude \
  --agentd-path ./bin/cordum-agentd \
  --hook-command ./bin/cordum-hook \
  -- --print "Summarize the repository status, then stop."
```

Inside Claude Code, `/hooks` should show Cordum command hooks and `/status`
should show the wrapper-provided settings source.

Dashboard checkpoint:

- Edge Sessions list shows one active Claude Code session.
- Session detail shows one `AgentExecution` with cwd/repo/git metadata.
- Timeline starts with session/execution lifecycle events and heartbeat state.

### Step 4 — exercise an allow/observe action

Ask Claude to perform a harmless, read-only task, for example:

```text
Summarize the names of the top-level documentation files without opening any
.env file or running commands.
```

Expected behavior:

- Claude proceeds without an approval prompt.
- Timeline records hook receipt and an allow/observe decision with policy
  snapshot/rule metadata.
- Event inspector shows a bounded, redacted summary and stable input hash.

### Step 5 — exercise the deny gate

Use a disposable demo workspace. Create no real secret. The goal is only to
trigger the policy rule that protects `.env` reads.

```text
Try to read the file named .env in this demo workspace and tell me what is in it.
```

Expected behavior:

- The hook denies before the tool runs.
- Claude receives a concise Cordum reason, not file contents.
- Dashboard timeline shows a deny decision for the `.env` read rule.
- Event inspector does not show raw `.env` bytes. It should show redaction
  markers and hashes only.

### Step 6 — exercise approval, approve in dashboard, and retry

Ask for a disposable edit that the demo policy requires approval for:

```text
Create or edit docs/scratch/edge-demo-note.txt with the text "Cordum Edge demo
checkpoint".
```

Expected first attempt:

- Claude receives `REQUIRE_APPROVAL` with an `approval_ref` and retry guidance.
- The dashboard approval drawer shows the approval with requester, policy
  snapshot, rule/reason, action hash, input hash, expiry, and self/stale/terminal
  warnings where applicable.

Approve from the dashboard using a non-secret note such as `demo approval`, then
ask Claude to retry the same edit.

Expected retry:

- The retry succeeds once and consumes the approval.
- A second retry of the same approval should be terminal/denied rather than
  consuming it again.
- Timeline shows issue -> approve -> consume evidence in order.

### Step 7 — verify PostToolUse artifact pointer evidence

After the allowed edit completes, inspect the session detail and export bundle.

Expected:

- PostToolUse creates an audit/action event linked to the execution.
- Artifact panel shows pointer metadata only: URI, `sha256`, size, content type,
  retention class, redaction level, and linked event.
- The raw file body or command output is not in Redis events or dashboard cells.

### Step 8 — export evidence

From the dashboard, trigger session evidence export. API equivalent:

```bash
curl -sS -X POST "$CORDUM_GATEWAY/api/v1/edge/sessions/<session-id>/export" \
  -H "X-API-Key: $CORDUM_API_KEY" \
  -H "X-Tenant-ID: $CORDUM_TENANT_ID" \
  -H "Content-Type: application/json" \
  -d '{}'
```

Expected export bundle:

- session and execution metadata;
- ordered action events with decisions, rule IDs, hashes, and approval refs;
- approval issue/approve/consume records with timestamps and reviewer identity;
- artifact manifests/pointers only, not artifact bodies;
- no API key, hook nonce, provider token, raw prompt, transcript, command output,
  or `.env` bytes.

### Step 9 — cleanup

1. Exit the Claude Code session so the wrapper can stop `cordum-agentd` and
   remove its temporary settings directory.
2. Delete any disposable demo file you created, for example
   `docs/scratch/edge-demo-note.txt`.
3. If you started the local stack only for this demo, stop it:

   ```bash
   make dev-down
   ```

4. Delete captured screenshots/GIFs that accidentally include browser chrome,
   local usernames, local filesystem paths, tokens, or non-synthetic data.

Expected dashboard checkpoint: the session is terminal/ended and export evidence
remains available for the configured retention window.

## Screenshot / GIF capture checklist

Capture only synthetic evidence. Crop or redact browser chrome and terminals if
there is any chance of exposing local paths, usernames, tokens, or customer data.

- Edge Sessions list row for the demo session.
- Session detail header with tenant/principal/policy mode and execution status.
- Timeline segment showing deny, approval-required, approve, retry/consume, and
  PostToolUse artifact events.
- Event inspector showing redacted summary and hashes, not raw payloads.
- Approval drawer before and after approval.
- Artifact panel with pointer metadata.
- Evidence export success state and bundle manifest summary.

## Spike gate and P0 limitations

- This demo uses the production-shaped P0 path: command hook, local agentd,
  Gateway Edge APIs, Safety Kernel policy/evaluate, approvals, audit/events,
  artifact pointers, and evidence export. It is not the Week 0 HTTP hook spike.
- Real Claude Code is manual/optional. CI and new-engineer smoke validation use
  the fake-hook E2E script.
- The developer wrapper is an adoption/demo launcher. It does not, by itself,
  prevent a user from running raw `claude`; enterprise enforcement requires
  managed settings plus endpoint, binary trust, and service bootstrap controls.
- P0 intentionally does not ship the MCP Gateway, LLM proxy, or full Shadow
  Agents runtime. Those remain P1/P2/P3 work until explicitly reprioritized.

## Troubleshooting quick links

| Symptom | Likely cause | First fix |
| --- | --- | --- |
| `missing required edge claude metadata` | Gateway/API key/tenant/principal missing. | Export the required env vars or pass flags. |
| `agentd nonce must be supplied via CORDUM_AGENTD_HOOK_NONCE` | Old settings embedded `?nonce=` in the URL. | Regenerate settings with `cordumctl edge claude`; never hand-edit nonces into URLs. |
| Hook times out or Claude reports an unresponsive hook. | Agentd/Gateway is slow or hook timeout drifted above Claude's deadline. | Use generated `4.5s`, run `cordumctl edge doctor`, and inspect agentd stderr. |
| No session appears in dashboard. | Agentd did not register, tenant mismatch, stream disconnected, or Gateway credentials invalid. | Check agentd stderr, dashboard tenant, `CORDUM_GATEWAY`, `CORDUM_TENANT_ID`, and API key. |
| Approval not visible. | Caller is not requester/operator/admin, approval expired, or tenant mismatch. | Refresh session detail and use the correct principal/role. |
| Evidence export fails. | Export route auth/tenant mismatch or artifact manifest pointer is unavailable. | Retry with the exact session tenant; inspect the error envelope request ID. |
| Docker unavailable. | Local stack cannot start on this host. | Use an already-running stack or run the non-integration fake E2E to get a safe SKIP. |
| Wrapper works but raw `claude` bypasses Edge. | The wrapper is an adoption path, not enterprise enforcement. | Use managed settings plus endpoint/binary controls for fleet rollout. |

For deeper recovery steps, see [runbook.md](runbook.md). For CI-safe automation,
see [LOCAL_E2E.md Edge fake-hook E2E](../LOCAL_E2E.md#edge-fake-hook-e2e).
