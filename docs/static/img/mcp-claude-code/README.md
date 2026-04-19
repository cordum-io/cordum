# mcp-claude-code screenshot drop

The four screenshots referenced by
[docs/getting-started/mcp-with-claude-code.md](../../getting-started/mcp-with-claude-code.md)
live here once captured on a live stack:

- `01-api-key.png` — Cordum dashboard Settings → API Keys panel
  with a freshly issued key highlighted.
- `02-version.png` — a terminal showing the output of
  `claude --version` on the tester's machine.
- `03-mcp-servers.png` — Claude Code's MCP server list showing
  `cordum` as an active connection.
- `04-audit-entry.png` — Cordum dashboard Audit page filtered to
  `agent_id=claude-code` with the first `mcp.tool_invocation`
  entry highlighted.

## How to capture

Screenshots are DoD-required but must come from a **real**
walkthrough, not synthesized imagery. Capture them while filling in
the
[mcp-onboarding-cold-test checklist](../../mcp/mcp-onboarding-cold-test.md) —
a human tester drives the onboarding end-to-end, captures each PNG
at the documented step, and commits the set in the same PR as the
filled-in cold-test report at
`docs/mcp/cold-tests/<date>-claude-code-<tester>.md`.

Dimensions: 1280×800 default. Redact any real tenant/agent IDs and
API keys before committing.
