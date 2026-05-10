package policy

import (
	"encoding/json"
	"errors"
	"testing"
)

// fixtureInputRule mirrors the shape `policybundles.RulesFromPolicyContent`
// produces for input bundles: id + decision + match + reason + tier +
// source. Keeping the fixture as map[string]any (not a typed struct) is
// deliberate — the translator must accept the live wire shape that
// flows through the gateway, not a schema we control.
func fixtureInputRule() map[string]any {
	return map[string]any{
		"id":       "block-aws-secret",
		"decision": "deny",
		"reason":   "AWS secret key matched",
		"match": map[string]any{
			"topics":   []any{"job.acme.evaluate"},
			"keywords": []any{"AKIA"},
		},
		"tier":     "global",
		"selector": map[string]any{"tenants": []any{"tenant-acme"}},
	}
}

func fixtureOutputRule() map[string]any {
	return map[string]any{
		"id":       "redact-pii",
		"decision": "redact",
		"reason":   "PII detected in output",
		"match": map[string]any{
			"scanners": []any{"pii"},
		},
	}
}

func fixturePackSource() *PackSource {
	return &PackSource{
		FragmentID:  "cordclaw/safety",
		Tier:        "global",
		PackID:      "cordclaw",
		OverlayName: "safety",
		Version:     "1.2.0",
		InstalledAt: "2026-05-09T10:00:00Z",
		Sha256:      "sha256:abc123",
	}
}

func TestLegacyMapToUnifiedRuleInputBucket(t *testing.T) {
	got, err := LegacyMapToUnifiedRule(fixtureInputRule(), RuleTypeInput, fixturePackSource())
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if got.ID != "block-aws-secret" {
		t.Errorf("ID = %q, want block-aws-secret", got.ID)
	}
	if got.Type != RuleTypeInput {
		t.Errorf("Type = %q, want input", got.Type)
	}
	// Name defaults to ID when legacy doesn't carry a name.
	if got.Name != "block-aws-secret" {
		t.Errorf("Name = %q, want id-as-name fallback", got.Name)
	}
	// Status defaults to published — YAML rules ARE production state.
	if got.Status != RuleStatusPublished {
		t.Errorf("Status = %q, want published default", got.Status)
	}
	// Description falls back to reason when not explicitly set.
	if got.Description != "AWS secret key matched" {
		t.Errorf("Description = %q, want reason-as-description", got.Description)
	}
	// Scope.Kind from legacy `tier`.
	if got.Scope.Kind != RuleScopeGlobal {
		t.Errorf("Scope.Kind = %q, want global", got.Scope.Kind)
	}
	// Match payload preserved as raw JSON (lossless round-trip).
	var matchOut map[string]any
	if err := json.Unmarshal(got.Match, &matchOut); err != nil {
		t.Fatalf("unmarshal match: %v", err)
	}
	if topics, _ := matchOut["topics"].([]any); len(topics) != 1 || topics[0] != "job.acme.evaluate" {
		t.Errorf("match.topics = %v, want [job.acme.evaluate]", matchOut["topics"])
	}
	// Decide synthesized from legacy `decision`.
	var decideOut map[string]any
	if err := json.Unmarshal(got.Decide, &decideOut); err != nil {
		t.Fatalf("unmarshal decide: %v", err)
	}
	if decideOut["decision"] != "deny" {
		t.Errorf("decide.decision = %v, want deny", decideOut["decision"])
	}
	// Pack source attribution lossless.
	if got.Audit.PackSource == nil {
		t.Fatal("PackSource missing on Audit")
	}
	if got.Audit.PackSource.PackID != "cordclaw" {
		t.Errorf("PackSource.PackID = %q, want cordclaw", got.Audit.PackSource.PackID)
	}
	if got.Audit.PackSource.Sha256 != "sha256:abc123" {
		t.Errorf("PackSource.Sha256 = %q, want sha256:abc123", got.Audit.PackSource.Sha256)
	}
}

func TestLegacyMapToUnifiedRuleOutputBucket(t *testing.T) {
	got, err := LegacyMapToUnifiedRule(fixtureOutputRule(), RuleTypeOutput, fixturePackSource())
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if got.Type != RuleTypeOutput {
		t.Errorf("Type = %q, want output", got.Type)
	}
	if got.ID != "redact-pii" {
		t.Errorf("ID = %q, want redact-pii", got.ID)
	}
	var decideOut map[string]any
	_ = json.Unmarshal(got.Decide, &decideOut)
	if decideOut["decision"] != "redact" {
		t.Errorf("decide.decision = %v, want redact", decideOut["decision"])
	}
}

func TestLegacyMapToUnifiedRulePreservesExplicitName(t *testing.T) {
	src := fixtureInputRule()
	src["name"] = "Block AWS Secrets" // explicit human name
	got, err := LegacyMapToUnifiedRule(src, RuleTypeInput, nil)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if got.Name != "Block AWS Secrets" {
		t.Errorf("Name = %q, want explicit name preserved", got.Name)
	}
}

func TestLegacyMapToUnifiedRuleRequiresID(t *testing.T) {
	src := map[string]any{"decision": "deny"}
	_, err := LegacyMapToUnifiedRule(src, RuleTypeInput, nil)
	if !errors.Is(err, ErrInvalidLegacyRule) {
		t.Errorf("err = %v, want ErrInvalidLegacyRule for missing id", err)
	}
}

func TestLegacyMapToUnifiedRuleRejectsInvalidType(t *testing.T) {
	_, err := LegacyMapToUnifiedRule(fixtureInputRule(), RuleType("bogus"), nil)
	if !errors.Is(err, ErrInvalidRuleType) {
		t.Errorf("err = %v, want ErrInvalidRuleType", err)
	}
}

func TestLegacyMapToUnifiedRuleScopeFromTier(t *testing.T) {
	// Tier=tenant + selector.tenants → Scope{Kind: tenant, Value: "tenant-acme"}.
	src := fixtureInputRule()
	src["tier"] = "tenant"
	got, err := LegacyMapToUnifiedRule(src, RuleTypeInput, nil)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if got.Scope.Kind != RuleScopeTenant {
		t.Errorf("Scope.Kind = %q, want tenant", got.Scope.Kind)
	}
	if got.Scope.Value != "tenant-acme" {
		t.Errorf("Scope.Value = %q, want tenant-acme", got.Scope.Value)
	}
}

func TestLegacyMapToUnifiedRuleNilSourceLeavesPackSourceNil(t *testing.T) {
	got, err := LegacyMapToUnifiedRule(fixtureInputRule(), RuleTypeInput, nil)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if got.Audit.PackSource != nil {
		t.Errorf("PackSource = %+v, want nil when no pack source supplied", got.Audit.PackSource)
	}
}

// VelocityResponseToUnifiedRule covers the velocity bucket which goes
// through a different code path (`velocityRuleFromBundle`); the
// integration is at the handler layer. Here we test the shape contract.
func TestVelocityShapeMaterializesAsType(t *testing.T) {
	// Velocity rules carry a `velocity` payload distinct from input/output;
	// the translator embeds it under match.velocity so downstream
	// evaluators can branch by type without re-parsing.
	src := map[string]any{
		"id":       "rate-limit-ingest",
		"decision": "throttle",
		"reason":   "ingest rate too high",
		"match":    map[string]any{"topics": []any{"job.ingest"}},
		"velocity": map[string]any{
			"max_requests":   100,
			"window_seconds": 60,
			"key":            "tenant_id",
		},
	}
	got, err := LegacyMapToUnifiedRule(src, RuleTypeVelocity, nil)
	if err != nil {
		t.Fatalf("translate velocity: %v", err)
	}
	if got.Type != RuleTypeVelocity {
		t.Errorf("Type = %q, want velocity", got.Type)
	}
	var matchOut map[string]any
	if err := json.Unmarshal(got.Match, &matchOut); err != nil {
		t.Fatalf("unmarshal match: %v", err)
	}
	velocity, ok := matchOut["velocity"].(map[string]any)
	if !ok {
		t.Fatalf("match.velocity = %v, want embedded map", matchOut["velocity"])
	}
	if velocity["max_requests"].(float64) != 100 {
		t.Errorf("velocity.max_requests = %v, want 100", velocity["max_requests"])
	}
}
