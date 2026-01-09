package scheduler

import (
	"strings"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestPoolRoutingTopicToPool(t *testing.T) {
	routing := PoolRouting{Topics: map[string][]string{"job.test": {"pool-a", "pool-b"}, "job.empty": {}}, Pools: map[string]PoolProfile{}}
	mapped := routing.TopicToPool()
	if mapped["job.test"] != "pool-a" {
		t.Fatalf("expected first pool mapping")
	}
	if _, ok := mapped["job.empty"]; ok {
		t.Fatalf("expected no mapping for empty pools")
	}
}

func TestRetryAfter(t *testing.T) {
	err := RetryAfter(nil, -5*time.Second)
	if err == nil {
		t.Fatalf("expected retry error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "retry") {
		t.Fatalf("unexpected error message: %s", msg)
	}
	if err.(*retryableError).Unwrap() == nil {
		t.Fatalf("expected unwrap error")
	}
}

func TestExtractTenantAndPrincipal(t *testing.T) {
	if ExtractTenant(nil) != DefaultTenant {
		t.Fatalf("expected default tenant")
	}
	req := &pb.JobRequest{TenantId: "t1", Env: map[string]string{"tenant_id": "t2"}, PrincipalId: "p1"}
	if ExtractTenant(req) != "t1" {
		t.Fatalf("expected tenant from request")
	}
	req = &pb.JobRequest{Env: map[string]string{"tenant_id": "t2"}}
	if ExtractTenant(req) != "t2" {
		t.Fatalf("expected tenant from env")
	}
	if ExtractPrincipal(req) != "" {
		t.Fatalf("expected empty principal")
	}
	req = &pb.JobRequest{PrincipalId: "p2"}
	if ExtractPrincipal(req) != "p2" {
		t.Fatalf("expected principal from request")
	}
}
