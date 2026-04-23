# MCP Quickstart — Claude Code

Wire Claude Code (Anthropic's terminal CLI) to Cordum's MCP server so
it can operate your cluster through natural-language prompts. Total
time: 5 minutes.

## Prereqs

See [_prereqs.md on GitHub](https://github.com/cordum-io/cordum/blob/main/docs/mcp/_prereqs.md). You'll need `CORDUM_API_KEY`,
`CORDUM_GATEWAY`, and a Cordum instance running (`make dev-up` works).

## Configure

Add Cordum to Claude Code's MCP server list — edit `~/.claude/claude.json`:

```json
{
  "mcpServers": {
    "cordum": {
      "command": "cordumctl",
      "args": ["mcp", "stdio"],
      "env": {
        "CORDUM_API_KEY": "${CORDUM_API_KEY}",
        "CORDUM_GATEWAY": "${CORDUM_GATEWAY}",
        "CORDUM_TENANT_ID": "default"
      }
    }
  }
}
```

If you prefer HTTP transport (no stdio bridge):

```json
{
  "mcpServers": {
    "cordum": {
      "type": "http",
      "url": "${CORDUM_GATEWAY}/mcp/sse",
      "headers": {
        "Authorization": "Bearer ${CORDUM_API_KEY}",
        "X-Tenant-ID": "default"
      }
    }
  }
}
```

Restart Claude Code. You should see the 20-entry tool catalogue under
the `/mcp` command.

## Try it

From inside Claude Code:

```
/mcp list           # should show cordum in the server list
list my recent jobs # Claude picks cordum_list_jobs
show me pending approvals
```

## What to try next: the 4 shipped prompts

Claude Code renders each registered prompt under the `/prompts` menu.
Cordum ships four first-party templates — each embeds domain context
the LLM would otherwise invent. Try them in the order below:

1. **`/prompts/summarize_approvals window=24h`** — read-only; renders a
   digest of approval activity so operators can audit a recent shift.
2. **`/prompts/explain_denial job_id=<jobID>`** — grab a deny decision
   from `/jobs` and have the LLM explain + suggest remediation.
3. **`/prompts/draft_safety_rule`** — the LLM writes a YAML rule
   scaffold; output carries a simulate-before-apply disclaimer the
   operator must respect. NEVER pipe the output straight into
   `cordumctl policy publish`.
4. **`/prompts/policy_migration_helper from_version=... to_version=...`** —
   rewrites a pasted policy bundle to a new grammar version. Re-sign
   and `cordum_audit_verify` the result before publish.

Full argument schemas + output shape live in
[docs/mcp/prompts.md](./prompts.md).

## Troubleshooting

* `-32097 approval gate misconfigured` — the gateway's middleware
  didn't stash tenant+principal in ctx. Confirm your Authorization
  header is present and valid.
* Empty `tools/list` — Claude Code treats MCP servers as opt-in per
  project; run `/mcp enable cordum` in the session.
* Cert errors on HTTP transport — pin the Cordum CA as described in
  [_prereqs.md on GitHub](https://github.com/cordum-io/cordum/blob/main/docs/mcp/_prereqs.md).
* `-32601 prompt not found` — the gateway hasn't registered prompts
  yet (older deploy). Bump to a release that ships
  `RegisterAllPrompts`.

## Related

* [docs/mcp/tools.md](./tools.md)
* [docs/mcp/resources.md](./resources.md)
* [docs/mcp/prompts.md](./prompts.md)
* [docs/mcp/quickstart-cursor.md](./quickstart-cursor.md)
* [docs/mcp/quickstart-vscode.md](./quickstart-vscode.md)
