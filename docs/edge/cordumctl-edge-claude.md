# cordumctl Edge Claude settings generation

`core/edge/claude` now provides reusable generators for Claude Code dev settings and enterprise managed-settings templates. The final user-facing `cordumctl edge claude --settings-output` launch wrapper is owned by EDGE-019; until that command lands, do not invent alternate command names in scripts or docs. The helper behavior implemented for that future command is:

- `--settings-output -` writes generated JSON to stdout.
- file output uses create-only semantics and refuses to overwrite existing files.
- preview output is redacted and includes the dev-vs-enterprise token tradeoff.
- enterprise template output is placeholders plus notes, not deployment automation.

## Dev settings generator

The dev generator emits a Claude `settings.json` payload with:

- `$schema: https://json.schemastore.org/claude-code-settings.json`
- `env` entries for `CORDUM_EDGE_SESSION_ID`, `CORDUM_EDGE_EXECUTION_ID`, `CORDUM_AGENTD_URL` or future socket, `CORDUM_AGENTD_HOOK_TIMEOUT`, `CORDUM_EDGE_MODE`, `CORDUM_AGENTD_FAIL_CLOSED`, `CORDUM_EDGE_APPROVAL_WAIT_TIMEOUT`, and `CORDUM_EDGE_PLATFORM`
- command hooks for `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `ConfigChange`, and `FileChanged`
- no HTTP hook URLs and no long-lived API keys or tokens

Default local-dev hook command is `cordum-hook` on `PATH`. Absolute paths with spaces are quoted for Claude command hooks.

## Enterprise managed settings generator

The managed template generator emits:

- `managed-settings.json` with `allowManagedHooksOnly: true`, `allowManagedMcpServersOnly: true`, `disableBypassPermissionsMode: "disable"`, `allowedHttpHookUrls: []`, managed Cordum command hooks, and enterprise-strict fail-closed env
- `managed-mcp.json` with a `cordum-edge` MCP server placeholder and `headersHelper` that calls `cordum-agentd`
- notes for Jamf/macOS, Intune/Windows, Linux/WSL `/etc/claude-code/`, and system-policy rollout

The LLM proxy placeholder uses `ANTHROPIC_BASE_URL`; credentials must come from `apiKeyHelper` or `headersHelper` backed by agentd memory/keychain/service bootstrap. Do not put `ANTHROPIC_API_KEY`, `CORDUM_API_KEY`, bearer tokens, raw prompts, or raw tool payloads in Claude settings.

## Verification in Claude Code

After the future EDGE-019 wrapper writes or points Claude at generated settings:

1. Run `/hooks` and confirm Cordum command hooks appear for the required events.
2. Run `/status` and confirm whether the settings source is local dev settings or enterprise managed settings.
3. Trigger a safe `PreToolUse` command and confirm the hook reaches local `cordum-agentd`.
4. Change a local `.claude/settings.local.json` file in enterprise-strict mode and confirm `ConfigChange` blocks unauthorized changes.
5. Change a watched file such as `CLAUDE.md` and confirm `FileChanged` is observed but not treated as a blocking event.

HTTP hooks are intentionally absent. They were allowed only for the EDGE-000 spike because Claude Code treats HTTP hook failures/timeouts/non-2xx responses as non-blocking.
