package contract

import (
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

func TestInvariantsValidatorRejects(t *testing.T) {
	for _, tc := range []struct {
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
	} {
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "invariants.yaml")
			write(t, p, tc.text)
			if _, err := LoadInvariantCatalog(p); err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error: %v", err)
			}
		})
	}
}
