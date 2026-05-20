package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/mcp"
)

// MCPCallMetadata carries the request-scoped context an ApprovalGate
// needs to evaluate a tools/call. The gateway's HTTP middleware stashes
// it in the request context via context.WithValue(ctx, mcpCallKey{}, ...)
// before dispatching the tools/call into core/mcp.
//
// Principal is the authenticated subject (API-key principal, SSO subject,
// etc.) — it is the identity used by the self-approval guard to decide
// whether the approver and the requester are the same person.
// AgentID is the display-facing MCP agent identifier resolved from the
// X-Agent-Id header; it may differ from Principal (e.g. a human operator
// invoking tools-call on behalf of an agent). Auditing records both so
// the dashboard can show "agent-alpha called by alice@corp".
type MCPCallMetadata struct {
	Tenant            string
	AgentID           string
	Principal         string
	RequesterIdentity string
}

type mcpCallKey struct{}

// WithMCPCallMetadata returns a context carrying the given call metadata.
// Exported so tests and transport adapters can inject the same shape the
// gate reads below.
func WithMCPCallMetadata(ctx context.Context, meta MCPCallMetadata) context.Context {
	return context.WithValue(ctx, mcpCallKey{}, meta)
}

// MCPCallMetadataFromContext retrieves the metadata the middleware wrote.
// Missing metadata is an error — the gate refuses to evaluate a tool call
// without knowing who is making it.
func MCPCallMetadataFromContext(ctx context.Context) (MCPCallMetadata, bool) {
	m, ok := ctx.Value(mcpCallKey{}).(MCPCallMetadata)
	return m, ok
}

func mcpRequesterIdentity(meta MCPCallMetadata) string {
	if requester := strings.TrimSpace(meta.RequesterIdentity); requester != "" {
		return requester
	}
	if principal := strings.TrimSpace(meta.Principal); principal != "" {
		return principal
	}
	return strings.TrimSpace(meta.AgentID)
}

// PreapprovalLookup answers whether an agent identity is explicitly
// allowed to call a specific mutating tool without a human approval.
// The admin-only write path for the underlying AgentIdentity field is
// in handlers_agents.go (PreapprovedMutatingTools).
//
// Implementations MUST be safe for concurrent use and MUST fail-closed
// on errors — returning (false, nil) when in doubt — so a store
// outage never silently bypasses the approval step. Audit callers
// keep calling the LLM through the normal human-approval path.
type PreapprovalLookup interface {
	IsPreapproved(ctx context.Context, tenant, agentID, toolName string) bool
}

// gatewayApprovalGate implements mcp.ApprovalGate by bridging into the
// MCPApprovalStore (legacy backward-compat path) and, when wired, the
// Edge approval store (EDGE-103 consumable path).
//
// edgeStore + policySnapshot drive the EDGE-103 mint: when the gate
// can source SessionID/ExecutionID from mcp.CallMetadata (the EDGE-102
// gateway-side metadata), an EdgeApproval is created and its ref is
// returned via ApprovalRequired.ApprovalRef / ConsumeActionGateDecision.
// When the gate cannot mint Edge (transport without Edge session), it
// falls back to legacy-only: ApprovalRef equals ApprovalID and the
// resume protocol is "retry with identical args" via ClaimPreApproved.
type gatewayApprovalGate struct {
	store          *MCPApprovalStore
	edgeStore      edge.Store
	policySnapshot func(context.Context) string
	serverName     string
	preapproval    PreapprovalLookup
	invariants     MCPInvariantLookup
}

// NewGatewayApprovalGate returns a gate backed by the given store.
// Passing a nil store yields a permissive gate (no gating) — useful for
// dev deploys without Redis.
func NewGatewayApprovalGate(store *MCPApprovalStore) mcp.ApprovalGate {
	return &gatewayApprovalGate{store: store}
}

// WithEdgeApprovalMint wires the Edge approval store + policy snapshot
// provider + canonical MCP server name so the gate's mint paths
// (Check + ConsumeActionGateDecision) can create EdgeApproval records
// consumable by the EDGE-103 resume path. Optional — when not wired,
// the gate falls back to legacy MCPApprovalStore-only mint and the
// returned ApprovalRef equals ApprovalID.
func (g *gatewayApprovalGate) WithEdgeApprovalMint(store edge.Store, snapshot func(context.Context) string, serverName string) *gatewayApprovalGate {
	if g != nil {
		g.edgeStore = store
		g.policySnapshot = snapshot
		g.serverName = serverName
	}
	return g
}

// WithPreapprovalLookup attaches a preapproval resolver. Exposed as a
// chaining helper so existing call sites don't need to change; wire
// from registerMCPRoutes via:
//
//	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)
//	gate.preapproval = ... // or use this method if you need the
//	// interface form.
//
// Kept simple — concrete struct method over an opaque option so the
// callsite intent is obvious.
func (g *gatewayApprovalGate) WithPreapproval(lookup PreapprovalLookup) *gatewayApprovalGate {
	if g != nil {
		g.preapproval = lookup
	}
	return g
}

// WithInvariantLookup attaches an MCP invariant lookup. The gate consults
// the lookup BEFORE the existing approval flow — if any invariant DENY
// rule's MCP matchers cover the tool/server/resource, the gate returns
// ErrMCPInvariantDeny instead of enqueuing an approval. This codifies the
// "Invariants are SECURITY FLOOR — never let a pack-contributed rule
// override an Invariants DENY" task rail. A nil lookup is permissive —
// the gate skips invariant consultation and proceeds to approval flow.
func (g *gatewayApprovalGate) WithInvariantLookup(lookup MCPInvariantLookup) *gatewayApprovalGate {
	if g != nil {
		g.invariants = lookup
	}
	return g
}

// Check is the central pre-approval + enqueue point.
//
// Flow:
//  1. Pull tenant/agent/principal from ctx. Missing metadata → wrap
//     ErrMissingMCPCallMeta with mcp.ErrApprovalGateMisconfigured so the
//     JSON-RPC layer can surface a distinctive -32097 instead of a
//     generic -32603 internal error.
//  2. Canonicalise params (UseNumber so big ints round-trip precisely)
//     and compute args_hash. Identical args across calls share an
//     approval; differing args re-gate.
//  3. ClaimPreApproved against (tenant, agent, tool, args_hash) — an
//     atomic find-and-consume. On hit, return (nil, nil) → handler runs.
//     Consume-once is guaranteed by CAS inside the store.
//  4. On miss, EnqueueMCPApproval (persisting the canonical args blob)
//     and return *ApprovalRequired → JSON-RPC -32099.
func (g *gatewayApprovalGate) Check(ctx context.Context, tool mcp.Tool, params json.RawMessage) (*mcp.ApprovalRequired, error) {
	if g == nil || g.store == nil {
		return nil, nil
	}
	// Defensive short-circuit: if the registry hands us a tool with
	// RequiresApproval=false, no enqueue is needed — the registry
	// itself normally skips the gate, but keeping the guard here
	// means direct gate.Check callers (tests, alternate transports)
	// can't accidentally enqueue against a non-gated tool.
	if !tool.RequiresApproval {
		return nil, nil
	}
	meta, ok := MCPCallMetadataFromContext(ctx)
	if !ok {
		// Wrap with the exported mcp package sentinel so the JSON-RPC
		// layer can map this condition to -32097 "gateway misconfigured"
		// rather than a generic -32603. Operators can page on -32097 to
		// distinguish middleware-wiring bugs from real handler failures.
		return nil, fmt.Errorf("%w: %w", mcp.ErrApprovalGateMisconfigured, ErrMissingMCPCallMeta)
	}
	canonical, argsHash, err := canonicalizeArgs(params)
	if err != nil {
		return nil, fmt.Errorf("mcp gate: args hash: %w", err)
	}

	// Invariants consultation (EDGE-052). Invariant DENY rules are the
	// SECURITY FLOOR — they fire before any approval flow so a pack
	// rule that allows a tool cannot override a SecOps invariant DENY.
	// Lookup is optional; nil lookup proceeds to existing flow.
	if g.invariants != nil {
		rules := g.invariants.InvariantsForMCPTool(ctx)
		if rule, denied := matchMCPInvariantDeny(rules, tool, meta); denied {
			reason := strings.TrimSpace(rule.Reason)
			if reason == "" {
				reason = "denied by SecOps invariant"
			}
			return nil, fmt.Errorf("%w: tool %q denied by invariant %q: %s",
				ErrMCPInvariantDeny, tool.Name, rule.ID, reason)
		}
	}

	// Scope-preapproval bypass (task-2d989055). If the agent
	// identity's PreapprovedMutatingTools list covers this tool, the
	// call executes WITHOUT enqueuing a human approval record. The
	// SIEM audit still fires — the invocation event is stamped with
	// approval_status=preapproved so forensics can tell bypasses
	// apart from human approvals. Reserved for CI-CD bots; human
	// operator identities should never be on this list.
	if g.preapproval != nil && tool.RequiresApproval {
		if g.preapproval.IsPreapproved(ctx, meta.Tenant, meta.AgentID, tool.Name) {
			if h := mcp.InvocationHandleFromContext(ctx); h != nil {
				h.MarkApprovalPreapproved(tool.Name)
			}
			return nil, nil
		}
	}

	if rec, err := g.store.ClaimPreApproved(ctx, meta.Tenant, meta.AgentID, tool.Name, argsHash); err != nil {
		return nil, fmt.Errorf("mcp gate: pre-approval claim: %w", err)
	} else if rec != nil {
		// Stamp the consumed approval onto the in-flight invocation
		// handle so the eventual mcp.tool_invocation event carries
		// approval_id + approval_status=consumed. This is the
		// correlation key SIEM consumers use to join invocation
		// events to their originating mcp.tool_approval(outcome=consume)
		// emission. The handle pointer is installed by the invocation
		// auditor in StartInbound — see mcp.InvocationHandleFromContext.
		if h := mcp.InvocationHandleFromContext(ctx); h != nil {
			h.MarkApprovalConsumed(rec.ID)
		}
		return nil, nil
	}

	// Requester is the authenticated composite identity when HTTP middleware
	// can supply one (API-key hash + principal) so the self-approval guard
	// can block same-key/principal-spoof attempts. Non-HTTP transports fall
	// back to principal, then AgentID, for backward compatibility.
	requester := mcpRequesterIdentity(meta)
	req := &MCPApprovalRequest{
		Tenant:    meta.Tenant,
		AgentID:   meta.AgentID,
		Principal: meta.Principal,
		ToolName:  tool.Name,
		ArgsHash:  argsHash,
		ArgsJSON:  canonical,
		Requester: requester,
		Reason:    approvalReasonFor(tool),
	}
	rec, err := g.store.EnqueueMCPApproval(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("mcp gate: enqueue: %w", err)
	}
	// Mark the handle so the invocation event records
	// approval_status=required + approval_id=<rec.ID> — lets SIEM
	// consumers find the pending approval by correlating on the same
	// id that lands on the mcp.tool_approval(outcome=enqueue) event.
	if h := mcp.InvocationHandleFromContext(ctx); h != nil {
		h.MarkApprovalRequired(rec.ID)
	}
	// EDGE-103: try to dual-write an EdgeApproval when the gate is
	// wired for it AND mcp.CallMetadata has SessionID/ExecutionID.
	// On success the returned ApprovalRef is the Edge ref consumable
	// by the new `_approval_ref` resume path. On legitimate miss (no
	// Edge wiring or no Edge metadata), ApprovalRef falls back to the
	// legacy MCPApprovalStore record id; the legacy retry-with-same-args
	// protocol still works via ClaimPreApproved above. On Edge-store
	// outage (wired + metadata present but EnqueueApproval errored),
	// fail-closed per DoD #5 — no silent fallback to a non-resumable
	// legacy approval_id.
	approvalRef, policySnapshot, expiresAt, mintErr := g.dualWriteEdgeApproval(ctx, meta, tool, params, rec)
	if mintErr != nil {
		return nil, fmt.Errorf("%w: edge approval mint failed: %w", mcp.ErrApprovalStoreUnavailable, mintErr)
	}
	return &mcp.ApprovalRequired{
		ApprovalID:     rec.ID,
		ApprovalRef:    approvalRef,
		ArgsHash:       argsHash,
		ExpiresAt:      expiresAt,
		PolicySnapshot: policySnapshot,
		RetryHint:      "retry_with_approval_ref",
		Reason:         rec.Reason,
		Tool:           tool.Name,
	}, nil
}

// dualWriteEdgeApproval mints an EdgeApproval alongside the legacy
// MCPApprovalStore record when the gate has been wired for Edge mint
// AND mcp.CallMetadata carries SessionID + ExecutionID. The Edge
// record's ref is the EDGE-103 `_approval_ref` resume handle; without
// it, the response falls back to the legacy ID (resumable via
// retry-with-same-args, not via `_approval_ref`).
//
// Returns a four-tuple — the last field is non-nil when Edge mint was
// ATTEMPTED (transport metadata present) and FAILED. Caller surfaces
// approval_store_unavailable per DoD #5 in that case. nil errors include
// both the success path and the legitimate-fallback path where Edge mint
// was skipped (no edgeStore wired, or no CallMetadata in ctx).
func (g *gatewayApprovalGate) dualWriteEdgeApproval(ctx context.Context, meta MCPCallMetadata, tool mcp.Tool, params json.RawMessage, rec *MCPApprovalRecord) (approvalRef, policySnapshot string, expiresAt time.Time, mintErr error) {
	approvalRef = rec.ID
	if rec.ExpiresAt > 0 {
		expiresAt = time.Unix(rec.ExpiresAt, 0).UTC()
	}
	if g.edgeStore == nil {
		return approvalRef, "", expiresAt, nil
	}
	edgeMeta, ok := mcp.CallMetadataFromContext(ctx)
	if !ok || edgeMeta.SessionID == "" || edgeMeta.ExecutionID == "" {
		return approvalRef, "", expiresAt, nil
	}
	if g.policySnapshot != nil {
		policySnapshot = g.policySnapshot(ctx)
	}
	actionHash, inputHash := mcp.BuildMCPApprovalBinding(
		edgeMeta.Tenant,
		g.serverName,
		mcp.ToolCallParams{Name: tool.Name, Arguments: params},
		policySnapshot,
	)
	edgeApproval, err := g.edgeStore.EnqueueApproval(ctx, edge.EdgeApprovalRequest{
		TenantID:       edgeMeta.Tenant,
		SessionID:      edgeMeta.SessionID,
		ExecutionID:    edgeMeta.ExecutionID,
		EventID:        edgeMeta.AgentID,
		PrincipalID:    edgeMeta.Principal,
		Requester:      mcpRequesterIdentity(meta),
		Reason:         rec.Reason,
		RuleID:         tool.ApprovalScope,
		PolicySnapshot: policySnapshot,
		ActionHash:     actionHash,
		InputHash:      inputHash,
	})
	if err != nil {
		return "", policySnapshot, expiresAt, err
	}
	if edgeApproval == nil {
		return "", policySnapshot, expiresAt, fmt.Errorf("edge store returned nil approval without error")
	}
	approvalRef = edgeApproval.ApprovalRef
	if edgeApproval.ExpiresAt != nil {
		expiresAt = edgeApproval.ExpiresAt.UTC()
	}
	return approvalRef, policySnapshot, expiresAt, nil
}

// approvalReasonFor builds the reason string stored on the approval
// record. Prefers the tool's ApprovalScope (runtime config) over the
// plain "requires approval" default.
func approvalReasonFor(tool mcp.Tool) string {
	if tool.ApprovalScope != "" {
		return fmt.Sprintf("tool %q matches approval scope %q", tool.Name, tool.ApprovalScope)
	}
	return fmt.Sprintf("tool %q requires human approval", tool.Name)
}

// canonicalizeArgs returns the normalised JSON representation of the
// tool-call args together with its SHA-256 hex hash.
//
// Delegates to mcp.CanonicaliseArgs (task-2d989055) which extends the
// original behaviour with:
//   - whitespace trim on every string value
//   - dropping null / empty-string / empty-array / empty-object keys
//   - preserving json.Number so big-ints don't collide via float64
//
// The net effect is that an LLM retrying a mutating tool call after
// a human approval lands on the same hash even if it reformatted the
// JSON or dropped an empty optional field. The old local
// implementation is gone; this function is now a thin wrapper so
// existing call sites keep working.
func canonicalizeArgs(raw json.RawMessage) (json.RawMessage, string, error) {
	return mcp.CanonicaliseArgs(raw)
}

// canonicalArgsHash returns just the hash portion of canonicalizeArgs.
// Kept for backward compat with existing callers and unit tests that
// only need the hash.
func canonicalArgsHash(raw json.RawMessage) (string, error) {
	_, hash, err := canonicalizeArgs(raw)
	return hash, err
}

// ErrMissingMCPCallMeta signals the gate was invoked without the
// tenant/agent metadata it needs. In production paths this is a
// programming error (middleware didn't set the ctx value); the gate
// wraps it with mcp.ErrApprovalGateMisconfigured so the JSON-RPC layer
// maps it to a distinctive -32097 code rather than a generic -32603.
var ErrMissingMCPCallMeta = errors.New("mcp gate: call metadata (tenant/agent_id) missing from context")

// compile-time assertion: *gatewayApprovalGate satisfies mcp.ApprovalGate.
var _ mcp.ApprovalGate = (*gatewayApprovalGate)(nil)
