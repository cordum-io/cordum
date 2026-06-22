package gateway

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cordum/cordum/core/controlplane/safetykernel"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/policy/actiongates"
	"github.com/redis/go-redis/v9"
)

// legacyMCPApprovalArgsHashZero is the 64-character zero-hex placeholder
// used when a legacy ArgsHash is missing or carries the short 8-character
// "00000000" sentinel that predates the canonical-args binding. A SHA-256
// hex digest is always 64 chars; the legacy SIEM correlation tables key
// off this column, so persisting a short value here would either truncate
// downstream rows (BigQuery STRING with NOT NULL + length constraint) or
// silently collide every "no-hash" approval into a single row.
const legacyMCPApprovalArgsHashZero = "0000000000000000000000000000000000000000000000000000000000000000"

// legacyMCPApprovalArgsHash normalizes the ArgsHash the legacy MCP
// approval store sees on the ClaimPreApproved + EnqueueMCPApproval path.
// EDGE-103 mint now derives a real SHA-256 via BuildMCPApprovalBinding,
// but the legacy fallback still accepts an upstream-supplied
// ctxData.ActionHash. Three normalizations:
//
//  1. Trim whitespace — guards against caller bugs that pass " <hex> ".
//  2. Empty string → 64-char zero hex. Empty would otherwise produce a
//     Redis key with a trailing colon and fan out across consumers.
//  3. Short "00000000" placeholder (the 8-char value some legacy emit
//     sites wrote before the canonical hash work) → 64-char zero hex.
//
// A real non-empty, non-placeholder value is preserved BYTE-FOR-BYTE so
// retry idempotency keys stay stable across this normalization pass.
// The function intentionally does NOT validate hex shape: the legacy
// SIEM table accepts arbitrary strings and a stricter check would
// reject perfectly-correlated rows that happened to write non-hex
// debug markers.
func legacyMCPApprovalArgsHash(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || trimmed == "00000000" {
		return legacyMCPApprovalArgsHashZero
	}
	return trimmed
}

// policyDispatcherAdapter wraps the production action-gate pipeline so
// the core/mcp policy layer can dispatch against it through the narrow
// mcp.PolicyDispatcher interface. The adapter exists to break the
// import cycle that core/policy/actiongates would otherwise close if
// core/mcp imported it (actiongates already imports core/mcp for
// AgentIdentity).
type policyDispatcherAdapter struct {
	pipeline *actiongates.Pipeline
}

// Dispatch maps the mcp call to actiongates.Pipeline.Run and adapts
// the returned ActionGateDecision into the mcp-local PolicyDecision
// shape. A nil pipeline returns the zero decision (treated as allow)
// so a gateway boot without the action gate wired falls through to
// the legacy approval flow without crashing.
//
// Production action gates are blockers/approval gates and do not emit
// enforceable generic constraints. The adapter still copies Constraints for
// fake/test dispatchers and future typed action-gate designs, but callers must
// not treat those generic map entries as the production enforcement path.
// Typed runtime constraints continue to flow through policy bundles and
// SafetyKernel PolicyConstraints.
func (a policyDispatcherAdapter) Dispatch(ctx context.Context, in *config.PolicyInput) (mcp.PolicyDecision, bool) {
	if a.pipeline == nil {
		return mcp.PolicyDecision{}, false
	}
	dec, fired := a.pipeline.Run(ctx, in)
	return mcp.PolicyDecision{
		Decision:    dec.Decision,
		GateID:      dec.GateID,
		Code:        dec.Code,
		Reason:      dec.Reason,
		SubReason:   dec.SubReason,
		Extra:       dec.Extra,
		Constraints: dec.Constraints,
	}, fired
}

// ConsumeActionGateDecision routes a REQUIRE_HUMAN gate decision into
// the existing MCPApprovalStore lifecycle. The gate has already
// computed the canonical action hash; we reuse it (instead of
// recomputing) so the gate's decision and the approval record stay
// bound to the same key. Returns the approval reference the client
// surfaces as an approval-pending marker.
//
// Precedence (matches gatewayApprovalGate.Check exactly):
//  1. MCPInvariantLookup DENY — SECURITY FLOOR, always wins.
//  2. PreapprovalLookup HIT — skip approval store entirely.
//  3. Fall through to MCPApprovalStore claim/enqueue.
//
// On invariant DENY this returns an error so the caller can map it
// to a -32097 / -32099 JSON-RPC code. On preapproval HIT, returns
// ("", nil) so the caller treats the call as immediately allowed.
func (g *gatewayApprovalGate) ConsumeActionGateDecision(ctx context.Context, _ mcp.PolicyDecision, ctxData mcp.ToolCallApprovalContext) (string, error) {
	if g == nil || g.store == nil {
		return "", fmt.Errorf("mcp gate: approval store unavailable")
	}
	// Invariant DENY first — fail closed regardless of the actiongate
	// decision so a pack-contributed actiongate ALLOW cannot override
	// a SecOps invariant block.
	if g.invariants != nil {
		rules := g.invariants.InvariantsForMCPTool(ctx)
		// We synthesize a minimal mcp.Tool/CallMetadata view from the
		// context payload so matchMCPInvariantDeny can run.
		tool := mcp.Tool{Name: ctxData.Tool}
		meta := MCPCallMetadata{Tenant: ctxData.Tenant, AgentID: ctxData.AgentID}
		if rule, denied := matchMCPInvariantDeny(rules, tool, meta); denied {
			return "", fmt.Errorf("%w: tool %q denied by invariant %q",
				ErrMCPInvariantDeny, ctxData.Tool, rule.ID)
		}
	}
	// Preapproval HIT short-circuits the approval store. The bridge's
	// pre event has already been emitted with Decision=require_approval;
	// returning ("", nil) lets the caller fall through to upstream as
	// if the call had been allowed. Production wiring should also fire
	// an audit-side preapproval emission via the invocation handle.
	if g.preapproval != nil && g.preapproval.IsPreapproved(ctx, ctxData.Tenant, ctxData.AgentID, ctxData.Tool) {
		return "", nil
	}
	// EDGE-103 reopen #1: Edge approval store is the source of truth
	// per DoD #4. Try Edge mint FIRST when wired + transport metadata
	// present. Three-way outcome from mintEdgeApprovalForActionGate:
	//   - (ref, nil, true):   Edge mint succeeded — return ref.
	//   - ("",  nil, false):  Edge unwired or no CallMetadata — fall
	//                         back to legacy MCPApprovalStore mint by
	//                         design (supports HTTP MCP transit without
	//                         an EdgeSession, dev/test deploys).
	//   - ("",  err, true):   Edge wired + metadata present but
	//                         EnqueueApproval errored — fail-closed per
	//                         DoD #5 (no silent legacy fallback that
	//                         would produce a non-resumable approval_id).
	ref, mintErr, didTry := g.mintEdgeApprovalForActionGate(ctx, ctxData)
	if mintErr != nil {
		return "", fmt.Errorf("%w: mcp gate Edge enqueue failed: %w", mcp.ErrApprovalStoreUnavailable, mintErr)
	}
	if didTry {
		return ref, nil
	}
	// Legacy MCPApprovalStore path (non-authoritative SIEM correlation).
	// Only reached when Edge mint was skipped because of missing wiring
	// or absent transport metadata. The legacy retry-with-same-args
	// protocol still works via ClaimPreApproved for pre-EDGE-103 SIEM
	// consumers that don't speak `_approval_ref`.
	//
	// ctxData.ActionHash is upstream-controlled — the canonical legacy
	// emit sites supplied a SHA-256 hex digest, but a few older code
	// paths wrote the short "00000000" placeholder before the canonical
	// binding existed. Normalize via legacyMCPApprovalArgsHash so the
	// SIEM correlation column always carries a stable 64-char shape.
	argsHash := legacyMCPApprovalArgsHash(ctxData.ActionHash)
	if rec, err := g.store.ClaimPreApproved(ctx, ctxData.Tenant, ctxData.AgentID, ctxData.Tool, argsHash); err != nil {
		return "", fmt.Errorf("mcp gate: pre-approval claim: %w", err)
	} else if rec != nil {
		return rec.ID, nil
	}
	req := &MCPApprovalRequest{
		Tenant:   ctxData.Tenant,
		AgentID:  ctxData.AgentID,
		ToolName: ctxData.Tool,
		ArgsHash: argsHash,
	}
	if gatewayMeta, ok := MCPCallMetadataFromContext(ctx); ok {
		req.Principal = strings.TrimSpace(gatewayMeta.Principal)
		req.Requester = mcpRequesterIdentity(gatewayMeta)
	}
	rec, err := g.store.EnqueueMCPApproval(ctx, req)
	if err != nil {
		return "", fmt.Errorf("mcp gate: enqueue approval: %w", err)
	}
	return rec.ID, nil
}

// mintEdgeApprovalForActionGate creates an EdgeApproval consumable via
// the EDGE-103 `_approval_ref` resume path. Returns a three-way outcome:
//   - (ref, nil, true):  Edge mint succeeded.
//   - ("",  nil, false): Edge unwired or required mcp.CallMetadata
//     fields missing — caller falls back to legacy mint by design.
//   - ("",  err, true):  Edge wired + metadata present but
//     EnqueueApproval failed — caller surfaces -32096
//     approval_store_unavailable (DoD #5; no silent legacy fallback).
//
// InputHash + ActionHash are derived from mcp.BuildMCPApprovalBinding so
// the mint side stores byte-identical hashes to what the consume path
// (ProcessApprovalClaim) computes. Without this match, edge.RedisStore
// classifyApprovalClaimMismatch surfaces ApprovalConflictKindArgsMismatch
// on every legitimate retry — the EDGE-103 reopen #1 core bug QA flagged.
func (g *gatewayApprovalGate) mintEdgeApprovalForActionGate(ctx context.Context, ctxData mcp.ToolCallApprovalContext) (string, error, bool) {
	if g == nil || g.edgeStore == nil {
		return "", nil, false
	}
	meta, ok := mcp.CallMetadataFromContext(ctx)
	if !ok || meta.SessionID == "" || meta.ExecutionID == "" {
		return "", nil, false
	}
	policySnapshot := ""
	if g.policySnapshot != nil {
		policySnapshot = g.policySnapshot(ctx)
	}
	actionHash, inputHash := mcp.BuildMCPApprovalBinding(
		meta.Tenant,
		g.serverName,
		mcp.ToolCallParams{Name: ctxData.Tool, Arguments: ctxData.Args},
		policySnapshot,
	)
	requester := strings.TrimSpace(meta.Principal)
	if gatewayMeta, ok := MCPCallMetadataFromContext(ctx); ok {
		requester = mcpRequesterIdentity(gatewayMeta)
	}
	approval, err := g.edgeStore.EnqueueApproval(ctx, edge.EdgeApprovalRequest{
		TenantID:       meta.Tenant,
		SessionID:      meta.SessionID,
		ExecutionID:    meta.ExecutionID,
		EventID:        meta.AgentID,
		PrincipalID:    meta.Principal,
		Requester:      requester,
		Reason:         "policy gate REQUIRE_HUMAN",
		ActionHash:     actionHash,
		InputHash:      inputHash,
		PolicySnapshot: policySnapshot,
	})
	if err != nil {
		return "", err, true
	}
	if approval == nil {
		return "", fmt.Errorf("edge store returned nil approval without error"), true
	}
	return approval.ApprovalRef, nil, true
}

// BuildMCPPolicyDeps assembles the production ToolCallDeps the MCP
// server consumes via MCPServer.WithPolicyGate. Fail-closed contract:
// any nil required dep (pipeline, emitter, store) returns the zero
// ToolCallDeps so the MCPServer.WithPolicyGate partial-wiring guard
// resets the gate to off and HasPolicyGate() reports false. Without
// this guard, noop-fallback adapters would satisfy the interface
// check inside server.go while silently dropping every event, leaving
// HasPolicyGate() falsely true on inert wiring.
//
// gate (ApprovalHandoff) may legitimately be nil — handlers_mcp.go
// disables the MCP approval store when Redis is unavailable and the
// EvaluateToolCall path skips the REQUIRE_HUMAN handoff branch in
// that case. Pipeline/emitter/store are mandatory because their nil
// branches would produce a silently-degraded gate.
//
// redisClient is the shared go-redis client owned by the gateway's
// JobStore — passed in here rather than re-dialled so cross-process
// dedupe reuses the existing connection pool (epic rail #3: no
// parallel subsystems). A nil client forces the in-process backend
// regardless of env-var override.
func BuildMCPPolicyDeps(pipeline *actiongates.Pipeline, gate *gatewayApprovalGate, emitter mcp.EventEmitter, store mcp.ArtifactStore, redisClient redis.Cmdable) mcp.ToolCallDeps {
	if pipeline == nil || emitter == nil || store == nil {
		return mcp.ToolCallDeps{}
	}
	return mcp.ToolCallDeps{
		Pipeline:        policyDispatcherAdapter{pipeline: pipeline},
		EventEmitter:    emitter,
		ArtifactStore:   store,
		ApprovalHandoff: gate,
		Redactor:        mcp.DefaultRedactor(),
		// Backend selection per mcp.SelectDedupeStore matrix: unset
		// env var defaults to Redis when the shared client is
		// available (cross-process collapse across multi-instance HA),
		// memory otherwise. Operators can pin a specific backend via
		// CORDUM_MCP_DEDUPE_BACKEND={redis,memory}; unknown values
		// degrade to memory without panicking the boot.
		DedupeState: mcp.SelectDedupeStore(os.Getenv(mcp.DedupeBackendEnvVar), redisClient),
		// Content session-taint (P3c): scan tool-call RESULTS with the shipped
		// prompt-injection scanner and persist a taint keyed by (tenant, session)
		// so a later destructive call in the same session is DENIED content-
		// awarely. ResultScanner is injected here (not imported by core/mcp) to
		// avoid a core/mcp->safetykernel import cycle; TaintStore reuses the SAME
		// shared Redis client as dedupe (epic rail: no parallel subsystems), with
		// its TTL from CORDUM_MCP_TAINT_TTL and backend from CORDUM_MCP_TAINT_BACKEND.
		ResultScanner: promptInjectionResultScanner,
		TaintStore:    mcp.SelectTaintStore(os.Getenv(mcp.TaintBackendEnvVar), redisClient),
	}
}

// promptInjectionResultScanner adapts the SHIPPED safetykernel prompt-injection
// scanner to the mcp.ResultScanner dep shape. This is the seam that lets core/mcp
// reuse the exact shipped detector WITHOUT importing safetykernel (which would be
// an import cycle): the gateway — the layer that may import both — bridges the
// two finding types here.
func promptInjectionResultScanner(content []byte) []mcp.ResultFinding {
	raw := safetykernel.ScanForPromptInjection(content)
	if len(raw) == 0 {
		return nil
	}
	out := make([]mcp.ResultFinding, 0, len(raw))
	for _, f := range raw {
		out = append(out, mcp.ResultFinding{
			Pattern:    f.Pattern,
			Snippet:    f.Snippet,
			Severity:   f.Severity,
			Confidence: f.Confidence,
		})
	}
	return out
}
