package workflow

import (
	"context"
	"reflect"
	"slices"
	"testing"
)

func seedIsolationIndexWorkflows(t *testing.T) *RedisStore {
	t.Helper()
	s, ctx := newTestStore(t), context.Background()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, org := range []string{"tenant-b", "tenant-a"} {
		if err := s.SaveWorkflow(ctx, &Workflow{ID: org + "-wf", OrgID: org, Name: org + "-marker"}); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateRun(ctx, &WorkflowRun{ID: org + "-run", WorkflowID: org + "-wf", OrgID: org, Status: RunStatusRunning}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestTenantIsolationWorkflowOrgIndexes(t *testing.T) {
	s, ctx := seedIsolationIndexWorkflows(t), context.Background()
	before, err := s.GetWorkflow(ctx, "tenant-b-wf")
	if err != nil || before == nil {
		t.Fatalf("B seed: %v", err)
	}
	got, err := s.ListWorkflows(ctx, "tenant-a", 10)
	if err != nil || len(got) != 1 || got[0].ID != "tenant-a-wf" || got[0].OrgID != "tenant-a" {
		t.Fatalf("A list: %v, %v", got, err)
	}
	after, err := s.GetWorkflow(ctx, "tenant-b-wf")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("B changed: %v", err)
	}
	for _, org := range []string{"tenant-a", "tenant-b"} {
		members, err := s.client.ZRange(ctx, workflowOrgIndexKey(org), 0, -1).Result()
		if err != nil || !slices.Equal(members, []string{org + "-wf"}) {
			t.Fatalf("org members: %v, %v", members, err)
		}
	}
}

func TestTenantIsolationWorkflowActiveIndex(t *testing.T) {
	s, ctx := seedIsolationIndexWorkflows(t), context.Background()
	before, err := s.GetRun(ctx, "tenant-b-run")
	if err != nil || before == nil {
		t.Fatalf("B seed: %v", err)
	}
	if got, err := s.CountActiveRuns(ctx, "tenant-a"); err != nil || got != 1 {
		t.Fatalf("A active: %d, %v", got, err)
	}
	for _, org := range []string{"tenant-a", "tenant-b"} {
		members, err := s.client.SMembers(ctx, runOrgActiveKey(org)).Result()
		if err != nil || !slices.Equal(members, []string{org + "-run"}) {
			t.Fatalf("active members: %v, %v", members, err)
		}
	}
	after, err := s.GetRun(ctx, "tenant-b-run")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("B changed: %v", err)
	}
}

// Global store indexes are internal. The gateway workflow/run handlers own
// the tenant boundary; their tests also try B's known workflow ID as A.
func TestTenantIsolationWorkflowSharedIndexes(t *testing.T) {
	s, ctx := seedIsolationIndexWorkflows(t), context.Background()
	workflows, err := s.ListWorkflows(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(workflows))
	for _, entry := range workflows {
		ids = append(ids, entry.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"tenant-a-wf", "tenant-b-wf"}) {
		t.Fatalf("shared workflow fixture: %v", ids)
	}
	runs, err := s.ListRuns(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	ids = ids[:0]
	for _, entry := range runs {
		ids = append(ids, entry.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"tenant-a-run", "tenant-b-run"}) {
		t.Fatalf("shared run fixture: %v", ids)
	}
	statusIDs, err := s.ListRunIDsByStatus(ctx, RunStatusRunning, 10)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(statusIDs)
	if !slices.Equal(statusIDs, ids) {
		t.Fatalf("status fixture: %v", statusIDs)
	}
	for _, org := range []string{"tenant-a", "tenant-b"} {
		got, err := s.ListRunsByWorkflow(ctx, org+"-wf", 10)
		if err != nil || len(got) != 1 || got[0].ID != org+"-run" || got[0].OrgID != org {
			t.Fatalf("workflow run fixture: %v, %v", got, err)
		}
	}
}
