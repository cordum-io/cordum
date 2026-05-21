// Package runtimeingest defines the bounded, redacted wire contract a
// trusted runtime sidecar (Tetragon, Falco, an in-cluster eBPF collector,
// etc.) uses to feed process / file / network / DNS telemetry into the
// existing edge.AgentActionEvent stream.
//
// Design rules (kept tight on purpose):
//
//   - Disabled by default. The gateway endpoint that consumes this
//     package's types is gated behind CORDUM_EDGE_RUNTIME_INGEST_ENABLED;
//     production behavior with the flag unset is 503 with no Redis
//     writes.
//
//   - Observe-only. Mapped events carry Layer=runtime, Decision=recorded,
//     RuleTier="". Runtime ingest never enforces; that would belong to a
//     separate epic.
//
//   - No raw evidence. Argv, file contents, packet payloads, DNS
//     response bodies, request bodies, headers, secrets, and tokens are
//     forbidden in the envelope. The adapter rejects envelopes that
//     try to smuggle them under a disguised key, and every redacted
//     string field is capped to MaxRuntimeRedactedStringBytes after
//     passing through edge.RedactValue.
//
//   - No orphan events. Every envelope must name an existing
//     (tenant_id, session_id, execution_id) tuple. Persistence goes
//     through edge.Store.AppendEvents — the same atomic MULTI/EXEC
//     pipeline the hook and MCP surfaces use — so cross-tenant /
//     missing-parent rejects and the per-execution event cap are
//     inherited automatically.
//
//   - No parallel bus. The adapter never writes to Redis or NATS
//     directly; it builds []edge.AgentActionEvent and hands them to the
//     store. There is no second store, no second stream, no second
//     event router.
//
//   - No Cordum Jobs. Runtime events are evidence records, not
//     production work units. They link to the session / execution /
//     artifact graph only.
//
// Out of scope for this package (and for EDGE-144 as a whole): the
// collector daemon itself, eBPF programs, cluster enforcement, a
// dashboard surface, a new retention class, and any new NATS subject.
// See docs/edge/runtime-ingestion.md for the full contract,
// docs/edge/observability.md for where this fits in the Edge timeline,
// and docs/edge/retention.md for the retention bounds runtime events
// inherit.
package runtimeingest
