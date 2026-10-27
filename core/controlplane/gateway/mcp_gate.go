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
	"github.com/cordum/cordum/core/model"
	"github.com/google/uuid"
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
	// approvalHold is set when the MCP server consumes `_approval_ref`
	// retries (WithApprovalHold, wired only with mcp.policy_gate_enabled).
	// Without it the -32099 reply asks for a same-args retry instead.
	approvalHold bool
}

// The -32099 retry_hint values: how the client resumes once a reviewer
// approves the call.
const (
	// mcpRetryWithApprovalRef: resend the call with the reply's approval_ref
	// in the `_approval_ref` argument; the approval hold claims it.
	mcpRetryWithApprovalRef = "retry_with_approval_ref"
	// mcpRetryWithSameArgs: resend the call unchanged; the gate claims the
	// caller's approved approval for it (claimApprovedTwin, ClaimPreApproved).
	mcpRetryWithSameArgs = "retry_with_same_args"
)

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
	// ToolRegistry.Call already ran CheckInvariants; repeating it keeps
	// the floor for direct Check callers. Nil lookup proceeds.
	if err := g.CheckInvariants(ctx, tool); err != nil {
		return nil, err
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
		return nil, fmt.Errorf("%w: mcp gate: pre-approval claim: %w", mcp.ErrInternal, err)
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

	// EDGE-103: when the gate is wired for Edge mint and mcp.CallMetadata
	// carries SessionID/ExecutionID, the approval is an EdgeApproval whose
	// ref is the `_approval_ref` resume handle, dual-written with the
	// legacy MCPApprovalStore record. The Edge approval is the pair's single
	// consume token: a retry with the same args claims it too
	// (claimApprovedTwin), and either approvals page resolves both halves.
	// While the same caller's approval for this call is still pending, a
	// re-ask gets that one back instead of a second pair. Without Edge
	// wiring or Edge metadata, ApprovalRef falls back to the legacy record
	// id and the legacy retry-with-same-args protocol still works via
	// ClaimPreApproved above. An Edge-store outage (wired + metadata present
	// but the mint errored) fails closed per DoD #5: no silent fallback to a
	// non-resumable legacy approval_id.
	edgeMeta, edgeMint := g.edgeMintMetadata(ctx)
	var binding mcpApprovalBinding
	if edgeMint {
		if binding, err = g.bindMCPApproval(ctx, edgeMeta, tool.Name, params); err != nil {
			return nil, fmt.Errorf("%w: edge approval mint failed: %w", mcp.ErrApprovalStoreUnavailable, err)
		}
		if approvalID, err := g.claimApprovedTwin(ctx, meta, edgeMeta, binding, tool.Name, argsHash); err != nil {
			return nil, fmt.Errorf("%w: mcp gate: pre-approval claim: %w", mcp.ErrInternal, err)
		} else if approvalID != "" {
			if h := mcp.InvocationHandleFromContext(ctx); h != nil {
				h.MarkApprovalConsumed(approvalID)
			}
			return nil, nil
		}
		// The pair is reused only while its legacy record is pending too, so
		// the reply never names a legacy record that is gone or resolved.
		if pending := g.pendingMCPApproval(ctx, edgeMeta, binding); pending != nil {
			if approvalID := pending.Metadata[mcpLegacyApprovalIDKey]; g.legacyApprovalPending(ctx, approvalID) {
				if h := mcp.InvocationHandleFromContext(ctx); h != nil {
					h.MarkApprovalRequired(approvalID)
				}
				return mcpApprovalRequired(tool.Name, argsHash, approvalID, g.edgeRetryHint(), pending), nil
			}
		}
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
	// The Edge approval is minted first, naming the legacy record id it is
	// dual-written with, so the record can name it back (EdgeApprovalRef).
	var approval *edge.EdgeApproval
	if edgeMint {
		if req.ID, err = mcpApprovalID(); err != nil {
			return nil, fmt.Errorf("%w: mcp gate: approval id: %w", mcp.ErrInternal, err)
		}
		if approval, err = g.mintMCPEdgeApproval(ctx, edgeMeta, binding, tool.Name, req.Reason, tool.ApprovalScope, req.ID); err != nil {
			return nil, fmt.Errorf("%w: edge approval mint failed: %w", mcp.ErrApprovalStoreUnavailable, err)
		}
		req.EdgeApprovalRef = approval.ApprovalRef
	}
	rec, err := g.store.EnqueueMCPApproval(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%w: mcp gate: enqueue: %w", mcp.ErrInternal, err)
	}
	// Mark the handle so the invocation event records
	// approval_status=required + approval_id=<rec.ID> — lets SIEM
	// consumers find the pending approval by correlating on the same
	// id that lands on the mcp.tool_approval(outcome=enqueue) event.
	if h := mcp.InvocationHandleFromContext(ctx); h != nil {
		h.MarkApprovalRequired(rec.ID)
	}
	if approval != nil {
		return mcpApprovalRequired(tool.Name, argsHash, rec.ID, g.edgeRetryHint(), approval), nil
	}
	// A legacy record id is not an Edge approval_ref, so the approval hold
	// cannot claim it; only a same-args retry does (ClaimPreApproved).
	legacy := &mcp.ApprovalRequired{
		ApprovalID:  rec.ID,
		ApprovalRef: rec.ID,
		ArgsHash:    argsHash,
		RetryHint:   mcpRetryWithSameArgs,
		Reason:      rec.Reason,
		Tool:        tool.Name,
	}
	if rec.ExpiresAt > 0 {
		legacy.ExpiresAt = time.UnixMicro(rec.ExpiresAt).UTC()
	}
	return legacy, nil
}

// claimApprovedTwin runs a retry without `_approval_ref` on the caller's
// approved Edge approval for this call, found through the legacy record
// dual-written with it. Claiming the Edge approval is what consumes the pair,
// whichever page approved it, so the call runs once across both retry styles.
// Returns the claimed pair's legacy record id, or "" when there is none.
func (g *gatewayApprovalGate) claimApprovedTwin(ctx context.Context, meta MCPCallMetadata, edgeMeta mcp.CallMetadata, b mcpApprovalBinding, tool, argsHash string) (string, error) {
	twins, err := g.store.EdgeTwins(ctx, meta.Tenant, meta.AgentID, tool, argsHash)
	if err != nil {
		return "", err
	}
	for _, rec := range twins {
		// mcpApprovalClaims binds the claim to the approval's own session,
		// execution and parent event when it is this caller's; the store
		// refuses anything else, and checks status, expiry, args and policy.
		_, claimed, err := mcpApprovalClaims{store: g.edgeStore}.ClaimApproval(ctx, edge.ApprovalClaimRequest{
			TenantID: meta.Tenant, ApprovalRef: rec.EdgeApprovalRef,
			ActionHash: b.actionHash, InputHash: b.inputHash, PolicySnapshot: b.policySnapshot,
			ConsumedAt: time.Now().UTC(), CallerAgentID: edgeMeta.Principal,
		})
		if err == nil && claimed {
			// The legacy half records the consume for its own audit trail.
			_ = g.store.MarkConsumed(ctx, rec.ID)
			g.store.pruneIndexFor(ctx, rec)
			return rec.ID, nil
		}
	}
	return "", nil
}

// edgeRetryHint is the retry_hint for an Edge approval: an `_approval_ref`
// retry where the approval hold consumes one, otherwise a same-args retry.
func (g *gatewayApprovalGate) edgeRetryHint() string {
	if g.approvalHold {
		return mcpRetryWithApprovalRef
	}
	return mcpRetryWithSameArgs
}

// mcpApprovalRequired is the -32099 payload for an Edge approval.
func mcpApprovalRequired(tool, argsHash, approvalID, retryHint string, approval *edge.EdgeApproval) *mcp.ApprovalRequired {
	out := &mcp.ApprovalRequired{
		ApprovalID:     approvalID,
		ApprovalRef:    approval.ApprovalRef,
		ArgsHash:       argsHash,
		PolicySnapshot: approval.PolicySnapshot,
		RetryHint:      retryHint,
		Reason:         approval.Reason,
		Tool:           tool,
	}
	if approval.ExpiresAt != nil {
		out.ExpiresAt = approval.ExpiresAt.UTC()
	}
	return out
}

const (
	// mcpApprovalAgentLabel is the Edge approval label naming the agent
	// identity an MCP approval was minted for. A claim binds to the approval,
	// and a re-ask reuses it, only for the same caller acting as the same
	// agent (mcpApprovalIsCallers).
	mcpApprovalAgentLabel = "agent_id"
	// mcpLegacyApprovalIDKey is the Edge approval metadata key holding the
	// legacy MCPApprovalStore record id dual-written with it.
	mcpLegacyApprovalIDKey = "mcp_approval_id"
)

// edgeMintMetadata returns the call's Edge linkage when the gate mints Edge
// approvals for it: Edge wiring plus a session and execution.
func (g *gatewayApprovalGate) edgeMintMetadata(ctx context.Context) (mcp.CallMetadata, bool) {
	if g == nil || g.edgeStore == nil {
		return mcp.CallMetadata{}, false
	}
	meta, ok := mcp.CallMetadataFromContext(ctx)
	return meta, ok && meta.SessionID != "" && meta.ExecutionID != ""
}

// mcpApprovalBinding is what an MCP Edge approval binds a tool call to.
type mcpApprovalBinding struct{ policySnapshot, actionHash, inputHash string }

func (g *gatewayApprovalGate) bindMCPApproval(ctx context.Context, meta mcp.CallMetadata, tool string, args json.RawMessage) (mcpApprovalBinding, error) {
	b := mcpApprovalBinding{}
	if g.policySnapshot != nil {
		b.policySnapshot = g.policySnapshot(ctx)
	}
	if b.policySnapshot == "" {
		return b, errors.New("policy snapshot unavailable")
	}
	b.actionHash, b.inputHash = mcp.BuildMCPApprovalBinding(meta.Tenant, g.serverName, mcp.ToolCallParams{Name: tool, Arguments: args}, b.policySnapshot)
	return b, nil
}

// pendingMCPApproval returns the caller's pending Edge approval for this
// call (same requester identity, agent, tool and target, args and policy
// snapshot), so a re-ask without `_approval_ref` does not mint another.
// ponytail: scans the principal's 50 newest pending approvals and does not
// re-check that the approval's Edge session is still open; a miss mints a new
// approval, as before.
func (g *gatewayApprovalGate) pendingMCPApproval(ctx context.Context, meta mcp.CallMetadata, b mcpApprovalBinding) *edge.EdgeApproval {
	page, err := g.edgeStore.ListApprovals(ctx, edge.ListApprovalsQuery{
		TenantID: meta.Tenant, PrincipalID: meta.Principal, Status: edge.ApprovalStatusPending, Limit: 50,
	})
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	for i := range page.Items {
		a := &page.Items[i]
		if a.ActionHash == b.actionHash && a.InputHash == b.inputHash && a.PolicySnapshot == b.policySnapshot &&
			(a.ExpiresAt == nil || a.ExpiresAt.After(now)) && mcpApprovalIsCallers(ctx, a) {
			return a
		}
	}
	return nil
}

// legacyApprovalPending reports whether the legacy MCPApprovalStore record id
// is still pending.
func (g *gatewayApprovalGate) legacyApprovalPending(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	rec, err := g.store.Get(ctx, id)
	return err == nil && rec != nil && rec.Status == model.ApprovalStatusPending
}

// mintMCPEdgeApproval records the tool call's approval.requested event and
// enqueues an Edge approval bound to it, as the Edge hook path appends its
// decision event before EnqueueApproval: the store only accepts an approval
// whose parent event exists in the execution, with the same policy snapshot
// and input hash. legacyID, when set, is the dual-written legacy record.
func (g *gatewayApprovalGate) mintMCPEdgeApproval(ctx context.Context, meta mcp.CallMetadata, b mcpApprovalBinding, tool, reason, ruleID, legacyID string) (*edge.EdgeApproval, error) {
	parent, err := g.edgeStore.AppendEvent(ctx, edge.AgentActionEvent{
		EventID:        uuid.NewString(),
		SessionID:      meta.SessionID,
		ExecutionID:    meta.ExecutionID,
		TenantID:       meta.Tenant,
		PrincipalID:    meta.Principal,
		Timestamp:      time.Now().UTC(),
		Layer:          edge.LayerMCP,
		Kind:           edge.EventKindApprovalRequested,
		ToolName:       tool,
		ActionName:     tool,
		Decision:       edge.DecisionRequireApproval,
		DecisionReason: reason,
		RuleID:         ruleID,
		Status:         edge.ActionStatusBlocked,
		PolicySnapshot: b.policySnapshot,
		InputHash:      b.inputHash,
	})
	if err != nil {
		return nil, fmt.Errorf("record approval event: %w", err)
	}
	var metadata edge.Metadata
	if legacyID != "" {
		metadata = edge.Metadata{mcpLegacyApprovalIDKey: legacyID}
	}
	requester, agentID := mcpCallRequester(ctx)
	approval, err := g.edgeStore.EnqueueApproval(ctx, edge.EdgeApprovalRequest{
		TenantID:       meta.Tenant,
		SessionID:      meta.SessionID,
		ExecutionID:    meta.ExecutionID,
		EventID:        parent.EventID,
		PrincipalID:    meta.Principal,
		Requester:      requester,
		Reason:         reason,
		RuleID:         ruleID,
		PolicySnapshot: b.policySnapshot,
		ActionHash:     b.actionHash,
		InputHash:      b.inputHash,
		Labels:         edge.Labels{mcpApprovalAgentLabel: agentID},
		Metadata:       metadata,
	})
	if err == nil && approval == nil {
		err = errors.New("edge store returned nil approval without error")
	}
	return approval, err
}

// mcpCallRequester returns who is making this MCP call, the way approvals
// record it: the authenticated requester identity (credential hash and
// principal) when the HTTP middleware supplied one, else the principal; and
// the agent identity the call acts as.
func mcpCallRequester(ctx context.Context) (requester, agentID string) {
	meta, _ := mcp.CallMetadataFromContext(ctx)
	requester, agentID = strings.TrimSpace(meta.Principal), strings.TrimSpace(meta.AgentID)
	if gatewayMeta, ok := MCPCallMetadataFromContext(ctx); ok {
		requester = mcpRequesterIdentity(gatewayMeta)
	}
	return requester, agentID
}

// mcpApprovalIsCallers reports whether approval was minted for the caller of
// ctx acting as the same agent.
func mcpApprovalIsCallers(ctx context.Context, approval *edge.EdgeApproval) bool {
	requester, agentID := mcpCallRequester(ctx)
	return approval != nil && requester != "" && approval.Requester == requester &&
		approval.Labels[mcpApprovalAgentLabel] == agentID
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
