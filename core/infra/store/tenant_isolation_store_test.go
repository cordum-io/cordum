package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
)

func TestTenantIsolationAgentIdentityReadOnly(t *testing.T) {
	s, ctx := newTestAgentIdentityStore(t), context.Background()
	created, err := s.Create(ctx, AgentIdentity{TenantID: "tenant-b", Name: "private-agent", Owner: "owner-b", RiskTier: "low"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(ctx, "tenant-b", created.ID)
	if err != nil || before == nil {
		t.Fatalf("seed read: %v", err)
	}
	got, err := s.Get(ctx, "tenant-a", created.ID)
	if err != nil || got != nil {
		t.Errorf("foreign read: %v, %v", got, err)
	}
	after, err := s.Get(ctx, "tenant-b", created.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("B identity changed: %v", err)
	}
}

func TestTenantIsolationDecisionLogIndexes(t *testing.T) {
	s, srv := newTestDecisionLogStore(t)
	defer srv.Close()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, base := context.Background(), decisionFixtureBase(t)
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		if err := s.AppendDecision(ctx, decisionFixture(tenant, tenant+"-job", base)); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []model.DecisionQuery{
		{}, {RuleID: "rule-default"}, {AgentID: "agent-default"},
		{Topic: "topic-default"}, {Verdict: model.SafetyAllow},
		{RuleID: "rule-default", AgentID: "agent-default", Topic: "topic-default", Verdict: model.SafetyAllow},
	} {
		q.Since, q.Until, q.Tenant = base-1, base+1, "tenant-b"
		before, err := s.QueryDecisions(ctx, q)
		if err != nil || len(before.Items) != 1 || before.Items[0].JobID != "tenant-b-job" {
			t.Fatalf("seed read: %v, %v", before, err)
		}
		foreign := q
		foreign.Tenant = "tenant-a"
		got, err := s.QueryDecisions(ctx, foreign)
		if err != nil || len(got.Items) != 1 || got.Items[0].Tenant != "tenant-a" || got.Items[0].JobID != "tenant-a-job" {
			t.Errorf("foreign decisions: %v, %v", got, err)
		}
		after, err := s.QueryDecisions(ctx, q)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Errorf("B decisions changed: %v", err)
		}
	}
}

func TestTenantIsolationEvalRunReadOnly(t *testing.T) {
	s, _ := newTestEvalRunStore(t)
	ctx := context.Background()
	if err := s.CreateRun(ctx, sampleRun("run-b", "tenant-b", "dataset-b", time.Now().UTC(), 100, 0)); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetRun(ctx, "tenant-b", "run-b")
	if err != nil || before.RunID != "run-b" {
		t.Fatalf("seed read: %v", err)
	}
	got, err := s.GetRun(ctx, "tenant-a", "run-b")
	if !errors.Is(err, ErrEvalRunNotFound) || got.RunID != "" {
		t.Errorf("foreign run: %v, %v", got, err)
	}
	page, err := s.ListRuns(ctx, "tenant-a", RunFilter{DatasetID: "dataset-b"}, "", 10)
	if err != nil || len(page.Items) != 0 {
		t.Errorf("foreign dataset index: %v, %v", page, err)
	}
	after, err := s.GetRun(ctx, "tenant-b", "run-b")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("B run changed: %v", err)
	}
}

// Existing dataset isolation tests cover filtering; this adds persisted-state equality.
func TestTenantIsolationEvalDatasetReadOnly(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()
	created, err := s.CreateEvalDataset(ctx, sampleDataset("dataset-b", 1, "tenant-b"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetEvalDataset(ctx, "tenant-b", created.ID)
	if err != nil || before.ID != created.ID {
		t.Fatalf("seed read: %v", err)
	}
	got, err := s.GetEvalDataset(ctx, "tenant-a", created.ID)
	if !errors.Is(err, ErrEvalDatasetNotFound) || got.ID != "" {
		t.Errorf("foreign dataset: %v, %v", got, err)
	}
	after, err := s.GetEvalDataset(ctx, "tenant-b", created.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("B dataset changed: %v", err)
	}
}
