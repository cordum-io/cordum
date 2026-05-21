package safetykernel

import (
	"slices"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestPolicyMetaFromRequest(t *testing.T) {
	req := &pb.PolicyCheckRequest{PrincipalId: "p1"}
	meta := policyMetaFromRequest(req)
	if meta.ActorID != "p1" {
		t.Fatalf("expected principal fallback")
	}

	req.Meta = &pb.JobMetadata{
		ActorId:        "a1",
		ActorType:      pb.ActorType_ACTOR_TYPE_SERVICE,
		IdempotencyKey: "idem",
		Capability:     "cap",
		RiskTags:       []string{"write"},
		Requires:       []string{"git"},
		PackId:         "pack",
	}
	meta = policyMetaFromRequest(req)
	if meta.ActorID != "a1" || meta.ActorType != "service" {
		t.Fatalf("unexpected meta: %#v", meta)
	}
	if meta.Capability != "cap" || meta.PackID != "pack" {
		t.Fatalf("unexpected meta fields")
	}
}

func TestSecretsPresent(t *testing.T) {
	labels := map[string]string{"secrets_present": "true"}
	if !secretsPresent(config.PolicyMeta{}, labels) {
		t.Fatalf("expected secrets present from label")
	}
	if secretsPresent(config.PolicyMeta{}, map[string]string{"secrets_present": "no"}) {
		t.Fatalf("expected secrets absent")
	}
	meta := config.PolicyMeta{RiskTags: []string{"secrets"}}
	if !secretsPresent(meta, nil) {
		t.Fatalf("expected secrets present from risk tags")
	}
}

func TestExtractMCPRequest(t *testing.T) {
	labels := map[string]string{
		"mcp.server":  "srv",
		"mcp_tool":    "tool",
		"mcpResource": "res",
		"mcp_action":  "READ",
	}
	req := extractMCPRequest(labels)
	if req.Server != "srv" || req.Tool != "tool" || req.Resource != "res" || req.Action != "read" {
		t.Fatalf("unexpected mcp request: %#v", req)
	}
}

func TestConstraintsHelpers(t *testing.T) {
	if !isConstraintsEmpty(config.PolicyConstraints{}) {
		t.Fatalf("expected empty constraints")
	}
	c := config.PolicyConstraints{
		Budgets: config.BudgetConstraints{MaxRuntimeMs: 1, MaxArtifactBytes: 2048},
		Sandbox: config.SandboxProfile{
			Isolated:   true,
			FsReadOnly: []string{"/workspace"},
		},
		Toolchain:      config.ToolchainConstraints{AllowedCommands: []string{"go test ./..."}},
		Diff:           config.DiffConstraints{MaxLines: 200, DenyPathGlobs: []string{"secrets/*"}},
		RedactionLevel: "strict",
	}
	proto := toProtoConstraints(c)
	if proto == nil || proto.GetBudgets().GetMaxRuntimeMs() != 1 || proto.GetBudgets().GetMaxArtifactBytes() != 2048 {
		t.Fatalf("unexpected policy-bundle budget constraints proto: %#v", proto)
	}
	if !proto.GetSandbox().GetIsolated() || !slices.Equal(proto.GetSandbox().GetFsReadOnly(), []string{"/workspace"}) {
		t.Fatalf("unexpected sandbox constraints proto: %#v", proto.GetSandbox())
	}
	if !slices.Equal(proto.GetToolchain().GetAllowedCommands(), []string{"go test ./..."}) {
		t.Fatalf("unexpected toolchain constraints proto: %#v", proto.GetToolchain())
	}
	if proto.GetDiff().GetMaxLines() != 200 || !slices.Equal(proto.GetDiff().GetDenyPathGlobs(), []string{"secrets/*"}) {
		t.Fatalf("unexpected diff constraints proto: %#v", proto.GetDiff())
	}
	if proto.GetRedactionLevel() != "strict" {
		t.Fatalf("redaction_level = %q, want strict", proto.GetRedactionLevel())
	}
}

func TestMatchHelpers(t *testing.T) {
	if !matchAny([]string{"job.*"}, "job.test") {
		t.Fatalf("expected match")
	}
	if configMatch("", "job.test") {
		t.Fatalf("expected no match for empty pattern")
	}
}

func TestParseBoolAndCombineSnapshots(t *testing.T) {
	if !parseBool("yes") || parseBool("no") {
		t.Fatalf("unexpected parseBool")
	}
	if combineSnapshots("a", "") != "a" {
		t.Fatalf("unexpected combine snapshots")
	}
	if combineSnapshots("a", "b") != "a|b" {
		t.Fatalf("unexpected combine snapshots")
	}
}
