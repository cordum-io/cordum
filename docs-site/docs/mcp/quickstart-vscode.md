# MCP Quickstart — VS Code

Wire VS Code's MCP-aware assistants (GitHub Copilot Chat with MCP, or
any MCP-compatible extension) to Cordum.

## Prereqs

See [_prereqs.md on GitHub](https://github.com/cordum-io/cordum/blob/main/docs/mcp/_prereqs.md). You'll need `CORDUM_API_KEY`,
`CORDUM_GATEWAY`, and a Cordum instance running.

## Configure

Open your user `settings.json` (Cmd/Ctrl+Shift+P → *Preferences: Open
User Settings (JSON)*) and add:

```json
{
  "mcp.servers": {
    "cordum": {
      "command": "cordumctl",
      "args": ["mcp", "stdio"],
      "env": {
        "CORDUM_API_KEY": "${env:CORDUM_API_KEY}",
        "CORDUM_GATEWAY": "${env:CORDUM_GATEWAY}",
        "CORDUM_TENANT_ID": "default"
      }
    }
  }
}
```

For HTTP transport, use an MCP-aware extension that supports it — the
endpoint is `${CORDUM_GATEWAY}/mcp/sse`, same Authorization and
X-Tenant-ID headers as the other clients.

Reload VS Code. The MCP panel should list `cordum` with the full
catalogue.

## Try it

In any MCP-aware chat panel:

```
list my recent jobs
show me pending approvals
what integration packs are installed
```

## TLS: trusting the self-signed dev CA

VS Code's HTTP MCP transport uses Node's default trust store. Two
options for local dev:

**Option 1 — use stdio (simplest).** `cordumctl mcp stdio` handles the
CA pinning itself, so VS Code never sees a TLS handshake.

**Option 2 — set `NODE_EXTRA_CA_CERTS` for the workspace.**

Add to `.vscode/settings.json` (workspace) or your shell profile:

```json
{
  "terminal.integrated.env.linux": {
    "NODE_EXTRA_CA_CERTS": "${env:CORDUM_TLS_DIR}/ca/ca.crt"
  },
  "terminal.integrated.env.osx": {
    "NODE_EXTRA_CA_CERTS": "${env:CORDUM_TLS_DIR}/ca/ca.crt"
  },
  "terminal.integrated.env.windows": {
    "NODE_EXTRA_CA_CERTS": "${env:CORDUM_TLS_DIR}\\ca\\ca.crt"
  }
}
```

Reload the window after saving so the MCP extension picks up the env.

## What to try next: the 4 shipped prompts

The four first-party prompts are available from any MCP-aware chat
panel under the Cordum server. See
[docs/mcp/prompts.md](./prompts.md) for full arg schemas. Typical
invocations:

1. **summarize_approvals** — `window=24h` for a recent-shift digest.
2. **explain_denial** — `job_id=<id>` for a plain-English deny reason.
3. **draft_safety_rule** — writes a YAML rule scaffold; simulate
   before applying via `/api/v1/policy/simulate`.
4. **policy_migration_helper** — `from_version=... to_version=...`
   rewrites a pasted bundle; re-sign + `cordum_audit_verify` before
   publish.

## Troubleshooting

* **`cordumctl: command not found`** — install via
  `go install github.com/cordum/cordum/cmd/cordumctl@latest` or add
  the repo's `dist/` to your PATH.
* **Empty tool list after reload** — VS Code caches extension schemas;
  try *Developer: Reload Window* twice.
* **401 Unauthorized** — regenerate the API key and restart the
  workspace so the `${env:...}` substitution picks up the new value.
* **TLS `UNABLE_TO_VERIFY_LEAF_SIGNATURE`** — see the TLS section
  above; stdio avoids the issue entirely.
* **`-32601 prompt not found`** — gateway hasn't registered prompts
  yet. Bump to a release that ships `RegisterAllPrompts`.

## Related

* [docs/mcp/tools.md](./tools.md)
* [docs/mcp/resources.md](./resources.md)
* [docs/mcp/prompts.md](./prompts.md)
* [docs/mcp/quickstart-claude-code.md](./quickstart-claude-code.md)
* [docs/mcp/quickstart-cursor.md](./quickstart-cursor.md)
