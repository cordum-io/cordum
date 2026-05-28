package agentd

import (
	"context"
	"errors"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
	"golang.org/x/sync/singleflight"
)

const defaultHookResponseWriteBudget = 250 * time.Millisecond

type EvaluatorConfig struct {
	Client      EvaluateClient
	EventWriter EventWriter
	State       SessionState

	Cache          *SafeAllowCache
	ApprovalWaiter ApprovalWaiter
	ApprovalConfig ApprovalDecisionConfig
	Recorder       edgecore.Recorder

	HookTimeout         time.Duration
	ResponseWriteBudget time.Duration
}

// Evaluator is the cordum-agentd policy orchestration boundary behind the
// local Claude hook endpoint. It calls Gateway /api/v1/edge/evaluate only,
// converts fresh responses to hook decisions, applies deterministic fail-mode
// behavior when Gateway is unavailable, and records redacted decision evidence.
type Evaluator struct {
	client      EvaluateClient
	eventWriter EventWriter
	state       SessionState

	cache          *SafeAllowCache
	approvalWaiter ApprovalWaiter
	approvalConfig ApprovalDecisionConfig
	recorder       edgecore.Recorder

	hookTimeout         time.Duration
	responseWriteBudget time.Duration
	coalesce            singleflight.Group
	coalesceWaitHook    func() // test hook; nil in production.
}

func NewEvaluator(cfg EvaluatorConfig) *Evaluator {
	hookTimeout := cfg.HookTimeout
	if hookTimeout <= 0 {
		hookTimeout = defaultHookTimeout
	}
	budget := cfg.ResponseWriteBudget
	if budget <= 0 {
		budget = defaultHookResponseWriteBudget
	}
	approvalCfg := cfg.ApprovalConfig
	if approvalCfg.PolicyMode == "" {
		approvalCfg.PolicyMode = cfg.State.PolicyMode
	}
	return &Evaluator{
		client:              cfg.Client,
		eventWriter:         cfg.EventWriter,
		state:               cfg.State,
		cache:               cfg.Cache,
		approvalWaiter:      cfg.ApprovalWaiter,
		approvalConfig:      approvalCfg,
		recorder:            cfg.Recorder,
		hookTimeout:         hookTimeout,
		responseWriteBudget: budget,
	}
}

func (e *Evaluator) EvaluateHook(ctx context.Context, req claude.AgentdRequest) (claude.AgentdDecision, error) {
	return e.evaluateHook(ctx, req, e.eventWriter, false)
}

func (e *Evaluator) EvaluateHookWithEventWriter(ctx context.Context, req claude.AgentdRequest, writer EventWriter) (claude.AgentdDecision, error) {
	return e.evaluateHook(ctx, req, writer, true)
}

func (e *Evaluator) evaluateHook(ctx context.Context, req claude.AgentdRequest, writer EventWriter, requireEvidence bool) (claude.AgentdDecision, error) {
	if e == nil || e.client == nil {
		return claude.AgentdDecision{}, errors.New("agentd evaluator not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	evalCtx, cancel := e.evaluationContext(ctx)
	defer cancel()
	startedAt := time.Now()

	evalReq := e.evaluateRequest(req)
	cacheReq := e.cacheRequest(req, evalReq)
	if cached, ok := e.cache.Get(cacheReq); ok {
		e.recordCacheLookup(evalReq, "hit")
		cached.EventID = ""
		decision := AgentdDecisionFromEvaluateResponse(evalCtx, cached, e.approvalConfig, e.approvalWaiter)
		e.recordDecisionObservability(req, evalReq, cached, decision, startedAt)
		if err := e.recordDecisionEvidence(writer, requireEvidence, decision, evalReq, DecisionEvidence{
			State:      e.state,
			Request:    req,
			Response:   cached,
			CacheHit:   true,
			DurationMS: req.DurationMS,
		}); err != nil {
			return decision, err
		}
		return decision, nil
	}
	if e.cache != nil {
		e.recordCacheLookup(evalReq, "miss")
	}
	if key, ok := evaluatorCoalesceKey(cacheReq); ok {
		return e.coalescedFreshDecision(ctx, evalCtx, key, req, evalReq, cacheReq, startedAt, writer, requireEvidence)
	}
	return e.evaluateFreshDecision(evalCtx, req, evalReq, cacheReq, startedAt, writer, requireEvidence)
}

func (e *Evaluator) coalescedFreshDecision(waitCtx, evalCtx context.Context, key string, req claude.AgentdRequest, evalReq EvaluateRequest, cacheReq SafeAllowCacheRequest, startedAt time.Time, writer EventWriter, requireEvidence bool) (claude.AgentdDecision, error) {
	ch := e.coalesce.DoChan(key, func() (any, error) {
		decision, err := e.evaluateFreshDecision(evalCtx, req, evalReq, cacheReq, startedAt, writer, requireEvidence)
		return coalescedEvaluateResult{decision: decision}, err
	})
	if e.coalesceWaitHook != nil {
		e.coalesceWaitHook()
	}
	select {
	case <-waitCtx.Done():
		return claude.AgentdDecision{}, waitCtx.Err()
	case result := <-ch:
		if result.Err != nil {
			return claude.AgentdDecision{}, result.Err
		}
		out, ok := result.Val.(coalescedEvaluateResult)
		if !ok {
			return claude.AgentdDecision{}, errors.New("agentd evaluator coalesce returned unexpected result")
		}
		return out.decision, nil
	}
}

type coalescedEvaluateResult struct {
	decision claude.AgentdDecision
}

func (e *Evaluator) evaluateFreshDecision(evalCtx context.Context, req claude.AgentdRequest, evalReq EvaluateRequest, cacheReq SafeAllowCacheRequest, startedAt time.Time, writer EventWriter, requireEvidence bool) (claude.AgentdDecision, error) {
	resp, err := e.client.Evaluate(evalCtx, evalReq)
	if err != nil {
		return e.degradedDecision(req, evalReq, err, cacheReq, startedAt, writer, requireEvidence)
	}
	if resp == nil {
		return e.degradedDecision(req, evalReq, ErrEvaluateResponseMalformed, cacheReq, startedAt, writer, requireEvidence)
	}
	decision := AgentdDecisionFromEvaluateResponse(evalCtx, *resp, e.approvalConfig, e.approvalWaiter)
	e.recordDecisionObservability(req, evalReq, *resp, decision, startedAt)
	_ = e.cache.Put(cacheRequestWithEvaluateResponse(cacheReq, *resp), *resp)
	// Gateway evaluate already persisted a hook.policy_decision event under
	// resp.EventID. The agentd-side evidence event captures separate local
	// metadata (cache lookup, fail-mode, agentd timing) and must be a distinct
	// record — reusing resp.EventID would collide with the gateway-written
	// event when the events/batch flush hits loadEventByIDInTx.
	freshResp := *resp
	freshResp.EventID = ""
	if err := e.recordDecisionEvidence(writer, requireEvidence, decision, evalReq, DecisionEvidence{
		State:      e.state,
		Request:    req,
		Response:   freshResp,
		CacheMiss:  e.cache != nil,
		DurationMS: req.DurationMS,
	}); err != nil {
		return decision, err
	}
	return decision, nil
}

func evaluatorCoalesceKey(req SafeAllowCacheRequest) (string, bool) {
	if strings.TrimSpace(req.TenantID) == "" ||
		strings.TrimSpace(req.PolicySnapshot) == "" ||
		strings.TrimSpace(req.ActionHash) == "" ||
		strings.TrimSpace(req.InputHash) == "" {
		return "", false
	}
	return "edge-evaluate:" + safeAllowCacheKey(req), true
}

func (e *Evaluator) evaluationContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := e.hookTimeout
	if timeout <= 0 {
		timeout = defaultHookTimeout
	}
	if budget := e.responseWriteBudget; budget > 0 && timeout > budget {
		timeout -= budget
	}
	return context.WithTimeout(parent, timeout)
}

func (e *Evaluator) evaluateRequest(req claude.AgentdRequest) EvaluateRequest {
	kind := req.Kind
	if strings.TrimSpace(kind) == "" {
		kind = string(hookEventKind(req.EventName))
	}
	layer := req.Layer
	if strings.TrimSpace(layer) == "" {
		layer = string(edgecore.LayerHook)
	}
	agentProduct := "claude-code"
	return boundedEvaluateRequest(EvaluateRequest{
		TenantID:      nonEmpty(req.TenantID, e.state.TenantID),
		PrincipalID:   nonEmpty(req.PrincipalID, e.state.PrincipalID),
		SessionID:     nonEmpty(req.SessionID, e.state.SessionID),
		ExecutionID:   nonEmpty(req.ExecutionID, e.state.ExecutionID),
		AgentProduct:  agentProduct,
		Layer:         layer,
		Kind:          kind,
		ToolName:      req.ToolName,
		ToolUseID:     req.ToolUseID,
		InputRedacted: safeInputRedacted(req.InputRedacted),
		InputHash:     req.InputHash,
		ActionName:    evaluatorActionName(req),
		Capability:    req.Capability,
		RiskTags:      append([]string(nil), req.RiskTags...),
		Labels:        evaluatorLabels(req.Labels),
		CWD:           req.CWD,
	})
}

func (e *Evaluator) cacheRequest(req claude.AgentdRequest, evalReq EvaluateRequest) SafeAllowCacheRequest {
	return SafeAllowCacheRequest{
		TenantID:                 evalReq.TenantID,
		PolicyMode:               nonEmptyPolicyMode(e.state.PolicyMode, edgecore.PolicyModeObserve),
		PolicySnapshot:           e.state.PolicySnapshot,
		WorkflowOverrideSnapshot: e.state.WorkflowOverrideSnapshot,
		JobOverrideSnapshot:      e.state.JobOverrideSnapshot,
		Kind:                     evalReq.Kind,
		ActionName:               evalReq.ActionName,
		Capability:               evalReq.Capability,
		RiskTags:                 append([]string(nil), evalReq.RiskTags...),
		Labels:                   evaluatorLabels(req.Labels),
		ActionHash:               req.ActionHash,
		InputHash:                evalReq.InputHash,
		InputRedacted:            evalReq.InputRedacted,
	}
}

func cacheRequestWithEvaluateResponse(req SafeAllowCacheRequest, resp EvaluateResponse) SafeAllowCacheRequest {
	if strings.TrimSpace(resp.PolicySnapshot) != "" {
		req.PolicySnapshot = resp.PolicySnapshot
	}
	if strings.TrimSpace(resp.WorkflowOverrideSnapshot) != "" {
		req.WorkflowOverrideSnapshot = resp.WorkflowOverrideSnapshot
	}
	if strings.TrimSpace(resp.JobOverrideSnapshot) != "" {
		req.JobOverrideSnapshot = resp.JobOverrideSnapshot
	}
	return req
}

func (e *Evaluator) degradedDecision(req claude.AgentdRequest, evalReq EvaluateRequest, err error, cacheReq SafeAllowCacheRequest, startedAt time.Time, writer EventWriter, requireEvidence bool) (claude.AgentdDecision, error) {
	category := ClassifyEvaluateError(err)
	outcome := ApplyFailMode(FailModeContext{
		PolicyMode:           nonEmptyPolicyMode(e.state.PolicyMode, edgecore.PolicyModeObserve),
		WorkflowEdgeRequired: evaluatorWorkflowEdgeRequired(req.Labels),
		ActionIsKnownSafe:    evaluatorActionKnownSafe(cacheReq),
		HasFreshDecision:     false,
		GatewayErrorCategory: category,
		ApprovalRef:          "",
	})
	decision := claude.AgentdDecision{
		Decision: outcome.Decision,
		// Surface the degraded flag on the wire to the hook. Previously this
		// field wasn't on the AgentdDecision struct at all — the hook lost
		// the signal that a fail-mode response was synthesized (vs. a real
		// gateway decision), and `hookOutputForRun` couldn't fail-close
		// on the degraded path. With the struct now carrying Degraded
		// (claude/agentd_client.go), the hook can synthesize a deny on
		// PreToolUse under enforce / enterprise-strict modes.
		Degraded: true,
	}
	if outcome.TerminalCopy != "" {
		decision.Reason = boundDecisionText(outcome.TerminalCopy)
	} else if outcome.Decision != claude.DecisionAllow {
		decision.Reason = boundDecisionText(outcome.Reason)
	}
	e.recordFailModeObservability(req, evalReq, outcome, category, startedAt)
	resp := EvaluateResponse{
		Decision:                 string(edgeDecisionFromClaude(outcome.Decision)),
		Reason:                   outcome.Reason,
		PolicySnapshot:           e.state.PolicySnapshot,
		WorkflowOverrideSnapshot: e.state.WorkflowOverrideSnapshot,
		JobOverrideSnapshot:      e.state.JobOverrideSnapshot,
		Degraded:                 true,
		ErrorCode:                string(category),
		ErrorMessage:             outcome.Reason,
		PermissionDecision:       string(outcome.Decision),
		TerminalMessage:          outcome.TerminalCopy,
	}
	if err := e.recordDecisionEvidence(writer, requireEvidence, decision, evalReq, DecisionEvidence{
		State:        e.state,
		Request:      req,
		Response:     resp,
		Degraded:     true,
		FailClosed:   outcome.FailClosed,
		ErrorCode:    string(category),
		ErrorMessage: outcome.Reason,
		DurationMS:   req.DurationMS,
	}); err != nil {
		return decision, err
	}
	return decision, nil
}

func (e *Evaluator) recordDecisionEvidence(writer EventWriter, requireEvidence bool, decision claude.AgentdDecision, evalReq EvaluateRequest, evidence DecisionEvidence) error {
	if _, err := RecordDecisionEvidence(context.Background(), writer, decision, evidence); err != nil {
		e.recordEvidenceFailure(evalReq)
		if requireEvidence {
			return err
		}
	}
	return nil
}

func evaluatorActionName(req claude.AgentdRequest) string {
	if strings.TrimSpace(req.ReasonCode) != "" {
		return boundMetadataString(req.ReasonCode)
	}
	if strings.TrimSpace(req.EventName) != "" {
		return boundMetadataString("claude." + strings.ToLower(strings.TrimSpace(req.EventName)))
	}
	return "edge.evaluate"
}

func evaluatorLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if isSensitiveMetadataKey(k) {
			continue
		}
		out[boundMetadataString(k)] = sanitizeEvaluateErrorText(v)
	}
	return out
}

func evaluatorActionKnownSafe(req SafeAllowCacheRequest) bool {
	if strings.ToLower(strings.TrimSpace(req.Labels["command.class"])) != "safe" {
		return false
	}
	for _, tag := range req.RiskTags {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "unknown", "review_required", "destructive", "network", "install", "deploy", "secrets", "mutating", "write":
			return false
		}
	}
	return true
}

func evaluatorWorkflowEdgeRequired(labels map[string]string) bool {
	for _, key := range []string{"requires-edge-governance", "requires_edge_governance", "workflow.requires-edge-governance", "workflow.requires_edge_governance"} {
		if parseBool(labels[key]) {
			return true
		}
	}
	return false
}

func edgeDecisionFromClaude(decision claude.Decision) edgecore.EdgeDecision {
	switch decision {
	case claude.DecisionAllow:
		return edgecore.DecisionAllow
	case claude.DecisionRequireApproval, claude.DecisionAsk:
		return edgecore.DecisionRequireApproval
	default:
		return edgecore.DecisionDeny
	}
}
