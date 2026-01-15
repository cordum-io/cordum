package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordum/cordum/core/infra/buildinfo"
	wf "github.com/cordum/cordum/core/workflow"
)

func TestNormalizeWorkflowMapAndHash(t *testing.T) {
	workflow := map[string]any{
		"id":         "wf-1",
		"created_at": "ignore",
		"updated_at": "ignore",
	}
	normalized := normalizeWorkflowMap(workflow)
	if _, ok := normalized["created_at"]; ok {
		t.Fatalf("expected created_at to be removed")
	}
	if _, ok := normalized["updated_at"]; ok {
		t.Fatalf("expected updated_at to be removed")
	}
	hash1, err := hashWorkflow(workflow)
	if err != nil {
		t.Fatalf("hash workflow: %v", err)
	}
	hash2, err := hashWorkflow(normalized)
	if err != nil {
		t.Fatalf("hash workflow normalized: %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("expected hash to ignore created/updated")
	}
}

func TestWorkflowToMap(t *testing.T) {
	if got := workflowToMap(nil); len(got) != 0 {
		t.Fatalf("expected empty map for nil workflow")
	}
	workflow := &wf.Workflow{ID: "wf-2", OrgID: "org"}
	out := workflowToMap(workflow)
	if out["id"] != "wf-2" {
		t.Fatalf("expected workflow id in map: %#v", out)
	}
}

func TestBuildDeletePatch(t *testing.T) {
	patch := map[string]any{
		"topics": map[string]any{"job.pack.topic": map[string]any{"timeout": 10}},
		"pools":  map[string]any{"pack.pool": map[string]any{"requires": []any{"gpu"}}},
	}
	out := buildDeletePatch(patch)
	if out["topics"] == nil || out["pools"] == nil {
		t.Fatalf("expected delete patch entries")
	}
}

func TestCanonicalJSONStable(t *testing.T) {
	a := map[string]any{"b": 2, "a": 1, "list": []any{"x", "y"}}
	b := map[string]any{"a": 1, "b": 2, "list": []any{"x", "y"}}
	hashA, err := hashValue(a)
	if err != nil {
		t.Fatalf("hash value: %v", err)
	}
	hashB, err := hashValue(b)
	if err != nil {
		t.Fatalf("hash value: %v", err)
	}
	if hashA != hashB {
		t.Fatalf("expected stable hash for map order")
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
		"topics": map[string]any{"job.pack1.ok": map[string]any{}},
	}
	if err := validateTimeoutsPatch(patch, "pack1"); err != nil {
		t.Fatalf("expected valid timeouts patch: %v", err)
	}
}

func TestNormalizeDecision(t *testing.T) {
	cases := map[string]string{
		"allow":                  "ALLOW",
		"DENY":                   "DENY",
		"require_human":          "REQUIRE_APPROVAL",
		"allow_with_constraints": "ALLOW_WITH_CONSTRAINTS",
		"throttle":               "THROTTLE",
	}
	for raw, expect := range cases {
		if got := normalizeDecision(raw); got != expect {
			t.Fatalf("decision %s expected %s got %s", raw, expect, got)
		}
	}
}

func TestPackPathHelpers(t *testing.T) {
	dir := t.TempDir()
	if got := isTarGz("pack.tgz"); !got {
		t.Fatalf("expected tgz suffix")
	}
	if got := isTarGz("pack.tar.gz"); !got {
		t.Fatalf("expected tar.gz suffix")
	}
	if isTarGz("pack.zip") {
		t.Fatalf("did not expect zip to match")
	}

	packPath := filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(packPath, []byte("id: test"), 0o600); err != nil {
		t.Fatalf("write pack.yaml: %v", err)
	}
	root, err := findPackRoot(dir)
	if err != nil || root != dir {
		t.Fatalf("expected pack root at dir, got %s err=%v", root, err)
	}

	nested := filepath.Join(t.TempDir(), "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "pack.yml"), []byte("id: test"), 0o600); err != nil {
		t.Fatalf("write pack.yml: %v", err)
	}
	parent := filepath.Dir(nested)
	root, err = findPackRoot(parent)
	if err != nil || root != nested {
		t.Fatalf("expected nested pack root, got %s err=%v", root, err)
	}
}

func TestSemverHelpers(t *testing.T) {
	if _, ok := parseSemver("1"); ok {
		t.Fatalf("expected short semver invalid")
	}
	if _, ok := parseSemver("v1.2.3"); !ok {
		t.Fatalf("expected valid semver")
	}
	if compareSemver([3]int{1, 2, 3}, [3]int{1, 2, 4}) != -1 {
		t.Fatalf("expected compare semver less")
	}
	if compareSemver([3]int{2, 0, 0}, [3]int{1, 9, 9}) != 1 {
		t.Fatalf("expected compare semver greater")
	}

	orig := buildinfo.Version
	buildinfo.Version = "1.2.0"
	t.Cleanup(func() { buildinfo.Version = orig })

	if err := ensureCoreVersionCompatible("1.3.0"); err == nil {
		t.Fatalf("expected minCoreVersion error")
	}
	if err := ensureCoreVersionCompatible("1.2.0"); err != nil {
		t.Fatalf("expected minCoreVersion ok: %v", err)
	}
	if err := ensureCoreVersionCompatible("bad"); err == nil {
		t.Fatalf("expected invalid minCoreVersion error")
	}
}
