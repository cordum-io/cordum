package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/logging"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	sagaStackKeyFmt   = "saga:%s:stack"
	sagaLockKeyFmt    = "saga:%s:lock"
	sagaSenderID      = "cordum-scheduler-saga"
	sagaCompLabel     = "saga_compensation"
	sagaWorkflowLabel = "saga_workflow_id"
)

// SagaManager records and replays compensation jobs for durable rollback.
type SagaManager struct {
	bus     Bus
	redis   redis.UniversalClient
	lockTTL time.Duration
	metrics SagaMetrics
	safety  SafetyChecker
}

func NewSagaManager(bus Bus, rdb redis.UniversalClient) *SagaManager {
	return &SagaManager{
		bus:     bus,
		redis:   rdb,
		lockTTL: 2 * time.Minute,
	}
}

// WithMetrics attaches optional saga metrics.
func (s *SagaManager) WithMetrics(metrics SagaMetrics) *SagaManager {
	if s == nil {
		return s
	}
	s.metrics = metrics
	return s
}

// WithSafety attaches an optional safety checker for compensation dispatch.
func (s *SagaManager) WithSafety(sc SafetyChecker) *SagaManager {
	if s == nil {
		return s
	}
	s.safety = sc
	return s
}

func sagaStackKey(workflowID string) string {
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return ""
	}
	return fmt.Sprintf(sagaStackKeyFmt, workflowID)
}

func sagaLockKey(workflowID string) string {
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return ""
	}
	return fmt.Sprintf(sagaLockKeyFmt, workflowID)
}

func isCompensationJob(req *pb.JobRequest) bool {
	if req == nil {
		return false
	}
	if req.Labels != nil {
		if raw := strings.TrimSpace(req.Labels[sagaCompLabel]); raw != "" && raw != "false" {
			return true
		}
	}
	if req.Env != nil {
		if raw := strings.TrimSpace(req.Env[sagaCompLabel]); raw != "" && raw != "false" {
			return true
		}
	}
	return false
}

// RecordCompensation stores the compensation job template when a step succeeds.
func (s *SagaManager) RecordCompensation(ctx context.Context, req *pb.JobRequest) error {
	if s == nil || s.redis == nil || req == nil {
		return nil
	}
	if isCompensationJob(req) || req.Compensation == nil {
		return nil
	}
	workflowID := strings.TrimSpace(req.WorkflowId)
	if workflowID == "" && req.Labels != nil {
		workflowID = strings.TrimSpace(req.Labels["workflow_id"])
	}
	key := sagaStackKey(workflowID)
	if key == "" {
		return nil
	}

	compReq, err := buildCompensationRequest(req)
	if err != nil {
		return err
	}
	if compReq == nil {
		return nil
	}
	data, err := proto.Marshal(compReq)
	if err != nil {
		return fmt.Errorf("marshal compensation request: %w", err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, storeOpTimeout)
	defer cancel()
	if err := s.redis.LPush(cctx, key, data).Err(); err != nil {
		return err
	}
	if s.metrics != nil {
		s.metrics.IncSagaRecorded()
	}
	return nil
}

// Rollback pops compensation requests and dispatches them in reverse order.
func (s *SagaManager) Rollback(ctx context.Context, workflowID string) error {
	if s == nil || s.redis == nil || s.bus == nil {
		return nil
	}
	key := sagaStackKey(workflowID)
	if key == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	started := time.Now()
	if s.metrics != nil {
		s.metrics.IncSagaRollbackTriggered()
		s.metrics.IncSagaActive()
		defer s.metrics.DecSagaActive()
		defer func() {
			s.metrics.ObserveSagaRollback(time.Since(started).Seconds())
		}()
	}

	lockKey := sagaLockKey(workflowID)
	if lockKey != "" && s.lockTTL > 0 {
		lockCtx, cancel := context.WithTimeout(ctx, storeOpTimeout)
		ok, err := s.redis.SetNX(lockCtx, lockKey, "1", s.lockTTL).Result()
		cancel()
		if err != nil {
			return fmt.Errorf("saga lock: %w", err)
		}
		if !ok {
			logging.Info("scheduler-saga", "rollback already in progress", "workflow_id", workflowID)
			return nil
		}
		defer func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), storeOpTimeout)
			_, _ = s.redis.Del(unlockCtx, lockKey).Result()
			cancel()
		}()
	}

	for {
		popCtx, cancel := context.WithTimeout(ctx, storeOpTimeout)
		data, err := s.redis.LPop(popCtx, key).Bytes()
		cancel()
		if err == redis.Nil {
			break
		}
		if err != nil {
			return fmt.Errorf("pop compensation: %w", err)
		}

		var req pb.JobRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			rawHex := hex.EncodeToString(data)
			if len(rawHex) > 128 {
				rawHex = rawHex[:128] + "..."
			}
			logging.Error("scheduler-saga", "unmarshal compensation failed",
				"workflow_id", workflowID, "error", err,
				"raw_bytes_len", len(data), "raw_hex", rawHex)
			if s.metrics != nil {
				s.metrics.IncSagaUnmarshalError()
			}
			s.sendBrokenCompensationToDLQ(workflowID, data, err)
			continue
		}

		if err := s.dispatchCompensation(&req, workflowID); err != nil {
			return err
		}
	}

	return nil
}

// sendBrokenCompensationToDLQ publishes a DLQ entry for data that could not
// be unmarshalled as a compensation request. Best-effort: publish failures are
// logged but not propagated.
func (s *SagaManager) sendBrokenCompensationToDLQ(workflowID string, raw []byte, unmarshalErr error) {
	if s == nil || s.bus == nil {
		return
	}
	packet := &pb.BusPacket{
		TraceId:         "saga-unmarshal-" + uuid.NewString(),
		SenderId:        sagaSenderID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{
				Status:       pb.JobStatus_JOB_STATUS_FAILED_FATAL,
				ErrorCode:    "saga_unmarshal_failed",
				ErrorMessage: fmt.Sprintf("workflow %s: unmarshal compensation: %v (raw_len=%d)", workflowID, unmarshalErr, len(raw)),
			},
		},
	}
	if err := s.bus.Publish(capsdk.SubjectDLQ, packet); err != nil {
		logging.Error("scheduler-saga", "failed to send broken compensation to DLQ",
			"workflow_id", workflowID, "error", err)
	}
}

func (s *SagaManager) dispatchCompensation(req *pb.JobRequest, workflowID string) error {
	if req == nil || s == nil || s.bus == nil {
		return nil
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		return fmt.Errorf("compensation topic required")
	}
	if !strings.HasPrefix(topic, "job.") {
		req.Topic = "job." + topic
	}

	req.JobId = "comp-" + uuid.NewString()
	req.Priority = pb.JobPriority_JOB_PRIORITY_CRITICAL
	if req.Labels == nil {
		req.Labels = map[string]string{}
	}
	req.Labels[sagaCompLabel] = "true"
	req.Labels["is_compensation"] = "true"
	if workflowID != "" {
		req.Labels[sagaWorkflowLabel] = workflowID
	}
	if req.Env == nil {
		req.Env = map[string]string{}
	}
	req.Env[sagaCompLabel] = "true"
	req.Env["is_compensation"] = "true"
	if workflowID != "" {
		req.Env[sagaWorkflowLabel] = workflowID
	}

	// Soft safety check: deny → skip compensation, unavailable → proceed.
	if s.safety != nil {
		record, err := s.safety.Check(req)
		if err != nil {
			logging.Warn("scheduler-saga", "safety check error for compensation, proceeding",
				"job_id", req.JobId, "workflow_id", workflowID, "error", err)
		} else if record.Decision == SafetyDeny {
			logging.Warn("scheduler-saga", "compensation denied by safety, skipping",
				"job_id", req.JobId, "workflow_id", workflowID,
				"reason", record.Reason, "rule_id", record.RuleID)
			return nil
		} else if record.Decision == SafetyUnavailable {
			logging.Warn("scheduler-saga", "safety unavailable for compensation, proceeding",
				"job_id", req.JobId, "workflow_id", workflowID)
		}
	}

	packet := &pb.BusPacket{
		TraceId:         req.JobId,
		SenderId:        sagaSenderID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: req,
		},
	}
	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		if s.metrics != nil {
			s.metrics.IncSagaCompensationFailed()
		}
		return fmt.Errorf("publish compensation: %w", err)
	}
	if s.metrics != nil {
		s.metrics.IncSagaCompensationDispatched()
	}
	return nil
}

func buildCompensationRequest(base *pb.JobRequest) (*pb.JobRequest, error) {
	if base == nil || base.Compensation == nil {
		return nil, nil
	}
	comp := base.Compensation
	topic := strings.TrimSpace(comp.Topic)
	if topic == "" {
		return nil, fmt.Errorf("compensation topic required")
	}

	req := proto.Clone(base).(*pb.JobRequest)
	req.Compensation = nil
	req.JobId = ""
	req.ParentJobId = base.JobId
	req.WorkflowId = base.WorkflowId

	if comp.Topic != "" {
		req.Topic = comp.Topic
	}
	if comp.ContextPtr != "" {
		req.ContextPtr = comp.ContextPtr
	}
	if comp.Priority != pb.JobPriority_JOB_PRIORITY_UNSPECIFIED {
		req.Priority = comp.Priority
	}
	if comp.AdapterId != "" {
		req.AdapterId = comp.AdapterId
	}
	if comp.MemoryId != "" {
		req.MemoryId = comp.MemoryId
	}
	if comp.ContextHints != nil {
		req.ContextHints = comp.ContextHints
	}
	if comp.Budget != nil {
		req.Budget = comp.Budget
	}
	if comp.TenantId != "" {
		req.TenantId = comp.TenantId
	}
	if comp.PrincipalId != "" {
		req.PrincipalId = comp.PrincipalId
	}
	if comp.Meta != nil {
		req.Meta = mergeJobMetadata(req.Meta, comp.Meta)
	}

	if len(comp.Env) > 0 {
		req.Env = mergeStringMap(req.Env, comp.Env)
	}
	if len(comp.Labels) > 0 {
		req.Labels = mergeStringMap(req.Labels, comp.Labels)
	}

	explicitIdem := comp.Meta != nil && strings.TrimSpace(comp.Meta.IdempotencyKey) != ""
	if !explicitIdem {
		if req.Meta == nil {
			req.Meta = &pb.JobMetadata{}
		}
		if key := compensationIdempotencyKey(base, comp); key != "" {
			req.Meta.IdempotencyKey = key
		}
	}

	req.Priority = pb.JobPriority_JOB_PRIORITY_CRITICAL
	return req, nil
}

func compensationIdempotencyKey(base *pb.JobRequest, comp *pb.Compensation) string {
	if base == nil || comp == nil {
		return ""
	}
	workflowID := strings.TrimSpace(base.WorkflowId)
	jobID := strings.TrimSpace(base.JobId)
	topic := strings.TrimSpace(comp.Topic)
	capability := ""
	if comp.Meta != nil {
		capability = strings.TrimSpace(comp.Meta.Capability)
	}
	if capability == "" && base.Meta != nil {
		capability = strings.TrimSpace(base.Meta.Capability)
	}
	step := ""
	if base.StepIndex != 0 {
		step = fmt.Sprintf("%d", base.StepIndex)
	}
	seed := strings.Trim(strings.Join([]string{workflowID, jobID, topic, capability, step}, "|"), "|")
	if seed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(seed))
	return "saga:" + hex.EncodeToString(sum[:16])
}

func mergeStringMap(base, override map[string]string) map[string]string {
	if base == nil && override == nil {
		return nil
	}
	var out map[string]string
	if base != nil {
		out = make(map[string]string, len(base)+len(override))
		for k, v := range base {
			out[k] = v
		}
	}
	if override != nil {
		if out == nil {
			out = make(map[string]string, len(override))
		}
		for k, v := range override {
			out[k] = v
		}
	}
	return out
}

func mergeJobMetadata(base, override *pb.JobMetadata) *pb.JobMetadata {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return proto.Clone(override).(*pb.JobMetadata)
	}
	if override == nil {
		return base
	}
	out := proto.Clone(base).(*pb.JobMetadata)
	if override.TenantId != "" {
		out.TenantId = override.TenantId
	}
	if override.ActorId != "" {
		out.ActorId = override.ActorId
	}
	if override.ActorType != pb.ActorType_ACTOR_TYPE_UNSPECIFIED {
		out.ActorType = override.ActorType
	}
	if override.IdempotencyKey != "" {
		out.IdempotencyKey = override.IdempotencyKey
	}
	if override.Capability != "" {
		out.Capability = override.Capability
	}
	if len(override.RiskTags) > 0 {
		out.RiskTags = append([]string{}, override.RiskTags...)
	}
	if len(override.Requires) > 0 {
		out.Requires = append([]string{}, override.Requires...)
	}
	if override.PackId != "" {
		out.PackId = override.PackId
	}
	if len(override.Labels) > 0 {
		out.Labels = mergeStringMap(out.Labels, override.Labels)
	}
	return out
}
