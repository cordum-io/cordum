package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/maputil"
	"github.com/cordum/cordum/core/infra/redisutil"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

const (
	outputMetaTimeout         = 100 * time.Millisecond
	outputContentTimeout      = 30 * time.Second
	outputContentMaxBytes     = 2 * 1024 * 1024
	outputContentFetchRetries = 6
	outputContentFetchBackoff = 100 * time.Millisecond
	outputCircuitOpenFor      = 30 * time.Second
	outputCircuitFailBudget   = 3
	outputCircuitHalfOpenMax  = 3
	outputCircuitCloseAfter   = 2
	outputPointerPrefix       = "redis://"
	outputRedactedTTL         = 24 * time.Hour
	outputRedactionMarker     = "[REDACTED]"
)

// OutputSafetyClient implements OutputSafetyChecker over OutputPolicyService gRPC.
type OutputSafetyClient struct {
	client       pb.OutputPolicyServiceClient
	conn         *grpc.ClientConn
	resultClient redis.UniversalClient
	cb           *RedisCircuitBreaker
}

var _ OutputSafetyChecker = (*OutputSafetyClient)(nil)

// NewOutputSafetyClient dials OutputPolicyService at addr.
func NewOutputSafetyClient(addr string) (*OutputSafetyClient, error) {
	return NewOutputSafetyClientWithRedis(addr, strings.TrimSpace(os.Getenv("REDIS_URL")))
}

// NewOutputSafetyClientWithRedis dials OutputPolicyService and configures Redis access for deep content checks.
func NewOutputSafetyClientWithRedis(addr, redisURL string) (*OutputSafetyClient, error) {
	creds, err := safetyTransportCredentials()
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(grpcClientKeepaliveParams()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial output policy service: %w", err)
	}
	var resultClient redis.UniversalClient
	if strings.TrimSpace(redisURL) != "" {
		resultClient, err = redisutil.NewClient(redisURL)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("connect output content store: %w", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = resultClient.Ping(ctx).Err()
		cancel()
		if err != nil {
			_ = resultClient.Close()
			_ = conn.Close()
			return nil, fmt.Errorf("ping output content store: %w", err)
		}
	}
	return &OutputSafetyClient{
		client:       pb.NewOutputPolicyServiceClient(conn),
		conn:         conn,
		resultClient: resultClient,
		cb: NewRedisCircuitBreaker(resultClient, "cordum:cb:safety:output", CircuitBreakerOpts{
			FailThreshold: outputCircuitFailBudget,
			OpenDuration:  outputCircuitOpenFor,
			HalfOpenMax:   outputCircuitHalfOpenMax,
			CloseAfter:    outputCircuitCloseAfter,
		}),
	}, nil
}

// Close releases the underlying gRPC connection.
func (c *OutputSafetyClient) Close() error {
	var closeErr error
	if c != nil && c.resultClient != nil {
		if err := c.resultClient.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	if c != nil && c.conn != nil {
		if err := c.conn.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

// CheckOutputMeta executes a fast metadata-focused output check.
func (c *OutputSafetyClient) CheckOutputMeta(res *pb.JobResult, req *pb.JobRequest) (OutputSafetyRecord, error) {
	evalReq, err := outputEvaluateRequestFromJob(res, req, false)
	if err != nil {
		return OutputSafetyRecord{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), outputMetaTimeout)
	defer cancel()
	return c.EvaluateOutput(ctx, evalReq)
}

// CheckOutputContent executes a deeper output content check.
func (c *OutputSafetyClient) CheckOutputContent(ctx context.Context, res *pb.JobResult, req *pb.JobRequest) (OutputSafetyRecord, error) {
	evalReq, err := outputEvaluateRequestFromJob(res, req, true)
	if err != nil {
		return OutputSafetyRecord{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, outputContentTimeout)
	defer cancel()
	return c.EvaluateOutput(ctx, evalReq)
}

// EvaluateOutput runs output policy checks using output content and original request context.
func (c *OutputSafetyClient) EvaluateOutput(ctx context.Context, req *OutputEvaluateRequest) (OutputSafetyRecord, error) {
	if c == nil || c.client == nil {
		return OutputSafetyRecord{}, fmt.Errorf("output safety client unavailable")
	}
	if req == nil {
		return OutputSafetyRecord{}, fmt.Errorf("output evaluate request is required")
	}
	if strings.TrimSpace(req.JobID) == "" {
		return OutputSafetyRecord{}, fmt.Errorf("job id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if c.cb.IsOpen(ctx) {
		return OutputSafetyRecord{}, fmt.Errorf("output safety circuit open")
	}

	checkReq := outputCheckRequestFromEvaluateRequest(req)
	if len(checkReq.GetOutputContent()) == 0 && strings.TrimSpace(checkReq.GetResultPtr()) != "" {
		content, err := c.loadOutputContent(ctx, checkReq.ResultPtr)
		if err != nil {
			c.cb.RecordFailure(ctx)
			return OutputSafetyRecord{}, fmt.Errorf("load output content: %w", err)
		}
		checkReq.OutputContent = content
	}
	if len(checkReq.GetOutputContent()) > outputContentMaxBytes {
		checkReq.OutputContent = checkReq.OutputContent[:outputContentMaxBytes]
	}
	if len(checkReq.GetOutputContent()) > 0 {
		if checkReq.GetOutputSizeBytes() <= 0 {
			checkReq.OutputSizeBytes = int64(len(checkReq.GetOutputContent()))
		}
		if strings.TrimSpace(checkReq.GetContentHash()) == "" {
			checkReq.ContentHash = outputContentHash(checkReq.GetOutputContent())
		}
	}

	resp, err := c.client.CheckOutput(ctx, checkReq)
	if err != nil {
		c.cb.RecordFailure(ctx)
		return OutputSafetyRecord{}, err
	}
	record := outputRecordFromProto(resp)
	record = c.materializeRedaction(ctx, checkReq, record)
	c.cb.RecordSuccess(ctx)
	return record, nil
}

func outputEvaluateRequestFromJob(res *pb.JobResult, req *pb.JobRequest, includeContent bool) (*OutputEvaluateRequest, error) {
	if res == nil || req == nil {
		return nil, fmt.Errorf("job result and request are required")
	}
	out := &OutputEvaluateRequest{
		JobID:          strings.TrimSpace(res.GetJobId()),
		Topic:          strings.TrimSpace(req.GetTopic()),
		Tenant:         ExtractTenant(req),
		Labels:         cloneStringMap(req.GetLabels()),
		ArtifactPtrs:   append([]string{}, res.GetArtifactPtrs()...),
		ErrorMessage:   strings.TrimSpace(res.GetErrorMessage()),
		ErrorCode:      strings.TrimSpace(res.GetErrorCode()),
		WorkerID:       strings.TrimSpace(res.GetWorkerId()),
		ExecutionMs:    res.GetExecutionMs(),
		WorkflowID:     strings.TrimSpace(req.GetWorkflowId()),
		StepID:         strings.TrimSpace(req.GetLabels()["step_id"]),
		PrincipalID:    strings.TrimSpace(req.GetPrincipalId()),
		ContentType:    strings.TrimSpace(req.GetLabels()["content_type"]),
		OriginalLabels: cloneStringMap(req.GetLabels()),
	}
	if includeContent {
		out.ResultPtr = strings.TrimSpace(res.GetResultPtr())
	}
	if out.StepID == "" {
		out.StepID = strings.TrimSpace(req.GetEnv()["step_id"])
	}
	if out.ContentType == "" {
		out.ContentType = strings.TrimSpace(req.GetEnv()["content_type"])
	}
	if meta := req.GetMeta(); meta != nil {
		if cap := strings.TrimSpace(meta.GetCapability()); cap != "" {
			out.Capabilities = append(out.Capabilities, cap)
		}
		out.Capabilities = append(out.Capabilities, meta.GetRequires()...)
		out.RiskTags = append(out.RiskTags, meta.GetRiskTags()...)
		if out.PackID == "" {
			out.PackID = strings.TrimSpace(meta.GetPackId())
		}
		if len(out.OriginalLabels) == 0 && len(meta.GetLabels()) > 0 {
			out.OriginalLabels = cloneStringMap(meta.GetLabels())
		}
	}
	return out, nil
}

func outputCheckRequestFromEvaluateRequest(req *OutputEvaluateRequest) *pb.OutputCheckRequest {
	if req == nil {
		return &pb.OutputCheckRequest{}
	}
	return &pb.OutputCheckRequest{
		JobId:           strings.TrimSpace(req.JobID),
		Topic:           strings.TrimSpace(req.Topic),
		Tenant:          strings.TrimSpace(req.Tenant),
		Labels:          cloneStringMap(req.Labels),
		ResultPtr:       strings.TrimSpace(req.ResultPtr),
		ArtifactPtrs:    append([]string{}, req.ArtifactPtrs...),
		ErrorMessage:    strings.TrimSpace(req.ErrorMessage),
		ErrorCode:       strings.TrimSpace(req.ErrorCode),
		WorkerId:        strings.TrimSpace(req.WorkerID),
		ExecutionMs:     req.ExecutionMs,
		OutputSizeBytes: req.OutputSizeBytes,
		ContentHash:     strings.TrimSpace(req.ContentHash),
		WorkflowId:      strings.TrimSpace(req.WorkflowID),
		StepId:          strings.TrimSpace(req.StepID),
		OutputContent:   append([]byte{}, req.OutputContent...),
		Capabilities:    append([]string{}, req.Capabilities...),
		RiskTags:        append([]string{}, req.RiskTags...),
		PrincipalId:     strings.TrimSpace(req.PrincipalID),
		PackId:          strings.TrimSpace(req.PackID),
		ContentType:     strings.TrimSpace(req.ContentType),
		OriginalLabels:  cloneStringMap(req.OriginalLabels),
	}
}

func (c *OutputSafetyClient) loadOutputContent(ctx context.Context, resultPtr string) ([]byte, error) {
	resultPtr = strings.TrimSpace(resultPtr)
	if resultPtr == "" {
		return nil, nil
	}
	if c.resultClient == nil {
		return nil, fmt.Errorf("output content store unavailable")
	}
	key, err := outputResultKeyFromPointer(resultPtr)
	if err != nil {
		return nil, err
	}
	var content []byte
	for attempt := 0; attempt < outputContentFetchRetries; attempt++ {
		content, err = c.resultClient.Get(ctx, key).Bytes()
		if err == nil {
			break
		}
		if !errors.Is(err, redis.Nil) {
			return nil, err
		}
		if attempt == outputContentFetchRetries-1 {
			return nil, nil
		}
		timer := time.NewTimer(outputContentFetchBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if len(content) > outputContentMaxBytes {
		content = content[:outputContentMaxBytes]
	}
	return content, nil
}

func outputResultKeyFromPointer(ptr string) (string, error) {
	if ptr == "" {
		return "", fmt.Errorf("empty pointer")
	}
	if !strings.HasPrefix(ptr, outputPointerPrefix) {
		return "", fmt.Errorf("invalid pointer prefix: %s", ptr)
	}
	key := strings.TrimPrefix(ptr, outputPointerPrefix)
	if key == "" {
		return "", fmt.Errorf("missing pointer key")
	}
	return key, nil
}

func outputContentHash(content []byte) string {
	if len(content) == 0 {
		return ""
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func outputRecordFromProto(resp *pb.OutputCheckResponse) OutputSafetyRecord {
	if resp == nil {
		return OutputSafetyRecord{Decision: OutputAllow}
	}
	record := OutputSafetyRecord{
		Decision:       outputDecisionFromProto(resp.GetDecision()),
		Reason:         strings.TrimSpace(resp.GetReason()),
		RuleID:         strings.TrimSpace(resp.GetRuleId()),
		PolicySnapshot: strings.TrimSpace(resp.GetPolicySnapshot()),
		RedactedPtr:    strings.TrimSpace(resp.GetRedactedPtr()),
	}
	if len(resp.GetFindings()) > 0 {
		record.Findings = make([]OutputFinding, 0, len(resp.GetFindings()))
		for _, finding := range resp.GetFindings() {
			if finding == nil {
				continue
			}
			record.Findings = append(record.Findings, OutputFinding{
				Type:           strings.TrimSpace(finding.GetType()),
				Severity:       strings.TrimSpace(finding.GetSeverity()),
				Detail:         strings.TrimSpace(finding.GetDetail()),
				Scanner:        strings.TrimSpace(finding.GetScanner()),
				Confidence:     float64(finding.GetConfidence()),
				MatchedPattern: strings.TrimSpace(finding.GetMatchedPattern()),
				Offset:         finding.GetOffset(),
				Length:         finding.GetLength(),
			})
		}
	}
	return record
}

func (c *OutputSafetyClient) materializeRedaction(ctx context.Context, req *pb.OutputCheckRequest, record OutputSafetyRecord) OutputSafetyRecord {
	if record.Decision != OutputRedact || strings.TrimSpace(record.RedactedPtr) != "" {
		return record
	}
	redactedPtr, err := c.storeRedactedOutput(ctx, req, record)
	if err != nil {
		record.Decision = OutputQuarantine
		if strings.TrimSpace(record.Reason) == "" {
			record.Reason = "output redaction required but sanitized output unavailable"
		} else {
			record.Reason = record.Reason + "; sanitized output unavailable"
		}
		return record
	}
	record.RedactedPtr = redactedPtr
	return record
}

func (c *OutputSafetyClient) storeRedactedOutput(ctx context.Context, req *pb.OutputCheckRequest, record OutputSafetyRecord) (string, error) {
	if req == nil {
		return "", fmt.Errorf("output check request is required")
	}
	if c == nil || c.resultClient == nil {
		return "", fmt.Errorf("output content store unavailable")
	}
	content := req.GetOutputContent()
	if len(content) == 0 {
		return "", fmt.Errorf("missing output content for redaction")
	}
	redacted := redactOutputContent(content, record.Findings)
	if len(redacted) == 0 {
		redacted = []byte(outputRedactionMarker)
	}

	baseKey := ""
	if ptr := strings.TrimSpace(req.GetResultPtr()); ptr != "" {
		key, err := outputResultKeyFromPointer(ptr)
		if err != nil {
			return "", err
		}
		baseKey = key
	}
	if baseKey == "" {
		jobID := strings.TrimSpace(req.GetJobId())
		if jobID == "" {
			return "", fmt.Errorf("missing job id for redacted output key")
		}
		baseKey = "res:" + jobID
	}

	sum := sha256.Sum256(redacted)
	redactedKey := fmt.Sprintf("%s:redacted:%s", baseKey, hex.EncodeToString(sum[:8]))
	if err := c.resultClient.Set(ctx, redactedKey, redacted, outputRedactedTTL).Err(); err != nil {
		return "", err
	}
	return outputPointerPrefix + redactedKey, nil
}

type outputByteRange struct {
	start int
	end   int
}

func redactOutputContent(content []byte, findings []OutputFinding) []byte {
	if len(content) == 0 {
		return []byte(outputRedactionMarker)
	}

	ranges := make([]outputByteRange, 0, len(findings))
	for _, finding := range findings {
		if finding.Length <= 0 || finding.Offset < 0 {
			continue
		}
		start := int(finding.Offset)
		if start >= len(content) {
			continue
		}
		end := start + int(finding.Length)
		if end <= start {
			continue
		}
		if end > len(content) {
			end = len(content)
		}
		ranges = append(ranges, outputByteRange{start: start, end: end})
	}
	if len(ranges) == 0 {
		return []byte(outputRedactionMarker)
	}

	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start == ranges[j].start {
			return ranges[i].end < ranges[j].end
		}
		return ranges[i].start < ranges[j].start
	})

	merged := make([]outputByteRange, 0, len(ranges))
	for _, rng := range ranges {
		if len(merged) == 0 {
			merged = append(merged, rng)
			continue
		}
		last := &merged[len(merged)-1]
		if rng.start <= last.end {
			if rng.end > last.end {
				last.end = rng.end
			}
			continue
		}
		merged = append(merged, rng)
	}

	out := make([]byte, 0, len(content))
	cursor := 0
	for _, rng := range merged {
		if rng.start > cursor {
			out = append(out, content[cursor:rng.start]...)
		}
		out = append(out, outputRedactionMarker...)
		cursor = rng.end
	}
	if cursor < len(content) {
		out = append(out, content[cursor:]...)
	}
	if len(out) == 0 {
		return []byte(outputRedactionMarker)
	}
	return out
}

func outputDecisionFromProto(decision pb.OutputDecision) OutputDecision {
	switch decision {
	case pb.OutputDecision_OUTPUT_DECISION_ALLOW:
		return OutputAllow
	case pb.OutputDecision_OUTPUT_DECISION_QUARANTINE:
		return OutputQuarantine
	case pb.OutputDecision_OUTPUT_DECISION_REDACT:
		return OutputRedact
	default:
		return OutputAllow
	}
}

// cloneStringMap delegates to the shared maputil implementation.
// Kept as a package-local alias for backward compatibility with callers.
var cloneStringMap = maputil.CloneStringMap
