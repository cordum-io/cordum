// Package llmingest maps a bounded, content-bearing LLM interaction batch
// from a trusted LLM proxy into redacted edge.AgentActionEvent records.
//
// This is the gateway-side "brain" of Cordum's LLM-proxy governance layer
// (Total Copilot governance, Phase 2): an enterprise HTTP(S) proxy intercepts
// every model request/response (every chat turn) and POSTs the prompt and
// completion here. The package then:
//
//  1. strictly decodes the wire batch (DisallowUnknownFields rejects smuggled
//     raw keys such as headers/authorization/cookies);
//  2. redacts the prompt/response content via edge.RedactValue, recording the
//     redaction findings (secret TYPES, never values);
//  3. maps each envelope to an edge.AgentActionEvent (Layer=LayerLLM, the
//     llm.* event kinds) classified via edge.ClassifyEvent so the chat turn
//     lands in the Edge audit/session trail with action_name=llm.request; and
//  4. returns a per-event advisory decision (record | redact) plus the bounded
//     redacted content + finding types so the proxy can forward a redacted
//     prompt.
//
// Mandatory redaction + audit of every prompt/response is the network-layer
// backstop that holds even when a cooperative hook is bypassed. The ALLOW/DENY
// POLICY decision (block a denied prompt) is intentionally NOT made here — it
// reuses the existing, layer-agnostic POST /api/v1/edge/evaluate path (already
// LayerLLM-capable via classifyLLMEvent + the safety kernel), so the kernel
// decision contract stays single-sourced. A proxy that must block pairs an
// evaluate call (decision) with an llm/events call (redaction + evidence).
//
// The shape deliberately mirrors core/edge/runtimeingest with two LLM-specific
// differences: (a) there is no sampling — every chat is governed; and (b) the
// proxy is shared tenant-scoped infrastructure fronting many developers, so the
// gateway binds ingest to the llm-proxy execution adapter within the tenant
// rather than to a per-session collector principal.
package llmingest
