# Drive Cordum with Claude Code

Wire Claude Code to Cordum's MCP server so you can talk to the
platform in natural language — list jobs, inspect policies, review
approvals, draft new rules — all with every tool call landing on the
governance audit trail.

**Target duration: ≤ 5 minutes.**

> This page is the DoD-named quickstart path. The canonical deep
> quickstart lives at [docs/mcp/quickstart-claude-code.md](../mcp/quickstart-claude-code.md)
> and is wired into the Docusaurus sidebar under "Drive Cordum with MCP".
> Keep the two in sync — both share the same five steps; this page
> carries the screenshot references the DoD requires.

## 1. Prereqs

Start from the shared prerequisites doc so tenant + API key + TLS
trust are configured once and used by every client:

→ [docs/mcp/_prereqs.md](../mcp/_prereqs.md)

![Obtain your CORDUM_API_KEY in the Cordum dashboard](../static/img/mcp-claude-code/01-api-key.png)

If the image is missing, the screenshot has not yet been captured
on a live stack. See [`cold-tests/README.md`](../mcp/mcp-onboarding-cold-test.md)
for the human-driven capture procedure.

## 2. Install Claude Code

Install Claude Code from [claude.com/claude-code](https://claude.com/claude-code).
Verify the install with `claude --version`.

![`claude --version` prints the current Claude Code release](../static/img/mcp-claude-code/02-version.png)

## 3. Configure the MCP server

Add Cordum to Claude Code's MCP config. Full snippet + variants
(stdio vs HTTP/SSE, self-signed TLS, custom CA trust) are in the
canonical quickstart:

→ [docs/mcp/quickstart-claude-code.md#3-configure-mcp](../mcp/quickstart-claude-code.md)

The minimal stdio snippet lands in `~/.claude/mcp.json` or your
project's `.claude/mcp.json`:

```json
{
  "mcpServers": {
    "cordum": {
      "command": "cordum-mcp",
      "env": {
        "CORDUM_API_KEY": "${CORDUM_API_KEY}",
        "CORDUM_TENANT_ID": "${CORDUM_TENANT_ID}"
      }
    }
  }
}
```

![Claude Code lists cordum under connected MCP servers](../static/img/mcp-claude-code/03-mcp-servers.png)

## 4. First tool calls

Restart Claude Code and ask:

- "List my cordum jobs." → calls `cordum_list_jobs`.
- "What policies are active?" → calls `cordum_list_policy_bundles`.
- "Show pending approvals." → calls `cordum_list_approvals`.

Every call lands on the audit chain as
`mcp.tool_invocation` — grep the dashboard's Audit page for the
`agent_id=claude-code` entries to verify.

![Dashboard audit entry for the first tool call](../static/img/mcp-claude-code/04-audit-entry.png)

## 5. What to try next

Invoke the four shipped prompts directly from Claude Code:

- `/mcp.cordum.draft_safety_rule` — drafts a policy YAML scaffold.
  **Do not pipe output directly into a policy publish**; always
  simulate first.
- `/mcp.cordum.explain_denial` — walks through why a specific job
  was denied and suggests safer remediation.
- `/mcp.cordum.summarize_approvals` — writes a human-readable
  summary of approval activity.
- `/mcp.cordum.policy_migration_helper` — converts a policy bundle
  between grammar versions. **Re-sign + `audit_verify` before
  publish.**

Full argument schemas live in [docs/mcp/prompts.md](../mcp/prompts.md).

## Troubleshooting

Troubleshooting entries (including `-32601 prompt not found` on
older gateway releases) live in the canonical quickstart's
Troubleshooting section:

→ [docs/mcp/quickstart-claude-code.md#troubleshooting](../mcp/quickstart-claude-code.md)

## Related

- [docs/mcp/prompts.md](../mcp/prompts.md) — prompt catalogue.
- [docs/mcp/tools.md](../mcp/tools.md) — tool catalogue.
- [docs/mcp/mutating-tools.md](../mcp/mutating-tools.md) — mutating
  tool surface + approval flow.
- [docs/mcp/scope-preapproval.md](../mcp/scope-preapproval.md) —
  CI-bot preapproval (not for human agent identities).
- [docs/mcp/mcp-onboarding-cold-test.md](../mcp/mcp-onboarding-cold-test.md) —
  human cold-test checklist (fills in the screenshots above).
