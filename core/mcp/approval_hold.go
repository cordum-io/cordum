package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cordum/cordum/core/edge"
)

// approvalRefArgKey is the JSON property name on tool-call arguments
// that callers use to present an existing approval reference for a
// resume/retry of a previously-held tool call. The `_`-prefix marks
// the field as server-derived/server-reserved — it never reaches the
// upstream tool handler. Strip happens after the claim succeeds.
const approvalRefArgKey = "_approval_ref"

// errMissingPolicySnapshot is the explicit fail-closed sentinel for the
// EDGE-103 approval-hold consume path (PR #276 Sub-E finding #16):
// ProcessApprovalClaim REFUSES to issue a ClaimApproval when the caller
// wired a Store but no PolicySnapshot provider. Otherwise the resulting
// ApprovalClaimRequest carries an empty PolicySnapshot, and the
// edge.RedisStore validation would either fail at the store layer (per-
// request churn) or — worse — let a hold register as "approved" against
// an empty snapshot. The boot-time guard at MCPServer.WithApprovalHold
// is the primary defense; this sentinel is the in-package contract that
// catches direct misuse of ProcessApprovalClaim.
var errMissingPolicySnapshot = errors.New("missing_policy_snapshot: ApprovalHoldDeps.PolicySnapshot is required")

// BuildMCPApprovalBinding centralises the hash tuple that binds an MCP
// approval lifecycle. Both the gateway handoff (mint side) and
// ProcessApprovalClaim (consume side) MUST derive the action+input
// hashes through this helper so the edge.RedisStore.ClaimApproval
// match never surfaces args_mismatch on a legitimate retry.
//
// The binding includes:
//   - tenant (separation boundary)
//   - server (MCP upstream identity)
//   - tool name (the action)
//   - normalized target path extracted from path-like args (action key)
//   - canonical SHA-256 of args AFTER stripping `_approval_ref` (input key)
//
// Server/tenant/tool/path drive ActionHash; canonical-stripped args drive
// InputHash. Empty/malformed args produce a stable empty-object canonical
// form so the binding is deterministic across nil and `{}` inputs.
// `_approval_ref` is stripped before canonicalisation so a resume retry
// (which echoes the ref in args) lands on the same InputHash as the
// original gated call.
//
// _policySnapshot is intentionally unused here: the approval store enforces
// snapshot freshness during ClaimApproval, while this binding must stay scoped
// to action/input hashes only. Naming the parameter prevents future mint vs
// consume drift if one side tries to include the snapshot in the hash.
func BuildMCPApprovalBinding(tenant, server string, params ToolCallParams, _policySnapshot string) (actionHash, inputHash string) {
	stripped := stripApprovalRefArg(params.Arguments)
	canonical, inputHash, _ := CanonicaliseArgs(stripped)
	var targetPath string
	if parsed, _ := parseArgsForDescriptor(canonical); parsed != nil {
		targetPath = extractTargetPathFromArgs(parsed)
	}
	actionHash = ActionTupleHash(tenant, server, params.Name, targetPath)
	return actionHash, inputHash
}

// stripApprovalRefArg returns args with the server-reserved
// `_approval_ref` field removed. nil/empty payloads return nil so the
// canonicaliser produces the stable empty-object form rather than
// hashing the original byte order.
//
// Always re-marshals when the args parse as a JSON object so two
// payloads with the same logical content but different key order /
// whitespace produce identical bytes. This is what makes
// BuildMCPApprovalBinding's InputHash stable.
func stripApprovalRefArg(args json.RawMessage) json.RawMessage {
	if len(args) == 0 {
		return nil
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(args, &parsed); err != nil {
		return args
	}
	delete(parsed, approvalRefArgKey)
	out, err := json.Marshal(parsed)
	if err != nil {
		return args
	}
	return out
}

// ApprovalClaimStore is the narrow contract this layer needs from the
// Edge approval store. Production wires this to edge.RedisStore.ClaimApproval;
// tests inject an in-memory fake.
//
// The (consumed, approval, err) return follows the existing RedisStore
// shape: consumed=true with a non-nil approval on success; consumed=false
// + non-nil err typed as *edge.ApprovalConflictError on a fail-closed
// lifecycle conflict; consumed=false + nil err is the benign "approval
// not in claimable state" miss the caller maps to not_found.
type ApprovalClaimStore interface {
	ClaimApproval(ctx context.Context, req edge.ApprovalClaimRequest) (*edge.EdgeApproval, bool, error)
}

// ApprovalHoldDeps bundles the production wiring an approval-claim
// check consumes. Every field is optional so a server without the
// approval store wired short-circuits to "no claim presented"
// (the call proceeds to EDGE-102's policy evaluation as before).
//
// ServerName MUST mirror the logical MCP server identifier the mint
// path used (typically MCPServer.policyServerName via WithPolicyGate).
// CanonicalActionHash includes the server in its tuple, so consuming
// with an empty ServerName silently fails to match approvals minted
// against a non-empty server.
type ApprovalHoldDeps struct {
	Store          ApprovalClaimStore
	PolicySnapshot func(ctx context.Context) string
	ServerName     string
}

// ApprovalClaimOutcome reports the result of inspecting tool-call
// arguments for an approval claim and consuming it when present.
//
// Consumed=false + nil ConflictErr + ClaimRef=="" means no `_approval_ref`
// was present; the caller proceeds with the normal policy path.
//
// Consumed=true + non-nil Approval means the store accepted the claim;
// the caller dispatches upstream with the approval-stripped arguments.
//
// Consumed=false + non-nil ConflictErr means the store refused the claim
// (fail-closed); the caller surfaces the conflict to the client as a
// JSON-RPC error keyed by ConflictErr.Kind.
type ApprovalClaimOutcome struct {
	Consumed     bool
	ClaimRef     string
	Approval     *edge.EdgeApproval
	ConflictErr  *edge.ApprovalConflictError
	StrippedArgs json.RawMessage
}

// ProcessApprovalClaim inspects the tool-call arguments for an
// `_approval_ref` field and, when present, calls the approval store's
// atomic Consume. On success the returned StrippedArgs has the
// `_approval_ref` field removed so the upstream tool handler never
// sees the server-reserved field; the policy gate also sees the
// stripped form (the request that was originally authorized).
//
// The Edge approval store enforces tenant isolation, expiry, args/policy
// matching, and the self-approval defense-in-depth check. ProcessApprovalClaim
// is the wire-format adapter — it does NOT duplicate any of those checks.
func ProcessApprovalClaim(ctx context.Context, deps ApprovalHoldDeps, params ToolCallParams) (ApprovalClaimOutcome, error) {
	if deps.Store == nil {
		return ApprovalClaimOutcome{}, nil
	}
	if len(params.Arguments) == 0 {
		return ApprovalClaimOutcome{}, nil
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(params.Arguments, &parsed); err != nil {
		// Malformed args is the caller's problem — the policy gate handles
		// it. ProcessApprovalClaim short-circuits.
		return ApprovalClaimOutcome{}, nil
	}
	rawRef, ok := parsed[approvalRefArgKey]
	if !ok {
		return ApprovalClaimOutcome{}, nil
	}
	var approvalRef string
	if err := json.Unmarshal(rawRef, &approvalRef); err != nil {
		return ApprovalClaimOutcome{}, fmt.Errorf("invalid %s: must be a string", approvalRefArgKey)
	}
	approvalRef = strings.TrimSpace(approvalRef)
	if approvalRef == "" {
		return ApprovalClaimOutcome{}, fmt.Errorf("invalid %s: must be non-empty", approvalRefArgKey)
	}

	meta, ok := CallMetadataFromContext(ctx)
	// Sub-E #26: fail closed when ANY linkage field is blank. The Edge
	// approval store keys consume on (TenantID, SessionID, ExecutionID,
	// AgentID); a single missing component breaks the audit join and
	// would silently mint or consume against an under-attributed
	// approval row. The EvaluateToolCall path has the symmetric check
	// for the gate-mint side.
	if !ok ||
		strings.TrimSpace(meta.Tenant) == "" ||
		strings.TrimSpace(meta.SessionID) == "" ||
		strings.TrimSpace(meta.ExecutionID) == "" ||
		strings.TrimSpace(meta.AgentID) == "" {
		return ApprovalClaimOutcome{}, errMissingMCPMetadata
	}

	// Strip the reserved key before computing the canonical hash + before
	// the upstream handler ever sees the payload.
	delete(parsed, approvalRefArgKey)
	strippedBytes, err := json.Marshal(parsed)
	if err != nil {
		return ApprovalClaimOutcome{}, fmt.Errorf("re-marshal stripped args: %w", err)
	}

	// Sub-E #16 defense-in-depth: even though MCPServer.WithApprovalHold
	// refuses to enable the path with a nil PolicySnapshot, direct callers
	// of ProcessApprovalClaim must also fail closed rather than minting a
	// claim with an empty PolicySnapshot. An empty snapshot would either
	// be rejected by the store (per-request churn) or — worse on a fake
	// store — allow the hold to register as "approved" against an empty
	// policy.
	if deps.PolicySnapshot == nil {
		return ApprovalClaimOutcome{}, errMissingPolicySnapshot
	}
	policySnapshot := deps.PolicySnapshot(ctx)

	// EDGE-103 reopen #1: derive the action+input hashes through the
	// same BuildMCPApprovalBinding helper the mint side calls. Without
	// this centralisation the consume path could re-implement the
	// tuple subtly differently (extractTargetPathFromArgs path key,
	// canonical-args-without-_approval_ref hashing, server inclusion)
	// and ClaimApproval would surface kind=args_mismatch on every
	// legitimate retry.
	resumeParams := ToolCallParams{Name: params.Name, Arguments: strippedBytes}
	actionHash, inputHash := BuildMCPApprovalBinding(meta.Tenant, deps.ServerName, resumeParams, policySnapshot)

	claim := edge.ApprovalClaimRequest{
		TenantID:    meta.Tenant,
		ApprovalRef: approvalRef,
		SessionID:   meta.SessionID,
		ExecutionID: meta.ExecutionID,
		// EventID must name the approval.requested event the mint side
		// recorded, which only the approval knows: the gateway's claim
		// store rebinds SessionID, ExecutionID and EventID to the
		// approval's own when the approval was minted for this caller
		// and agent. Without that rebinding this placeholder never
		// matches and the store refuses the claim (tuple_mismatch).
		EventID:        meta.AgentID,
		ActionHash:     actionHash,
		InputHash:      inputHash,
		PolicySnapshot: policySnapshot,
		ConsumedAt:     time.Now().UTC(),
		CallerAgentID:  meta.Principal,
	}

	approval, consumed, claimErr := deps.Store.ClaimApproval(ctx, claim)
	if claimErr != nil {
		var conflict *edge.ApprovalConflictError
		if errors.As(claimErr, &conflict) {
			return ApprovalClaimOutcome{
				ClaimRef:    approvalRef,
				ConflictErr: mcpApprovalClaimConflict(conflict),
			}, nil
		}
		if errors.Is(claimErr, edge.ErrApprovalConflict) {
			// The store's text names internal ids (session, execution, event).
			slog.WarnContext(ctx, "mcp: approval claim conflict", "approval_ref", approvalRef, "error", claimErr)
			return ApprovalClaimOutcome{
				ClaimRef:    approvalRef,
				ConflictErr: &edge.ApprovalConflictError{Kind: edge.ApprovalConflictKindUnknown, Reason: "approval not claimable"},
			}, nil
		}
		if errors.Is(claimErr, edge.ErrNotFound) {
			return ApprovalClaimOutcome{
				ClaimRef:    approvalRef,
				ConflictErr: &edge.ApprovalConflictError{Kind: edge.ApprovalConflictKindNotFound, Reason: "approval not found"},
			}, nil
		}
		return ApprovalClaimOutcome{}, fmt.Errorf("%w: claim approval: %w", ErrInternal, claimErr)
	}
	if !consumed || approval == nil {
		return ApprovalClaimOutcome{
			ClaimRef:    approvalRef,
			ConflictErr: &edge.ApprovalConflictError{Kind: edge.ApprovalConflictKindNotFound, Reason: "approval not claimable"},
		}, nil
	}
	return ApprovalClaimOutcome{
		Consumed:     true,
		ClaimRef:     approvalRef,
		Approval:     approval,
		StrippedArgs: strippedBytes,
	}, nil
}

// claimedCallKey marks a tools/call whose `_approval_ref` claim was consumed.
type claimedCallKey struct{}

// claimedCall is a consumed claim. The Edge store matched it to this tool and
// these args, so it stands for the human approval the registry's
// RequiresApproval gate and the policy gate's REQUIRE_HUMAN would ask for. It
// covers this one call only: the claim is single-use.
type claimedCall struct{ tool, inputHash string }

// withClaimedCall records on ctx that the claim for tool with args was consumed.
func withClaimedCall(ctx context.Context, tool string, args json.RawMessage) context.Context {
	_, inputHash := BuildMCPApprovalBinding("", "", ToolCallParams{Name: tool, Arguments: args}, "")
	return context.WithValue(ctx, claimedCallKey{}, claimedCall{tool: tool, inputHash: inputHash})
}

// claimCoversCall reports whether ctx carries a consumed claim for tool with
// these args.
func claimCoversCall(ctx context.Context, tool string, args json.RawMessage) bool {
	claim, ok := ctx.Value(claimedCallKey{}).(claimedCall)
	if !ok || claim.tool != tool {
		return false
	}
	_, inputHash := BuildMCPApprovalBinding("", "", ToolCallParams{Name: tool, Arguments: args}, "")
	return inputHash == claim.inputHash
}

func mcpApprovalClaimConflict(conflict *edge.ApprovalConflictError) *edge.ApprovalConflictError {
	if conflict == nil || !isStoreTerminalApprovalConflict(conflict) {
		return conflict
	}
	return &edge.ApprovalConflictError{
		Kind:   edge.ApprovalConflictKindNotFound,
		Reason: "approval not claimable",
	}
}

func isStoreTerminalApprovalConflict(conflict *edge.ApprovalConflictError) bool {
	reason := strings.TrimSpace(conflict.Reason)
	switch conflict.Kind {
	case edge.ApprovalConflictKindRejected:
		return reason == "" || strings.EqualFold(reason, "approval rejected")
	case edge.ApprovalConflictKindConsumed:
		return reason == "" || strings.EqualFold(reason, "approval already consumed")
	default:
		return false
	}
}

// ApprovalConflictKindFromError extracts the typed
// edge.ApprovalConflictKind from any error returned by the approval
// store. Unknown errors return (Kind="", false) so callers can decide
// whether to map to a generic JSON-RPC error or a structured one.
func ApprovalConflictKindFromError(err error) (edge.ApprovalConflictKind, bool) {
	var typed *edge.ApprovalConflictError
	if errors.As(err, &typed) {
		return typed.Kind, true
	}
	return edge.ApprovalConflictKindUnknown, false
}
