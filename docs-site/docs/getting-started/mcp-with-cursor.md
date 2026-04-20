# Drive Cordum with Cursor

Wire Cursor to Cordum's MCP server so you can ask the editor to
list jobs, inspect policies, review approvals, and draft rules — all
with every tool call landing on the governance audit trail.

**Target duration: ≤ 5 minutes.**

> This page is the DoD-named quickstart path. The canonical deep
> quickstart with full TLS/self-signed-CA trust options lives at
> [docs/mcp/quickstart-cursor.md](../mcp/quickstart-cursor.md).

## 1. Prereqs

Shared prerequisites (same across every MCP client):

- `CORDUM_API_KEY` exported from `.env`.
- `CORDUM_TENANT_ID` set (defaults to `default`).
- `cordum-mcp` on `$PATH` for stdio (cert-free) OR gateway over HTTPS with `--cacert` for self-signed dev.
- Gateway rate limit: 100 req/s per API key.

Full reference: [`docs/mcp/_prereqs.md`](https://github.com/cordum-io/cordum/blob/main/docs/mcp/_prereqs.md) on GitHub.

![CORDUM_API_KEY in the Cordum dashboard](pathname:///img/mcp-cursor/01-api-key.png)

If an image is a 1×1 placeholder, the screenshot has not yet been captured on a live stack — human testers capture during the onboarding cold-test.

## 2. Install Cursor

Download Cursor from [cursor.sh](https://cursor.sh). Verify the
install opens to the welcome screen.

![Cursor welcome screen](pathname:///img/mcp-cursor/02-welcome.png)

## 3. Configure the MCP server

Open **Settings → Features → Model Context Protocol**. Add a
server with name `cordum`, command `cordum-mcp`, and the
`CORDUM_API_KEY` + `CORDUM_TENANT_ID` env vars. Full stdio + HTTP
snippets + per-OS CA-install steps are in the canonical quickstart:

→ [docs/mcp/quickstart-cursor.md#3-configure-mcp](../mcp/quickstart-cursor.md)

![Cursor MCP settings panel with cordum server listed](pathname:///img/mcp-cursor/03-mcp-settings.png)

## 4. First tool calls

In the chat panel, ask:

- "List my cordum jobs." → calls `cordum_list_jobs`.
- "What policies are active?" → calls `cordum_list_policy_bundles`.
- "Show pending approvals." → calls `cordum_list_approvals`.

![Cursor chat panel showing a cordum tool call result](pathname:///img/mcp-cursor/04-tool-call.png)

## 5. What to try next

The four shipped prompts (`draft_safety_rule`, `explain_denial`,
`summarize_approvals`, `policy_migration_helper`) are callable via
`/mcp.cordum.<prompt_name>` — full argument schemas in
[docs/mcp/prompts.md](../mcp/prompts.md).

## Troubleshooting

→ [docs/mcp/quickstart-cursor.md#troubleshooting](../mcp/quickstart-cursor.md)

## Related

- [docs/mcp/prompts.md](../mcp/prompts.md)
- [docs/mcp/tools.md](../mcp/tools.md)
- [`mcp-onboarding-cold-test.md`](https://github.com/cordum-io/cordum/blob/main/docs/mcp/mcp-onboarding-cold-test.md) on GitHub
