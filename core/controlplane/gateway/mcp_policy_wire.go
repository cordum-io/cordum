package gateway

import (
	"context"
	"errors"
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

// ConsumeActionGateDecision routes an action-gate REQUIRE_HUMAN decision
// into the Edge approval lifecycle. It returns the ref of an Edge approval
// bound to the call's policy pre-event; the client surfaces it as an
// approval-pending marker and resumes with it as `_approval_ref`.
//
// Precedence:
//  1. MCPInvariantLookup DENY — SECURITY FLOOR, always wins.
//  2. The Edge approval mint, bound to the policy pre-event.
//  3. Otherwise -32097 (mcp.ErrApprovalGateMisconfigured): without the
//     Edge mint and Edge session metadata no resumable approval exists.
//
// It returns a non-empty Edge ref or an error. Scope preapproval and the
// legacy MCP approval store's records are not consulted (task-06784e6d).
func (g *gatewayApprovalGate) ConsumeActionGateDecision(ctx context.Context, _ mcp.PolicyDecision, ctxData mcp.ToolCallApprovalContext) (string, error) {
	if g == nil || g.store == nil {
		return "", fmt.Errorf("mcp gate: approval store unavailable")
	}
	// Invariant DENY first — fail closed regardless of the actiongate
	// decision so a pack-contributed actiongate ALLOW cannot override
	// a SecOps invariant block.
	if err := g.actionGateInvariantDeny(ctx, ctxData); err != nil {
		return "", err
	}
	// Scope preapproval waives only the registry gate's per-tool approval (gatewayApprovalGate.Check).
	// It never waives an action-gate REQUIRE_HUMAN such as MCPGate's taint-unavailable hold (task-06784e6d R1).
	//
	// EDGE-103 reopen #1: the Edge approval store is the source of truth
	// per DoD #4. Three-way outcome from mintEdgeApprovalForActionGate:
	//   - (ref, nil, true):   Edge mint succeeded — return ref.
	//   - ("",  err, true):   the mint ran and failed: a policy change
	//                         since the pre-event (retryable), or a
	//                         missing pre-event id or EnqueueApproval
	//                         error (-32096 approval_store_unavailable).
	//   - ("",  nil, false):  the mint was not attempted (Edge store
	//                         unwired or no Edge session metadata) —
	//                         fail closed with -32097. There is no
	//                         legacy fallback (task-06784e6d R2).
	ref, mintErr, didTry := g.mintEdgeApprovalForActionGate(ctx, ctxData)
	if mintErr != nil {
		// A policy change is retryable, not a store outage.
		if errors.Is(mintErr, mcp.ErrPolicySnapshotChanged) {
			return "", mintErr
		}
		return "", fmt.Errorf("%w: mcp gate Edge enqueue failed: %w", mcp.ErrApprovalStoreUnavailable, mintErr)
	}
	if !didTry {
		return "", fmt.Errorf("%w: action-gate REQUIRE_HUMAN needs the Edge approval mint and Edge session metadata; "+
			"the legacy MCP approval store is not an approval authority under the policy gate", mcp.ErrApprovalGateMisconfigured)
	}
	return ref, nil
}

var _ mcp.ConsumedApprovalAuthorizer = (*gatewayApprovalGate)(nil)

// AuthorizeConsumedApproval authorizes a REQUIRE_HUMAN call resumed with
// its consumed Edge approval (task-cd9df90a C2). Only the invariant DENY
// floor applies: the approval already stands in for the preapproval and
// the mint, so neither runs.
func (g *gatewayApprovalGate) AuthorizeConsumedApproval(ctx context.Context, ctxData mcp.ToolCallApprovalContext) error {
	if g == nil || g.store == nil {
		return fmt.Errorf("mcp gate: approval store unavailable")
	}
	return g.actionGateInvariantDeny(ctx, ctxData)
}

// actionGateInvariantDeny is the SecOps invariant DENY floor of the
// action-gate handoff. It synthesizes a minimal mcp.Tool/CallMetadata view
// from the context payload so matchMCPInvariantDeny can run.
func (g *gatewayApprovalGate) actionGateInvariantDeny(ctx context.Context, ctxData mcp.ToolCallApprovalContext) error {
	if g.invariants == nil {
		return nil
	}
	rules := g.invariants.InvariantsForMCPTool(ctx)
	tool := mcp.Tool{Name: ctxData.Tool}
	meta := MCPCallMetadata{Tenant: ctxData.Tenant, AgentID: ctxData.AgentID}
	if rule, denied := matchMCPInvariantDeny(rules, tool, meta); denied {
		return fmt.Errorf("%w: tool %q denied by invariant %q",
			ErrMCPInvariantDeny, ctxData.Tool, rule.ID)
	}
	return nil
}

// mintEdgeApprovalForActionGate creates an EdgeApproval consumable via
// the EDGE-103 `_approval_ref` resume path. Returns a three-way outcome:
//   - (ref, nil, true):  Edge mint succeeded.
//   - ("",  nil, false): the mint was not attempted: Edge unwired or
//     required mcp.CallMetadata fields missing. The caller fails closed
//     with -32097 (mcp.ErrApprovalGateMisconfigured); this is never a
//     legacy fallback (task-06784e6d R2).
//   - ("",  err, true):  Edge wired + metadata present but the policy
//     pre-event id is missing or EnqueueApproval failed — caller
//     surfaces -32096 approval_store_unavailable (DoD #5; no silent
//     legacy fallback). A policy change since the pre-event instead
//     returns ErrPolicySnapshotChanged (retryable).
//
// The approval binds to the policy pre-event (mcp.tool.pre) named by
// ctxData.EventID: edge.EnqueueApproval requires an existing event whose
// InputHash and PolicySnapshot match this mint (task-63400922 E1).
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
	eventID := strings.TrimSpace(ctxData.EventID)
	if eventID == "" {
		return "", errors.New("policy pre-event id missing from approval context"), true
	}
	policySnapshot := g.policySnapshotForEvents(ctx)
	if err := edgeMintSnapshotDrift(ctxData.PolicySnapshot, policySnapshot); err != nil {
		return "", err, true
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
		EventID:        eventID,
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
		// Stamps the policy pre-event with the snapshot the Edge mint
		// binds to (task-63400922 E2). A method value, read per call,
		// because WithEdgeApprovalMint may attach the snapshot func after
		// these deps are built.
		// ponytail: one loadPolicyBundles per policy-gated call (a config
		// GET plus a workflow list); read only the policy doc's updated-at
		// once that cost shows (task-2688feeb).
		PolicySnapshot: gate.policySnapshotForEvents,
		Redactor:       mcp.DefaultRedactor(),
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
