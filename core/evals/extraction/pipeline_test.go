package extraction

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

func TestPipelineExtractsDeniesAndApprovals(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	decisionLog := &fakeDecisionLogStore{
		pages: map[model.SafetyDecision][]model.DecisionPage{
			model.SafetyDeny: {{
				Items: []model.DecisionLogRecord{
					{JobID: "job-deny", Tenant: "acme", Topic: "support.email", Verdict: model.SafetyDeny, RuleID: "rule-pii", PolicyVersion: "v1", Timestamp: now.Add(-2 * time.Hour).UnixMilli()},
				},
			}},
			model.SafetyRequireApproval: {{
				Items: []model.DecisionLogRecord{
					{JobID: "job-approve", Tenant: "acme", Topic: "support.chat", Verdict: model.SafetyRequireApproval, RuleID: "rule-human", PolicyVersion: "v1", Timestamp: now.Add(-1 * time.Hour).UnixMilli()},
				},
			}},
		},
	}
	jobStore := &fakeJobStore{
		requests: map[string]*pb.JobRequest{
			"job-deny": {
				JobId:    "job-deny",
				Topic:    "support.email",
				TenantId: "acme",
				Labels:   map[string]string{"origin": "ticket-42", "_content.prompt": "redacted"},
				Meta: &pb.JobMetadata{
					Capability: "read",
					RiskTags:   []string{"pii"},
				},
			},
			"job-approve": {
				JobId:    "job-approve",
				Topic:    "support.chat",
				TenantId: "acme",
				Meta: &pb.JobMetadata{
					Capability: "write",
					RiskTags:   []string{"manual-review"},
				},
			},
		},
	}
	datasetStore := &fakeEvalDatasetStore{
		versions: []model.EvalDataset{{Version: 2}},
		nextID:   "dataset-123",
	}

	svc := New(ExtractionDeps{
		DecisionLog:  decisionLog,
		JobStore:     jobStore,
		EvalDatasets: datasetStore,
		Now:          func() time.Time { return now },
	})

	got, err := svc.Run(context.Background(), ExtractionRequest{
		Tenant:       "acme",
		DatasetName:  "incident-pack",
		TopicPattern: "support.*",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.DatasetID != "dataset-123" {
		t.Fatalf("DatasetID = %q want dataset-123", got.DatasetID)
	}
	if got.Version != 3 {
		t.Fatalf("Version = %d want 3", got.Version)
	}
	if got.EntryCount != 2 {
		t.Fatalf("EntryCount = %d want 2", got.EntryCount)
	}
	if len(datasetStore.created) != 1 {
		t.Fatalf("CreateEvalDataset calls = %d want 1", len(datasetStore.created))
	}
	created := datasetStore.created[0]
	if len(created.Entries) != 2 {
		t.Fatalf("created entries = %d want 2", len(created.Entries))
	}
	if created.Entries[0].Source != model.EvalEntrySourceAuditImport || created.Entries[1].Source != model.EvalEntrySourceAuditImport {
		t.Fatalf("unexpected entry sources: %#v", created.Entries)
	}
	if created.Entries[0].Metadata["policy_version"] != "v1" {
		t.Fatalf("policy_version metadata missing: %#v", created.Entries[0].Metadata)
	}
}

func TestPipelineDedupeKeepsOldestEntry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	decisionLog := &fakeDecisionLogStore{
		pages: map[model.SafetyDecision][]model.DecisionPage{
			model.SafetyDeny: {{
				Items: []model.DecisionLogRecord{
					{JobID: "job-new", Tenant: "acme", Topic: "support.email", Verdict: model.SafetyDeny, RuleID: "rule-pii", Timestamp: now.Add(-1 * time.Hour).UnixMilli()},
					{JobID: "job-old", Tenant: "acme", Topic: "support.email", Verdict: model.SafetyDeny, RuleID: "rule-pii", Timestamp: now.Add(-2 * time.Hour).UnixMilli()},
				},
			}},
		},
	}
	jobRequest := &pb.JobRequest{
		Topic:    "support.email",
		TenantId: "acme",
		Meta: &pb.JobMetadata{
			Capability: "read",
			RiskTags:   []string{"pii"},
		},
	}
	jobStore := &fakeJobStore{
		requests: map[string]*pb.JobRequest{
			"job-new": jobRequest,
			"job-old": jobRequest,
		},
	}

	svc := New(ExtractionDeps{
		DecisionLog:  decisionLog,
		JobStore:     jobStore,
		EvalDatasets: &fakeEvalDatasetStore{},
		Now:          func() time.Time { return now },
	})

	got, err := svc.Run(context.Background(), ExtractionRequest{
		Tenant:      "acme",
		DatasetName: "incident-pack",
		DryRun:      true,
		Verdicts:    []model.SafetyDecision{model.SafetyDeny},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.EntryCount != 1 {
		t.Fatalf("EntryCount = %d want 1", got.EntryCount)
	}
	if got.DedupedCount != 1 {
		t.Fatalf("DedupedCount = %d want 1", got.DedupedCount)
	}
}

func TestPipelineDryRunDoesNotWrite(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	svc := New(ExtractionDeps{
		DecisionLog: &fakeDecisionLogStore{
			pages: map[model.SafetyDecision][]model.DecisionPage{
				model.SafetyDeny: {{
					Items: []model.DecisionLogRecord{
						{JobID: "job-1", Tenant: "acme", Topic: "support", Verdict: model.SafetyDeny, Timestamp: now.UnixMilli()},
					},
				}},
			},
		},
		JobStore: &fakeJobStore{
			requests: map[string]*pb.JobRequest{
				"job-1": {JobId: "job-1", Topic: "support", TenantId: "acme"},
			},
		},
		EvalDatasets: &fakeEvalDatasetStore{},
		Now:          func() time.Time { return now },
	})

	got, err := svc.Run(context.Background(), ExtractionRequest{
		Tenant:      "acme",
		DatasetName: "incident-pack",
		DryRun:      true,
		Verdicts:    []model.SafetyDecision{model.SafetyDeny},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.DatasetID != "" {
		t.Fatalf("DatasetID = %q want empty for dry run", got.DatasetID)
	}
}

func TestPipelineWarnsOnMissingJob(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	svc := New(ExtractionDeps{
		DecisionLog: &fakeDecisionLogStore{
			pages: map[model.SafetyDecision][]model.DecisionPage{
				model.SafetyDeny: {{
					Items: []model.DecisionLogRecord{
						{JobID: "job-missing", Tenant: "acme", Topic: "support", Verdict: model.SafetyDeny, Timestamp: now.Add(-2 * time.Hour).UnixMilli()},
						{JobID: "job-ok", Tenant: "acme", Topic: "support", Verdict: model.SafetyDeny, Timestamp: now.Add(-1 * time.Hour).UnixMilli()},
					},
				}},
			},
		},
		JobStore: &fakeJobStore{
			requests: map[string]*pb.JobRequest{
				"job-ok": {JobId: "job-ok", Topic: "support", TenantId: "acme"},
			},
			errors: map[string]error{"job-missing": redis.Nil},
		},
		EvalDatasets: &fakeEvalDatasetStore{},
		Now:          func() time.Time { return now },
	})

	got, err := svc.Run(context.Background(), ExtractionRequest{
		Tenant:      "acme",
		DatasetName: "incident-pack",
		DryRun:      true,
		Verdicts:    []model.SafetyDecision{model.SafetyDeny},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.EntryCount != 1 {
		t.Fatalf("EntryCount = %d want 1", got.EntryCount)
	}
	if got.ScannedDecisions != 2 {
		t.Fatalf("ScannedDecisions = %d want 2", got.ScannedDecisions)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("Warnings = %v want 1 warning", got.Warnings)
	}
}

func TestPipelineRetriesOnVersionCollision(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	datasetStore := &fakeEvalDatasetStore{
		versions:     []model.EvalDataset{{Version: 1}},
		createErrors: []error{store.ErrEvalDatasetVersionExists, nil},
		nextID:       "dataset-after-retry",
	}
	svc := New(ExtractionDeps{
		DecisionLog: &fakeDecisionLogStore{
			pages: map[model.SafetyDecision][]model.DecisionPage{
				model.SafetyDeny: {{
					Items: []model.DecisionLogRecord{
						{JobID: "job-1", Tenant: "acme", Topic: "support", Verdict: model.SafetyDeny, Timestamp: now.UnixMilli()},
					},
				}},
			},
		},
		JobStore: &fakeJobStore{
			requests: map[string]*pb.JobRequest{
				"job-1": {JobId: "job-1", Topic: "support", TenantId: "acme"},
			},
		},
		EvalDatasets: datasetStore,
		Now:          func() time.Time { return now },
	})

	got, err := svc.Run(context.Background(), ExtractionRequest{
		Tenant:      "acme",
		DatasetName: "incident-pack",
		Verdicts:    []model.SafetyDecision{model.SafetyDeny},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.Version != 3 {
		t.Fatalf("Version = %d want 3 after retry", got.Version)
	}
	if len(datasetStore.created) != 2 {
		t.Fatalf("CreateEvalDataset calls = %d want 2", len(datasetStore.created))
	}
	if datasetStore.created[0].Version != 2 || datasetStore.created[1].Version != 3 {
		t.Fatalf("created versions = %#v want [2 3]", datasetStore.created)
	}
}

func TestPipelineStopsAtCandidateCap(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	makeRecord := func(jobID string, offset time.Duration) model.DecisionLogRecord {
		return model.DecisionLogRecord{
			JobID:     jobID,
			Tenant:    "acme",
			Topic:     "support",
			Verdict:   model.SafetyDeny,
			Timestamp: now.Add(offset).UnixMilli(),
		}
	}

	decisionLog := &fakeDecisionLogStore{
		pages: map[model.SafetyDecision][]model.DecisionPage{
			model.SafetyDeny: {
				{Items: []model.DecisionLogRecord{
					makeRecord("job-1", -6*time.Hour),
					makeRecord("job-2", -5*time.Hour),
					makeRecord("job-3", -4*time.Hour),
				}, NextCursor: "cursor-1"},
				{Items: []model.DecisionLogRecord{
					makeRecord("job-4", -3*time.Hour),
					makeRecord("job-5", -2*time.Hour),
					makeRecord("job-6", -1*time.Hour),
				}, NextCursor: "cursor-2"},
				{Items: []model.DecisionLogRecord{
					makeRecord("job-7", -30*time.Minute),
				}},
			},
		},
	}
	requests := map[string]*pb.JobRequest{}
	for i := 1; i <= 7; i++ {
		jobID := "job-" + string(rune('0'+i))
		requests[jobID] = &pb.JobRequest{JobId: jobID, Topic: "support", TenantId: "acme"}
	}
	svc := New(ExtractionDeps{
		DecisionLog:  decisionLog,
		JobStore:     &fakeJobStore{requests: requests},
		EvalDatasets: &fakeEvalDatasetStore{},
		Now:          func() time.Time { return now },
	})

	got, err := svc.Run(context.Background(), ExtractionRequest{
		Tenant:      "acme",
		DatasetName: "incident-pack",
		DryRun:      true,
		MaxEntries:  2,
		Verdicts:    []model.SafetyDecision{model.SafetyDeny},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.ScannedDecisions != 6 {
		t.Fatalf("ScannedDecisions = %d want 6", got.ScannedDecisions)
	}
	if len(decisionLog.queries) != 2 {
		t.Fatalf("QueryDecisions calls = %d want 2", len(decisionLog.queries))
	}
	if len(got.Warnings) == 0 {
		t.Fatalf("expected cap warning, got none")
	}
}
