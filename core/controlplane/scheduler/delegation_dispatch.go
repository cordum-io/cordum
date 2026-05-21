package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/delegation"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

const (
	delegationRevokedBeforeDispatchReasonCode  = "delegation.revoked_before_dispatch"
	delegationExpiredBeforeDispatchReasonCode  = "delegation.expired_before_dispatch"
	delegationInvalidBeforeDispatchReasonCode  = "delegation.invalid_before_dispatch"
	delegationScopeBeforeDispatchReasonCode    = "delegation.scope_exceeded_before_dispatch"
	delegationChainBeforeDispatchReasonCode    = "delegation.chain_too_deep_before_dispatch"
	delegationAudienceBeforeDispatchReasonCode = "delegation.audience_mismatch_before_dispatch"
	delegationMissingDispatchTokenReasonCode   = "delegation.missing_dispatch_token"
	delegationDispatchUnavailableReasonCode    = "delegation.dispatch_reverify_unavailable"
	delegationLineageAuditKeyPrefix            = "cordum:audit:delegation_lineage:"
)

type schedulerDelegationPermissionsResolver struct {
	store *infraStore.AgentIdentityStore
}

func (r schedulerDelegationPermissionsResolver) ResolveAgentPermissions(ctx context.Context, agentID string) (delegation.AgentPermissions, error) {
	if r.store == nil {
		return delegation.AgentPermissions{}, fmt.Errorf("agent identity store unavailable")
	}
	identity, err := r.store.Get(ctx, "", agentID)
	if err != nil {
		return delegation.AgentPermissions{}, err
	}
	if identity == nil {
		return delegation.AgentPermissions{}, fmt.Errorf("agent identity not found")
	}
	return delegation.AgentPermissions{
		AllowedActions: identity.AllowedTools,
		AllowedTopics:  identity.AllowedTopics,
	}, nil
}

func (e *Engine) delegationTokenService() (*delegation.TokenService, error) {
	if e == nil || e.jobStore == nil {
		return nil, fmt.Errorf("delegation token service unavailable")
	}
	clientProvider, ok := e.jobStore.(interface{ Client() redis.UniversalClient })
	if !ok || clientProvider.Client() == nil {
		return nil, fmt.Errorf("delegation token service unavailable")
	}
	signingKey, err := delegation.LoadSigningKeyFromEnv()
	if err != nil {
		return nil, err
	}
	keyring, err := delegation.LoadVerificationKeysFromEnv()
	if err != nil {
		return nil, err
	}
	client := clientProvider.Client()
	return delegation.NewTokenService(
		signingKey,
		keyring,
		schedulerDelegationPermissionsResolver{store: infraStore.NewAgentIdentityStoreFromClient(client)},
		delegation.NewRedisRevocationStoreFromClient(client),
	)
}

func (e *Engine) verifyDelegationBeforeDispatch(ctx context.Context, req *pb.JobRequest) (model.DelegationLineage, bool, error) {
	if e == nil || req == nil || strings.TrimSpace(req.GetJobId()) == "" {
		return model.DelegationLineage{}, false, nil
	}
	tokenStore, ok := e.jobStore.(model.DelegationDispatchTokenStore)
	if !ok {
		if requestCarriesDelegation(req) {
			return model.DelegationLineage{}, true, fmt.Errorf("delegation dispatch token store unavailable")
		}
		return model.DelegationLineage{}, false, nil
	}
	tokenRecord, err := tokenStore.GetDelegationDispatchToken(ctx, req.GetJobId())
	if err != nil {
		return model.DelegationLineage{}, true, err
	}
	if strings.TrimSpace(tokenRecord.Token) == "" {
		if requestCarriesDelegation(req) {
			return model.DelegationLineage{}, true, fmt.Errorf("delegation dispatch token missing")
		}
		return model.DelegationLineage{}, false, nil
	}
	service, err := e.delegationTokenService()
	if err != nil {
		return model.DelegationLineage{}, true, err
	}
	verified, err := service.VerifyDelegationToken(ctx, tokenRecord.Token, tokenRecord.Audience)
	// Wipe the raw bearer token from the job-metadata TTL regardless of
	// verification outcome (Blocker 4 from #198 review). Even a rejected
	// token is sensitive material that should not sit in the 7-day
	// metadata hash; the DelegationLineage persisted below carries the
	// non-sensitive chain fields operators and audit need.
	if clearErr := tokenStore.ClearDelegationDispatchToken(ctx, req.GetJobId()); clearErr != nil {
		slog.Warn("delegation dispatch: failed to clear raw token after re-verify",
			"job_id", req.GetJobId(), "error", clearErr)
	}
	if err != nil {
		return model.DelegationLineage{}, true, err
	}
	lineage := delegationLineageFromVerifiedToken(verified)
	if err := e.jobStore.SetDelegationLineage(ctx, req.GetJobId(), lineage); err != nil {
		return model.DelegationLineage{}, true, err
	}
	return lineage, true, nil
}

func requestCarriesDelegation(req *pb.JobRequest) bool {
	if req == nil || req.GetLabels() == nil {
		return false
	}
	labels := req.GetLabels()
	return strings.TrimSpace(labels["_delegation.jti"]) != "" ||
		strings.TrimSpace(labels["_delegation.depth"]) != "" ||
		strings.TrimSpace(labels["_delegation.issuer_chain"]) != ""
}

func delegationLineageFromVerifiedToken(verified delegation.VerifiedToken) model.DelegationLineage {
	chain := make([]model.DelegationChainLink, 0, len(verified.DelegationChain))
	for _, link := range verified.DelegationChain {
		chain = append(chain, model.DelegationChainLink{
			AgentID:   strings.TrimSpace(link.AgentID),
			IssuedAt:  strings.TrimSpace(link.IssuedAt),
			ExpiresAt: strings.TrimSpace(link.ExpiresAt),
			JTI:       strings.TrimSpace(link.JTI),
			ParentJTI: strings.TrimSpace(link.ParentJTI),
			IssuedBy:  strings.TrimSpace(link.IssuedBy),
		})
	}
	rootIssuer := ""
	parentIssuer := ""
	if len(chain) > 0 {
		rootIssuer = chain[0].AgentID
		parentIssuer = chain[len(chain)-1].AgentID
	}
	expiresAt := ""
	if !verified.ExpiresAt.IsZero() {
		expiresAt = verified.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	return model.DelegationLineage{
		TokenJTI:       strings.TrimSpace(verified.JTI),
		ParentTokenJTI: strings.TrimSpace(verified.ParentTokenJTI),
		Subject:        strings.TrimSpace(verified.Subject),
		Audience:       strings.TrimSpace(verified.Audience),
		Tenant:         strings.TrimSpace(verified.Tenant),
		RootIssuer:     rootIssuer,
		ParentIssuer:   parentIssuer,
		IssuerChain:    chain,
		ChainDepth:     verified.ChainDepth,
		ExpiresAt:      expiresAt,
		Scope:          append([]string(nil), verified.AllowedActions...),
		AllowedTopics:  append([]string(nil), verified.AllowedTopics...),
		VerifiedAt:     time.Now().UTC().UnixMicro(),
	}
}

func (e *Engine) emitDelegationLineageAudit(ctx context.Context, req *pb.JobRequest, lineage model.DelegationLineage) {
	if e == nil || e.dispatchAuditSink == nil || req == nil {
		return
	}
	if !e.claimDelegationLineageAuditEmission(ctx, strings.TrimSpace(req.GetJobId()), strings.TrimSpace(lineage.TokenJTI)) {
		return
	}
	chain := delegationLineageAuditChain(lineage)
	if chain == "" {
		return
	}
	extra := map[string]string{
		"chain":        chain,
		"chain_length": strconv.Itoa(delegationLineageChainLength(lineage)),
	}
	if topic := strings.TrimSpace(req.GetTopic()); topic != "" {
		extra["topic"] = topic
	}
	e.dispatchAuditSink.Emit(ctx, audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventDelegationLineage,
		Severity:  audit.SeverityInfo,
		TenantID:  delegationAuditTenant(req, lineage),
		AgentID:   delegationAuditAgentID(req, lineage),
		JobID:     strings.TrimSpace(req.GetJobId()),
		Action:    "dispatch",
		Extra:     extra,
	})
}

func (e *Engine) emitDelegationDispatchFailureAudit(ctx context.Context, req *pb.JobRequest, reasonCode string) {
	if e == nil || e.dispatchAuditSink == nil || req == nil {
		return
	}
	extra := map[string]string{}
	if topic := strings.TrimSpace(req.GetTopic()); topic != "" {
		extra["topic"] = topic
	}
	e.dispatchAuditSink.Emit(ctx, audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventDelegationRevokedBeforeDispatch,
		Severity:  audit.SeverityMedium,
		TenantID:  strings.TrimSpace(req.GetTenantId()),
		AgentID:   delegationAuditAgentID(req, model.DelegationLineage{}),
		JobID:     strings.TrimSpace(req.GetJobId()),
		Action:    "dispatch",
		Reason:    strings.TrimSpace(reasonCode),
		Extra:     extra,
	})
}

func (e *Engine) claimDelegationLineageAuditEmission(ctx context.Context, jobID, tokenJTI string) bool {
	jobID = strings.TrimSpace(jobID)
	tokenJTI = strings.TrimSpace(tokenJTI)
	if jobID == "" || tokenJTI == "" || e == nil || e.jobStore == nil {
		return true
	}
	clientProvider, ok := e.jobStore.(interface{ Client() redis.UniversalClient })
	if !ok || clientProvider.Client() == nil {
		return true
	}
	key := delegationLineageAuditKeyPrefix + jobID + ":" + tokenJTI
	okSet, err := clientProvider.Client().SetNX(ctx, key, "1", 30*24*time.Hour).Result()
	if err != nil {
		slog.Warn("delegation lineage audit dedupe failed", "job_id", jobID, "token_jti", tokenJTI, "error", err)
		return true
	}
	return okSet
}

func delegationLineageAuditChain(lineage model.DelegationLineage) string {
	if len(lineage.IssuerChain) == 0 {
		return ""
	}
	chain := make([]string, 0, len(lineage.IssuerChain))
	for _, link := range lineage.IssuerChain {
		if agentID := strings.TrimSpace(link.AgentID); agentID != "" {
			chain = append(chain, agentID)
		}
	}
	return strings.Join(chain, ">")
}

func delegationLineageChainLength(lineage model.DelegationLineage) int {
	if len(lineage.IssuerChain) == 0 {
		return 0
	}
	count := 0
	for _, link := range lineage.IssuerChain {
		if strings.TrimSpace(link.AgentID) != "" {
			count++
		}
	}
	return count
}

func delegationAuditAgentID(req *pb.JobRequest, lineage model.DelegationLineage) string {
	if req != nil && req.GetLabels() != nil {
		if agentID := strings.TrimSpace(req.GetLabels()["agent_id"]); agentID != "" {
			return agentID
		}
	}
	return strings.TrimSpace(lineage.Audience)
}

func delegationAuditTenant(req *pb.JobRequest, lineage model.DelegationLineage) string {
	if req != nil {
		if tenant := strings.TrimSpace(req.GetTenantId()); tenant != "" {
			return tenant
		}
	}
	return strings.TrimSpace(lineage.Tenant)
}

func classifyDelegationDispatchError(err error) (reason, reasonCode string, retry bool) {
	switch delegation.ErrorCode(err) {
	case "revoked":
		return "delegation token revoked before dispatch", delegationRevokedBeforeDispatchReasonCode, false
	case "expired":
		return "delegation token expired before dispatch", delegationExpiredBeforeDispatchReasonCode, false
	case "audience_mismatch":
		return "delegation token audience mismatch before dispatch", delegationAudienceBeforeDispatchReasonCode, false
	case "scope_exceeded":
		return "delegation token scope exceeded before dispatch", delegationScopeBeforeDispatchReasonCode, false
	case "chain_too_deep":
		return "delegation token chain too deep before dispatch", delegationChainBeforeDispatchReasonCode, false
	case "malformed", "bad_signature", "unknown_kid", "not_yet_valid":
		return "delegation token invalid before dispatch", delegationInvalidBeforeDispatchReasonCode, false
	}

	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(lower, "dispatch token missing"):
		return "delegation token missing before dispatch", delegationMissingDispatchTokenReasonCode, false
	case strings.Contains(lower, "dispatch token store unavailable"):
		return "delegation dispatch verification unavailable", delegationDispatchUnavailableReasonCode, true
	case lower != "":
		return strings.TrimSpace(err.Error()), delegationDispatchUnavailableReasonCode, true
	default:
		return "delegation dispatch verification unavailable", delegationDispatchUnavailableReasonCode, true
	}
}
