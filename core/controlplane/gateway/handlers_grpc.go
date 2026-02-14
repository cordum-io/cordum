package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/infra/secrets"
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
			logging.Error("api-gateway", "idempotency lookup failed", "error", err)
		}
	}
	if err := s.enforceJobBackpressure(ctx, orgID, req.GetTeamId()); err != nil {
		var bp jobBackpressureError
		if errors.As(err, &bp) {
			return nil, status.Error(codes.ResourceExhausted, bp.Error())
		}
		logging.Error("api-gateway", "job backpressure check failed", "error", err)
		return nil, status.Error(codes.Unavailable, "job submission unavailable")
	}

	jobID := uuid.NewString()
	traceID := uuid.NewString()
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
				logging.Error("api-gateway", "idempotency lookup failed", "error", err)
			}
			return nil, status.Error(codes.AlreadyExists, "idempotency key already used")
		}
	}

	ctxKey := store.MakeContextKey(jobID)
	ctxPtr := store.PointerForKey(ctxKey)
	jobPriority := parsePriority(req.GetPriority())

	payloadReq := submitJobRequest{
		Prompt:         req.GetPrompt(),
		Topic:          req.GetTopic(),
		AdapterId:      req.GetAdapterId(),
		Priority:       req.GetPriority(),
		TenantId:       orgID, // Use OrgId for TenantId in payloadReq
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
		// SubmitJobRequest does not carry budget limits yet; defaults are applied below.
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
	// For gRPC, validation of basic fields like prompt, topic happens earlier via protobuf definition
	// For complex validation rules, we can still use a simplified applyDefaults and validate for payloadReq.
	payloadReq.applyDefaults(s.tenant)
	// Basic validation, primarily for prompt length and topic prefix
	if err := payloadReq.validate(s.tenant); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	payload := map[string]any{
		"prompt":     payloadReq.Prompt,
		"adapter_id": payloadReq.AdapterId,
		"priority":   payloadReq.Priority,
		"topic":      payloadReq.Topic,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"tenant_id":  orgID, // Use OrgId here
	}
	// Context is not directly passed in SubmitJobRequest, but could be added
	payloadBytes, _ := json.Marshal(payload)
	if s.memStore == nil {
		return nil, status.Error(codes.Unavailable, "memory store unavailable")
	}
	if err := s.memStore.PutContext(ctx, ctxKey, payloadBytes); err != nil {
		logging.Error("api-gateway", "failed to persist job context", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to persist job context")
	}

	// Set initial state
	if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
		logging.Error("api-gateway", "failed to initialize job state", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to initialize job state")
	}
	if err := s.jobStore.SetTopic(ctx, jobID, payloadReq.Topic); err != nil {
		logging.Error("api-gateway", "failed to set job topic", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to initialize job metadata")
	}
	if err := s.jobStore.SetTenant(ctx, jobID, orgID); err != nil {
		logging.Error("api-gateway", "failed to set job tenant", "job_id", jobID, "error", err)
		return nil, status.Error(codes.Unavailable, "failed to initialize job metadata")
	} // Use OrgId here

	secretsPresent := secrets.ContainsSecretRefs(payloadReq.Prompt)
	if secretsPresent {
		payloadReq.RiskTags = appendUniqueTag(payloadReq.RiskTags, "secrets")
		if payloadReq.Labels == nil {
			payloadReq.Labels = map[string]string{}
		}
		payloadReq.Labels["secrets_present"] = "true"
	}

	maxInput := int64(8000)
	maxOutput := int64(1024)
	memoryID := payloadReq.MemoryId
	if memoryID == "" {
		memoryID = deriveMemoryIDFromReq(payloadReq.Topic, "", jobID)
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
			logging.Error("api-gateway", "failed to persist job metadata", "job_id", jobID, "error", err)
			return nil, status.Error(codes.Unavailable, "failed to persist job metadata")
		}
		if err := s.jobStore.SetJobRequest(ctx, jobReq); err != nil {
			logging.Error("api-gateway", "failed to persist job request", "job_id", jobID, "error", err)
			return nil, status.Error(codes.Unavailable, "failed to persist job metadata")
		}
		if err := s.jobStore.AddJobToTrace(ctx, traceID, jobID); err != nil {
			logging.Error("api-gateway", "failed to add job to trace", "job_id", jobID, "trace_id", traceID, "error", err)
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
		logging.Error("api-gateway", "job publish failed", "job_id", jobID, "error", err)
		return nil, status.Errorf(codes.Unavailable, "failed to enqueue job")
	}

	logging.Info("api-gateway", "job submitted", "job_id", jobID)
	return &pb.SubmitJobResponse{JobId: jobID, TraceId: traceID}, nil
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
	}
	if requested != "" {
		return requested, nil
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
