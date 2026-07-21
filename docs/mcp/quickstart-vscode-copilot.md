# Govern GitHub Copilot (VS Code agent mode) with Cordum

Connect **GitHub Copilot agent mode** in VS Code to Cordum's MCP server so
every Copilot tool call is **policy-gated by Cordum's safety kernel**
(allow / deny / require-approval) and each interaction is recorded as a
**Copilot audit session** (transcript + linked jobs + governance decisions) in
the dashboard.

Copilot connects over MCP HTTP/SSE. Unlike Claude Code, Copilot does not run
command hooks, so MCP is the governance surface.

---

## 1. Enable the MCP policy gate (operator, one-time)

The policy gate ships **off by default**. Turn it on in gateway config:

```yaml
mcp:
  enabled: true
  transport: http          # http | both (HTTP/SSE)
  policy_gate_enabled: true # route every tools/call through the safety kernel
```

Prereqs for the gate (already present in a normal deployment): the action-gate
pipeline, the edge store, the artifact store, and Redis. After enabling,
confirm the boot log shows `mcp.policy_gate wired` / `policy_gate_active=true`.
With the gate off, tool calls are still scope-filtered but not policy-evaluated.

## 2. Provision a least-privilege Copilot identity

Create an agent identity for Copilot (read-first; mutations require approval):

```bash
curl -sk -X POST https://<gateway>:8081/api/v1/agents \
  -H "X-API-Key: $CORDUM_ADMIN_KEY" -H "X-Tenant-ID: <tenant>" \
  -d '{
    "name": "GitHub Copilot",
    "risk_tier": "low",
    "allowed_tools": ["cordum_list_jobs","cordum_get_job","cordum_query_policy","cordum_list_approvals"],
    "data_classifications": ["general"],
    "preapproved_mutating_tools": []
  }'
```

Note the returned agent `id`. Then mint a scoped API key bound to that identity
+ tenant with `mcp.read` permission (use your normal key-issuance flow). Keep
`preapproved_mutating_tools` empty so `cordum_submit_job` and other mutations
hit human approval.

## 3. Generate the VS Code `mcp.json`

`cordumctl` writes the Copilot-correct schema (`servers` + prompted `inputs` +
auth `headers`). Preview first, then apply:

```bash
cordumctl mcp preview --client vscode \
  --gateway-transport sse \
  --gateway-endpoint https://<gateway>:8081/mcp/sse \
  --gateway-tenant <tenant> \
  --gateway-agent-id <copilot-agent-id>

cordumctl mcp attach --apply --client vscode \
  --gateway-transport sse \
  --gateway-endpoint https://<gateway>:8081/mcp/sse \
  --gateway-tenant <tenant> \
  --gateway-agent-id <copilot-agent-id> \
  --config-path .vscode/mcp.json   # per-workspace; omit for the user-profile mcp.json (VS Code "MCP: Open User Configuration")
```

This produces:

```jsonc
{
  "inputs": [
    { "id": "cordum-api-key", "type": "promptString",
      "description": "Cordum MCP API key (Copilot agent identity)", "password": true }
  ],
  "servers": {
    "cordum": {
      "type": "sse",
      "url": "https://<gateway>:8081/mcp/sse",
      "headers": {
        "X-API-Key": "${input:cordum-api-key}",
        "X-Tenant-ID": "<tenant>",
        "X-Agent-Id": "<copilot-agent-id>"
      }
    }
  }
}
```

The API key is **never written to disk** — VS Code prompts for it once
(`${input:cordum-api-key}`) and stores it in the OS secret store. Tenant and
agent id are non-secret literals.

> **Self-signed gateways:** trust the gateway CA in the OS trust store (or set
> `NODE_EXTRA_CA_CERTS` for VS Code) so the TLS handshake succeeds.
>
> **Group calls into one audit session (optional):** add
> `"X-Copilot-Session-Id": "<some-id>"` to `headers` to group a workspace's
> calls under one Copilot session and label spawned jobs with it. If omitted,
> the MCP transport session id is used.

## 4. Use it

1. Open VS Code Copilot **agent mode**; it discovers the `cordum` MCP server.
2. `tools/list` returns only the identity's allow-listed tools (scope filter,
   fail-closed).
3. **Allow:** "list my recent Cordum jobs" → `cordum_list_jobs` runs; emits
   `mcp.tool.pre`/`.post` + `mcp.tool_invocation` to the audit chain.
4. **Deny:** a tool outside the allow-list returns a JSON-RPC error + a deny
   audit event.
5. **Approval:** a mutating tool (e.g. `cordum_submit_job`) returns
   **"approval pending"**. A human approves in the dashboard approvals queue or
   via `cordumctl mcp pending` / `cordumctl mcp approve <id> --reason "..."`,
   then you re-ask Copilot.

   > Copilot does not auto-resume a held call via the `_approval_ref` argument
   > or branch on JSON-RPC error codes, so the supported flow is
   > **request → pending → human approves → re-ask**. Keep mutating tools
   > approval-gated.

## 5. Review the audit session

Every governed Copilot tool call is recorded. Open
`/copilot/sessions/<session-id>` in the dashboard to see the transcript,
the jobs it spawned (linked via the auto-applied `session_id` job label), and
the governance decisions for those jobs (decisions require `governance.read`).

## Security notes

- Least-privilege identity (read-first, empty preapproved-mutating list) means
  any state change requires explicit human approval.
- The key only ever exists in VS Code's secret store via `${input:...}`.
- Fail-closed: a revoked/invalid key disconnects the SSE stream and returns an
  empty tool list.
