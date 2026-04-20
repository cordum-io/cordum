# MCP Quickstart — Cursor

Wire Cursor to Cordum's MCP server so its AI assistant can operate
your Cordum cluster through natural-language prompts.

## Prereqs

See [_prereqs.md](./_prereqs.md). You'll need `CORDUM_API_KEY`,
`CORDUM_GATEWAY`, and a Cordum instance running.

## Configure

In Cursor → Settings → **Features** → **MCP Servers** → **Add New Server**:

* **Name:** `cordum`
* **Transport:** `stdio` (preferred for local dev)
* **Command:** `cordumctl`
* **Args:** `mcp stdio`
* **Env:** `CORDUM_API_KEY=${CORDUM_API_KEY}`, `CORDUM_GATEWAY=${CORDUM_GATEWAY}`, `CORDUM_TENANT_ID=default`.

For HTTP transport:

```json
{
  "type": "http",
  "url": "${CORDUM_GATEWAY}/api/v1/mcp/sse",
  "headers": {
    "Authorization": "Bearer ${CORDUM_API_KEY}",
    "X-Tenant-ID": "default"
  }
}
```

Reload Cursor. Check **Features → MCP Servers** → `cordum` should show
a green dot and list ~20 tools.

## Try it

In a Cursor chat panel:

```
list my recent jobs
show me pending approvals
what workflows are available
```

## TLS: trusting the self-signed dev CA

If the HTTP transport fails with a `certificate signed by unknown
authority` error, Cursor is rejecting the local dev CA. Two options:

**Option 1 — use stdio (simplest).** `cordumctl mcp stdio` opens a
TLS connection via the Cordum CLI which already trusts
`${CORDUM_TLS_DIR}/ca/ca.crt`. No Cursor-side cert work needed.

**Option 2 — install the CA in the OS trust store.**

- macOS: `sudo security add-trusted-cert -d -r trustRoot -k
  /Library/Keychains/System.keychain ${CORDUM_TLS_DIR}/ca/ca.crt`
- Linux (Debian/Ubuntu): `sudo cp ${CORDUM_TLS_DIR}/ca/ca.crt
  /usr/local/share/ca-certificates/cordum-dev.crt && sudo
  update-ca-certificates`
- Windows: `certutil -addstore "Root" %CORDUM_TLS_DIR%\ca\ca.crt` in
  an elevated shell.

Restart Cursor after the install so its embedded HTTP client re-reads
the system trust store.

## What to try next: the 4 shipped prompts

The same four first-party templates power Cordum's operator workflows.
See [docs/mcp/prompts.md](./prompts.md) for full arg schemas. In
Cursor, open the chat panel and invoke:

1. `/mcp.cordum.summarize_approvals window=24h` — approval digest.
2. `/mcp.cordum.explain_denial job_id=<id>` — plain-English deny reason.
3. `/mcp.cordum.draft_safety_rule` — YAML rule scaffold (simulate before apply).
4. `/mcp.cordum.policy_migration_helper from_version=... to_version=...` — grammar-diff-driven bundle rewrite.

## Troubleshooting

* **No tools listed.** Cursor caches MCP schemas at connection time;
  reconnect the server from the settings panel.
* **HTTP 401.** API key stale or not set. `cordumctl auth key list`
  and regenerate via `cordumctl auth key create` if needed.
* **TLS errors.** See the TLS section above — stdio avoids the whole
  issue; OS trust-store install fixes HTTP.
* **`-32601 prompt not found`.** Gateway hasn't registered prompts
  yet (older deploy). Bump to a release that ships
  `RegisterAllPrompts`.

## Related

* [docs/mcp/tools.md](./tools.md)
* [docs/mcp/resources.md](./resources.md)
* [docs/mcp/prompts.md](./prompts.md)
* [docs/mcp/quickstart-claude-code.md](./quickstart-claude-code.md)
* [docs/mcp/quickstart-vscode.md](./quickstart-vscode.md)
