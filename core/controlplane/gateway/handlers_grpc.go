package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/secrets"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// --- gRPC Implementations ---

func (s *server) SubmitJob(ctx context.Context, req *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	// The incoming gRPC request (req) directly contains the new identity fields.
	// We'll use them to populate the pb.JobRequest.

	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}

	// RBAC: only admin and user roles may submit jobs.
	// This matches the HTTP handler (handleSubmitJobHTTP) which also allows
	// "admin" and "user" roles. Keep both in sync to avoid role asymmetry.
	if err := s.requireRoleGRPC(ctx, "admin", "user"); err != nil {
		actorID, role := "anonymous", "none"
		if ac := authFromContext(ctx); ac != nil {
			actorID, role = ac.PrincipalID, ac.Role
		}
		s.appendAuditEntryNamed(ctx, "submit_denied", "job", "", "", actorID, role, "job submit denied: "+err.Error())
		return nil, err
	}

	orgID, err := resolveGRPCTenant(ctx, req.GetOrgId(), s.tenant)
	if err != nil {
		return nil, err
	}
	principalID := req.GetPrincipalId()
	if auth := authFromContext(ctx); auth != nil && auth.PrincipalID != "" {
		principalID = auth.PrincipalID
	}

	key := strings.TrimSpace(req.GetIdempotencyKey())
	if key != "" && s.jobStore != nil {
		existingID, err := s.jobStore.GetJobByIdempotencyKeyScoped(ctx, orgID, key)
		if err == nil && existingID != "" {
			traceID, _ := s.jobStore.GetTraceID(ctx, existingID)
			return &pb.SubmitJobResponse{JobId: existingID, TraceId: traceID}, nil
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			slog.Error("idempotency lookup failed", "error", err)
		}
	}
	if err := s.enforceJobBackpressure(ctx, orgID, req.GetTeamId()); err != nil {
		var bp jobBackpressureError
		if errors.As(err, &bp) {
			return nil, status.Error(codes.ResourceExhausted, bp.Error())
		}
		slog.Error("job backpressure check failed", "error", err)
		return nil, status.Error(codes.Unavailable, "job submission unavailable")
	}

	jobID := uuid.NewString()
	traceID := uuid.NewString()

	// --- Build payloadReq, validate, and detect secrets (before policy check) ---
	payloadReq := submitJobRequest{
		Prompt:         req.GetPrompt(),
		Topic:          req.GetTopic(),
		AdapterId:      req.GetAdapterId(),
		Priority:       req.GetPriority(),
		TenantId:       orgID,
		PrincipalId:    principalID,
		OrgId:          orgID,
		ActorId:        req.GetActorId(),
		ActorType:      req.GetActorType(),
		IdempotencyKey: req.GetIdempotencyKey(),
		PackId:         req.GetPackId(),
		Capability:     req.GetCapability(),
		RiskTags:       req.GetRiskTags(),
		Requires:       req.GetRequires(),
		Labels:         req.GetLabels(),
		MemoryId:       req.GetMemoryId(),
	}
	rawMemoryID := strings.TrimSpace(req.GetMemoryId())
	explicitMemoryID := store.NormalizeMemoryID(rawMemoryID)
	if rawMemoryID != "" && explicitMemoryID == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid memory id")
	}
	if explicitMemoryID != "" {
		if err := s.enforceMemoryID(ctx, orgID, req.GetTeamId(), "", "", explicitMemoryID); err != nil {
			var perr memoryPolicyError
			if errors.As(err, &perr) {
				switch perr.status {
				case http.StatusForbidden:
					return nil, status.Error(codes.PermissionDenied, perr.msg)
				case http.StatusServiceUnavailable:
					return nil, status.Error(codes.Unavailable, perr.msg)
				default:
					return nil, status.Error(codes.InvalidArgument, perr.msg)
				}
			}
			return nil, status.Error(codes.Internal, "memory policy check failed")
		}
	}
	payloadReq.MemoryId = explicitMemoryID
	payloadReq.applyDefaults(s.tenant)
	if err := payloadReq.validate(s.tenant, s.promptCharLimit()); err != nil {
		var limitErr *licensing.TierLimitError
		if errors.As(err, &limitErr) {
			return nil, status.Error(codes.PermissionDenied, limitErr.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	secretsPresent := secrets.ContainsSecretRefs(payloadReq.Prompt)
	if secretsPresent {
		payloadReq.RiskTags = appendUniqueTag(payloadReq.RiskTags, "secrets")
		if payloadReq.Labels == nil {
			payloadReq.Labels = map[string]string{}
		}
		payloadReq.Labels["secrets_present"] = "true"
	}

	memoryID := payloadReq.MemoryId
	if memoryID == "" {
		memoryID = deriveMemoryIDFromReq(payloadReq.Topic, "", jobID)
	}

	// --- Build metadata (needed for policy check) ---
	actorID := strings.TrimSpace(payloadReq.ActorId)
	if actorID == "" {
		actorID = principalID
	}
	meta := &pb.JobMetadata{
		TenantId:       orgID,
		ActorId:        actorID,
		ActorType:      parseActorType(payloadReq.ActorType),
		IdempotencyKey: strings.TrimSpace(payloadReq.IdempotencyKey),
		Capability:     strings.TrimSpace(payloadReq.Capability),
		RiskTags:       append([]string{}, payloadReq.RiskTags...),
		Requires:       append([]string{}, payloadReq.Requires...),
		PackId:         strings.TrimSpace(payloadReq.PackId),
	}
	if len(payloadReq.Labels) > 0 {
		meta.Labels = payloadReq.Labels
	}

	maxInput := int64(8000)
	maxOutput := int64(1024)

	// --- Submit-time policy check (before any state persistence) ---
	policyResult := s.evaluateSubmitPolicy(ctx, jobID, payloadReq.Topic, orgID, principalID, payloadReq.Priority, meta, payloadReq.Labels, &pb.Budget{
		MaxInputTokens:  maxInput,
		MaxOutputTokens: maxOutput,
	}, memoryID)
	if policyResult.Denied {
		reason := "policy denied"
		if policyResult.Reason != "" {
			reason = policyResult.Reason
		}
		actorForAudit, roleForAudit := "anonymous", "none"
		if ac := authFromContext(ctx); ac != nil {
			actorForAudit, roleForAudit = ac.PrincipalID, ac.Role
		}
		s.appendAuditEntryNamed(ctx, "submit_denied", "job", jobID, payloadReq.Topic, actorForAudit, roleForAudit, "submit-time policy denied: "+reason)
		return nil, status.Error(codes.PermissionDenied, reason)
	}
	if policyResult.Throttled {
		reason := "policy throttled"
		if policyResult.Reason != "" {
			reason = policyResult.Reason
		}
		return nil, status.Error(codes.ResourceExhausted, reason)
	}
	if policyResult.ApprovalRequired {
		slog.Info("submit-time policy requires approval",
			"job_id", jobID, "topic", payloadReq.Topic, "reason", policyResult.Reason)
		actorForAudit, roleForAudit := "anonymous", "none"
		if ac := authFromContext(ctx); ac != nil {
			actorForAudit, roleForAudit = ac.PrincipalID, ac.Role
		}
		s.appendAuditEntryNamed(ctx, "submit_approval_required", "job", jobID, payloadReq.Topic, actorForAudit, roleForAudit, "submit-time policy requires approval: "+policyResult.Reason)

		// Reserve idempotency key to prevent duplicate approval jobs.
		if key != "" && s.jobStore != nil {
			reserved, existingID, err := s.jobStore.TrySetIdempotencyKeyScoped(ctx, orgID, key, jobID)
			if err != nil {
				return nil, status.Error(codes.Internal, "idempotency reservation failed")
			}
			if !reserved {
				if existingID == "" {
					existingID, _ = s.jobStore.GetJobByIdempotencyKeyScoped(ctx, orgID, key)
				}
				if existingID != "" {
					return &pb.SubmitJobResponse{JobId: existingID, TraceId: traceID}, nil
				}
				return nil, status.Error(codes.AlreadyExists, "idempotency key already used")
			}
		}

		// Build and persist the full job context + request so the approval
		// endpoint can retrieve, hash-validate, and publish them later.
		ctxKey := store.MakeContextKey(jobID)
		ctxPtr := store.PointerForKey(ctxKey)
		jobPriority := parsePriority(req.GetPriority())
		envVars := map[string]string{
			"tenant_id":         orgID,
			"memory_id":         memoryID,
			"context_mode":      "",
			"max_input_tokens":  fmt.Sprintf("%d", maxInput),
			"max_output_tokens": fmt.Sprintf("%d", maxOutput),
		}
		if team := req.GetTeamId(); team != "" {
			envVars["team_id"] = team
		}
		if project := req.GetProjectId(); project != "" {
			envVars["project_id"] = project
		}
		payload := map[string]any{
			"prompt": payloadReq.Prompt, "adapter_id": payloadReq.AdapterId,
			"priority": payloadReq.Priority, "topic": payloadReq.Topic,
			"created_at": time.Now().UTC().Format(time.RFC3339), "tenant_id": orgID,
		}
		payloadBytes, _ := json.Marshal(payload)
		if s.memStore != nil {
			if err := s.memStore.PutContext(ctx, ctxKey, payloadBytes); err != nil {
				slog.Error("failed to persist approval job context", "job_id", jobID, "error", err)
			}
		}

		jobReq := &pb.JobRequest{
			JobId: jobID, Topic: payloadReq.Topic, Priority: jobPriority,
			ContextPtr: ctxPtr, AdapterId: payloadReq.AdapterId, Env: envVars,
			MemoryId: memoryID, TenantId: orgID, PrincipalId: principalID,
			Labels: payloadReq.Labels, Meta: meta,
			ContextHints: &pb.ContextHints{
				MaxInputTokens: int32(maxInput), AllowSummarization: false,
				AllowRetrieval: false,
			},
			Budget: &pb.Budget{
				MaxInputTokens: maxInput, MaxOutputTokens: maxOutput,
			},
		}

		if s.jobStore != nil {
			if err := s.jobStore.SetState(ctx, jobID, model.JobStateApproval); err != nil {
				slog.Error("failed to set approval state", "job_id", jobID, "error", err)
				return nil, status.Error(codes.Unavailable, "failed to initialize job state")
			}
			if err := s.jobStore.SetTopic(ctx, jobID, payloadReq.Topic); err != nil {
				slog.Error("failed to set job topic", "job_id", jobID, "error", err)
			}
			if err := s.jobStore.SetTenant(ctx, jobID, orgID); err != nil {
				slog.Error("failed to set job tenant", "job_id", jobID, "error", err)
			}
			if err := s.jobStore.AddJobToTrace(ctx, traceID, jobID); err != nil {
				slog.Error("failed to add approval job to trace", "job_id", jobID, "error", err)
			}
			if err := s.jobStore.SetJobMeta(ctx, jobReq); err != nil {
				slog.Error("failed to persist approval job metadata", "job_id", jobID, "error", err)
			}
			if err := s.jobStore.SetJobRequest(ctx, jobReq); err != nil {
				slog.Error("failed to persist approval job request", "job_id", jobID, "error", err)
			}

			jobHash, _ := scheduler.HashJobRequest(jobReq)
			safetyRecord := model.SafetyDecisionRecord{
				Decision:         model.SafetyRequireApproval,
				Reason:           policyResult.Reason,
				RuleID:           policyResult.RuleId,
				PolicySnapshot:   policyResult.PolicySnapshot,
				Constraints:      policyResult.Constraints,
				ApprovalRequired: true,
				ApprovalRef:      jobID,
				JobHash:          jobHash,
				Remediations:     policyResult.Remediations,
				CheckedAt:        time.Now().UnixMicro(),
			}
			if err := s.jobStore.SetSafetyDecision(ctx, jobID, safetyRecord); err != nil {
				slog.Error("failed to persist safety decision for approval", "job_id", jobID, "error", err)
			}
			s.syncApprovalQueueDepth(ctx)
		}
		return &pb.SubmitJobResponse{
			JobId:   jobID,
			TraceId: traceID,
			Status:  "approval_required",
			Reason:  policyResult.Reason,
		}, nil
	}

	// --- Idempotency reservation (after policy check to avoid orphaned keys) ---
	if key != "" && s.jobStore != nil {
		reserved, existingID, err := s.jobStore.TrySetIdempotencyKeyScoped(ctx, orgID, key, jobID)
		if err != nil {
			return nil, status.Error(codes.Internal, "idempotency reservation failed")
		}
		if !reserved {
			if existingID == "" {
				existingID, err = s.jobStore.GetJobByIdempotencyKeyScoped(ctx, orgID, key)
			}
			if err == nil && existingID != "" {
				traceID, _ := s.jobStore.GetTraceID(ctx, existingID)
				return &pb.SubmitJobResponse{JobId: existingID, TraceId: traceID}, nil
			}
			if err != nil && !errors.Is(err, redis.Nil) {
				slog.Error("idempotency lookup failed", "error", err)
			}
			return nil, status.Error(codes.AlreadyExists, "idempotency key already used")
		}
	}

	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)
	jobPriority := parsePriority(req.GetPriority())

	payload := map[string]any{
		"prompt":     payloadReq.Prompt,
		"adapter_id": payloadReq.AdapterId,
		"priority":   payloadReq.Priority,
		"topic":      payloadReq.Topic,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"tenant_id":  orgID,
	}
	payloadBytes, _ := json.Marshal(payload)
	if s.memStore == nil {
		return nil, status.Error(codes.Unavailable, "memory store unavailable")
	}
	if err := s.memStore.PutContext(ctx, ctxKey, payloadBytes); err != nil {
		slog.Error("failed to persist job context", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to persist job context")
	}

	// Set initial state
	if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
		slog.Error("failed to initialize job state", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to initialize job state")
	}
	if err := s.jobStore.SetTopic(ctx, jobID, payloadReq.Topic); err != nil {
		slog.Error("failed to set job topic", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to initialize job metadata")
	}
	if err := s.jobStore.SetTenant(ctx, jobID, orgID); err != nil {
		slog.Error("failed to set job tenant", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to initialize job metadata")
	}

	envVars := map[string]string{
		"tenant_id":         orgID,
		"memory_id":         memoryID,
		"context_mode":      "",
		"max_input_tokens":  fmt.Sprintf("%d", maxInput),
		"max_output_tokens": fmt.Sprintf("%d", maxOutput),
	}
	if team := req.GetTeamId(); team != "" {
		envVars["team_id"] = team
	}
	if project := req.GetProjectId(); project != "" {
		envVars["project_id"] = project
	}
	if mode := parseContextMode(payloadReq.Topic, ""); mode != "" {
		envVars["context_mode"] = mode
	}

	jobReq := &pb.JobRequest{
		JobId:       jobID,
		Topic:       payloadReq.Topic,
		Priority:    jobPriority,
		ContextPtr:  ctxPtr,
		AdapterId:   payloadReq.AdapterId,
		Env:         envVars,
		MemoryId:    memoryID,
		TenantId:    orgID,       // Use OrgId here
		PrincipalId: principalID, // Populated from new field
		Labels:      payloadReq.Labels,
		Meta:        meta,
		ContextHints: &pb.ContextHints{
			MaxInputTokens:     int32(maxInput),
			AllowSummarization: false,
			AllowRetrieval:     false,
			Tags:               nil,
		},
		Budget: &pb.Budget{
			MaxInputTokens:  maxInput,
			MaxOutputTokens: maxOutput,
			MaxTotalTokens:  0,
			DeadlineMs:      0,
		},
	}

	if s.jobStore != nil {
		if err := s.jobStore.SetJobMeta(ctx, jobReq); err != nil {
			slog.Error("failed to persist job metadata", "job_id", jobID, "error", err)
			return nil, status.Error(codes.Unavailable, "failed to persist job metadata")
		}
		if err := s.jobStore.SetJobRequest(ctx, jobReq); err != nil {
			slog.Error("failed to persist job request", "job_id", jobID, "error", err)
			return nil, status.Error(codes.Unavailable, "failed to persist job metadata")
		}
		if err := s.jobStore.AddJobToTrace(ctx, traceID, jobID); err != nil {
			slog.Error("failed to add job to trace", "job_id", jobID, "trace_id", traceID, "error", err)
			return nil, status.Error(codes.Unavailable, "failed to persist trace metadata")
		}
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}

	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		_ = s.jobStore.SetState(ctx, jobID, model.JobStateFailed)
		slog.Error("job publish failed", "job_id", jobID, "error", err)
		return nil, status.Errorf(codes.Unavailable, "failed to enqueue job")
	}

	slog.Info("job submitted",
		"jobId", jobID,
		"traceId", traceID,
		"topic", payloadReq.Topic,
	)
	return &pb.SubmitJobResponse{JobId: jobID, TraceId: traceID}, nil
}

// requireRoleGRPC checks that the gRPC context carries an AuthContext with
// one of the allowed roles.  It mirrors the HTTP requireRole helper but
// returns proper gRPC status errors.  When no auth provider is configured
// (s.auth == nil) the check is skipped — matching the HTTP requireRole
// behaviour used in tests.
func (s *server) requireRoleGRPC(ctx context.Context, roles ...string) error {
	if s == nil || s.auth == nil {
		return nil
	}
	authCtx := authFromContext(ctx)
	if authCtx == nil {
		return status.Error(codes.Unauthenticated, "authentication required")
	}
	role := normalizeRole(authCtx.Role)
	if role == "" {
		return status.Error(codes.PermissionDenied, "role required")
	}
	for _, candidate := range roles {
		if normalizeRole(candidate) == role {
			return nil
		}
	}
	slog.Warn("gRPC permission denied", "role", role, "required", roles)
	return status.Error(codes.PermissionDenied, "permission denied")
}

func resolveGRPCTenant(ctx context.Context, requested, fallback string) (string, error) {
	requested = strings.TrimSpace(requested)
	if auth := authFromContext(ctx); auth != nil {
		authTenant := strings.TrimSpace(auth.Tenant)
		if authTenant != "" {
			if requested != "" && !auth.AllowCrossTenant && requested != authTenant {
				return "", status.Error(codes.PermissionDenied, "tenant access denied")
			}
			if requested == "" {
				return authTenant, nil
			}
			return requested, nil
		}
		// Unscoped key: reject arbitrary tenant selection to prevent
		// impersonation. Fall through to default tenant.
		if requested != "" && requested != strings.TrimSpace(fallback) {
			slog.Warn("gRPC tenant denied: unscoped credentials", "requested", requested)
			return "", status.Error(codes.PermissionDenied, "unscoped credentials cannot select tenant")
		}
	}
	return strings.TrimSpace(fallback), nil
}

func (s *server) GetJobStatus(ctx context.Context, req *pb.GetJobStatusRequest) (*pb.GetJobStatusResponse, error) {
	if auth := authFromContext(ctx); auth != nil && auth.Tenant != "" && s.jobStore != nil {
		if tenant, _ := s.jobStore.GetTenant(ctx, req.GetJobId()); tenant != "" && tenant != auth.Tenant && !auth.AllowCrossTenant {
			return nil, status.Error(codes.PermissionDenied, "tenant access denied")
		}
	}
	state, err := s.jobStore.GetState(ctx, req.GetJobId())
	if err != nil {
		state = "UNKNOWN"
	}
	resPtr, _ := s.jobStore.GetResultPtr(ctx, req.GetJobId())
	return &pb.GetJobStatusResponse{
		JobId:     req.GetJobId(),
		Status:    string(state),
		ResultPtr: resPtr,
	}, nil
}
