# Claude hook mapper contract

`core/edge/claude` contains the Cordum-owned mapper between Claude Code hook
payloads and Cordum Edge action/evaluate shapes. The mapper is intentionally
Claude-specific: generic Edge models, stores, and Gateway handlers do not parse
Claude hook JSON directly.

This contract was reviewed against the official Claude Code hooks reference:
<https://docs.anthropic.com/en/docs/claude-code/hooks>. Treat that page as the
source for Claude hook event and output semantics; keep this page focused on the
Cordum mapping rules.

## Inputs

`HookInput` parses the bounded command-hook stdin JSON and keeps the verbatim
bytes only in `RawPayload` for in-memory local agentd forwarding. The mapper
uses these known fields when present:

| Field | Mapper use |
| --- | --- |
| `hook_event_name` | Selects the Edge action kind and Claude output mode. |
| `session_id` | Informational Claude session value; env-derived Cordum session wins. |
| `transcript_path`, `cwd`, `permission_mode` | Bounded diagnostics only; never raw labels. |
| `tool_name`, `tool_use_id` | Tool classification and action replay binding. |
| `tool_input` | Main PreToolUse/PostToolUse action input. |
| `prompt` | UserPromptSubmit redacted input. |
| `tool_response`, `duration_ms`, `error` | PostToolUse/PostToolUseFailure redacted evidence. |
| `source`, `file_path`, `event`, `is_interrupt` | ConfigChange/FileChanged evidence. |

`MappingContext` supplies trusted Cordum metadata from the runner or agentd
environment: tenant, principal, session, execution, agent product, and agent
version. These values are sanitized at the hook boundary before they can appear
in mapped requests or labels.

## Supported events

| Claude hook event | Edge kind | Blocking meaning |
| --- | --- | --- |
| `PreToolUse` | `hook.pre_tool_use` | May allow, deny, ask, or constrain before the tool runs. |
| `PostToolUse` | `hook.post_tool_use` | Tool already ran; output is feedback/context only. |
| `PostToolUseFailure` | `hook.post_tool_use_failure` | Failure evidence and feedback/context only. |
| `UserPromptSubmit` | `hook.user_prompt_submit` | May block the submitted prompt or add context. |
| `ConfigChange` | `hook.config_change` | Can block in strict/fail-closed modes. |
| `FileChanged` | `hook.file_changed` | Audit-only; never claims prevention. |

Unsupported future events map to a degraded action with
`reason_code=unknown_hook_event`, `capability=edge.unknown`, and
`risk_tags=["review_required","unknown"]`.

## Supported tools and classification

The mapper calls the shared Edge classifier after redaction. Client-provided
capability, risk tags, and labels are not trusted.

| Claude tool name | Capability |
| --- | --- |
| `Bash` | `exec.shell` |
| `Read` | `file.read` |
| `Edit`, `Write`, `MultiEdit` | `file.write` |
| `Delete`, `Remove` | `file.delete` |
| `Move`, `Rename` | `file.move` |
| Anything else | `edge.unknown` plus review-required risk tags |

Bash command families and path classes come from the server-side classifier.
Unrecognized shell shapes are not marked safe; they keep review-required risk.

## Version drift and degraded mappings

Claude may add fields without notice. Cordum handles that conservatively:

- Unknown top-level and nested fields are not promoted to labels.
- Known action-relevant maps are redacted before classification.
- Missing `tool_name` on tool hooks maps to `reason_code=missing_tool_name`.
- Missing `tool_input` on `PreToolUse` maps to
  `reason_code=missing_tool_input`.
- Unexpected classifier failures map to
  `reason_code=unsupported_tool_input_shape` with unknown/review-required risk.
- Malformed JSON, multiple JSON objects, non-object JSON, empty stdin, oversize
  stdin, and stdin timeouts are runner errors, not mapper actions.

## Redaction and hashing

The mapper uses the Edge redaction helper before producing public mapped data:

- `InputRedacted` contains the action-relevant redacted view only.
- Raw prompts, commands, tool responses, transcripts, local paths, credentials,
  and authorization material must not appear in labels, docs, fixtures, stderr,
  or mapped requests.
- `RawPayload` is the only place where verbatim hook stdin can remain, and it is
  in-memory only for local agentd processing.
- `InputHash` is `sha256:<hex>` over canonical JSON for `InputRedacted`.
- `ActionHash` binds kind, tool name, tool use ID, capability, and `InputHash`
  for approval retry/replay checks.

Synthetic redacted example:

```json
{
  "hook_event_name": "PreToolUse",
  "session_id": "sess_synthetic_001",
  "cwd": "/redacted/workspace",
  "tool_name": "Bash",
  "tool_use_id": "tu_synthetic_001",
  "tool_input": {
    "command": "go test ./core/edge"
  }
}
```

Public mapped data may include the redacted input and hashes:

```json
{
  "layer": "hook",
  "kind": "hook.pre_tool_use",
  "capability": "exec.shell",
  "risk_tags": ["exec", "test"],
  "input_redacted": {
    "command": "go test ./core/edge"
  },
  "input_hash": "sha256:<synthetic-redacted-hash>",
  "action_hash": "sha256:<synthetic-redacted-hash>"
}
```

If the original hook payload contained credential-shaped values, the public
shape must show placeholders such as `<redacted>`, never the raw value.

## Agentd request mapping

`agentdRequest` forwards both legacy hook fields and the EDGE-016 mapped fields
to local `cordum-agentd`:

- `layer`, `kind`, `capability`, `risk_tags`, `labels`
- `input_redacted`, `input_hash`, `action_hash`
- tenant/principal/session/execution IDs from sanitized mapping context
- `reason_code` for degraded parseable payloads
- `raw_payload` only for bounded in-memory local processing

The hook must not persist this request and must not send it to a remote Gateway.
Agentd is responsible for converting the mapped action to the final Gateway
evaluate request.

## Output mapping

`MapEdgeDecisionToHookOutput` accepts canonical Edge decisions
`ALLOW`, `DENY`, `REQUIRE_APPROVAL`, `THROTTLE`, and `CONSTRAIN`
(case-insensitive for defensive legacy passthrough). Unknown decisions return
an empty Claude output plus an error so the runner can choose strict fail-closed
or no-op behavior.

### PreToolUse

| Edge decision | Claude output |
| --- | --- |
| `ALLOW` | `hookSpecificOutput.permissionDecision="allow"` with a redacted reason. |
| `DENY` | `permissionDecision="deny"` with a redacted reason shown to Claude. |
| `THROTTLE` | `permissionDecision="deny"` with retry/throttle guidance. |
| `REQUIRE_APPROVAL` | Immediate `permissionDecision="deny"` with `approval_ref` and "approve then retry the tool call" guidance. |
| `CONSTRAIN` | `permissionDecision="allow"` plus safe `updatedInput`; missing `updatedInput` falls back to deny. |

Approval example:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "deny",
    "permissionDecisionReason": "approval required; approval_ref=edge_appr_synthetic_001; approve then retry the tool call"
  }
}
```

### UserPromptSubmit

`DENY`, `REQUIRE_APPROVAL`, and `THROTTLE` map to top-level
`decision="block"` with a redacted reason. `ALLOW` and `CONSTRAIN` produce
empty output unless there is safe `additionalContext`; prompts are not rewritten.

### PostToolUse and PostToolUseFailure

`DENY`, `REQUIRE_APPROVAL`, and `THROTTLE` map to top-level
`decision="block"` and optional `additionalContext`. This is feedback for
Claude after execution. The mapper never emits a PreToolUse
`permissionDecision` for these events and never claims the tool was prevented.

### ConfigChange and FileChanged

`ConfigChange` can return top-level `decision="block"` for deny-like decisions
in strict/fail-closed paths. `FileChanged` always returns an empty output because
it is audit-only.

## Fixture and docs hygiene

Mapper fixtures live under `core/edge/claude/testdata/hooks/` and must remain
synthetic. Do not commit raw Claude transcripts, real workspace paths, prompts,
tool outputs, credentials, or authorization material. Use placeholders such as
`sess_synthetic_001`, `/redacted/workspace`, and `<redacted>`.
