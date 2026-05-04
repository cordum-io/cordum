# cordum-hook command contract

`cordum-hook` is the Cordum Edge P0 Claude Code command hook binary. It is not the Week 0 HTTP spike server. Claude Code starts the binary from `type: "command"` hook settings, sends one hook payload on stdin, and reads any Claude-compatible hook output from stdout. The hook talks only to local `cordum-agentd`; it must not call the Cordum Gateway directly.

## Supported Claude hook events

The EDGE-015 binary supports these subcommands:

```bash
cordum-hook claude pre-tool-use
cordum-hook claude post-tool-use
cordum-hook claude post-tool-use-failure
cordum-hook claude user-prompt-submit
cordum-hook claude config-change
cordum-hook claude file-changed
```

Unsupported subcommands return usage on stderr, write nothing to stdout, and exit `2`. Unsupported `hook_event_name` values in stdin are treated as non-governed unknown events by default: redacted stderr warning, empty stdout, exit `0`. In enterprise strict mode, unknown events exit `2` because the hook cannot prove they are safe.

## Stdin limits and timeouts

The hook reads exactly one JSON object from stdin.

| Setting | Default | Description |
| --- | --- | --- |
| `CORDUM_HOOK_MAX_INPUT_BYTES` | `1048576` | Maximum stdin payload size. Values above 8 MiB are ignored. |
| `CORDUM_AGENTD_HOOK_TIMEOUT` | `4.5s` | Total hook wall-clock budget for stdin read, local agentd decision, and response write. Duration strings such as `500ms`, `2s`, or numeric seconds are accepted; values `>=5s` are rejected. |

Hook timeout MUST stay strictly below Claude Code's 5s hook deadline (PRD §7.4) or Claude treats the hook as unresponsive and may proceed without Cordum's governance decision for that tool call. The default `4.5s` budget is split into a `4s` local agentd POST budget plus a `500ms` response-write reserve. Custom `CORDUM_AGENTD_HOOK_TIMEOUT` values below `5s` shrink the agentd budget proportionally so response serialization still has reserved time.

Migration note: if you previously customized this value to `8s` or relied on the old `10s` default, lower it below `5s` (prefer the new `4.5s` default) before rollout.

Invalid, empty, non-object, multiple JSON values, oversize input, and stdin timeout all produce empty stdout, redacted stderr, and exit `2`.

## Local agentd endpoint

EDGE-015 supports a loopback HTTP endpoint for the local agentd contract:

| Setting | Default | Description |
| --- | --- | --- |
| `CORDUM_AGENTD_URL` | `http://127.0.0.1:8765/v1/edge/hooks/claude` | Local agentd decision endpoint. Host must be loopback (`localhost`, `127.0.0.1`, or `::1`). Remote Gateway URLs are rejected. Must be the bare URL without `?nonce=` in generated settings. |
| `CORDUM_AGENTD_HOOK_NONCE` | empty | Runtime-only process env injected by the trusted launcher/agentd wrapper. `cordum-hook` sends it as `X-Cordum-Agentd-Nonce`; generated Claude settings and managed-settings JSON must never persist this value. |
| `CORDUM_EDGE_EXECUTION_ID` | empty | Optional execution correlation ID forwarded to agentd. |
| `CORDUM_EDGE_SESSION_ID` | empty | Optional generated settings/session correlation ID. Claude's hook payload `session_id` remains the primary runtime session ID. |
| `CORDUM_EDGE_APPROVAL_WAIT_TIMEOUT` | empty | Optional generated settings value for future approval wait UX; EDGE-015 does not inline-wait. |
| `CORDUM_EDGE_PLATFORM` | empty | Optional generated settings platform marker used for diagnostics and docs. |

Future `cordum-agentd` work may add a user-only socket transport. Until then,
the loopback URL is still a local-agentd boundary; do not configure the hook to
call Gateway or any remote host. Existing `CORDUM_AGENTD_URL?...nonce=` settings
fail closed with a migration error; regenerate settings so the URL stays bare
and `CORDUM_AGENTD_HOOK_NONCE` is supplied only in the hook process environment.

The request sent to agentd contains bounded hook metadata, session/execution IDs, tool metadata, and the bounded raw Claude payload only in memory. The hook does not persist or log raw payloads.

Hook generates `session_id` (or inherits from the wrapper's
`~/.claude/settings.json`) and validates it against the auth tenant at the
gateway boundary. `execution_id` is issued by agentd at execution-create
time. See [Edge identity contract](identity-contract.md) for the full
ownership chain (session/execution/event/trace/job/workflow_run) and the
tenant-scoping invariant established by EDGE-008.7.

For the developer wrapper that starts agentd, generates temporary settings,
and launches Claude Code, see [`cordumctl edge claude`](./cordumctl-edge-claude.md).

See [`LOCAL_E2E.md` § Edge fake-hook E2E](../LOCAL_E2E.md#edge-fake-hook-e2e)
for a CI-safe end-to-end exerciser of the Gateway side of this contract.

## Mapper contract

Before calling local `cordum-agentd`, the hook runs the EDGE-016 Claude mapper
documented in [`claude-hook-mapper.md`](./claude-hook-mapper.md). The mapper:

- parses the known Claude hook fields from bounded stdin and tolerates unknown
  future fields without trusting them;
- maps supported events to Edge action kinds for `PreToolUse`, `PostToolUse`,
  `PostToolUseFailure`, `UserPromptSubmit`, `ConfigChange`, and `FileChanged`;
- classifies tools through the shared Edge classifier (`Bash`, `Read`,
  `Edit`/`Write`/`MultiEdit`, `Delete`/`Remove`, `Move`/`Rename`, and unknown
  tools);
- redacts action inputs and sanitized context before building agentd/evaluate
  fields; and
- emits stable `input_hash` and `action_hash` values used by approval retry and
  replay checks.

`RawPayload` is the only verbatim hook input copy and stays in memory for the
local agentd boundary. It must not be logged, persisted, written to docs, or sent
directly to Gateway.

## Stdout and stderr rules

- Stdout is reserved for Claude hook JSON only.
- Usage, degraded warnings, and errors go to stderr.
- Diagnostics are bounded and redacted by key and value. They must not include raw `tool_input.command`, file contents, prompts, transcript contents, Authorization headers, API keys, tokens, passwords, or raw agentd response bodies.

## Decision behavior

### PreToolUse

Agentd decisions map to Claude `hookSpecificOutput`:

| Agentd decision | Claude output |
| --- | --- |
| `allow` | `permissionDecision: "allow"` with the agentd reason. |
| `deny` | `permissionDecision: "deny"` with the agentd reason. |
| `ask` | `permissionDecision: "ask"` with the agentd reason and optional `updatedInput`. |
| `require_approval` | Immediate `permissionDecision: "deny"` containing `approval_ref` plus retry guidance. No inline wait is performed in EDGE-015. |

### UserPromptSubmit

Denied prompts return top-level `decision: "block"` with a redacted reason. Allowed prompts normally produce empty stdout unless agentd supplies safe additional context.

### PostToolUse and PostToolUseFailure

The tool has already run. Denials are mapped to Claude feedback/block/additional context only; the hook output must not claim execution was prevented.

### ConfigChange

`ConfigChange` receives Claude settings/skill change metadata (`source` and optional `file_path`) and forwards the bounded payload to local `cordum-agentd`. In `enterprise-strict` or fail-closed mode a deny decision becomes top-level `decision: "block"` so Claude Code rejects the new user/project/local settings. Outside strict mode, ConfigChange is observe-only and returns empty stdout even if agentd would have blocked.

Managed policy settings changes cannot be blocked by Claude Code; Cordum still observes them for audit.

### FileChanged

`FileChanged` receives `file_path` and `event` for watched config files such as `.claude/settings.json`, `.claude/settings.local.json`, and `CLAUDE.md`. Claude Code does not let FileChanged hooks block the file change, so Cordum forwards the event to agentd and returns empty stdout in every mode.

## Fail modes

| Mode | Agentd unavailable, timeout, or malformed response | Malformed hook input |
| --- | --- | --- |
| `observe` (default) | Allow/no-op, emit a redacted `agentd_unavailable` or `agentd_timeout` warning to stderr. | Exit `2`, empty stdout, redacted stable error. |
| `local-dev-enforce` | For `PreToolUse`, deny risky or unclassified actions with structured Claude deny JSON. | Exit `2`, empty stdout. |
| `enterprise-strict` or `CORDUM_AGENTD_FAIL_CLOSED=true` | Fail closed. For parseable `PreToolUse`, emit structured deny JSON. For `ConfigChange`, emit structured `decision:"block"`. `FileChanged` remains non-blocking. Hooks without a safe block JSON exit `2`. | Exit `2`, empty stdout. |

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Hook handled the event. Stdout may be empty or contain Claude-compatible JSON. Strict `PreToolUse` deny JSON also exits `0` because Claude consumes the JSON decision. |
| `2` | The hook could not safely process the event or command. Stdout is empty unless a parseable event was safely converted to deny JSON. |

## Example Claude settings

Use command hooks. HTTP hooks are permitted only for the EDGE-000 spike.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "cordum-hook claude pre-tool-use",
            "timeout": 5
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "cordum-hook claude post-tool-use",
            "timeout": 5
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "cordum-hook claude user-prompt-submit",
            "timeout": 5
          }
        ]
      }
    ],
    "ConfigChange": [
      {
        "matcher": "user_settings|project_settings|local_settings|skills",
        "hooks": [
          {
            "type": "command",
            "command": "cordum-hook claude config-change",
            "timeout": 5
          }
        ]
      }
    ],
    "FileChanged": [
      {
        "matcher": ".claude/settings.json|.claude/settings.local.json|CLAUDE.md",
        "hooks": [
          {
            "type": "command",
            "command": "cordum-hook claude file-changed",
            "timeout": 5
          }
        ]
      }
    ]
  }
}
```

Use `/hooks` in Claude Code to confirm the command hooks are active and `/status` to confirm which settings source (local project/user or enterprise managed settings) provided them. HTTP hooks are forbidden for production enforcement because Claude Code treats HTTP connection failures, timeouts, and non-2xx responses as non-blocking. For Claude Code hook JSON semantics, see the official Claude Code hooks documentation: <https://docs.anthropic.com/en/docs/claude-code/hooks>.
