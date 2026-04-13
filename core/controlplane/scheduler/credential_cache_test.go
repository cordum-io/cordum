package scheduler

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
)

func TestWorkerCredentialCacheVerifyReturnsDetachedRecord(t *testing.T) {
	service, cache, cleanup := newWorkerAttestationTestDeps(t)
	defer cleanup()

	issued, err := service.Create(context.Background(), workercredentials.IssueInput{
		WorkerID:      "worker-1",
		AllowedPools:  []string{"default"},
		AllowedTopics: []string{"job.default"},
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh cache: %v", err)
	}

	record, ok, err := cache.Verify("worker-1", issued.Token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok || record == nil {
		t.Fatalf("expected verified record, got ok=%v record=%v", ok, record)
	}

	record.AllowedPools[0] = "mutated-pool"
	record.AllowedTopics[0] = "job.mutated"

	record2, ok, err := cache.Verify("worker-1", issued.Token)
	if err != nil {
		t.Fatalf("second verify: %v", err)
	}
	if !ok || record2 == nil {
		t.Fatalf("expected second verified record, got ok=%v record=%v", ok, record2)
	}
	if record2.AllowedPools[0] != "default" {
		t.Fatalf("expected cached allowed pool to stay default, got %q", record2.AllowedPools[0])
	}
	if record2.AllowedTopics[0] != "job.default" {
		t.Fatalf("expected cached allowed topic to stay job.default, got %q", record2.AllowedTopics[0])
	}
}
