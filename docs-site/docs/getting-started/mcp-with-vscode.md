# Drive Cordum with VS Code

Wire VS Code (with a supported MCP extension) to Cordum so you can
inspect jobs, policies, and approvals from the editor — and have
every call land on the governance audit trail.

**Target duration: ≤ 5 minutes.**

> This page is the DoD-named quickstart path. The canonical deep
> quickstart with stdio vs HTTP, workspace-vs-user settings, and CA
> trust details lives at
> [docs/mcp/quickstart-vscode.md](../mcp/quickstart-vscode.md).

## 1. Prereqs

Shared prerequisites (same across every MCP client):

- `CORDUM_API_KEY` exported from `.env`.
- `CORDUM_TENANT_ID` set (defaults to `default`).
- `cordum-mcp` on `$PATH` for stdio (cert-free) OR gateway over HTTPS with `--cacert` for self-signed dev.
- Gateway rate limit: 100 req/s per API key.

Full reference: [`docs/mcp/_prereqs.md`](https://github.com/cordum-io/cordum/blob/main/docs/mcp/_prereqs.md) on GitHub.

![CORDUM_API_KEY in the Cordum dashboard](pathname:///img/mcp-vscode/01-api-key.png)

## 2. Install a VS Code MCP extension

Install a VS Code MCP extension (examples in the canonical
quickstart). Verify the extension appears in the Extensions panel.

![VS Code Extensions panel with the MCP extension installed](pathname:///img/mcp-vscode/02-extension.png)

## 3. Configure Cordum as an MCP server

Add a Cordum server entry to the extension's `mcp.json` (workspace
or user scope) — the stdio form is the cert-free default:

```json
{
  "mcpServers": {
    "cordum": {
      "command": "cordum-mcp",
      "env": {
        "CORDUM_API_KEY": "${env:CORDUM_API_KEY}",
        "CORDUM_TENANT_ID": "${env:CORDUM_TENANT_ID}"
      }
    }
  }
}
```

Full HTTP + self-signed-CA variants:

→ [docs/mcp/quickstart-vscode.md#3-configure-mcp](../mcp/quickstart-vscode.md)

![VS Code MCP panel listing cordum as a connected server](pathname:///img/mcp-vscode/03-mcp-panel.png)

## 4. First tool calls

Open the extension's chat panel and ask:

- "List my cordum jobs." → `cordum_list_jobs`.
- "What policies are active?" → `cordum_list_policy_bundles`.
- "Show pending approvals." → `cordum_list_approvals`.

![VS Code chat panel surfacing a cordum tool-call result](pathname:///img/mcp-vscode/04-tool-call.png)

## 5. What to try next

The four shipped prompts are callable via
`/mcp.cordum.<prompt_name>`; schemas in
[docs/mcp/prompts.md](../mcp/prompts.md).

## Troubleshooting

→ [docs/mcp/quickstart-vscode.md#troubleshooting](../mcp/quickstart-vscode.md)

## Related

- [docs/mcp/prompts.md](../mcp/prompts.md)
- [docs/mcp/tools.md](../mcp/tools.md)
- [`mcp-onboarding-cold-test.md`](https://github.com/cordum-io/cordum/blob/main/docs/mcp/mcp-onboarding-cold-test.md) on GitHub
