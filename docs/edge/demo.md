# Cordum Edge Claude Code demo

This is the manual P0 demo for the real Edge path:

```text
cordumctl edge claude -> cordum-agentd -> cordum-hook -> Gateway Edge APIs
```

Use synthetic/disposable inputs only. Do not paste real secrets, real `.env`
contents, production commands, or raw transcripts into prompts or docs.

## Prerequisites

1. Start a local Cordum stack and capture the generated Gateway API key.
2. Build the Edge binaries from the repository root:

   ```bash
   make build SERVICE=cordumctl
   make build SERVICE=cordum-hook
   make build SERVICE=cordum-agentd
   ```

3. Ensure Claude Code is installed and either on `PATH` or passed with
   `--claude-path`.
4. Export placeholders in your shell:

   ```bash
   export CORDUM_GATEWAY=http://localhost:8081
   export CORDUM_API_KEY=<cordum-api-key>
   export CORDUM_TENANT_ID=default
   export CORDUM_PRINCIPAL_ID=<your-user-id>
   ```

## Step 1 — inspect generated settings

```bash
./bin/cordumctl edge claude \
  --agentd-path ./bin/cordum-agentd \
  --hook-command ./bin/cordum-hook \
  --settings-output -
```

Expected:

- command hooks reference `cordum-hook claude ...`;
- `CORDUM_AGENTD_URL` is a loopback URL without `?nonce=`;
- `CORDUM_AGENTD_HOOK_TIMEOUT` is below `5s`;
- no API key, hook nonce, provider token, raw prompt, or transcript appears.

## Step 2 — dry-run the wrapper

```bash
./bin/cordumctl edge claude \
  --agentd-path ./bin/cordum-agentd \
  --hook-command ./bin/cordum-hook \
  --dry-run
```

Expected JSON includes `api_key_configured: true`, tenant, principal, policy
mode, agentd URL, settings path, session/execution IDs, and a dashboard URL. It
must not include the API key or hook nonce.

## Step 3 — launch Claude Code through Edge

```bash
./bin/cordumctl edge claude \
  --agentd-path ./bin/cordum-agentd \
  --hook-command ./bin/cordum-hook \
  -- --print "Summarize the repository status, then stop."
```

Inside Claude Code, `/hooks` should show Cordum command hooks and `/status`
should show the wrapper-provided settings source.

## Step 4 — exercise decisions

Run a safe read-only request first, then a governed request that your local Edge
policy is configured to deny or require approval. Use a disposable fixture; do
not ask the agent to touch real secrets or production state.

Expected Claude behavior:

- safe action: quiet allow or minimal additional context;
- denied action: concise Cordum reason before the tool runs;
- approval action: `approval_ref` plus approve-then-retry guidance;
- post-tool evidence: recorded after already-run tools without claiming the tool
  was prevented.

## Expected dashboard evidence

In the dashboard, the Edge session should show:

- one `EdgeSession` with Claude Code agent metadata, policy mode, principal,
  heartbeat/end state, and dashboard URL;
- one `AgentExecution` for the launched Claude process;
- ordered action events for hook receipt, evaluate, decision, degraded state,
  approval, and artifact pointer metadata where applicable;
- approval drawer entries with requester, reason/rule, policy snapshot,
  action/input hashes, expiry, and self/stale/terminal warnings;
- artifact panel rows that show pointer metadata only: type, retention,
  redaction level, sha256, linked event, and safe view/download affordance when
  supported;
- evidence export success/error state for `POST /api/v1/edge/sessions/{id}/export`.

## Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `missing required edge claude metadata` | Gateway/API key/tenant/principal missing. | Export the required env vars or pass flags. |
| `agentd nonce must be supplied via CORDUM_AGENTD_HOOK_NONCE` | Old settings embedded `?nonce=` in the URL. | Regenerate settings with `cordumctl edge claude`; do not hand-edit nonces into URLs. |
| Hook times out or Claude reports unresponsive hook. | `CORDUM_AGENTD_HOOK_TIMEOUT` too high or agentd/Gateway too slow. | Use generated `4.5s`, check Gateway health, and avoid long-running hook-side work. |
| No session appears in dashboard. | Agentd did not register, tenant mismatch, or Gateway credentials invalid. | Check agentd stderr, `CORDUM_GATEWAY`, `CORDUM_TENANT_ID`, and API key. |
| Approval not visible. | Caller is not the requester or an operator/admin, approval expired, or tenant mismatch. | Refresh evidence and use the right principal/role. |
| Windows state-dir failure with strict perms. | Broad inherited ACL with `CORDUM_AGENTD_STRICT_PERMS=1`. | Move state under the user profile or fix ACLs. |
| Wrapper works but raw `claude` bypasses Edge. | Wrapper is not fleet enforcement. | Deploy managed settings and endpoint controls for enterprise rollout. |

For CI or machines without Claude Code, use
[`LOCAL_E2E.md` Edge fake-hook E2E](../LOCAL_E2E.md#edge-fake-hook-e2e).
