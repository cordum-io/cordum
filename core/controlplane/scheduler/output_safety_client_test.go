package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/redisutil"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type fakeOutputPolicyClient struct {
	mu         sync.Mutex
	lastReq    *pb.OutputCheckRequest
	resp       *pb.OutputCheckResponse
	err        error
	waitForCtx bool
	decide     func(*pb.OutputCheckRequest) (*pb.OutputCheckResponse, error)
}

func (f *fakeOutputPolicyClient) CheckOutput(ctx context.Context, req *pb.OutputCheckRequest, _ ...grpc.CallOption) (*pb.OutputCheckResponse, error) {
	if f.waitForCtx {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if req != nil {
		if cloned, ok := proto.Clone(req).(*pb.OutputCheckRequest); ok {
			f.mu.Lock()
			f.lastReq = cloned
			f.mu.Unlock()
		}
	}
	if f.decide != nil {
		return f.decide(req)
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &pb.OutputCheckResponse{Decision: pb.OutputDecision_OUTPUT_DECISION_ALLOW}, nil
}

func newOutputTestCB() *RedisCircuitBreaker {
	return NewRedisCircuitBreaker(nil, "cordum:cb:safety:output:test", CircuitBreakerOpts{
		FailThreshold: outputCircuitFailBudget,
		OpenDuration:  outputCircuitOpenFor,
		HalfOpenMax:   outputCircuitHalfOpenMax,
		CloseAfter:    outputCircuitCloseAfter,
	})
}

func (f *fakeOutputPolicyClient) lastRequest() *pb.OutputCheckRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastReq == nil {
		return nil
	}
	if cloned, ok := proto.Clone(f.lastReq).(*pb.OutputCheckRequest); ok {
		return cloned
	}
	return f.lastReq
}

func TestOutputClientMetadataModeExcludesContent(t *testing.T) {
	fake := &fakeOutputPolicyClient{}
	client := &OutputSafetyClient{client: fake, cb: newOutputTestCB()}

	_, err := client.CheckOutputMeta(
		&pb.JobResult{JobId: "job-meta", ResultPtr: "redis://res:job-meta"},
		&pb.JobRequest{JobId: "job-meta", Topic: "job.demo", TenantId: "tenant-a"},
	)
	if err != nil {
		t.Fatalf("CheckOutputMeta returned error: %v", err)
	}

	got := fake.lastRequest()
	if got == nil {
		t.Fatalf("expected output check request")
	}
	if got.GetResultPtr() != "" {
		t.Fatalf("expected metadata mode to omit result_ptr, got %q", got.GetResultPtr())
	}
	if len(got.GetOutputContent()) != 0 {
		t.Fatalf("expected metadata mode to omit output_content")
	}
}

func TestOutputEvaluateRequestFromJobCapturesOriginalContext(t *testing.T) {
	res := &pb.JobResult{
		JobId:       "job-eval",
		ResultPtr:   "redis://res:job-eval",
		WorkerId:    "worker-1",
		ExecutionMs: 42,
	}
	req := &pb.JobRequest{
		JobId:       "job-eval",
		Topic:       "job.demo",
		TenantId:    "tenant-a",
		PrincipalId: "principal-a",
		Labels: map[string]string{
			"step_id":      "step-from-label",
			"content_type": "text/plain",
		},
		Meta: &pb.JobMetadata{
			Capability: "code.execute",
			RiskTags:   []string{"secrets"},
			Requires:   []string{"shell"},
			PackId:     "pack-1",
		},
	}

	got, err := outputEvaluateRequestFromJob(res, req, true)
	if err != nil {
		t.Fatalf("outputEvaluateRequestFromJob returned error: %v", err)
	}
	if got.JobID != "job-eval" || got.Topic != "job.demo" || got.Tenant != "tenant-a" {
		t.Fatalf("unexpected base fields: %#v", got)
	}
	if got.ResultPtr != "redis://res:job-eval" {
		t.Fatalf("expected result ptr to be carried, got %q", got.ResultPtr)
	}
	if got.PrincipalID != "principal-a" || got.PackID != "pack-1" {
		t.Fatalf("expected original principal/pack context, got %#v", got)
	}
	if len(got.Capabilities) != 2 || got.Capabilities[0] != "code.execute" || got.Capabilities[1] != "shell" {
		t.Fatalf("unexpected capabilities: %#v", got.Capabilities)
	}
	if len(got.RiskTags) != 1 || got.RiskTags[0] != "secrets" {
		t.Fatalf("unexpected risk tags: %#v", got.RiskTags)
	}
	if got.StepID != "step-from-label" || got.ContentType != "text/plain" {
		t.Fatalf("unexpected step/content-type context: %#v", got)
	}
}

func TestOutputClientContentModeLoadsResultFromRedis(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer mr.Close()

	resultClient, err := redisutil.NewClient("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new redis client: %v", err)
	}
	defer resultClient.Close()

	content := []byte("token=ghp_abcdefghijklmnopqrstuvwxyz1234")
	if err := resultClient.Set(context.Background(), "res:job-content", content, 0).Err(); err != nil {
		t.Fatalf("seed result content: %v", err)
	}

	fake := &fakeOutputPolicyClient{}
	client := &OutputSafetyClient{
		client:       fake,
		resultClient: resultClient,
		cb:           newOutputTestCB(),
	}

	_, err = client.CheckOutputContent(
		context.Background(),
		&pb.JobResult{JobId: "job-content", ResultPtr: "redis://res:job-content"},
		&pb.JobRequest{JobId: "job-content", Topic: "job.demo", TenantId: "tenant-a"},
	)
	if err != nil {
		t.Fatalf("CheckOutputContent returned error: %v", err)
	}

	got := fake.lastRequest()
	if got == nil {
		t.Fatalf("expected output check request")
	}
	if got.GetResultPtr() != "redis://res:job-content" {
		t.Fatalf("expected result_ptr in content mode, got %q", got.GetResultPtr())
	}
	if len(got.GetOutputContent()) == 0 {
		t.Fatalf("expected output_content to be populated")
	}
	if got.GetOutputSizeBytes() != int64(len(content)) {
		t.Fatalf("expected output_size_bytes=%d got %d", len(content), got.GetOutputSizeBytes())
	}
	if !strings.HasPrefix(got.GetContentHash(), "sha256:") {
		t.Fatalf("expected sha256 content hash, got %q", got.GetContentHash())
	}
}

func TestOutputClientContentModeRetriesMissingResultFromRedis(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer mr.Close()

	resultClient, err := redisutil.NewClient("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new redis client: %v", err)
	}
	defer resultClient.Close()

	content := []byte("retry content with AKIA1234567890ABCDEF")
	go func() {
		time.Sleep(outputContentFetchBackoff * 2)
		_ = resultClient.Set(context.Background(), "res:job-content-retry", content, 0).Err()
	}()

	fake := &fakeOutputPolicyClient{}
	client := &OutputSafetyClient{
		client:       fake,
		resultClient: resultClient,
		cb:           newOutputTestCB(),
	}

	_, err = client.CheckOutputContent(
		context.Background(),
		&pb.JobResult{JobId: "job-content-retry", ResultPtr: "redis://res:job-content-retry"},
		&pb.JobRequest{JobId: "job-content-retry", Topic: "job.demo", TenantId: "tenant-a"},
	)
	if err != nil {
		t.Fatalf("CheckOutputContent returned error: %v", err)
	}

	got := fake.lastRequest()
	if got == nil {
		t.Fatalf("expected output check request")
	}
	if len(got.GetOutputContent()) == 0 {
		t.Fatalf("expected output_content to be populated after retry")
	}
	if got.GetOutputSizeBytes() != int64(len(content)) {
		t.Fatalf("expected output_size_bytes=%d got %d", len(content), got.GetOutputSizeBytes())
	}
}

func TestEvaluateOutputUsesProvidedContextFields(t *testing.T) {
	fake := &fakeOutputPolicyClient{}
	client := &OutputSafetyClient{client: fake, cb: newOutputTestCB()}

	_, err := client.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID:          "job-direct",
		Topic:          "job.demo",
		Tenant:         "tenant-a",
		Labels:         map[string]string{"team": "platform"},
		ArtifactPtrs:   []string{"redis://art:1"},
		ErrorMessage:   "none",
		ErrorCode:      "ok",
		WorkerID:       "worker-a",
		ExecutionMs:    15,
		WorkflowID:     "wf-1",
		StepID:         "step-1",
		OutputContent:  []byte("plain output"),
		Capabilities:   []string{"cap-a"},
		RiskTags:       []string{"low"},
		PrincipalID:    "principal-a",
		PackID:         "pack-a",
		ContentType:    "text/plain",
		OriginalLabels: map[string]string{"mcp.tool": "jobs.run"},
	})
	if err != nil {
		t.Fatalf("EvaluateOutput returned error: %v", err)
	}

	got := fake.lastRequest()
	if got == nil {
		t.Fatalf("expected output check request")
	}
	if got.GetJobId() != "job-direct" || got.GetTopic() != "job.demo" || got.GetTenant() != "tenant-a" {
		t.Fatalf("unexpected request identity: %#v", got)
	}
	if got.GetPrincipalId() != "principal-a" || got.GetPackId() != "pack-a" || got.GetContentType() != "text/plain" {
		t.Fatalf("expected original context in request, got %#v", got)
	}
	if got.GetOutputSizeBytes() != int64(len("plain output")) {
		t.Fatalf("expected auto output size, got %d", got.GetOutputSizeBytes())
	}
	if !strings.HasPrefix(got.GetContentHash(), "sha256:") {
		t.Fatalf("expected auto content hash, got %q", got.GetContentHash())
	}
}

func TestOutputClientFailOpenErrorSurface(t *testing.T) {
	fake := &fakeOutputPolicyClient{err: fmt.Errorf("output backend unavailable")}
	client := &OutputSafetyClient{client: fake, cb: newOutputTestCB()}

	_, err := client.CheckOutputMeta(
		&pb.JobResult{JobId: "job-fail-open"},
		&pb.JobRequest{JobId: "job-fail-open", Topic: "job.demo", TenantId: "tenant-a"},
	)
	if err == nil {
		t.Fatalf("expected error from output policy backend")
	}
}

func TestOutputClientCircuitBreaker(t *testing.T) {
	fake := &fakeOutputPolicyClient{err: fmt.Errorf("forced failure")}
	client := &OutputSafetyClient{client: fake, cb: newOutputTestCB()}
	res := &pb.JobResult{JobId: "job-circuit"}
	req := &pb.JobRequest{JobId: "job-circuit", Topic: "job.demo", TenantId: "tenant-a"}

	for i := 0; i < outputCircuitFailBudget; i++ {
		if _, err := client.CheckOutputMeta(res, req); err == nil {
			t.Fatalf("expected failure %d to return error", i+1)
		}
	}

	_, err := client.CheckOutputMeta(res, req)
	if err == nil || !strings.Contains(err.Error(), "circuit open") {
		t.Fatalf("expected circuit open error, got %v", err)
	}
}

func TestOutputClientTimeout(t *testing.T) {
	fake := &fakeOutputPolicyClient{waitForCtx: true}
	client := &OutputSafetyClient{client: fake, cb: newOutputTestCB()}

	_, err := client.CheckOutputMeta(
		&pb.JobResult{JobId: "job-timeout"},
		&pb.JobRequest{JobId: "job-timeout", Topic: "job.demo", TenantId: "tenant-a"},
	)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestOutputClientContentModeStoresRedactedOutput(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer mr.Close()

	resultClient, err := redisutil.NewClient("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new redis client: %v", err)
	}
	defer resultClient.Close()

	secret := []byte("prefix AKIA1234567890ABCDEF suffix")
	if err := resultClient.Set(context.Background(), "res:job-redact", secret, 0).Err(); err != nil {
		t.Fatalf("seed result content: %v", err)
	}

	fake := &fakeOutputPolicyClient{
		decide: func(req *pb.OutputCheckRequest) (*pb.OutputCheckResponse, error) {
			if req == nil {
				return nil, fmt.Errorf("missing request")
			}
			needle := []byte("AKIA1234567890ABCDEF")
			idx := bytes.Index(req.GetOutputContent(), needle)
			if idx < 0 {
				return &pb.OutputCheckResponse{Decision: pb.OutputDecision_OUTPUT_DECISION_ALLOW}, nil
			}
			return &pb.OutputCheckResponse{
				Decision: pb.OutputDecision_OUTPUT_DECISION_REDACT,
				Reason:   "secret detected",
				Findings: []*pb.OutputFinding{
					{
						Type:   "secret_leak",
						Detail: "aws key",
						Offset: int64(idx),
						Length: int64(len(needle)),
					},
				},
			}, nil
		},
	}
	client := &OutputSafetyClient{
		client:       fake,
		resultClient: resultClient,
		cb:           newOutputTestCB(),
	}

	record, err := client.CheckOutputContent(
		context.Background(),
		&pb.JobResult{JobId: "job-redact", ResultPtr: "redis://res:job-redact"},
		&pb.JobRequest{JobId: "job-redact", Topic: "job.demo", TenantId: "tenant-a"},
	)
	if err != nil {
		t.Fatalf("CheckOutputContent returned error: %v", err)
	}
	if record.Decision != OutputRedact {
		t.Fatalf("expected redact decision, got %#v", record)
	}
	if !strings.HasPrefix(record.RedactedPtr, "redis://res:job-redact:redacted:") {
		t.Fatalf("expected redacted ptr to be materialized, got %q", record.RedactedPtr)
	}

	key, err := outputResultKeyFromPointer(record.RedactedPtr)
	if err != nil {
		t.Fatalf("parse redacted ptr: %v", err)
	}
	redacted, err := resultClient.Get(context.Background(), key).Bytes()
	if err != nil {
		t.Fatalf("load redacted output: %v", err)
	}
	if bytes.Contains(redacted, []byte("AKIA1234567890ABCDEF")) {
		t.Fatalf("redacted output still contains secret: %q", string(redacted))
	}
	if !bytes.Contains(redacted, []byte(outputRedactionMarker)) {
		t.Fatalf("redacted output missing marker: %q", string(redacted))
	}
}

func TestEvaluateOutputRedactionFallsBackToQuarantineWhenStoreUnavailable(t *testing.T) {
	fake := &fakeOutputPolicyClient{
		resp: &pb.OutputCheckResponse{
			Decision: pb.OutputDecision_OUTPUT_DECISION_REDACT,
			Reason:   "needs redaction",
			Findings: []*pb.OutputFinding{
				{Offset: 0, Length: 6},
			},
		},
	}
	client := &OutputSafetyClient{client: fake, cb: newOutputTestCB()}

	record, err := client.EvaluateOutput(context.Background(), &OutputEvaluateRequest{
		JobID:         "job-no-store",
		Topic:         "job.demo",
		Tenant:        "tenant-a",
		OutputContent: []byte("secret content"),
	})
	if err != nil {
		t.Fatalf("EvaluateOutput returned error: %v", err)
	}
	if record.Decision != OutputQuarantine {
		t.Fatalf("expected secure fallback to quarantine, got %#v", record)
	}
	if !strings.Contains(strings.ToLower(record.Reason), "sanitized output unavailable") {
		t.Fatalf("expected fallback reason to explain missing sanitized output, got %q", record.Reason)
	}
}
