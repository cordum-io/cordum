package contract

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func invariantsFixture(t *testing.T) (string, *InvariantCatalog) {
	t.Helper()
	root, err := repoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadInvariantCatalog(filepath.Join(root, "docs/contract/invariants.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return root, c
}

func TestInvariantsCatalog(t *testing.T) {
	root, c := invariantsFixture(t)
	r, err := LoadClaimRegister(filepath.Join(root, "docs/contract/claims.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Validate(root, r); len(got) != 0 {
		t.Fatalf("issues: %+v", got)
	}
	if len(c.Invariants) != 10 || len(c.Findings) != 15 {
		t.Fatalf("counts: %d/%d", len(c.Invariants), len(c.Findings))
	}
}

var invariantRejectCases = []struct {
	name   string
	mutate func(*InvariantCatalog, *ClaimRegister)
	want   string
}{
	{"dangling_test", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Invariants[2].Tests = append(c.Invariants[2].Tests, TestRef{"core/controlplane/gateway", "TestInvariantsNoSuchTest"})
	}, "test-missing:I03"},
	{"dangling_claim_ref", func(_ *InvariantCatalog, r *ClaimRegister) {
		ids := []string{"I11"}
		r.Claims = []Claim{{ID: "CLM-900", Invariants: &ids}}
	}, "claim-invariant-unknown:CLM-900"},
	{"holds_with_open_finding", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants[2].Status = "holds" }, "status:I03"},
	{"open_finding_not_violated", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants[0].Status = "untested" }, "status:I01"},
	{"violated_without_open_finding", func(c *InvariantCatalog, _ *ClaimRegister) { c.Findings[14].Status = "fixed" }, "status:I10"},
	{"finding_unmapped", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Findings[12].Invariants = nil
		c.Invariants[0].Findings = []string{"R02"}
	}, "finding-map:R13"},
	{"finding_mismatch", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants[2].Findings = []string{"R09"} }, "finding-map:R05"},
	{"finding_missing", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Findings = slices.Delete(c.Findings, 7, 8)
		c.Invariants[1].Findings = []string{"R01", "R07"}
	}, "finding-set:R08"},
	{"invariant_renamed", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Invariants[9].ID = "I11"
		c.Findings[14].Invariants = []string{"I11"}
	}, "invariant-set:I10 invariant-set:I11"},
	{"statement_changed", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants[3].Statement += "!" }, "invariant-statement:I04"},
	{"testable_form_missing", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants[5].TestableForm = " " }, "testable-form:I06"},
	{"entry_point_required", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants[6].EntryPoints = nil }, "entry-point-required:I07"},
	{"entry_point_unknown", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Invariants[4].EntryPoints = append(c.Invariants[4].EntryPoints, "no-such-entry")
	}, "entry-point-unknown:I05"},
	{"anchor_unresolved", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.EntryPoints[0].Anchors[0].Symbol = "Evaluator.NoSuchMethod"
	}, "entry-point-anchor:edge-hook"},
	{"test_scope_missing", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants[3].TestScope = "" }, "test-scope:I04"},
	{"suite_missing", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Invariants[9].Suites = append(c.Invariants[9].Suites, "core/controlplane/gateway/no_such_suite.go")
	}, "suite-missing:I10"},
	{"suite_unlinked", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Invariants[9].Suites = slices.DeleteFunc(c.Invariants[9].Suites, func(s string) bool { return strings.HasSuffix(s, "/edge_evaluate_invariants_test.go") })
	}, "suite-unlinked:core/controlplane/gateway/edge_evaluate_invariants_test.go"},
	{"schema_version", func(c *InvariantCatalog, _ *ClaimRegister) { c.SchemaVersion = 2 }, "schema-version:catalog"},
	{"duplicate_invariant", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants = append(c.Invariants, c.Invariants[0]) }, "invariant-set:I01"},
	{"duplicate_point", func(c *InvariantCatalog, _ *ClaimRegister) { c.EntryPoints = append(c.EntryPoints, c.EntryPoints[0]) }, "entry-point-anchor:edge-hook"},
	{"missing_anchor", func(c *InvariantCatalog, _ *ClaimRegister) { c.EntryPoints[0].Anchors = nil }, "entry-point-anchor:edge-hook"},
	{"invalid_anchor_path", func(c *InvariantCatalog, _ *ClaimRegister) { c.EntryPoints[0].Anchors[0].Path = "../escape.go" }, "entry-point-anchor:edge-hook"},
	{"duplicate_finding", func(c *InvariantCatalog, _ *ClaimRegister) { c.Findings = append(c.Findings, c.Findings[0]) }, "finding-set:R01"},
	{"invalid_finding_status", func(c *InvariantCatalog, _ *ClaimRegister) { c.Findings[0].Status = "unknown" }, "finding-set:R01"},
	{"unexpected_finding", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Findings = append(c.Findings, FindingRow{"R16", "fixed", []string{"I01"}})
		c.Invariants[0].Findings = append(c.Invariants[0].Findings, "R16")
	}, "finding-set:R16"},
	{"finding_backcheck", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Invariants[0].Findings = append(c.Invariants[0].Findings, "R01")
	}, "finding-map:R01"},
	{"invalid_status", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants[0].Status = "unknown" }, "status:I01"},
	{"holds_without_tests", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Findings[1].Status = "fixed"
		c.Findings[12].Status = "fixed"
		c.Invariants[0].Status = "holds"
	}, "status:I01"},
	{"suite_parent_path", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants[0].Suites = []string{"../escape.go"} }, "suite-missing:I01"},
	{"suite_backslash", func(c *InvariantCatalog, _ *ClaimRegister) {
		c.Invariants[0].Suites = []string{`core\controlplane\gateway\mcp_gate_invariants.go`}
	}, "suite-missing:I01"},
	{"suite_directory", func(c *InvariantCatalog, _ *ClaimRegister) { c.Invariants[0].Suites = []string{"core"} }, "suite-missing:I01"},
}

func TestInvariantsValidatorRejects(t *testing.T) {
	for _, tc := range invariantRejectCases {
		t.Run(tc.name, func(t *testing.T) {
			root, c := invariantsFixture(t)
			r := &ClaimRegister{}
			tc.mutate(c, r)
			var got []string
			for _, issue := range c.Validate(root, r) {
				got = append(got, issue.Rule+":"+issue.ID)
			}
			slices.Sort(got)
			if strings.Join(got, " ") != tc.want {
				t.Fatalf("got %v, want %s", got, tc.want)
			}
		})
	}
}

func TestInvariantsLoadRejects(t *testing.T) {
	for _, tc := range []struct{ name, text, contains string }{
		{"unknown_key", "schema_version: 1\nentry_point: []\n", "entry_point"},
		{"second_document", "schema_version: 1\n---\nschema_version: 1\n", "more than one"},
		{"empty", "", "EOF"},
		{"duplicate_key", "schema_version: 1\nschema_version: 1\n", "already defined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "invariants.yaml")
			write(t, p, tc.text)
			if _, err := LoadInvariantCatalog(p); err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error: %v", err)
			}
		})
	}
	if _, err := LoadInvariantCatalog(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestInvariantsFormatsAndStatuses(t *testing.T) {
	root, c := invariantsFixture(t)
	b, err := os.ReadFile(filepath.Join(root, "docs/contract/invariants.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	for _, text := range []string{string(b), "\ufeff" + string(b), strings.ReplaceAll(string(b), "\n", "\r\n")} {
		file := filepath.Join(t.TempDir(), "invariants.yaml")
		write(t, file, text)
		loaded, err := LoadInvariantCatalog(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := loaded.Validate(root, nil); len(got) != 0 {
			t.Fatalf("format/cwd: %+v", got)
		}
	}
	r := &ClaimRegister{Claims: []Claim{{ID: "CLM-900"}}}
	c.Findings[1].Status, c.Findings[12].Status = "fixed", "fixed"
	c.Invariants[0].Status = "untested"
	c.Findings[4].Status, c.Findings[8].Status = "fixed", "fixed"
	c.Invariants[2].Status, c.Invariants[6].Status = "holds", "untested"
	if got := c.Validate(root, r); len(got) != 0 {
		t.Fatalf("fixed findings/nil invariants: %+v", got)
	}
}

func TestInvariantsIssueOrder(t *testing.T) {
	root, c := invariantsFixture(t)
	c.EntryPoints[0].Anchors = nil
	c.Invariants[3].Statement += "!"
	c.Findings[7].Status = "unknown"
	ids := []string{"I11"}
	r := &ClaimRegister{Claims: []Claim{{ID: "CLM-900", Invariants: &ids}}}
	want := []string{"entry-point-anchor:edge-hook", "invariant-statement:I04", "finding-set:R08", "claim-invariant-unknown:CLM-900"}
	for n := 0; n < 3; n++ {
		var got []string
		for _, issue := range c.Validate(root, r) {
			got = append(got, issue.Rule+":"+issue.ID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("order: got %v, want %v", got, want)
		}
	}
}
