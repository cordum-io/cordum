# MCP Audit Events

Four SIEMEvent types cover the full MCP tool lifecycle. Every terminal
tools/call lands on the Merkle audit chain and flows through the
configured SIEM exporter (webhook / syslog / Datadog / CloudWatch).

| EventType | Emitted when |
|-----------|--------------|
| `mcp.tool_invocation` | Every inbound tools/call terminates (success or handler error). |
| `mcp.tool_outbound_invocation` | Every Cordum-initiated call to an external MCP server terminates. |
| `mcp.tool_denied` | Scope filter rejects a tools/call before dispatch. |
| `mcp.tool_approval` | Approval lifecycle event: enqueue / approve / reject / expire / consume. |

A single inbound call that passes scope + approval produces:
1. `mcp.tool_approval` (`outcome=consume`) with the approval_id — if gated.
2. `mcp.tool_invocation` with `approval_status=consumed` and the same approval_id for correlation.

A denied call produces:
1. `mcp.tool_denied` with `sub_reason` (tool_not_in_allowed_list |
   risk_tier_too_low | missing_data_classification | no_identity).
2. (No invocation event — the call never reached the dispatcher.)

## Extra field catalogue

### mcp.tool_invocation / mcp.tool_outbound_invocation

| Field | Meaning |
|-------|---------|
| `tool_name` | The tool name the caller asked for. |
| `direction` | `inbound` or `outbound`. |
| `args_redacted` | The JSON argument blob after redaction. Sensitive fields (`password`, `api_key`, `token`, `authorization`, `secret`, `private_key`, etc.) replaced with `[REDACTED:<rule>]`. Regex heuristics also redact AWS access keys, Stripe secrets, JWTs, and PEM private keys. Never contains plaintext secrets. |
| `result_type` | `ok` when the handler returned a non-error `ToolCallResult`; `error` when the handler returned an error or `result.IsError==true`. |
| `result_count` | Length of `result.Content`. |
| `result_hash` | SHA-256 of the full result JSON — proves result integrity without logging the body. |
| `latency_ms` | Wall-clock time from Start to Finish, in milliseconds. |
| `approval_status` | `consumed` when an approval_id was present on ctx; `none` otherwise. |
| `approval_id` | Correlates to the matching `mcp.tool_approval(outcome=consume)` event. Present only when an approval was consumed. |
| `server_id` | Outbound only — the external MCP server's identifier. |
| `error_code` | Present only when `result_type=error`; truncated to 512 bytes. |
| `identity_missing` | `true` when the call ran with an empty agent_id. Flag legacy call paths for cleanup. |

### mcp.tool_denied

| Field | Meaning |
|-------|---------|
| `tool_name` | The tool the caller tried to invoke. |
| `sub_reason` | One of `tool_not_in_allowed_list`, `risk_tier_too_low`, `missing_data_classification`, `no_identity`. |
| `agent_id` | Duplicated from the top-level field for SIEM rules that key on Extra. |

### mcp.tool_approval

| Field | Meaning |
|-------|---------|
| `tool_name` | Tool requiring approval. |
| `args_hash` | SHA-256 of the canonical args JSON (truncation-resistant match). |
| `approval_id` | Stable ID correlating with the matching `mcp.tool_invocation`. |
| `requester` | Principal that initiated the gated call. |
| `resolver` | Principal that approved/rejected (present on consume). |
| `outcome` | `enqueue` | `approve` | `reject` | `expire` | `consume`. |
| `reason` | Trigger reason (set at enqueue, never overwritten). |

## Correlation patterns

### Splunk SPL

```
index=cordum_audit event_type IN ("mcp.tool_invocation","mcp.tool_approval")
 | eval approval_id = coalesce('extra.approval_id', 'extra.approval_id')
 | stats values(event_type) as types, values(extra.outcome) as outcomes,
         values(extra.result_type) as result by tenant_id, approval_id
 | where mvcount(types) > 1
```

### Datadog Logs

```
service:cordum-gateway @event_type:(mcp.tool_invocation OR mcp.tool_approval)
 group @extra.approval_id
 aggregate count by @tenant_id, @extra.approval_id
```

### CloudWatch Logs Insights

```
fields @timestamp, event_type, tenant_id, extra.tool_name, extra.approval_id, extra.result_type
| filter event_type in ["mcp.tool_invocation", "mcp.tool_approval"]
| stats count() by tenant_id, extra.approval_id
```

## Redaction testing

Operators can dry-run the redactor against a sample payload without
shipping real secrets to logs via the `cordumctl mcp redact-test`
subcommand (follow-up task). The current mechanism: apply the
default redactor to an example JSON in-process and diff the result.
The redactor NEVER logs the raw input — only the redacted output.

Customers who want tenant-scoped rules can extend `DefaultRedactionRules`
via an OpenAPI-described PolicyBundle addition (future work) — the
`RedactionRule` type already supports field-name or regex rules with a
description visible in the replacement marker.

## Retention

MCP audit events flow through the same Merkle audit chain as every
other SIEM event. They inherit the tenant retention window
(`CORDUM_AUDIT_RETENTION_HOURS`, default 168h = 7 days) and legal-hold
policy. Compliance exports filter by `event_type` and time range via
`GET /api/v1/audit/export?format=json` (see task-70dc1bb1 / SOC2).
