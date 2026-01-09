package scheduler

import (
	"testing"

	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestHashJobRequestIgnoresApprovalLabelsAndEffectiveConfig(t *testing.T) {
	base := &pb.JobRequest{
		JobId:    "job-1",
		Topic:    "job.test",
		TenantId: "default",
		Labels: map[string]string{
			"foo":              "bar",
			"approval_granted": "true",
			"approval_reason":  "ok",
			bus.LabelBusMsgID:  "msg-1",
		},
		Env: map[string]string{
			config.EffectiveConfigEnvVar: `{"safety":{"deny_topics":["job.bad"]}}`,
		},
	}
	hash1, err := HashJobRequest(base)
	if err != nil {
		t.Fatalf("hash1: %v", err)
	}

	clone := protoClone(base)
	clone.Labels["approval_granted"] = "false"
	clone.Labels[bus.LabelBusMsgID] = "msg-2"
	clone.Env[config.EffectiveConfigEnvVar] = `{"changed":true}`
	hash2, err := HashJobRequest(clone)
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("expected same hash when only approval/effective config changed")
	}

	clone.Labels["foo"] = "baz"
	hash3, err := HashJobRequest(clone)
	if err != nil {
		t.Fatalf("hash3: %v", err)
	}
	if hash3 == hash1 {
		t.Fatalf("expected different hash when stable label changed")
	}
}

func protoClone(req *pb.JobRequest) *pb.JobRequest {
	out := &pb.JobRequest{
		JobId:    req.JobId,
		Topic:    req.Topic,
		TenantId: req.TenantId,
	}
	if req.Labels != nil {
		out.Labels = map[string]string{}
		for k, v := range req.Labels {
			out.Labels[k] = v
		}
	}
	if req.Env != nil {
		out.Env = map[string]string{}
		for k, v := range req.Env {
			out.Env[k] = v
		}
	}
	return out
}
