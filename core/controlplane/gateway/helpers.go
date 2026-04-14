package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type submitJobRequest struct {
	Prompt             string            `json:"prompt"`
	Topic              string            `json:"topic"`
	AdapterId          string            `json:"adapter_id"`
	Priority           string            `json:"priority"`
	Context            any               `json:"context"`
	MemoryId           string            `json:"memory_id"`
	Mode               string            `json:"context_mode"`
	TenantId           string            `json:"tenant_id"`
	PrincipalId        string            `json:"principal_id"`
	ActorId            string            `json:"actor_id"`
	ActorType          string            `json:"actor_type"`
	IdempotencyKey     string            `json:"idempotency_key"`
	PackId             string            `json:"pack_id"`
	Capability         string            `json:"capability"`
	RiskTags           []string          `json:"risk_tags"`
	Requires           []string          `json:"requires"`
	OrgId              string            `json:"org_id"`
	TeamId             string            `json:"team_id"`
	ProjectId          string            `json:"project_id"`
	Labels             map[string]string `json:"labels"`
	MaxInputTokens     int32             `json:"max_input_tokens"`
	AllowSummarization bool              `json:"allow_summarization"`
	AllowRetrieval     bool              `json:"allow_retrieval"`
	Tags               []string          `json:"tags"`
	MaxOutputTokens    int64             `json:"max_output_tokens"`
	MaxTotalTokens     int64             `json:"max_total_tokens"`
	DeadlineMs         int64             `json:"deadline_ms"`
}

type policyMetaRequest struct {
	TenantId       string            `json:"tenant_id"`
	ActorId        string            `json:"actor_id"`
	ActorType      string            `json:"actor_type"`
	IdempotencyKey string            `json:"idempotency_key"`
	Capability     string            `json:"capability"`
	RiskTags       []string          `json:"risk_tags"`
	Requires       []string          `json:"requires"`
	PackId         string            `json:"pack_id"`
	Labels         map[string]string `json:"labels"`
}

type policyCheckRequest struct {
	JobId           string             `json:"job_id"`
	Topic           string             `json:"topic"`
	Tenant          string             `json:"tenant"`
	OrgId           string             `json:"org_id"`
	TeamId          string             `json:"team_id"`
	WorkflowId      string             `json:"workflow_id"`
	StepId          string             `json:"step_id"`
	PrincipalId     string             `json:"principal_id"`
	Priority        string             `json:"priority"`
	EstimatedCost   float64            `json:"estimated_cost"`
	Budget          *pb.Budget         `json:"budget"`
	Labels          map[string]string  `json:"labels"`
	MemoryId        string             `json:"memory_id"`
	EffectiveConfig any                `json:"effective_config"`
	Meta            *policyMetaRequest `json:"meta"`
}

func (r *submitJobRequest) applyDefaults(defaultTenant string) {
	if r.MaxInputTokens == 0 {
		r.MaxInputTokens = 8000
	}
	if r.MaxOutputTokens == 0 {
		r.MaxOutputTokens = 1024
	}
	if r.Topic == "" {
		r.Topic = "job.default"
	}
	// Prioritize OrgId, then TenantId, then default
	if r.OrgId == "" {
		if r.TenantId != "" {
			r.OrgId = r.TenantId
		} else {
			r.OrgId = defaultTenant
		}
	}
	r.TenantId = r.OrgId // Ensure TenantId is consistent with OrgId
}

func (r *submitJobRequest) validate(defaultTenant string, promptLimits ...int) error {
	if r == nil {
		return errors.New("request required")
	}
	if len(r.Prompt) == 0 {
		return errors.New("prompt is required")
	}
	promptLimit := maxPromptChars
	for _, limit := range promptLimits {
		if limit > 0 {
			promptLimit = limit
			break
		}
	}
	if len(r.Prompt) > promptLimit {
		return fmt.Errorf(
			"prompt too long (>%d chars): %w",
			promptLimit,
			&licensing.TierLimitError{
				Limit:      "max_prompt_chars",
				Current:    int64(len(r.Prompt)),
				Allowed:    int64(promptLimit),
				UpgradeURL: licensing.DefaultUpgradeURL,
			},
		)
	}
	if r.Topic == "" {
		return errors.New("topic is required")
	}
	// SECURITY: Strict topic validation to prevent injection attacks
	if !validTopicRegex.MatchString(r.Topic) {
		return errors.New("invalid topic format: must match job.name.segments (alphanumeric, dots, hyphens, underscores only)")
	}
	if len(r.Topic) > 256 {
		return errors.New("topic too long (max 256 chars)")
	}
	if r.MaxInputTokens < 0 || r.MaxOutputTokens < 0 || r.MaxTotalTokens < 0 {
		return errors.New("token limits must be non-negative")
	}
	if r.DeadlineMs < 0 {
		return errors.New("deadline_ms must be non-negative")
	}
	if r.ActorType != "" && parseActorType(r.ActorType) == pb.ActorType_ACTOR_TYPE_UNSPECIFIED {
		return errors.New("actor_type must be 'human' or 'service'")
	}
	if len(r.Tags) > 50 {
		return errors.New("too many tags (max 50)")
	}
	if len(r.Labels) > 50 {
		return errors.New("too many labels (max 50)")
	}
	// SECURITY: Validate label key and value lengths to prevent DoS
	for k, v := range r.Labels {
		if len(k) > maxLabelKeyLen {
			return fmt.Errorf("label key too long (max %d chars)", maxLabelKeyLen)
		}
		if len(v) > maxLabelValueLen {
			return fmt.Errorf("label value too long (max %d chars)", maxLabelValueLen)
		}
	}
	if r.OrgId == "" {
		if r.TenantId != "" {
			r.OrgId = r.TenantId
		} else {
			r.OrgId = defaultTenant
		}
	}
	return nil
}

func buildJobMetadata(metaReq *policyMetaRequest, tenant, principal string) *pb.JobMetadata {
	if metaReq == nil && tenant == "" && principal == "" {
		return nil
	}
	meta := &pb.JobMetadata{
		TenantId: tenant,
	}
	if metaReq != nil {
		if metaReq.TenantId != "" {
			meta.TenantId = metaReq.TenantId
		}
		meta.ActorId = strings.TrimSpace(metaReq.ActorId)
		meta.ActorType = parseActorType(metaReq.ActorType)
		meta.IdempotencyKey = strings.TrimSpace(metaReq.IdempotencyKey)
		meta.Capability = strings.TrimSpace(metaReq.Capability)
		meta.RiskTags = append(meta.RiskTags, metaReq.RiskTags...)
		meta.Requires = append(meta.Requires, metaReq.Requires...)
		meta.PackId = strings.TrimSpace(metaReq.PackId)
		if len(metaReq.Labels) > 0 {
			meta.Labels = metaReq.Labels
		}
	}
	if meta.ActorId == "" {
		meta.ActorId = principal
	}
	return meta
}

// submitPolicyDecision describes the outcome of a submit-time policy check.
// It carries enough information for the approval flow to persist a safety
// decision record (snapshot, hash, constraints, remediations) without needing
// to re-evaluate the policy.
type submitPolicyDecision struct {
	Allowed          bool
	Denied           bool
	Throttled        bool
	ApprovalRequired bool
	Reason           string
	Constraints      *pb.PolicyConstraints
	PolicySnapshot   string
	RuleId           string
	Remediations     []*pb.PolicyRemediation
}

// evaluateSubmitPolicy performs a synchronous policy check at job submission
// time before any state is persisted or the job is published. It reuses
// buildPolicyCheckRequest to ensure request shape stays aligned with the rest
// of the gateway. When the safety client is unavailable, behavior is controlled
// by POLICY_CHECK_FAIL_MODE (shared with the scheduler): "open" allows the job,
// "closed" (default) rejects it.
func (s *server) evaluateSubmitPolicy(ctx context.Context, jobID, topic, tenant, principalID, priority string, meta *pb.JobMetadata, labels map[string]string, budget *pb.Budget, memoryID string) submitPolicyDecision {
	if s == nil || s.safetyClient == nil {
		return submitPolicyDecision{Allowed: true}
	}

	// Build the policy check request through the shared builder so HTTP/gRPC
	// submit requests and explicit /policy/check calls use the same shape.
	policyMeta := policyMetaFromJobMetadata(meta)
	checkReq, err := buildPolicyCheckRequest(ctx, &policyCheckRequest{
		JobId:       jobID,
		Topic:       topic,
		Tenant:      tenant,
		OrgId:       tenant,
		PrincipalId: principalID,
		Priority:    priority,
		Labels:      labels,
		MemoryId:    memoryID,
		Budget:      budget,
		Meta:        policyMeta,
	}, s.configSvc, s.tenant)
	if err != nil {
		slog.Error("submit-time policy request build failed", "job_id", jobID, "topic", topic, "error", err)
		return submitPolicyDecision{Denied: true, Reason: "policy request build failed: " + err.Error()}
	}

	evalCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := s.safetyClient.Evaluate(evalCtx, checkReq)
	if err != nil {
		slog.Error("submit-time policy check failed", "job_id", jobID, "topic", topic, "error", err)
		if isPolicyFailOpen() {
			slog.Warn("submit-time policy unavailable — FAIL-OPEN: allowing job",
				"job_id", jobID, "topic", topic)
			return submitPolicyDecision{Allowed: true, Reason: "fail-open: safety unavailable"}
		}
		return submitPolicyDecision{Denied: true, Reason: "policy check unavailable"}
	}

	// Capture full response metadata for the approval flow.
	base := submitPolicyDecision{
		Reason:         resp.GetReason(),
		Constraints:    resp.GetConstraints(),
		PolicySnapshot: resp.GetPolicySnapshot(),
		RuleId:         resp.GetRuleId(),
		Remediations:   resp.GetRemediations(),
	}

	if resp.GetApprovalRequired() || resp.GetDecision() == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
		base.ApprovalRequired = true
		return base
	}

	switch resp.GetDecision() {
	case pb.DecisionType_DECISION_TYPE_ALLOW, pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		base.Allowed = true
		return base
	case pb.DecisionType_DECISION_TYPE_DENY:
		slog.Info("submit-time policy denied", "job_id", jobID, "topic", topic, "reason", resp.GetReason())
		base.Denied = true
		return base
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		slog.Info("submit-time policy throttled", "job_id", jobID, "topic", topic, "reason", resp.GetReason())
		base.Throttled = true
		return base
	default:
		slog.Warn("submit-time policy unknown decision", "job_id", jobID, "decision", resp.GetDecision().String())
		base.Denied = true
		base.Reason = "unknown policy decision"
		return base
	}
}

// policyMetaFromJobMetadata converts pb.JobMetadata to policyMetaRequest
// for the shared policy check request builder.
func policyMetaFromJobMetadata(m *pb.JobMetadata) *policyMetaRequest {
	if m == nil {
		return nil
	}
	return &policyMetaRequest{
		TenantId:       m.GetTenantId(),
		ActorId:        m.GetActorId(),
		ActorType:      m.GetActorType().String(),
		IdempotencyKey: m.GetIdempotencyKey(),
		Capability:     m.GetCapability(),
		RiskTags:       m.GetRiskTags(),
		Requires:       m.GetRequires(),
		PackId:         m.GetPackId(),
		Labels:         m.GetLabels(),
	}
}

// isPolicyFailOpen returns true if POLICY_CHECK_FAIL_MODE is "open".
// Default is "closed" (deny on safety unavailability).
func isPolicyFailOpen() bool {
	mode := strings.TrimSpace(os.Getenv("POLICY_CHECK_FAIL_MODE"))
	return strings.EqualFold(mode, "open")
}

func buildPolicyCheckRequest(ctx context.Context, req *policyCheckRequest, cfgSvc *configsvc.Service, defaultTenant string) (*pb.PolicyCheckRequest, error) {
	if req == nil {
		return nil, errors.New("request required")
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		return nil, errors.New("topic is required")
	}
	tenant := strings.TrimSpace(req.Tenant)
	if tenant == "" {
		tenant = strings.TrimSpace(req.OrgId)
	}
	if tenant == "" {
		tenant = defaultTenant
	}
	meta := buildJobMetadata(req.Meta, tenant, strings.TrimSpace(req.PrincipalId))

	checkReq := &pb.PolicyCheckRequest{
		JobId:         strings.TrimSpace(req.JobId),
		Topic:         topic,
		Tenant:        tenant,
		Priority:      parsePriority(req.Priority),
		EstimatedCost: req.EstimatedCost,
		Budget:        req.Budget,
		PrincipalId:   strings.TrimSpace(req.PrincipalId),
		Labels:        req.Labels,
		MemoryId:      strings.TrimSpace(req.MemoryId),
		Meta:          meta,
	}

	if req.EffectiveConfig != nil {
		if data, err := json.Marshal(req.EffectiveConfig); err == nil {
			checkReq.EffectiveConfig = data
		}
	} else if cfgSvc != nil {
		orgID := req.OrgId
		if orgID == "" {
			orgID = tenant
		}
		if snap, err := cfgSvc.EffectiveSnapshot(ctx, orgID, req.TeamId, req.WorkflowId, req.StepId); err == nil && snap != nil {
			if data, err := json.Marshal(snap); err == nil {
				checkReq.EffectiveConfig = data
			}
		}
	}

	return checkReq, nil
}

func addrFromEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

func intFromEnv(key string, fallback int) int {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

func dialSafetyKernel(addr string) (*grpc.ClientConn, pb.SafetyKernelClient, error) {
	creds, err := safetyTransportCredentials()
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(gatewayGRPCClientKeepaliveParams()),
	)
	if err != nil {
		return nil, nil, err
	}
	return conn, pb.NewSafetyKernelClient(conn), nil
}

func gatewayGRPCClientKeepaliveParams() keepalive.ClientParameters {
	return keepalive.ClientParameters{
		Time:                env.DurationOr("CORDUM_GRPC_CLIENT_KEEPALIVE_TIME", 30*time.Second),
		Timeout:             env.DurationOr("CORDUM_GRPC_CLIENT_KEEPALIVE_TIMEOUT", 10*time.Second),
		PermitWithoutStream: true,
	}
}

func safetyTransportCredentials() (credentials.TransportCredentials, error) {
	caPath := strings.TrimSpace(os.Getenv("SAFETY_KERNEL_TLS_CA"))
	requireTLS := env.IsProduction() || env.Bool("SAFETY_KERNEL_TLS_REQUIRED")
	insecureAllowed := env.Bool("SAFETY_KERNEL_INSECURE")

	if caPath == "" {
		if requireTLS {
			return nil, fmt.Errorf("safety_kernel_tls_ca required")
		}
		if insecureAllowed || !env.IsProduction() {
			return insecure.NewCredentials(), nil
		}
		return nil, fmt.Errorf("safety kernel tls required")
	}

	// #nosec G304,G703 -- CA path is configured by the operator (TLS cert path from env config).
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("safety kernel tls ca read: %w", err)
	}
	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(pem); !ok {
		return nil, fmt.Errorf("safety kernel tls ca parse: %s", caPath)
	}
	cfg := &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
	if env.TLSMinVersion() == tls.VersionTLS13 {
		cfg.MinVersion = tls.VersionTLS13
	}
	return credentials.NewTLS(cfg), nil
}

func parsePriority(priority string) pb.JobPriority {
	switch strings.ToLower(priority) {
	case "batch":
		return pb.JobPriority_JOB_PRIORITY_BATCH
	case "critical":
		return pb.JobPriority_JOB_PRIORITY_CRITICAL
	case "interactive":
		return pb.JobPriority_JOB_PRIORITY_INTERACTIVE
	default:
		return pb.JobPriority_JOB_PRIORITY_INTERACTIVE
	}
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseFloatEnv(key string, defaultVal float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		slog.Warn("invalid float env var, using default", "key", key, "value", raw, "default", defaultVal)
		return defaultVal
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func idempotencyKeyFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	candidates := []string{
		r.Header.Get("Idempotency-Key"),
		r.Header.Get("X-Idempotency-Key"),
		r.URL.Query().Get("idempotency_key"),
		r.URL.Query().Get("idempotency-key"),
	}
	for _, raw := range candidates {
		if val := strings.TrimSpace(raw); val != "" {
			return val
		}
	}
	return ""
}

func tenantFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if tenant := headerValue(r, "X-Tenant-ID"); tenant != "" {
		return tenant
	}
	if websocket.IsWebSocketUpgrade(r) {
		if tenant := strings.TrimSpace(r.URL.Query().Get("tenant_id")); tenant != "" {
			return tenant
		}
		if tenant := strings.TrimSpace(r.URL.Query().Get("tenant")); tenant != "" {
			return tenant
		}
	}
	// Fall back to auth context tenant (e.g. from session token)
	if authCtx := authFromRequest(r); authCtx != nil && authCtx.Tenant != "" {
		return authCtx.Tenant
	}
	return ""
}

func artifactMaxBytes() int64 {
	if raw := strings.TrimSpace(os.Getenv(envArtifactMaxBytes)); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return defaultArtifactMaxBytes
}

func artifactRequestedMaxBytes(r *http.Request) int64 {
	if r == nil {
		return 0
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("max_bytes")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	if raw := strings.TrimSpace(r.Header.Get("X-Max-Artifact-Bytes")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return 0
}

func artifactMaxBytesLimit(r *http.Request) int64 {
	maxBytes := artifactMaxBytes()
	if requested := artifactRequestedMaxBytes(r); requested > 0 && requested < maxBytes {
		return requested
	}
	return maxBytes
}

func (s *server) tenantForBusPacket(ctx context.Context, evt *pb.BusPacket) (string, bool) {
	if evt == nil {
		return "", false
	}
	if req := evt.GetJobRequest(); req != nil {
		if tenant := strings.TrimSpace(req.GetTenantId()); tenant != "" {
			return tenant, true
		}
		if meta := req.GetMeta(); meta != nil {
			if tenant := strings.TrimSpace(meta.GetTenantId()); tenant != "" {
				return tenant, true
			}
		}
	}
	if res := evt.GetJobResult(); res != nil {
		return s.tenantForJobID(ctx, res.GetJobId())
	}
	if prog := evt.GetJobProgress(); prog != nil {
		return s.tenantForJobID(ctx, prog.GetJobId())
	}
	if cancel := evt.GetJobCancel(); cancel != nil {
		return s.tenantForJobID(ctx, cancel.GetJobId())
	}
	return "", false
}

func jobIDForBusPacket(evt *pb.BusPacket) string {
	if evt == nil {
		return ""
	}
	if req := evt.GetJobRequest(); req != nil {
		return strings.TrimSpace(req.GetJobId())
	}
	if res := evt.GetJobResult(); res != nil {
		return strings.TrimSpace(res.GetJobId())
	}
	if prog := evt.GetJobProgress(); prog != nil {
		return strings.TrimSpace(prog.GetJobId())
	}
	if cancel := evt.GetJobCancel(); cancel != nil {
		return strings.TrimSpace(cancel.GetJobId())
	}
	return ""
}

func (s *server) tenantForJobID(ctx context.Context, jobID string) (string, bool) {
	if s == nil || s.jobStore == nil {
		return "", false
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return "", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tenant, err := s.jobStore.GetTenant(ctx, jobID)
	if err != nil {
		return "", false
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "", false
	}
	return tenant, true
}

func (s *server) maxConcurrentRuns(ctx context.Context, orgID, teamID string) int {
	if s.configSvc == nil {
		return 0
	}
	cfg, err := s.configSvc.Effective(ctx, orgID, teamID, "", "")
	if err != nil || cfg == nil {
		return 0
	}
	if limit := lookupIntPath(cfg, "limits", "max_concurrent_runs"); limit > 0 {
		return limit
	}
	if limit := lookupIntPath(cfg, "rate_limits", "concurrent_workflows"); limit > 0 {
		return limit
	}
	return 0
}

type jobBackpressureError struct {
	active int
	limit  int
}

func (e jobBackpressureError) Error() string {
	return fmt.Sprintf("job queue full (active=%d, limit=%d)", e.active, e.limit)
}

// enforceJobBackpressure is the system-capacity gate. It checks active job
// count against the system config limit (rate_limits.concurrent_jobs + queue_size).
// The scheduler separately enforces a per-job policy concurrency limit from the
// safety kernel (PolicyConstraints.Budgets.MaxConcurrentJobs) against the same
// active count — that is the policy-enforcement gate.
func (s *server) enforceJobBackpressure(ctx context.Context, orgID, teamID string) error {
	if s == nil || s.jobStore == nil {
		return nil
	}
	if s.configSvc == nil {
		return nil
	}
	cfg, err := s.configSvc.Effective(ctx, orgID, teamID, "", "")
	if err != nil || cfg == nil {
		return nil
	}
	limit := lookupIntPath(cfg, "limits", "max_concurrent_jobs")
	if limit <= 0 {
		limit = lookupIntPath(cfg, "rate_limits", "concurrent_jobs")
	}
	if limit <= 0 {
		return nil
	}
	queueSize := lookupIntPath(cfg, "rate_limits", "queue_size")
	if queueSize < 0 {
		queueSize = 0
	}
	maxActive := limit + queueSize
	active, err := s.jobStore.CountActiveByTenant(ctx, orgID)
	if err != nil {
		return fmt.Errorf("active job count: %w", err)
	}
	if active >= maxActive {
		return jobBackpressureError{active: active, limit: maxActive}
	}
	return nil
}

func lookupIntPath(data map[string]any, keys ...string) int {
	if data == nil || len(keys) == 0 {
		return 0
	}
	var cur any = data
	for _, key := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = m[key]
	}
	switch v := cur.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return 0
}

func parseActorType(raw string) pb.ActorType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "human":
		return pb.ActorType_ACTOR_TYPE_HUMAN
	case "service":
		return pb.ActorType_ACTOR_TYPE_SERVICE
	default:
		return pb.ActorType_ACTOR_TYPE_UNSPECIFIED
	}
}

func appendUniqueTag(tags []string, tag string) []string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return tags
	}
	for _, existing := range tags {
		if strings.EqualFold(existing, tag) {
			return tags
		}
	}
	return append(tags, tag)
}

func parseContextMode(topic, explicit string) string {
	switch strings.ToLower(explicit) {
	case "chat":
		return "chat"
	case "rag":
		return "rag"
	case "raw":
		return "raw"
	}
	return "raw"
}

type memoryPolicyError struct {
	status int
	msg    string
}

func (e memoryPolicyError) Error() string {
	return e.msg
}

func (s *server) enforceMemoryID(ctx context.Context, orgID, teamID, workflowID, stepID, memoryID string) error {
	memoryID = store.NormalizeMemoryID(memoryID)
	if memoryID == "" {
		return nil
	}
	if s == nil || s.configSvc == nil {
		return memoryPolicyError{status: http.StatusServiceUnavailable, msg: "config service unavailable"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if _, err := s.configSvc.Get(cctx, configsvc.ScopeSystem, "default"); err != nil && !errors.Is(err, redis.Nil) {
		return memoryPolicyError{status: http.StatusServiceUnavailable, msg: "config service unavailable"}
	}
	cfgMap, err := s.configSvc.Effective(cctx, orgID, teamID, workflowID, stepID)
	if err != nil {
		return memoryPolicyError{status: http.StatusServiceUnavailable, msg: "config service unavailable"}
	}
	cfg, ok := config.ParseEffectiveContextMap(cfgMap)
	if !ok {
		return nil
	}
	allowed, reason := config.MemoryIDAllowed(cfg, memoryID)
	if !allowed {
		return memoryPolicyError{status: http.StatusForbidden, msg: reason}
	}
	return nil
}

func deriveMemoryIDFromReq(topic, explicit, jobID string) string {
	if explicit != "" {
		return store.NormalizeMemoryID(explicit)
	}
	return strings.TrimSpace(jobID)
}

func normalizeTimestampMicrosLower(ts int64) int64 {
	if ts <= 0 {
		return ts
	}
	switch {
	case ts < secondsThreshold:
		return ts * microsPerSecond
	case ts < millisThreshold:
		return ts * microsPerMillisecond
	case ts < microsThreshold:
		return ts
	default:
		return ts / microsPerMillisecond
	}
}

func normalizeTimestampMicrosUpper(ts int64) int64 {
	if ts <= 0 {
		return ts
	}
	switch {
	case ts < secondsThreshold:
		return ts*microsPerSecond + (microsPerSecond - 1)
	case ts < millisThreshold:
		return ts*microsPerMillisecond + (microsPerMillisecond - 1)
	case ts < microsThreshold:
		return ts
	default:
		return ts / microsPerMillisecond
	}
}

func normalizeTimestampSecondsUpper(ts int64) int64 {
	if ts <= 0 {
		return ts
	}
	switch {
	case ts < secondsThreshold:
		return ts
	case ts < millisThreshold:
		return ts / 1_000
	case ts < microsThreshold:
		return ts / 1_000_000
	default:
		return ts / 1_000_000_000
	}
}

func parseLockMode(raw string) locks.Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "shared":
		return locks.ModeShared
	default:
		return locks.ModeExclusive
	}
}

func parseRetention(raw string) artifacts.RetentionClass {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short":
		return artifacts.RetentionShort
	case "audit":
		return artifacts.RetentionAudit
	default:
		return artifacts.RetentionStandard
	}
}

// truncateForError truncates s to max characters for safe inclusion in error
// messages. If truncated, the result ends with "..." to indicate truncation.
// This prevents user-supplied input from inflating error message size (BUG-7).
func truncateForError(s string, max int) string {
	if max <= 0 {
		max = 256
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// ---------- JSON helpers (from json_helpers.go) ----------

const (
	defaultMaxJSONBodyBytes    int64 = 2 * 1024 * 1024
	envGatewayMaxJSONBodyBytes       = "GATEWAY_MAX_JSON_BODY_BYTES"
)

var errRequestBodyTooLarge = errors.New("request body too large")

type requestBodyLimitKey struct{}

func maxJSONBodyBytes() int64 {
	if raw := strings.TrimSpace(os.Getenv(envGatewayMaxJSONBodyBytes)); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return defaultMaxJSONBodyBytes
}

// writeJSON encodes v as JSON into w, logging any encoding error.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("json encode failed", "error", err)
	}
}

// writeErrorJSON writes a structured JSON error response with the given HTTP status.
func writeErrorJSON(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{"error": message, "status": status}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Warn("json encode error response failed", "error", err)
	}
}

func writeTierLimitJSON(w http.ResponseWriter, limitErr *licensing.TierLimitError) {
	if limitErr == nil {
		writeErrorJSON(w, http.StatusForbidden, "tier_limit_exceeded")
		return
	}
	payload := limitErr.ToHTTPError()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"error":       payload.Code,
		"code":        payload.Code,
		"status":      http.StatusForbidden,
		"message":     payload.Message,
		"limit":       payload.Limit,
		"current":     payload.Current,
		"allowed":     payload.Allowed,
		"upgrade_url": payload.UpgradeURL,
	}); err != nil {
		slog.Warn("json encode tier limit response failed", "error", err)
	}
}

// writeInternalError logs the real error server-side and returns a generic message to the client.
// Use for ALL 5xx responses to prevent leaking internal details (Redis URLs, config paths, etc.).
func writeInternalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	slog.Error(operation+" failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeErrorJSON(w, http.StatusInternalServerError, "internal error")
}

// writeBadGateway logs an upstream failure server-side and returns a generic message.
func writeBadGateway(w http.ResponseWriter, r *http.Request, operation string, err error) {
	slog.Error(operation+" upstream failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeErrorJSON(w, http.StatusBadGateway, "upstream service error")
}

// writeServiceUnavailable logs a service-unavailable error server-side and returns a generic message.
func writeServiceUnavailable(w http.ResponseWriter, r *http.Request, operation string, err error) {
	slog.Error(operation+" unavailable", "method", r.Method, "path", r.URL.Path, "error", err)
	writeErrorJSON(w, http.StatusServiceUnavailable, "service unavailable")
}

// writeForbidden logs the auth failure server-side and returns a generic message.
// Use for ALL 403 responses to avoid leaking tenant IDs and role requirements.
func writeForbidden(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("access denied", "method", r.Method, "path", r.URL.Path, "error", err)
	writeErrorJSON(w, http.StatusForbidden, "access denied")
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	if r == nil {
		return errors.New("request required")
	}
	limit := maxJSONBodyBytes()
	if requestLimit, ok := requestBodyLimitFromContext(r.Context()); ok && requestLimit > 0 {
		limit = requestLimit
	}
	if limit > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return tierLimitFromMaxBytes(int64(maxErr.Limit))
		}
		return err
	}
	return nil
}

func writeJSONDecodeError(w http.ResponseWriter, err error, invalidMsg string) {
	var limitErr *licensing.TierLimitError
	if errors.As(err, &limitErr) {
		writeTierLimitJSON(w, limitErr)
		return
	}
	if errors.Is(err, errRequestBodyTooLarge) {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeErrorJSON(w, http.StatusBadRequest, invalidMsg)
}

// maxBodyMiddleware enforces a body size limit on all requests that carry a
// body to prevent large-body DoS. Multipart uploads are excluded since those
// routes manage their own limits (e.g. pack install).
func maxBodyMiddleware(next http.Handler, resolvers ...*licensing.EntitlementResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip requests that never carry a body or have no body present.
		if r.Body == nil || r.Body == http.NoBody || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		// Skip multipart uploads — they have per-route limits.
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "multipart/") {
			next.ServeHTTP(w, r)
			return
		}

		limit := entitlementBodyBytesLimit(resolvers...)
		if limit > 0 {
			r = r.WithContext(context.WithValue(r.Context(), requestBodyLimitKey{}, limit))
		}

		// Fast reject if Content-Length is declared and exceeds limit.
		if r.ContentLength > limit {
			writeTierLimitJSON(w, &licensing.TierLimitError{
				Limit:      "max_body_bytes",
				Current:    r.ContentLength,
				Allowed:    limit,
				UpgradeURL: licensing.DefaultUpgradeURL,
			})
			return
		}

		// Wrap body to enforce limit even when Content-Length is absent (chunked).
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}

		next.ServeHTTP(w, r)
	})
}

func requestBodyLimitFromContext(ctx context.Context) (int64, bool) {
	if ctx == nil {
		return 0, false
	}
	limit, ok := ctx.Value(requestBodyLimitKey{}).(int64)
	return limit, ok
}

func tierLimitFromMaxBytes(limit int64) *licensing.TierLimitError {
	if limit <= 0 {
		limit = maxJSONBodyBytes()
	}
	return &licensing.TierLimitError{
		Limit:      "max_body_bytes",
		Current:    limit + 1,
		Allowed:    limit,
		UpgradeURL: licensing.DefaultUpgradeURL,
	}
}

// ---------- List limits (from gateway_limits.go) ----------

const maxListLimit int64 = 500

func clampListLimit(limit int64) int64 {
	if limit <= 0 {
		return limit
	}
	if limit > maxListLimit {
		return maxListLimit
	}
	return limit
}

// parsePagination extracts limit and cursor from query parameters.
// Returns clamped limit and cursor defaulting to now (microseconds).
func parsePagination(r *http.Request, defaultLimit int64) (limit, cursor int64) {
	limit = defaultLimit
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	limit = clampListLimit(limit)
	cursor = time.Now().UnixNano() / int64(time.Microsecond)
	if q := r.URL.Query().Get("cursor"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			cursor = v
		}
	}
	return limit, cursor
}

// ---------- Handler guard helpers ----------

// requireStoreAndRole checks that all provided stores are non-nil and the
// caller has one of the required roles. Returns false (and writes the error
// response) if any check fails; the caller should return immediately.
// Pass no roles to skip the role check (public endpoints).
func (s *server) requireStoreAndRole(w http.ResponseWriter, r *http.Request, roles []string, stores ...any) bool {
	for _, store := range stores {
		if isNilStore(store) {
			writeErrorJSON(w, http.StatusServiceUnavailable, "service unavailable")
			return false
		}
	}
	if len(roles) > 0 {
		if err := s.requireRole(r, roles...); err != nil {
			writeForbidden(w, r, err)
			return false
		}
	}
	return true
}

// isNilStore checks if a value is nil, handling the Go nil-interface trap
// where a typed nil pointer wrapped in any is != nil at the interface level.
func isNilStore(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Ptr && rv.IsNil()
}

// requireJobTenantAccess verifies the caller has access to the tenant that
// owns the given job. Returns false (and writes 403) if denied.
func (s *server) requireJobTenantAccess(w http.ResponseWriter, r *http.Request, jobID string) bool {
	if s.jobStore == nil {
		return true
	}
	if tenant, _ := s.jobStore.GetTenant(r.Context(), jobID); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return false
		}
	}
	return true
}

// requirePathParam extracts a path parameter and writes a 400 error if empty.
// Returns the value and true on success, or "" and false on failure.
func requirePathParam(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	val := r.PathValue(name)
	if val == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing "+name)
		return "", false
	}
	return val, true
}
