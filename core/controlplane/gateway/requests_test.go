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
		Topic:           "job.test",
		OrgId:           "org",
		TeamId:          "team",
		Labels:          map[string]string{"env": "test"},
		Meta:            &policyMetaRequest{ActorType: "service"},
		Priority:        "critical",
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

func TestTopicValidation(t *testing.T) {
	tests := []struct {
		name         string
		topic        string
		applyDefault bool
		wantErr      bool
	}{
		// Valid topics
		{"simple", "job.default", false, false},
		{"with-dots", "job.pack.action", false, false},
		{"with-hyphens", "job.my-pack.my-action", false, false},
		{"with-underscores", "job.my_pack.my_action", false, false},
		{"complex", "job.demo-guardrails.write", false, false},
		{"numbers", "job.pack123.action456", false, false},
		{"mixed", "job.my-pack_v2.action-1_test", false, false},
		{"empty-gets-default", "", true, false}, // applyDefaults sets job.default

		// Invalid topics (no default applied)
		{"no-prefix", "pack.action", false, true},
		{"sys-prefix", "sys.dangerous", false, true},
		{"double-dots", "job..action", false, true},
		{"starts-with-dot", "job.", false, true},
		{"ends-with-dot", "job.pack.", false, true},
		{"starts-with-hyphen", "job.-pack.action", false, true},
		{"special-chars", "job.pack;inject.action", false, true},
		{"space", "job.pack name.action", false, true},
		{"path-traversal", "job.../../../etc/passwd", false, true},
		{"newline", "job.pack\naction", false, true},
		{"unicode", "job.pack.действие", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := submitJobRequest{
				Prompt: "test",
				Topic:  tt.topic,
			}
			if tt.applyDefault {
				req.applyDefaults("tenant")
			}
			err := req.validate("tenant")
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLabelValidation(t *testing.T) {
	// Test label key too long
	longKey := make([]byte, 300)
	for i := range longKey {
		longKey[i] = 'a'
	}
	req := submitJobRequest{
		Prompt: "test",
		Topic:  "job.default",
		Labels: map[string]string{string(longKey): "value"},
	}
	req.applyDefaults("tenant")
	if err := req.validate("tenant"); err == nil {
		t.Error("expected error for label key too long")
	}

	// Test label value too long
	longValue := make([]byte, 5000)
	for i := range longValue {
		longValue[i] = 'a'
	}
	req = submitJobRequest{
		Prompt: "test",
		Topic:  "job.default",
		Labels: map[string]string{"key": string(longValue)},
	}
	req.applyDefaults("tenant")
	if err := req.validate("tenant"); err == nil {
		t.Error("expected error for label value too long")
	}
}
