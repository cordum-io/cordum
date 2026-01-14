package main

import (
	"testing"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
)

func TestValidatePackManifest(t *testing.T) {
	if err := validatePackManifest(nil); err == nil {
		t.Fatalf("expected error for nil manifest")
	}
	manifest := &packManifest{}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for missing metadata")
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "Bad ID", Version: "1.0.0"},
	}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for invalid id")
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: ""},
	}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for missing version")
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: "1.0.0"},
		Topics:   []packTopic{{Name: "job.other.topic"}},
	}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for un-namespaced topic")
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: "1.0.0"},
		Topics:   []packTopic{{Name: "job.pack1.topic"}},
		Resources: packResources{
			Schemas:   []packResource{{ID: "schema", Path: "schemas/a.json"}},
			Workflows: []packResource{{ID: "workflow", Path: "workflows/a.json"}},
		},
	}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for un-namespaced resources")
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: "1.0.0"},
		Topics:   []packTopic{{Name: "job.pack1.topic"}},
		Resources: packResources{
			Schemas:   []packResource{{ID: "pack1/schema", Path: "schemas/a.json"}},
			Workflows: []packResource{{ID: "pack1.workflow", Path: "workflows/a.json"}},
		},
	}
	if err := validatePackManifest(manifest); err != nil {
		t.Fatalf("expected valid manifest: %v", err)
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: "1.0.0"},
		Compatibility: packCompatibility{
			MinCoreVersion: "not-a-version",
		},
		Topics: []packTopic{{Name: "job.pack1.topic"}},
		Resources: packResources{
			Schemas:   []packResource{{ID: "pack1/schema", Path: "schemas/a.json"}},
			Workflows: []packResource{{ID: "pack1.workflow", Path: "workflows/a.json"}},
		},
	}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for invalid minCoreVersion")
	}

	manifest.Compatibility.MinCoreVersion = "0.6.0"
	manifest.Compatibility.MaxCoreVersion = "1.2.3"
	if err := validatePackManifest(manifest); err != nil {
		t.Fatalf("expected valid core version constraints: %v", err)
	}
}

func TestEnsureProtocolCompatible(t *testing.T) {
	manifest := &packManifest{}
	if err := ensureProtocolCompatible(manifest); err == nil {
		t.Fatalf("expected error for missing protocol")
	}
	manifest.Compatibility.ProtocolVersion = capsdk.DefaultProtocolVersion + 1
	if err := ensureProtocolCompatible(manifest); err == nil {
		t.Fatalf("expected error for mismatched protocol")
	}
	manifest.Compatibility.ProtocolVersion = capsdk.DefaultProtocolVersion
	if err := ensureProtocolCompatible(manifest); err != nil {
		t.Fatalf("expected protocol match: %v", err)
	}
}

func TestOverlayHelpers(t *testing.T) {
	overlay := packConfigOverlay{Key: "pools"}
	if !shouldSkipConfigOverlay(true, overlay) {
		t.Fatalf("expected pools overlay to be skipped when inactive")
	}
	if shouldSkipConfigOverlay(false, overlay) {
		t.Fatalf("did not expect skip when active")
	}
	overlays := []packAppliedConfigOverlay{{Key: "timeouts"}, {Key: "pools"}}
	if !hasPoolOverlay(overlays) {
		t.Fatalf("expected pool overlay detected")
	}
}

func TestPolicyFragmentID(t *testing.T) {
	if got := policyFragmentID("pack1", ""); got != "pack1/default" {
		t.Fatalf("unexpected fragment id: %s", got)
	}
	if got := policyFragmentID("pack1", "custom"); got != "pack1/custom" {
		t.Fatalf("unexpected fragment id: %s", got)
	}
}

func TestNormalizeDecision(t *testing.T) {
	cases := map[string]string{
		"allow":                  "ALLOW",
		"DECISION_TYPE_DENY":     "DENY",
		"require_human":          "REQUIRE_APPROVAL",
		"allow_with_constraints": "ALLOW_WITH_CONSTRAINTS",
		"DECISION_TYPE_THROTTLE": "THROTTLE",
		"custom":                 "CUSTOM",
	}
	for raw, expect := range cases {
		if got := normalizeDecision(raw); got != expect {
			t.Fatalf("decision %s expected %s got %s", raw, expect, got)
		}
	}
}

func TestRecordsToAny(t *testing.T) {
	records := map[string]packRecord{
		"pack1": {ID: "pack1", Version: "1.0.0", Status: "ACTIVE"},
	}
	out := recordsToAny(records)
	if _, ok := out["pack1"]; !ok {
		t.Fatalf("expected pack record in map")
	}
}

func TestValidatePoolsPatch(t *testing.T) {
	patch := map[string]any{
		"topics": map[string]any{"job.bad": map[string]any{}},
	}
	if err := validatePoolsPatch(patch, "pack1", nil); err == nil {
		t.Fatalf("expected namespacing error")
	}

	patch = map[string]any{
		"pools": map[string]any{"shared": map[string]any{}},
	}
	if err := validatePoolsPatch(patch, "pack1", nil); err == nil {
		t.Fatalf("expected pool namespacing error")
	}

	current := map[string]any{"pools": map[string]any{"shared": map[string]any{}}}
	if err := validatePoolsPatch(patch, "pack1", current); err != nil {
		t.Fatalf("expected existing pool to be allowed: %v", err)
	}

	patch = map[string]any{"topics": map[string]any{"job.pack1.ok": map[string]any{}}, "extra": 1}
	if err := validatePoolsPatch(patch, "pack1", nil); err == nil {
		t.Fatalf("expected unsupported key error")
	}
}

func TestValidateTimeoutsPatch(t *testing.T) {
	patch := map[string]any{
		"topics": map[string]any{"job.bad": map[string]any{}},
	}
	if err := validateTimeoutsPatch(patch, "pack1"); err == nil {
		t.Fatalf("expected namespacing error")
	}

	patch = map[string]any{
		"workflows": map[string]any{"bad.workflow": map[string]any{}},
	}
	if err := validateTimeoutsPatch(patch, "pack1"); err == nil {
		t.Fatalf("expected workflow namespacing error")
	}

	patch = map[string]any{"topics": map[string]any{"job.pack1.ok": map[string]any{}}, "extra": 1}
	if err := validateTimeoutsPatch(patch, "pack1"); err == nil {
		t.Fatalf("expected unsupported key error")
	}

	patch = map[string]any{
		"topics":    map[string]any{"job.pack1.ok": map[string]any{}},
		"workflows": map[string]any{"pack1.workflow": map[string]any{}},
	}
	if err := validateTimeoutsPatch(patch, "pack1"); err != nil {
		t.Fatalf("expected valid timeouts patch: %v", err)
	}
}

func TestBuildDeletePatch(t *testing.T) {
	patch := map[string]any{
		"topics": map[string]any{"job.pack1.ok": map[string]any{"timeout": 10}},
		"pools":  map[string]any{"pack1.pool": map[string]any{"requires": []any{"gpu"}}},
	}
	out := buildDeletePatch(patch)
	topics, ok := out["topics"].(map[string]any)
	if !ok || topics["job.pack1.ok"] == nil {
		t.Fatalf("expected delete patch for topics")
	}
}
