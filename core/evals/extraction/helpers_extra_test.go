package extraction

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestTimeoutErrorBehaviors(t *testing.T) {
	t.Parallel()

	var nilErr *TimeoutError
	if got := nilErr.Error(); got != "extraction timed out" {
		t.Fatalf("nil timeout error string = %q", got)
	}
	if err := nilErr.Unwrap(); err != nil {
		t.Fatalf("nil timeout error unwrap = %v want nil", err)
	}

	timeoutErr := &TimeoutError{Err: context.DeadlineExceeded}
	if got := timeoutErr.Error(); got != context.DeadlineExceeded.Error() {
		t.Fatalf("timeout error string = %q want %q", got, context.DeadlineExceeded)
	}
	if !errors.Is(timeoutErr, context.DeadlineExceeded) {
		t.Fatalf("expected TimeoutError to unwrap to deadline exceeded")
	}
}

func TestServiceRunValidatesRequiredDependencies(t *testing.T) {
	t.Parallel()

	var nilSvc *Service
	if _, err := nilSvc.Validate(ExtractionRequest{}); err == nil || !strings.Contains(err.Error(), "extractor is nil") {
		t.Fatalf("Validate(nil) error = %v", err)
	}
	if _, err := nilSvc.Run(context.TODO(), ExtractionRequest{}); err == nil || !strings.Contains(err.Error(), "extractor is nil") {
		t.Fatalf("Run(nil) error = %v", err)
	}

	svc := New(ExtractionDeps{
		JobStore: &fakeJobStore{},
		Now:      func() time.Time { return time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC) },
	})
	if svc.deps.Now == nil {
		t.Fatal("expected New to populate default clock")
	}
	if _, err := svc.Run(context.Background(), ExtractionRequest{Tenant: "acme", DatasetName: "incident-pack"}); err == nil || !strings.Contains(err.Error(), "decision log store is required") {
		t.Fatalf("missing decision log error = %v", err)
	}

	svc = New(ExtractionDeps{
		DecisionLog: &fakeDecisionLogStore{
			pages: map[model.SafetyDecision][]model.DecisionPage{
				model.SafetyDeny:            {{}},
				model.SafetyRequireApproval: {{}},
			},
		},
		JobStore: &fakeJobStore{},
		Now:      func() time.Time { return time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC) },
	})
	if _, err := svc.Run(context.Background(), ExtractionRequest{Tenant: "acme", DatasetName: "incident-pack"}); err == nil || !strings.Contains(err.Error(), "eval dataset store is required") {
		t.Fatalf("missing dataset store error = %v", err)
	}

	result, err := svc.Run(context.Background(), ExtractionRequest{
		Tenant:      "acme",
		DatasetName: "incident-pack",
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("dry-run Run() error = %v", err)
	}
	if result.EntryCount != 0 || result.ScannedDecisions != 0 {
		t.Fatalf("unexpected dry-run empty result: %+v", result)
	}
}

func TestCompileTopicMatcherVariants(t *testing.T) {
	t.Parallel()

	matcher, err := compileTopicMatcher("")
	if err != nil {
		t.Fatalf("compile empty matcher: %v", err)
	}
	if !matcher.matches("anything") {
		t.Fatal("empty matcher should match any topic")
	}

	matcher, err = compileTopicMatcher("payments.*")
	if err != nil {
		t.Fatalf("compile glob matcher: %v", err)
	}
	if !matcher.matches("payments.api") || matcher.matches("support.api") {
		t.Fatalf("glob matcher behaved unexpectedly")
	}

	matcher, err = compileTopicMatcher("re:^support\\.(email|chat)$")
	if err != nil {
		t.Fatalf("compile regex matcher: %v", err)
	}
	if !matcher.matches("support.email") || matcher.matches("support.voice") {
		t.Fatalf("regex matcher behaved unexpectedly")
	}

	matcher, err = compileTopicMatcher("support.email")
	if err != nil {
		t.Fatalf("compile exact matcher: %v", err)
	}
	if matcher.exact != "support.email" || !matcher.matches("support.email") || matcher.matches("support.chat") {
		t.Fatalf("exact matcher behaved unexpectedly")
	}

	if _, err := compileTopicMatcher("re:["); err == nil {
		t.Fatal("expected invalid regex error")
	}
}

func TestJobRequestCacheReusesEntriesAndEvicts(t *testing.T) {
	t.Parallel()

	cache := newJobRequestCache(0)
	store := &fakeJobStore{
		requests: map[string]*pb.JobRequest{
			"job-1": {JobId: "job-1", Topic: "support", TenantId: "acme"},
			"job-2": {JobId: "job-2", Topic: "payments", TenantId: "acme"},
		},
	}

	first, err := cache.getOrLoad(context.Background(), "job-1", store)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, err := cache.getOrLoad(context.Background(), "job-1", store)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if first != second {
		t.Fatalf("expected cache hit to reuse request instance")
	}
	if len(store.calls) != 1 {
		t.Fatalf("job store calls = %d want 1", len(store.calls))
	}

	if _, err := cache.getOrLoad(context.Background(), "job-2", store); err != nil {
		t.Fatalf("load second job: %v", err)
	}
	if _, err := cache.getOrLoad(context.Background(), "job-1", store); err != nil {
		t.Fatalf("reload evicted job: %v", err)
	}
	if len(store.calls) != 3 {
		t.Fatalf("job store calls after eviction = %d want 3", len(store.calls))
	}

	if _, err := cache.getOrLoad(context.Background(), "job-x", nil); err == nil || !strings.Contains(err.Error(), "job store is required") {
		t.Fatalf("nil store error = %v", err)
	}
}

func TestBuildInputSnapshotFallbacksAndSizeLimits(t *testing.T) {
	t.Parallel()

	if _, _, err := buildInputSnapshot(nil); err == nil || !strings.Contains(err.Error(), "job request is nil") {
		t.Fatalf("nil request error = %v", err)
	}

	largeMetadata := map[string]string{
		"agent_id":        "agent-ignored",
		"_content.prompt": "redacted",
	}
	for i := 0; i < maxMetadataKeys; i++ {
		largeMetadata[time.Date(2026, time.April, 20, 0, 0, i, 0, time.UTC).Format("meta-150405")] = strings.Repeat("x", maxMetadataValue)
	}

	req := &pb.JobRequest{
		JobId:    "job-large",
		Topic:    strings.Repeat("topic-", 900),
		TenantId: "acme",
		Labels: map[string]string{
			"agent_id": "agent-root",
		},
		Meta: &pb.JobMetadata{
			Capability: "write",
			RiskTags:   []string{"beta", "alpha", "beta"},
			Labels:     largeMetadata,
		},
	}

	snapshot, raw, err := buildInputSnapshot(req)
	if err != nil {
		t.Fatalf("buildInputSnapshot fallback error: %v", err)
	}
	if len(raw) > maxSnapshotBytes {
		t.Fatalf("snapshot size = %d want <= %d", len(raw), maxSnapshotBytes)
	}
	if snapshot.Metadata != nil {
		t.Fatalf("expected metadata to be dropped when snapshot is oversized, got %+v", snapshot.Metadata)
	}
	if snapshot.AgentID != "agent-root" {
		t.Fatalf("AgentID = %q want agent-root", snapshot.AgentID)
	}
	if len(snapshot.Capabilities) != 1 || snapshot.Capabilities[0] != "write" {
		t.Fatalf("Capabilities = %#v", snapshot.Capabilities)
	}
	if len(snapshot.RiskTags) != 2 || snapshot.RiskTags[0] != "alpha" || snapshot.RiskTags[1] != "beta" {
		t.Fatalf("RiskTags = %#v", snapshot.RiskTags)
	}
	if snapshot.InputHash == "" {
		t.Fatal("expected input hash")
	}

	tooLarge := &pb.JobRequest{
		JobId:    "job-too-large",
		Topic:    strings.Repeat("z", maxSnapshotBytes+32),
		TenantId: "acme",
		Meta: &pb.JobMetadata{
			Labels: map[string]string{"meta": "value"},
		},
	}
	if _, _, err := buildInputSnapshot(tooLarge); err == nil || !strings.Contains(err.Error(), "input snapshot exceeds") {
		t.Fatalf("expected oversize snapshot error, got %v", err)
	}
}

func TestBuildDecisionMetadataHelpers(t *testing.T) {
	t.Parallel()

	record := model.DecisionLogRecord{
		PolicyVersion: "v2",
		Timestamp:     time.Date(2026, time.April, 20, 12, 30, 0, 123000000, time.UTC).UnixMilli(),
	}
	meta := buildDecisionMetadata(record)
	if meta["policy_version"] != "v2" {
		t.Fatalf("policy_version = %q", meta["policy_version"])
	}
	if meta["decision_ts"] != "2026-04-20T12:30:00.123Z" {
		t.Fatalf("decision_ts = %q", meta["decision_ts"])
	}
	if got := timeRFC3339(0); got != "" {
		t.Fatalf("timeRFC3339(0) = %q want empty", got)
	}
}
