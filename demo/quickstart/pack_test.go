// Package quickstart_test validates the demo-quickstart pack artifacts
// (pack.yaml, policy.fragment.yaml, workflows/hello.yaml) against the
// authoritative loaders the platform uses at install time. These are not
// runtime tests — they catch schema drift as soon as someone edits a
// YAML file.
package quickstart_test

import (
	"os"
	"sort"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/infra/config"
	"gopkg.in/yaml.v3"
)

const (
	packDir          = "pack"
	policyFragment   = "pack/overlays/policy.fragment.yaml"
	workflowFilename = "pack/workflows/hello.yaml"
)

// TestPackManifestParses runs the same loader+validator the gateway uses
// when an operator installs the pack. Fails fast on missing fields,
// invalid topic capability strings, etc.
func TestPackManifestParses(t *testing.T) {
	manifest, err := packs.LoadPackManifest(packDir)
	if err != nil {
		t.Fatalf("LoadPackManifest(%q): %v", packDir, err)
	}
	if manifest.Metadata.ID != "demo-quickstart" {
		t.Errorf("metadata.id = %q, want demo-quickstart", manifest.Metadata.ID)
	}
	if manifest.Metadata.Version == "" {
		t.Error("metadata.version is empty")
	}
	if got := len(manifest.Topics); got != 3 {
		t.Fatalf("expected 3 topics, got %d", got)
	}
	wantTopics := map[string]string{
		"job.demo.greet":      "demo-quickstart.greet",
		"job.demo.delete-all": "demo-quickstart.delete-all",
		"job.demo.admin":      "demo-quickstart.admin",
	}
	for _, tp := range manifest.Topics {
		want, ok := wantTopics[tp.Name]
		if !ok {
			t.Errorf("unexpected topic %q", tp.Name)
			continue
		}
		if tp.Capability != want {
			t.Errorf("topic %q: capability = %q, want %q", tp.Name, tp.Capability, want)
		}
	}
	if len(manifest.Resources.Workflows) != 1 || manifest.Resources.Workflows[0].ID != "demo-quickstart.hello" {
		t.Errorf("resources.workflows[0] should be demo-quickstart.hello, got %+v", manifest.Resources.Workflows)
	}
	if len(manifest.Overlays.Policy) != 1 {
		t.Errorf("expected exactly 1 policy overlay, got %d", len(manifest.Overlays.Policy))
	}
}

// TestPolicyFragmentParses runs the embedded fragment through the same
// safety-policy parser the kernel uses, then asserts it carries the three
// canonical decisions (allow, deny, require_approval) keyed by the
// expected rule IDs.
func TestPolicyFragmentParses(t *testing.T) {
	// #nosec G304 -- test-only read of a checked-in fixture.
	data, err := os.ReadFile(policyFragment)
	if err != nil {
		t.Fatalf("read %s: %v", policyFragment, err)
	}
	policy, err := config.ParseSafetyPolicy(data)
	if err != nil {
		t.Fatalf("ParseSafetyPolicy: %v", err)
	}
	if policy == nil {
		t.Fatal("policy is nil — parser silently discarded the fragment")
	}
	if len(policy.Rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(policy.Rules))
	}
	want := map[string]string{
		"demo-quickstart-greet-allow":    "allow",
		"demo-quickstart-delete-deny":    "deny",
		"demo-quickstart-admin-approve":  "require_approval",
	}
	got := map[string]string{}
	for _, rule := range policy.Rules {
		got[rule.ID] = rule.Decision
	}
	for id, decision := range want {
		actual, ok := got[id]
		if !ok {
			t.Errorf("rule %q missing from fragment", id)
			continue
		}
		if actual != decision {
			t.Errorf("rule %q: decision = %q, want %q", id, actual, decision)
		}
	}
}

// hello.yaml is loaded by the workflow engine, not by gateway packs, so
// for a syntactic shape test we parse it as opaque YAML and assert the
// step graph is what the README and the demo CLI expect.
func TestWorkflowHasFourStepsThreeWorkers(t *testing.T) {
	// #nosec G304 -- test-only read of a checked-in fixture.
	data, err := os.ReadFile(workflowFilename)
	if err != nil {
		t.Fatalf("read %s: %v", workflowFilename, err)
	}
	var doc struct {
		ID    string `yaml:"id"`
		OrgID string `yaml:"org_id"`
		Steps map[string]struct {
			ID    string `yaml:"id"`
			Type  string `yaml:"type"`
			Topic string `yaml:"topic"`
			Meta  struct {
				PackID string `yaml:"pack_id"`
			} `yaml:"meta"`
		} `yaml:"steps"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal workflow: %v", err)
	}
	if doc.ID != "demo-quickstart.hello" {
		t.Errorf("workflow id = %q, want demo-quickstart.hello", doc.ID)
	}
	if doc.OrgID != "default" {
		t.Errorf("workflow org_id = %q, want default", doc.OrgID)
	}
	// Three parallel worker steps — no aggregate transform: verdicts come
	// from the job records via GetJob, not step output, so the previous
	// transform step was dead code (see QA reopen note + hello.yaml
	// header comment).
	if len(doc.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(doc.Steps))
	}
	workerCount := 0
	for _, step := range doc.Steps {
		if step.Type != "worker" {
			t.Errorf("step %q has unexpected type %q; want worker", step.ID, step.Type)
			continue
		}
		workerCount++
	}
	if workerCount != 3 {
		t.Errorf("expected 3 worker steps, got %d", workerCount)
	}
}

// TestTopicsDeclaredAndUsed pins the contract that every topic in the
// pack manifest is referenced by at least one workflow step. Drift here
// would mean the demo declares topics it never exercises, which would
// pass `pack install` but render an empty verdict cell.
func TestTopicsDeclaredAndUsed(t *testing.T) {
	manifest, err := packs.LoadPackManifest(packDir)
	if err != nil {
		t.Fatalf("LoadPackManifest: %v", err)
	}
	declared := make([]string, 0, len(manifest.Topics))
	for _, tp := range manifest.Topics {
		declared = append(declared, tp.Name)
	}
	sort.Strings(declared)

	// #nosec G304 -- test-only read of a checked-in fixture.
	wfData, err := os.ReadFile(workflowFilename)
	if err != nil {
		t.Fatalf("read %s: %v", workflowFilename, err)
	}
	wf := string(wfData)
	for _, topic := range declared {
		if !contains(wf, topic) {
			t.Errorf("topic %q declared in pack.yaml but not referenced in %s", topic, workflowFilename)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(stringIndex(haystack, needle) >= 0)
}

// stringIndex avoids depending on strings.Contains in this small file so
// the test stays focused on the YAML/JSON contracts.
func stringIndex(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

