package store

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
)

func TestTenantIsolationAgentIdentityIndex(t *testing.T) {
	s, ctx := newTestAgentIdentityStore(t), context.Background()
	b := createTenantAgent(t, s, "tenant-b", "private-b")
	a := createTenantAgent(t, s, "tenant-a", "public-a")
	before, err := s.client.Get(ctx, agentIdentityKeyPrefix+b.ID).Result()
	if err != nil {
		t.Fatal(err)
	}
	index, err := s.client.ZRangeWithScores(ctx, agentIdentityIndexKey, 0, -1).Result()
	if err != nil || len(index) != 2 {
		t.Fatalf("index: %v, %v", index, err)
	}
	for _, cursor := range []string{"", "0:0"} {
		got, _, err := s.List(ctx, "tenant-a", cursor, 10, AgentIdentityFilter{})
		if err != nil || len(got) != 1 || got[0].ID != a.ID {
			t.Fatalf("A list: %v, %v", got, err)
		}
	}
	if got, err := s.Get(ctx, "tenant-a", b.ID); err != nil || got != nil {
		t.Fatalf("foreign member: %v, %v", got, err)
	}
	after, err := s.client.Get(ctx, agentIdentityKeyPrefix+b.ID).Result()
	if err != nil || after != before {
		t.Fatalf("B changed: %v", err)
	}
	indexAfter, err := s.client.ZRangeWithScores(ctx, agentIdentityIndexKey, 0, -1).Result()
	if err != nil || !reflect.DeepEqual(index, indexAfter) {
		t.Fatalf("index changed: %v", err)
	}
}

func TestTenantIsolationAgentWorkerIndexes(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "tenant-links"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			s, ctx := newTestAgentIdentityStore(t), context.Background()
			b := createTenantAgent(t, s, "tenant-b", "private-b")
			a := createTenantAgent(t, s, "tenant-a", "public-a")
			for _, entry := range []*AgentIdentity{b, a} {
				if legacy {
					plantLegacyLink(t, s, entry.TenantID+"-worker", entry.ID)
				} else if err := s.LinkWorker(ctx, entry.ID, entry.TenantID+"-worker"); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.Get(ctx, "tenant-b", b.ID)
			if err != nil || before == nil {
				t.Fatalf("B seed: %v", err)
			}
			assertLinkedAgent(t, s, "tenant-a", "tenant-a-worker", a.ID)
			assertLinkedAgent(t, s, "tenant-a", "tenant-b-worker", "")
			assertLinkedAgent(t, s, "tenant-b", "tenant-b-worker", b.ID)
			after, err := s.Get(ctx, "tenant-b", b.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("B changed: %v", err)
			}
			if legacy {
				got, err := s.client.Get(ctx, agentByWorkerKeyPrefix+"tenant-b-worker").Result()
				if err != nil || got != b.ID {
					t.Fatalf("legacy link changed: %v", err)
				}
			} else {
				got, err := s.client.HGetAll(ctx, agentWorkerLinksKeyPrefix+"tenant-b").Result()
				if err != nil || !reflect.DeepEqual(got, map[string]string{"tenant-b-worker": b.ID}) {
					t.Fatalf("B links changed: %v", err)
				}
			}
		})
	}
}

func seedIndexJobs(t *testing.T, s *RedisJobStore) {
	t.Helper()
	ctx := context.Background()
	for _, tenant := range []string{"tenant-b", "tenant-a"} {
		id := tenant + "-job"
		seedIsolationJob(t, s, id, tenant)
		for _, err := range []error{
			s.SetState(ctx, id, model.JobStateRunning),
			s.SetWorkerID(ctx, id, "shared-worker"),
			s.AddJobToTrace(ctx, "shared-trace", id),
			s.SetDeadline(ctx, id, time.Now().Add(-time.Minute)),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

// These internal APIs take no tenant. Gateway read-path tests must filter
// their results; this test proves the fixture actually has both tenants.
func TestTenantIsolationJobSharedIndexes(t *testing.T) {
	s, _ := newIsolationJobStore(t)
	seedIndexJobs(t, s)
	ctx := context.Background()
	for name, read := range map[string]func() ([]model.JobRecord, error){
		"worker": func() ([]model.JobRecord, error) { return s.ListWorkerJobs(ctx, "shared-worker", 10) },
		"trace":  func() ([]model.JobRecord, error) { return s.GetTraceJobs(ctx, "shared-trace") },
		"state": func() ([]model.JobRecord, error) {
			return s.ListJobsByState(ctx, model.JobStateRunning, time.Now().Unix()+1, 10)
		},
		"recent":   func() ([]model.JobRecord, error) { return s.ListRecentJobs(ctx, 10) },
		"deadline": func() ([]model.JobRecord, error) { return s.ListExpiredDeadlines(ctx, time.Now().Unix(), 10) },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := read()
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(got))
			for _, job := range got {
				if job.ID != job.Tenant+"-job" {
					t.Fatalf("tenant binding: %+v", job)
				}
				ids = append(ids, job.ID)
			}
			slices.Sort(ids)
			assertStoreStrings(t, name, ids, []string{"tenant-a-job", "tenant-b-job"})
			assertIsolationJobUnchanged(t, s, "tenant-b-job", "tenant-b")
		})
	}
}

func TestTenantIsolationJobActiveIndex(t *testing.T) {
	s, _ := newIsolationJobStore(t)
	seedIndexJobs(t, s)
	ctx := context.Background()
	before, err := s.client.SMembers(ctx, tenantActiveKey("tenant-b")).Result()
	if err != nil || !slices.Equal(before, []string{"tenant-b-job"}) {
		t.Fatalf("B seed: %v, %v", before, err)
	}
	if got, err := s.CountActiveByTenant(ctx, "tenant-a"); err != nil || got != 1 {
		t.Fatalf("A count: %d, %v", got, err)
	}
	members, err := s.client.SMembers(ctx, tenantActiveKey("tenant-a")).Result()
	if err != nil || !slices.Equal(members, []string{"tenant-a-job"}) {
		t.Fatalf("A members: %v, %v", members, err)
	}
	after, err := s.client.SMembers(ctx, tenantActiveKey("tenant-b")).Result()
	if err != nil || !slices.Equal(before, after) {
		t.Fatalf("B index changed: %v", err)
	}
	assertIsolationJobUnchanged(t, s, "tenant-b-job", "tenant-b")
}

// DLQ entries carry a job ID, not a tenant. The gateway resolves the job's
// tenant before returning any entry; the store index intentionally has both.
func TestTenantIsolationDLQSharedIndex(t *testing.T) {
	s, ctx := newDLQStore(t), context.Background()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, tenant := range []string{"tenant-b", "tenant-a"} {
		if err := s.Add(ctx, DLQEntry{JobID: tenant + "-job", Reason: tenant + "-marker", CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.Get(ctx, "tenant-b-job")
	if err != nil || before == nil {
		t.Fatalf("B seed: %v", err)
	}
	got, err := s.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(got))
	for _, entry := range got {
		ids = append(ids, entry.JobID)
	}
	slices.Sort(ids)
	assertStoreStrings(t, "DLQ", ids, []string{"tenant-a-job", "tenant-b-job"})
	after, err := s.Get(ctx, "tenant-b-job")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("B changed: %v", err)
	}
}
