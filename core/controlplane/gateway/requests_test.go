package gateway

import (
	"context"
	"encoding/json"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestSubmitJobRequestDefaultsAndValidate(t *testing.T) {
	req := submitJobRequest{Prompt: "hello"}
	req.applyDefaults("tenant")
	if req.MaxInputTokens != 8000 || req.MaxOutputTokens != 1024 {
		t.Fatalf("expected default token limits")
	}
	if req.Topic != "job.default" {
		t.Fatalf("expected default topic")
	}
	if req.OrgId != "tenant" || req.TenantId != "tenant" {
		t.Fatalf("expected tenant defaulting")
	}

	if err := req.validate("tenant"); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	bad := submitJobRequest{}
	if err := bad.validate("tenant"); err == nil {
		t.Fatalf("expected validation error for missing prompt")
	}
	bad = submitJobRequest{Prompt: "ok", Topic: "bad"}
	bad.applyDefaults("tenant")
	if err := bad.validate("tenant"); err == nil {
		t.Fatalf("expected validation error for topic")
	}
	bad = submitJobRequest{Prompt: "ok", Topic: "job.test", ActorType: "robot"}
	bad.applyDefaults("tenant")
	if err := bad.validate("tenant"); err == nil {
		t.Fatalf("expected validation error for actor type")
	}
	bad = submitJobRequest{Prompt: "ok", Topic: "job.test", MaxInputTokens: -1}
	if err := bad.validate("tenant"); err == nil {
		t.Fatalf("expected validation error for tokens")
	}
}

func TestBuildJobMetadata(t *testing.T) {
	meta := buildJobMetadata(&policyMetaRequest{ActorId: "actor", ActorType: "human", PackId: "pack", Labels: map[string]string{"k": "v"}}, "tenant", "principal")
	if meta == nil {
		t.Fatalf("expected metadata")
	}
	if meta.ActorId != "actor" || meta.ActorType != pb.ActorType_ACTOR_TYPE_HUMAN {
		t.Fatalf("unexpected actor")
	}
	if meta.TenantId != "tenant" || meta.PackId != "pack" {
		t.Fatalf("unexpected tenant or pack")
	}
	if meta.Labels["k"] != "v" {
		t.Fatalf("expected labels")
	}

	meta = buildJobMetadata(nil, "tenant", "principal")
	if meta == nil || meta.ActorId != "principal" {
		t.Fatalf("expected principal actor fallback")
	}
}

func TestBuildPolicyCheckRequest(t *testing.T) {
	req := &policyCheckRequest{
		Topic:   "job.test",
		OrgId:   "org",
		TeamId:  "team",
		Labels:  map[string]string{"env": "test"},
		Meta:    &policyMetaRequest{ActorType: "service"},
		Priority: "critical",
		EffectiveConfig: map[string]any{"a": 1},
	}
	out, err := buildPolicyCheckRequest(context.Background(), req, nil, "default")
	if err != nil {
		t.Fatalf("build policy request: %v", err)
	}
	if out.Tenant != "org" || out.Topic != "job.test" {
		t.Fatalf("unexpected tenant/topic")
	}
	if out.Meta == nil || out.Meta.ActorType != pb.ActorType_ACTOR_TYPE_SERVICE {
		t.Fatalf("unexpected meta")
	}
	if out.EffectiveConfig == nil {
		t.Fatalf("expected effective config")
	}
	var cfg map[string]any
	if err := json.Unmarshal(out.EffectiveConfig, &cfg); err != nil || cfg["a"].(float64) != 1 {
		t.Fatalf("unexpected effective config payload")
	}

	_, err = buildPolicyCheckRequest(context.Background(), nil, nil, "default")
	if err == nil {
		t.Fatalf("expected error for nil request")
	}
}

func TestParseActorTypeAndTags(t *testing.T) {
	if parseActorType("human") != pb.ActorType_ACTOR_TYPE_HUMAN {
		t.Fatalf("expected human")
	}
	if parseActorType("service") != pb.ActorType_ACTOR_TYPE_SERVICE {
		t.Fatalf("expected service")
	}
	if parseActorType("unknown") != pb.ActorType_ACTOR_TYPE_UNSPECIFIED {
		t.Fatalf("expected unspecified")
	}

	out := appendUniqueTag([]string{"a"}, "a")
	if len(out) != 1 {
		t.Fatalf("expected tag de-dup")
	}
	out = appendUniqueTag([]string{"a"}, "b")
	if len(out) != 2 {
		t.Fatalf("expected tag append")
	}
}
