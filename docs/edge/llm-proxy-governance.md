# LLM-proxy governance — every chat turn redacted + audited

> Total Copilot governance, **Phase 2**. Phase 1 (`docs/edge/...` Copilot agent
> hooks) governs every chat + tool action cooperatively. This phase adds the
> **mandatory network-layer backstop**: an enterprise LLM proxy intercepts every
> model request/response and routes it through Cordum for redaction + audit, so
> secrets cannot reach a provider and every chat turn lands in the Edge audit
> trail even if a cooperative hook is bypassed.

## The two endpoints a proxy uses

A governed LLM proxy pairs **two existing gateway endpoints** per chat turn —
this keeps the safety-kernel decision single-sourced and the redaction/audit
mandatory:

| Concern | Endpoint | Who decides |
|---|---|---|
| **Block a prompt/response** (ALLOW / DENY / REQUIRE_APPROVAL) | `POST /api/v1/edge/evaluate` | Safety kernel (layer-agnostic; already classifies `layer=llm`) |
| **Redact + audit** every prompt/response (mandatory) | `POST /api/v1/edge/llm/events` *(new)* | The Edge redactor; advisory `record`/`redact` returned to the proxy |

> The LLM ingest endpoint deliberately does **not** make the allow/deny policy
> decision. That stays in `/evaluate` so there is exactly one kernel contract.
> The ingest endpoint owns the part that must hold unconditionally: nothing
> sensitive is persisted (or forwarded, if the proxy honors the advisory), and
> the chat turn is recorded for compliance.

### Per-turn flow

```
Copilot ──prompt──▶ LLM proxy ─┬─▶ POST /edge/evaluate   {layer:llm, kind:llm.request.pre, input_redacted}
                               │      └─ DENY  → proxy blocks the turn
                               │      └─ ALLOW → continue
                               ├─▶ POST /edge/llm/events {kind:llm.request.pre, content:<raw>}
                               │      └─ decision=redact → forward redacted_content (secrets masked)
                               │      └─ decision=record → forward as-is
                               └─▶ provider … completion
                          ◀── completion ── proxy ─▶ POST /edge/llm/events {kind:llm.request.post, content:<completion>}
```

## `POST /api/v1/edge/llm/events`

Disabled by default. Set `CORDUM_EDGE_LLM_INGEST_ENABLED=true` to expose it;
unset returns `503 service_unavailable` with no writes.

### Auth

The proxy authenticates as a principal holding the `edge.llm.ingest`
permission (built-in fallback role `llm_proxy`, or any RBAC role granting
`edge.llm.ingest` / `edge.llm.*` / `edge.*`). The request `source.source_id`
**must** equal the authenticated principal. Generic `jobs.write` is rejected.

### Parent binding (why it differs from runtime ingest)

A runtime sidecar is bound 1:1 to the session/execution it produced, so runtime
ingest checks `session.principal == collector`. An LLM proxy is **shared
tenant-scoped infrastructure** fronting many developers, so it is **not** bound
to a session principal. Instead the gateway requires:

- the referenced `session_id` / `execution_id` exist **within the tenant**
  (`X-Tenant-ID`; cross-tenant references are rejected),
- the execution was created under the **`llm-proxy` adapter**
  (`AdapterLLMProxy`) — so a proxy can only annotate executions it created, never
  a hook or MCP execution, and
- the execution's `worker_id` **matches the authenticated proxy's own
  principal** (its `source.source_id` / auth identity) — so, in a tenant with
  multiple LLM proxies or API keys, one proxy cannot append events onto another
  proxy's execution merely by learning or guessing its `execution_id`. A
  missing or mismatched `worker_id` is rejected (`403 access_denied`), not just
  logged. The proxy must therefore stamp its own identity into `worker_id`
  when it creates the execution (`POST .../executions`).

The proxy therefore creates one `edge` session + `llm-proxy` execution per
governed conversation (via `POST /api/v1/edge/sessions` + `/executions`,
setting `worker_id` to its own principal) and references them on every
`llm/events` call.

### Wire envelope

Strict-schema decode (`DisallowUnknownFields`) rejects smuggled keys
(`authorization`, `headers`, `cookies`, `api_key`, provider keys, …) at the
boundary. The full schema is `EdgeLLMIngestRequest` in
`docs/api/openapi/cordum-api.yaml`.

```jsonc
{
  "source": { "source_id": "<proxy-principal>" },
  "nonce": "<16-64 char [A-Za-z0-9-]>",   // optional (see below)
  "events": [{
    "tenant_id": "...", "session_id": "...", "execution_id": "...",
    "source_event_id": "...",              // stable → idempotent EventID
    "observed_at": "2026-06-24T12:00:00Z",
    "kind": "llm.request.pre",             // | llm.request.post | llm.stream.chunk | llm.cost.recorded
    "provider": "anthropic", "model": "claude-opus-4-8",
    "direction": "prompt",                 // | response
    "content": "<prompt or completion text — redacted server-side>",
    "messages": [{ "role": "user", "content": "..." }],
    "tokens": { "input_tokens": 100, "output_tokens": 20 },
    "cost_usd": 0.0012,
    "labels": { "repo": "cordum" },
    "stream_id": "...",                    // only for kind=llm.stream.chunk
    "sequence": 0,                         // 0-based chunk position within stream_id
    "final": false                         // true on the LAST chunk (see below)
  }]
}
```

### Response

```jsonc
{
  "accepted_count": 1,
  "decisions": [{
    "source_event_id": "...",
    "kind": "llm.request.pre",
    "decision": "redact",                  // | record
    "redacted": true,
    "truncated": false,
    "redacted_content": "deploy with <redacted> now",
    "redacted_messages": [                 // present when the event used `messages`
      { "role": "user", "content": "deploy with <redacted> now" }
    ],
    "findings": ["aws_credential"],        // secret TYPES only, never values
    "redaction_complete": true             // see "Streaming chunk redaction limits"
  }]
}
```

- `decision=record` — no secret detected; the original content is safe to forward.
- `decision=redact` — a secret was detected and masked. The proxy SHOULD forward
  `redacted_content` (flattened transcript) or, for a `messages`-shaped event,
  the role-preserving `redacted_messages` array — instead of the original. If
  `truncated=true` the content exceeded the redacted-evidence cap and the proxy
  must apply its own full-length redaction before forwarding.
- `redaction_complete` — whether this decision reflects a scan of the FULL turn
  content. Always `true` except for `kind=llm.stream.chunk`; see below.

### Streaming chunk redaction limits

`kind=llm.stream.chunk` lets a proxy report a streamed response incrementally,
one delta at a time, so governance visibility doesn't wait for the full
completion. **Each chunk is redacted in isolation.** A secret split across a
chunk boundary (e.g. half an API key in chunk *N*, the other half in chunk
*N+1*) can evade per-chunk scanning even though every individual chunk looks
clean — full server-side reassembly-before-classification (buffering chunks by
`stream_id`, ordering by `sequence`, scanning once on `final`) is a real
architectural feature and is **not implemented yet**. This is a known,
documented gap, not a silent one — treat it as follow-up work, tracked
alongside this doc.

Until reassembly lands, the contract is:

- `stream_id` / `sequence` / `final` exist on the wire **today** so proxies can
  start tagging chunks now and a future reassembly pass has a stable key to
  build on.
- Every `llm.stream.chunk` decision carries `redaction_complete`. It is `true`
  **only** when the envelope was submitted with `final: true` — and a
  `final: true` chunk is **required** to carry the FULL aggregated response
  text in `content` (or `messages`), not just the last delta; a final chunk
  with nothing to scan is rejected (`400 invalid_request`). Every other chunk
  is `redaction_complete: false`.
- **Proxies MUST NOT treat a `redaction_complete: false` decision as a
  governance verdict for forwarding purposes.** Per-chunk decisions are
  best-effort only. A proxy that wants a real, complete-content redaction pass
  for a streamed turn MUST submit a final aggregate — either a `final: true`
  stream chunk carrying the whole response, or a normal `llm.request.post`
  event once the stream completes (the existing, always-redaction-complete
  path used for non-streamed responses).
- The persisted audit event is stamped `llm.redaction_incomplete=true` for any
  non-final chunk, so an auditor querying the Edge event store directly can
  tell a per-chunk-only scan apart from a complete-content one without
  re-deriving it from `kind`/`final`.

Each accepted event is also recorded as a `layer=llm`, `decision=RECORDED`
`AgentActionEvent` (classified `action_name=llm.request`) through the normal Edge
store + SIEM path, with `findings` surfaced as `llm.finding.<type>` labels — so
the chat turn appears in the session/audit trail without persisting the raw
prompt.

### Idempotency & replay

`AppendEvents` is keyed by `EventID`, which is derived deterministically from
`(source_id, tenant, session, execution, kind, source_event_id)`. A `nonce`,
when present, is deduplicated against a Redis replay window scoped to
`(tenant, llm-proxy)` so a proxy retry returns `200 {replayed:true}` without
double-recording. Nonce is **optional** by default (one synchronous call per
chat turn); set `CORDUM_EDGE_LLM_REPLAY_REQUIRED=true` to mandate it.

### Limits (all-or-nothing batch)

| Cap | Value |
|---|---|
| events per batch | 64 |
| HTTP body | 4 MiB |
| raw envelope | 1 MiB |
| redacted string (per field) | 16 KiB |
| redacted total (per event) | 48 KiB |
| labels / messages | 16 / 64 |

A single invalid envelope aborts the whole batch — nothing is persisted.

## Wiring Copilot to the proxy

The proxy is fronted to Copilot as an enterprise model endpoint via managed
settings (`ANTHROPIC_BASE_URL` / provider base URL + a custom CA for TLS
interception). See `core/edge/claude/managed_settings.go` and
`docs/edge/managed-settings-deploy.md`. The proxy binary itself (the MITM
forwarder that calls these two endpoints) is an infrastructure component; this
phase delivers the gateway-side governance it depends on.

## Enforcement bar

| Surface | Guarantee |
|---|---|
| Secret never reaches the provider | Mandatory (proxy honors `redact`; nothing raw is persisted) — **except** a secret split across `llm.stream.chunk` boundaries; see "Streaming chunk redaction limits" |
| Every chat turn audited | Mandatory (recorded `layer=llm` event) |
| Prompt/response policy block | Via `/evaluate` (safety kernel) |
| Holds against hook bypass | Yes — the proxy is in the network path |
| Execution ownership (cross-proxy isolation) | Mandatory — `worker_id` must match the authenticated proxy; fails closed, not just logged |
