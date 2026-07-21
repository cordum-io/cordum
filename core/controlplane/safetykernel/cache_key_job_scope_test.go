package safetykernel

import (
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestCacheKeyForRequest_DoesNotCollideAcrossJobs proves the fix for the
// JobId-zeroing bug: two distinct jobs with otherwise identical
// PolicyCheckRequest fields must never share a cache key, since
// resolvePolicyScope/scopedPolicyForRequest select a job-scoped policy by
// JobId. See mem:task-task-a13f83fab9f84c8292cc01e424a5494c-handoff.
func TestCacheKeyForRequest_DoesNotCollideAcrossJobs(t *testing.T) {
	reqA := &pb.PolicyCheckRequest{JobId: "job-A", Topic: "job.chat.simple", Tenant: "tenant-1"}
	reqB := &pb.PolicyCheckRequest{JobId: "job-B", Topic: "job.chat.simple", Tenant: "tenant-1"}

	keyA := cacheKeyForRequest(reqA, "snapshot-1")
	keyB := cacheKeyForRequest(reqB, "snapshot-1")

	if keyA == keyB {
		t.Fatalf("cacheKeyForRequest collided across distinct jobs: both = %q", keyA)
	}
}

func TestCacheKeyForRequest_BindsActivePolicySnapshot(t *testing.T) {
	req := &pb.PolicyCheckRequest{JobId: "job-A", Topic: "job.chat.simple", Tenant: "tenant-1"}
	keyOld := cacheKeyForRequest(req, "snapshot-1")
	keyNew := cacheKeyForRequest(req, "snapshot-2")
	if keyOld == keyNew {
		t.Fatalf("cacheKeyForRequest did not change across policy snapshot rotation: both = %q", keyOld)
	}
}

func TestCacheKeyForRequestBindsCanonicalIdentityAndContent(t *testing.T) {
	req := &pb.PolicyCheckRequest{
		JobId: "job-A", Topic: "job.chat.simple", Tenant: "tenant-1",
		PrincipalId: "principal-a", InputContent: []byte("content-a"),
	}
	base := cacheKeyForRequest(req, "snapshot-1")
	req.PrincipalId = "principal-b"
	identityChanged := cacheKeyForRequest(req, "snapshot-1")
	req.PrincipalId = "principal-a"
	req.InputContent = []byte("content-b")
	contentChanged := cacheKeyForRequest(req, "snapshot-1")
	if base == identityChanged || base == contentChanged || identityChanged == contentChanged {
		t.Fatalf("canonical key collision: base=%q identity=%q content=%q", base, identityChanged, contentChanged)
	}
}
