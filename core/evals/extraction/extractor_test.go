package extraction

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestServiceRunHappyPath(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC)
	decisionLog := &fakeDecisionLogStore{
		pages: map[model.SafetyDecision][]model.DecisionPage{
			model.SafetyDeny: {{
				Items: []model.DecisionLogRecord{
					{JobID: "job-1", Tenant: "acme", Topic: "support", Verdict: model.SafetyDeny, Timestamp: now.UnixMilli()},
				},
			}},
			model.SafetyRequireApproval: {{}},
		},
	}
	jobStore := &fakeJobStore{
		requests: map[string]*pb.JobRequest{
			"job-1": {
				JobId:    "job-1",
				Topic:    "support",
				TenantId: "acme",
				Meta: &pb.JobMetadata{
					Capability: "read",
					RiskTags:   []string{"pii"},
				},
			},
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
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.Name != "incident-pack" {
		t.Fatalf("Name = %q want incident-pack", got.Name)
	}
	if got.ScannedDecisions != 1 {
		t.Fatalf("ScannedDecisions = %d want 1", got.ScannedDecisions)
	}
	if decisionLog.lastQuery.Tenant != "acme" {
		t.Fatalf("tenant query = %q want acme", decisionLog.lastQuery.Tenant)
	}
	if decisionLog.lastQuery.Limit != 500 {
		t.Fatalf("query limit = %d want 500", decisionLog.lastQuery.Limit)
	}
	if decisionLog.lastQuery.Verdict != model.SafetyRequireApproval {
		t.Fatalf("last verdict query = %q want %q", decisionLog.lastQuery.Verdict, model.SafetyRequireApproval)
	}
}

func TestServiceRunZeroMatchesReturnsErrNoIncidents(t *testing.T) {
	t.Parallel()

	svc := New(ExtractionDeps{
		DecisionLog: &fakeDecisionLogStore{
			pages: map[model.SafetyDecision][]model.DecisionPage{
				model.SafetyDeny:            {{}},
				model.SafetyRequireApproval: {{}},
			},
		},
		JobStore:     &fakeJobStore{},
		EvalDatasets: &fakeEvalDatasetStore{},
		Now:          func() time.Time { return time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC) },
	})

	_, err := svc.Run(context.Background(), ExtractionRequest{
		Tenant:      "acme",
		DatasetName: "incident-pack",
	})
	if !errors.Is(err, ErrNoIncidents) {
		t.Fatalf("Run() error = %v want ErrNoIncidents", err)
	}
}

func TestExtractionRequestNormalizeRejectsInvalidWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC)
	_, err := (ExtractionRequest{
		Tenant:      "acme",
		DatasetName: "incident-pack",
		Since:       now.Add(-(MaxExtractionDays + 1) * 24 * time.Hour),
		Until:       now,
	}).Normalize(now)
	if err == nil || err.Error() != "time window must be <= 90 days" {
		t.Fatalf("Normalize() error = %v want time window validation", err)
	}
}

func TestExtractionRequestNormalizeRejectsInvalidVerdict(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC)
	_, err := (ExtractionRequest{
		Tenant:      "acme",
		DatasetName: "incident-pack",
		Verdicts:    []model.SafetyDecision{model.SafetyUnavailable},
	}).Normalize(now)
	if err == nil || err.Error() != "invalid verdict \"UNAVAILABLE\"" {
		t.Fatalf("Normalize() error = %v want invalid verdict", err)
	}
}
