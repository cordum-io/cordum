package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
)

func TestWorkerCredentialCacheLookupReturnsDefensiveClone(t *testing.T) {
	t.Parallel()
	cache := NewWorkerCredentialCache(nil)
	seed := workercredentials.Credential{
		WorkerID: "worker-1", TenantID: "tenant-1", AgentID: "agent-1",
		ProofKeyID: "proof-1", AllowedTopics: []string{"jobs.allowed"},
	}
	cache.records["worker-1"] = seed
	cache.authority["worker-1"] = seed
	cache.authorityReady = true

	record, ok := cache.Lookup("worker-1")
	if !ok || record == nil {
		t.Fatal("expected cached credential")
	}
	record.AllowedTopics[0] = "jobs.mutated"
	again, ok := cache.Lookup("worker-1")
	if !ok || again.AllowedTopics[0] != "jobs.allowed" {
		t.Fatalf("cache exposed mutable topics: %+v", again)
	}
}

func TestWorkerCredentialCacheLookupDropsAbsentAuthorityAfterRefresh(t *testing.T) {
	t.Parallel()
	cache := NewWorkerCredentialCache(nil)
	cache.records["worker-1"] = boundaryCredential()
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		return []workercredentials.Credential{}, nil
	}
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, ok := cache.Lookup("worker-1"); ok {
		t.Fatal("absent credential retained authenticated lookup authority")
	}
	if _, retained := cache.records["worker-1"]; !retained {
		t.Fatal("legacy compatibility snapshot was unexpectedly deleted")
	}
}

func TestWorkerCredentialCacheLookupFailsClosedAfterRefreshError(t *testing.T) {
	t.Parallel()
	cache := NewWorkerCredentialCache(nil)
	cache.records["worker-1"] = boundaryCredential()
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		return nil, errors.New("authority unavailable")
	}
	if cache.RefreshAuthority(context.Background()) {
		t.Fatal("refresh error reported authenticated authority available")
	}
	if _, ok := cache.Lookup("worker-1"); ok {
		t.Fatal("refresh error retained authenticated lookup authority")
	}
}

func TestWorkerCredentialCacheLookupFailsClosedWhileRefreshInFlight(t *testing.T) {
	t.Parallel()
	cache := NewWorkerCredentialCache(nil)
	cache.authority["worker-1"] = boundaryCredential()
	cache.authorityReady = true
	cache.refreshing.Store(true)
	if cache.RefreshAuthority(context.Background()) {
		t.Fatal("overlapping refresh reported authenticated authority available")
	}
	if _, ok := cache.Lookup("worker-1"); ok {
		t.Fatal("in-flight refresh exposed the prior authority snapshot")
	}
}

func boundaryCredential() workercredentials.Credential {
	return workercredentials.Credential{
		WorkerID: "worker-1", TenantID: "tenant-1", AgentID: "agent-1",
		ProofKeyID: "proof-1", AllowedTopics: []string{"jobs.allowed"},
	}
}
