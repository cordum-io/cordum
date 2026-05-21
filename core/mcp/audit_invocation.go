package mcp

// Tool-invocation auditor for MCP: emits one SIEMEvent per terminal
// inbound or outbound tools/call, carrying redacted arguments + a
// small result summary + latency. Separate from the legacy
// ToolCallAuditHook in audit_hook.go — that hook only fired on
// success and didn't carry redacted args. The new auditor interface
// is the canonical surface going forward; the legacy hook stays
// wired for back-compat but emits the same rich shape when run
// through the invocation wrapper.
//
// Outbound coordination contract (task-ba236f62): signed-outbound
// clients should implement OutboundClient and be wrapped via
// AuditedOutboundClient{Inner: signedClient, Auditor: auditor}
// rather than rolling their own audit emission.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/model"
)

// InvocationHandle carries the context of a started invocation to
// FinishInbound/FinishOutbound. Unexported fields so callers treat
// it as opaque.
//
// The approval gate (core/controlplane/gateway.gatewayApprovalGate)
// may write approvalID/approvalStatus onto the handle during the
// call — see approvalGateHookFromContext. Writes happen sequentially
// between StartInbound and FinishInbound (no concurrent access), so
// no mutex is required.
type InvocationHandle struct {
	startedAt      time.Time
	direction      string // "inbound" | "outbound"
	agentID        string
	tenantID       string
	toolName       string
	serverID       string // outbound only
	approvalID     string
	approvalStatus string // "" (default → "none"), "required", "consumed"
	argsRaw        json.RawMessage
}

// MarkApprovalConsumed is called by the approval gate after a successful
// pre-approval claim so the upcoming FinishInbound emits
// approval_status="consumed" + approval_id=<rec.ID>.
func (h *InvocationHandle) MarkApprovalConsumed(approvalID string) {
	if h == nil {
		return
	}
	h.approvalID = approvalID
	h.approvalStatus = "consumed"
}

// MarkApprovalRequired is called by the approval gate when it enqueues
// a pending approval request; the invocation audit then records
// approval_status="required" and stamps the freshly-created approval_id.
func (h *InvocationHandle) MarkApprovalRequired(approvalID string) {
	if h == nil {
		return
	}
	h.approvalID = approvalID
	h.approvalStatus = "required"
}

// MarkApprovalPreapproved is called by the approval gate when the
// agent identity's PreapprovedMutatingTools list covers this call and
// the gate skips the human-approval enqueue. The invocation audit
// records approval_status="preapproved" (NOT "consumed") so SIEM
// consumers can distinguish scope-based bypass from a consumed human
// approval — the distinction matters for forensics and for alerting
// when a preapproved bot identity goes rogue. No approval_id is
// stamped because no record was written.
func (h *InvocationHandle) MarkApprovalPreapproved(toolName string) {
	if h == nil {
		return
	}
	h.approvalID = ""
	h.approvalStatus = "preapproved"
}

// invocationHandleCtxKey is the unexported ctx key used to propagate an
// *InvocationHandle from the server into the approval gate (and any
// downstream writer). Consumers use InvocationHandleFromContext so the
// ctx key itself stays package-private.
type invocationHandleCtxKey struct{}

// contextWithInvocationHandle attaches a handle to ctx.
func contextWithInvocationHandle(ctx context.Context, h *InvocationHandle) context.Context {
	if h == nil {
		return ctx
	}
	return context.WithValue(ctx, invocationHandleCtxKey{}, h)
}

// InvocationHandleFromContext returns the handle installed by the
// invocation auditor. Used by the approval gate to stamp
// approval_id/approval_status on the invocation event without threading
// a second return value through ApprovalGate.Check. Returns nil when
// no auditor is wired.
func InvocationHandleFromContext(ctx context.Context) *InvocationHandle {
	if ctx == nil {
		return nil
	}
	h, _ := ctx.Value(invocationHandleCtxKey{}).(*InvocationHandle)
	return h
}

// ToolInvocationAuditor is the contract the MCP server and outbound
// clients call to audit each tool invocation. Implementations must be
// safe for concurrent use.
//
// StartInbound/StartOutbound return a derived ctx that carries the
// *InvocationHandle under an unexported key. The approval gate pulls
// the handle via InvocationHandleFromContext and marks
// approval_status=consumed (after ClaimPreApproved hits) or
// approval_status=required (after EnqueueMCPApproval) so the Finish*
// event surfaces the approval correlation data without threading an
// extra return value through ApprovalGate.Check.
type ToolInvocationAuditor interface {
	// StartInbound records the beginning of a tools/call handled by
	// Cordum's MCP server (Cordum as server). The returned ctx MUST
	// be passed down to ToolRegistry.Call so the approval gate can
	// stamp approval correlation on the handle.
	StartInbound(ctx context.Context, agentID, tenantID, toolName string, args json.RawMessage) (context.Context, *InvocationHandle)
	// FinishInbound emits the terminal invocation event with result
	// summary, latency, and decision derived from result+err. A nil
	// handle is ignored.
	FinishInbound(h *InvocationHandle, result *ToolCallResult, err error)

	// StartOutbound records a Cordum-initiated call to an external
	// MCP server (Cordum as client).
	StartOutbound(ctx context.Context, agentID, tenantID, serverID, toolName string, args json.RawMessage) (context.Context, *InvocationHandle)
	// FinishOutbound emits the outbound counterpart event.
	FinishOutbound(h *InvocationHandle, result *ToolCallResult, err error)
}

// ArgumentRedactor scrubs sensitive fields from the arguments blob
// before it lands in an audit event. Implementations MUST be
// nil-safe (nil/empty input returns nil/empty output) and must never
// panic on malformed JSON.
type ArgumentRedactor interface {
	Redact(args json.RawMessage) json.RawMessage
}

// auditor is the default ToolInvocationAuditor implementation.
type auditor struct {
	mu       sync.RWMutex
	sender   audit.AuditSender
	redactor ArgumentRedactor
	now      func() time.Time
}

// NewToolInvocationAuditor wires sender + redactor. Nil sender is
// accepted; events are silently dropped — useful for dev deploys.
func NewToolInvocationAuditor(sender audit.AuditSender, redactor ArgumentRedactor) ToolInvocationAuditor {
	if redactor == nil {
		redactor = DefaultRedactor()
	}
	return &auditor{
		sender:   sender,
		redactor: redactor,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// SetRedactor replaces the active redactor — used when policy bundles
// reload so a fresh rule set applies to subsequent calls.
func (a *auditor) SetRedactor(r ArgumentRedactor) {
	if a == nil || r == nil {
		return
	}
	a.mu.Lock()
	a.redactor = r
	a.mu.Unlock()
}

func (a *auditor) currentRedactor() ArgumentRedactor {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.redactor
}

func (a *auditor) StartInbound(ctx context.Context, agentID, tenantID, toolName string, args json.RawMessage) (context.Context, *InvocationHandle) {
	if a == nil {
		return ctx, nil
	}
	if agentID == "" {
		agentID = "unknown"
	}
	// Default empty tenant to model.DefaultTenant at handle creation
	// time so every downstream emit (including the recover() path) sees
	// a non-empty TenantID. Anonymous-session tool calls (no auth ctx,
	// no header) land on the default tenant chain rather than tripping
	// the chain sender's slog.Warn. task-3fad45d3.
	tenantID = model.ResolveTenantForAudit(tenantID, "")
	h := &InvocationHandle{
		startedAt:  a.now(),
		direction:  "inbound",
		agentID:    agentID,
		tenantID:   tenantID,
		toolName:   toolName,
		argsRaw:    append(json.RawMessage(nil), args...),
		approvalID: ApprovalIDFromContext(ctx),
	}
	// If the caller already consumed an approval (e.g. a sync
	// test harness pre-populates approval_id on ctx), propagate
	// the "consumed" status. Live approvals set it via
	// MarkApprovalConsumed on the handle during gate.Check.
	if h.approvalID != "" {
		h.approvalStatus = "consumed"
	}
	return contextWithInvocationHandle(ctx, h), h
}

func (a *auditor) FinishInbound(h *InvocationHandle, result *ToolCallResult, err error) {
	if a == nil || h == nil {
		return
	}
	a.emit(h, audit.EventMCPToolInvocation, result, err)
}

func (a *auditor) StartOutbound(ctx context.Context, agentID, tenantID, serverID, toolName string, args json.RawMessage) (context.Context, *InvocationHandle) {
	if a == nil {
		return ctx, nil
	}
	if agentID == "" {
		agentID = "unknown"
	}
	// Same producer-side default as StartInbound; see comment there.
	tenantID = model.ResolveTenantForAudit(tenantID, "")
	h := &InvocationHandle{
		startedAt:  a.now(),
		direction:  "outbound",
		agentID:    agentID,
		tenantID:   tenantID,
		toolName:   toolName,
		serverID:   serverID,
		argsRaw:    append(json.RawMessage(nil), args...),
		approvalID: ApprovalIDFromContext(ctx),
	}
	if h.approvalID != "" {
		h.approvalStatus = "consumed"
	}
	return contextWithInvocationHandle(ctx, h), h
}

func (a *auditor) FinishOutbound(h *InvocationHandle, result *ToolCallResult, err error) {
	if a == nil || h == nil {
		return
	}
	a.emit(h, audit.EventMCPToolOutboundInvocation, result, err)
}

// emit is the single code path producing an invocation SIEMEvent from
// a handle + terminal state. Wraps redaction in a recover() so a
// malformed redactor rule never crashes the audit path.
func (a *auditor) emit(h *InvocationHandle, eventType string, result *ToolCallResult, err error) {
	if a == nil || a.sender == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			// Best-effort: emit a minimal event so the gap is visible.
			a.sender.Send(audit.SIEMEvent{
				Timestamp: a.now(),
				EventType: eventType,
				Severity:  audit.SeverityHigh,
				TenantID:  h.tenantID,
				AgentID:   h.agentID,
				Action:    "invoke",
				Extra: map[string]string{
					"auditor_internal_error": toString(r),
					"tool_name":              h.toolName,
					"direction":              h.direction,
				},
			})
		}
	}()

	redacted := a.currentRedactor().Redact(h.argsRaw)
	latency := a.now().Sub(h.startedAt)

	decision := "allow"
	resultType := "ok"
	errorCode := ""
	subReason := ""
	switch e := err.(type) {
	case *NotAuthorized:
		// Scope-filter denial — EvaluateForIdentity rejected this call.
		// The DenyAuditor emits mcp.tool_denied too; the invocation
		// event is the unified per-call record so decision=deny must
		// surface here to satisfy QA's correlation tests.
		decision = "deny"
		resultType = "error"
		errorCode = e.Error()
		subReason = string(e.SubReason)
	case *ApprovalRequired:
		// Approval gate enqueued a pending request. The tool body
		// never ran; approval_status=required signals the LLM/client
		// to resubmit once an operator resolves the approval.
		decision = "allow"
		resultType = "error"
		errorCode = e.Error()
		if h.approvalID == "" {
			h.approvalID = e.ApprovalID
		}
		h.approvalStatus = "required"
	case nil:
		if result != nil && result.IsError {
			resultType = "error"
		}
	default:
		resultType = "error"
		errorCode = err.Error()
	}

	contentCount := 0
	resultHash := ""
	if result != nil {
		contentCount = len(result.Content)
		if raw, mErr := json.Marshal(result); mErr == nil {
			sum := sha256.Sum256(raw)
			resultHash = hex.EncodeToString(sum[:])
		}
	}

	extra := map[string]string{
		"tool_name":       h.toolName,
		"direction":       h.direction,
		"latency_ms":      strconv.FormatInt(latency.Milliseconds(), 10),
		"args_redacted":   string(redacted),
		"result_type":     resultType,
		"result_count":    strconv.Itoa(contentCount),
		"result_hash":     resultHash,
		"approval_status": approvalStatusForHandle(h),
	}
	if h.serverID != "" {
		extra["server_id"] = h.serverID
	}
	if h.approvalID != "" {
		extra["approval_id"] = h.approvalID
	}
	if errorCode != "" {
		extra["error_code"] = truncate(errorCode, 512)
	}
	if subReason != "" {
		extra["sub_reason"] = subReason
	}
	if h.agentID == "unknown" {
		extra["identity_missing"] = "true"
	}

	a.sender.Send(audit.SIEMEvent{
		Timestamp: a.now(),
		EventType: eventType,
		Severity:  severityForResultWithDecision(resultType, decision),
		TenantID:  h.tenantID,
		AgentID:   h.agentID,
		Action:    "invoke",
		Decision:  decision,
		Reason:    subReason,
		Extra:     extra,
	})
}

// approvalStatusForHandle returns the current approval_status stamped
// on the handle. Defaults to "none" when nothing has written it —
// matches the QA contract that the absence of an approval is visible
// as an explicit status, not a missing field.
func approvalStatusForHandle(h *InvocationHandle) string {
	if h == nil {
		return "none"
	}
	if h.approvalStatus != "" {
		return h.approvalStatus
	}
	if h.approvalID != "" {
		return "consumed"
	}
	return "none"
}

// severityForResultWithDecision: a deny is higher-severity than a
// plain ok; error-results without a deny stay at medium.
func severityForResultWithDecision(resultType, decision string) string {
	if decision == "deny" {
		return audit.SeverityMedium
	}
	return severityForResult(resultType)
}

func severityForResult(resultType string) string {
	if resultType == "error" {
		return audit.SeverityMedium
	}
	return audit.SeverityInfo
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// OutboundClient is the minimal interface AuditedOutboundClient wraps.
// Task-ba236f62's signed-outbound client MUST implement this so audit
// happens at the same choke point for every outbound MCP call.
type OutboundClient interface {
	Call(ctx context.Context, serverID, toolName string, args json.RawMessage) (*ToolCallResult, error)
}

// AuditedOutboundClient wraps an OutboundClient with audit emission.
// Agent/tenant come from context via IdentityFromContext + TenantFromContext.
type AuditedOutboundClient struct {
	Inner   OutboundClient
	Auditor ToolInvocationAuditor
}

// Call forwards to the inner client, emitting StartOutbound /
// FinishOutbound around the call. Panics in the inner client are
// recovered so the audit event still fires before re-panicking.
func (c *AuditedOutboundClient) Call(ctx context.Context, serverID, toolName string, args json.RawMessage) (*ToolCallResult, error) {
	if c == nil || c.Inner == nil {
		return nil, nil
	}
	agentID := ""
	if id := IdentityFromContext(ctx); id != nil {
		agentID = id.ID
	}
	tenantID := TenantFromContext(ctx)
	var handle *InvocationHandle
	if c.Auditor != nil {
		ctx, handle = c.Auditor.StartOutbound(ctx, agentID, tenantID, serverID, toolName, args)
	}
	var (
		result *ToolCallResult
		err    error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				if c.Auditor != nil {
					c.Auditor.FinishOutbound(handle, nil, panicErr(r))
				}
				panic(r)
			}
		}()
		result, err = c.Inner.Call(ctx, serverID, toolName, args)
	}()
	if c.Auditor != nil {
		c.Auditor.FinishOutbound(handle, result, err)
	}
	return result, err
}

type errString string

func (e errString) Error() string { return string(e) }

func panicErr(v any) error {
	return errString("panic: " + toString(v))
}
