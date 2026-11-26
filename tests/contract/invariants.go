package contract

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// InvariantCatalog is the versioned invariant, entry-point and finding catalog.
type InvariantCatalog struct {
	SchemaVersion int             `yaml:"schema_version"`
	EntryPoints   []EntryPointDef `yaml:"entry_points"`
	Invariants    []Invariant
	Findings      []FindingRow
}

// EntryPointDef names an entry point through static repository anchors.
type EntryPointDef struct {
	ID      string
	Anchors []EnforcementPoint
}

// Invariant records a canonical statement, testable form and scoped references.
type Invariant struct {
	ID, Statement, Status string
	TestableForm          string   `yaml:"testable_form"`
	EntryPoints           []string `yaml:"entry_points"`
	Findings              []string
	Tests                 []TestRef
	TestScope             string `yaml:"test_scope"`
	Suites                []string
}

// FindingRow records only an id, open/fixed status and invariant ids.
type FindingRow struct {
	ID, Status string
	Invariants []string
}

// CatalogIssue identifies a catalog rule violation and its subject.
type CatalogIssue struct{ Rule, ID, Detail string }

var canonicalInvariants = map[string]string{
	"I01": "No authority, no protected effect.",
	"I02": "Unsupported restriction means deny, not unrestricted allow.",
	"I03": "Approval never overrides a hard prohibition.",
	"I04": "Different effective policy means different policy identity.",
	"I05": "The authorized inputs are the inputs actually used.",
	"I06": "Duplicate delivery does not imply completed processing.",
	"I07": "Revocation prevents future authorized effects within a published bound.",
	"I08": "Unknown outcome is never reported as success.",
	"I09": "Missing required evidence is never reported as verified.",
	"I10": "A new execution path cannot weaken existing authorization.",
}

var namedSuites = []string{
	"core/controlplane/safetykernel/kernel_invariants.go",
	"core/controlplane/safetykernel/kernel_invariants_test.go",
	"core/controlplane/safetykernel/global_invariants_integration_test.go",
	"core/controlplane/gateway/mcp_gate_invariants.go",
	"core/controlplane/gateway/mcp_gate_invariants_test.go",
	"core/controlplane/gateway/policybundles/invariants.go",
	"core/controlplane/gateway/policybundles/invariants_test.go",
	"core/controlplane/gateway/edge_evaluate_invariants_test.go",
}

// LoadInvariantCatalog rejects unknown keys, empty files and extra documents.
func LoadInvariantCatalog(file string) (*InvariantCatalog, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var c InvariantCatalog
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if err := dec.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: more than one YAML document", file)
	}
	return &c, nil
}

type catalogIssues []CatalogIssue

func (is *catalogIssues) check(bad bool, rule, id, detail string) {
	if bad {
		*is = append(*is, CatalogIssue{rule, id, detail})
	}
}

// Validate checks exact id sets, canonical statements, nonempty forms, static
// references, reciprocal finding maps, open-finding statuses and claim ids.
// Issues follow entry-point, invariant, finding, missing-id, suite, claim order.
func (c *InvariantCatalog) Validate(root string, reg *ClaimRegister) []CatalogIssue {
	var is catalogIssues
	is.check(c.SchemaVersion != 1, "schema-version", "catalog", "schema_version must be 1")
	points := c.validatePoints(root, &is)
	invs, rows := map[string]Invariant{}, map[string]FindingRow{}
	for _, inv := range c.Invariants {
		invs[inv.ID] = inv
	}
	for _, row := range c.Findings {
		rows[row.ID] = row
	}
	seen, linked := map[string]bool{}, map[string]bool{}
	for _, inv := range c.Invariants {
		_, known := canonicalInvariants[inv.ID]
		is.check(!known || seen[inv.ID], "invariant-set", inv.ID, "unexpected or duplicate id")
		seen[inv.ID] = true
		inv.validate(root, points, rows, &is)
		for _, suite := range inv.Suites {
			linked[suite] = true
		}
	}
	c.validateFindings(invs, &is)
	for n := 1; n <= 10; n++ {
		id := fmt.Sprintf("I%02d", n)
		is.check(!seen[id], "invariant-set", id, "missing invariant")
	}
	for n := 1; n <= 15; n++ {
		id := fmt.Sprintf("R%02d", n)
		_, found := rows[id]
		is.check(!found, "finding-set", id, "missing finding")
	}
	for _, suite := range namedSuites {
		is.check(!linked[suite], "suite-unlinked", suite, "named suite has no link")
	}
	if reg != nil {
		for _, claim := range reg.Claims {
			for _, id := range deref(claim.Invariants) {
				_, found := invs[id]
				is.check(!found, "claim-invariant-unknown", claim.ID, id)
			}
		}
	}
	return is
}

func (c *InvariantCatalog) validatePoints(root string, is *catalogIssues) map[string]bool {
	points := map[string]bool{}
	for _, p := range c.EntryPoints {
		is.check(len(p.Anchors) == 0 || points[p.ID], "entry-point-anchor", p.ID, "missing anchors or duplicate id")
		points[p.ID] = true
		for _, anchor := range p.Anchors {
			is.check(!hasSymbol(root, anchor.Path, anchor.Symbol), "entry-point-anchor", p.ID, anchor.Path+":"+anchor.Symbol)
		}
	}
	return points
}

func (inv Invariant) validate(root string, points map[string]bool, rows map[string]FindingRow, is *catalogIssues) {
	statement, known := canonicalInvariants[inv.ID]
	is.check(known && inv.Statement != statement, "invariant-statement", inv.ID, "statement differs from canonical text")
	is.check(strings.TrimSpace(inv.TestableForm) == "", "testable-form", inv.ID, "testable_form is empty")
	is.check(len(inv.EntryPoints) == 0, "entry-point-required", inv.ID, "at least one entry point required")
	for _, p := range inv.EntryPoints {
		is.check(!points[p], "entry-point-unknown", inv.ID, p)
	}
	open := false
	for _, row := range rows {
		open = open || row.Status == "open" && slices.Contains(row.Invariants, inv.ID)
	}
	for _, id := range inv.Findings {
		row, found := rows[id]
		is.check(!found || !slices.Contains(row.Invariants, inv.ID), "finding-map", id, inv.ID+" lacks reciprocal mapping")
	}
	badStatus := !slices.Contains([]string{"holds", "violated", "untested"}, inv.Status) ||
		inv.Status == "holds" && (open || len(inv.Tests) == 0) ||
		inv.Status == "violated" && !open || inv.Status == "untested" && open
	is.check(badStatus, "status", inv.ID, "status inconsistent with open findings or linked tests")
	for _, test := range inv.Tests {
		is.check(!hasTest(root, test.Package, test.Name), "test-missing", inv.ID, test.Package+":"+test.Name)
	}
	is.check(len(inv.Tests) > 0 && strings.TrimSpace(inv.TestScope) == "", "test-scope", inv.ID, "linked tests need test_scope")
	for _, suite := range inv.Suites {
		full, err := repoFile(root, suite)
		if err != nil {
			is.check(true, "suite-missing", inv.ID, err.Error())
			continue
		}
		st, err := os.Stat(full)
		is.check(err != nil || st.IsDir(), "suite-missing", inv.ID, suite)
	}
}

func (c *InvariantCatalog) validateFindings(invs map[string]Invariant, is *catalogIssues) {
	seen := map[string]bool{}
	for _, row := range c.Findings {
		is.check(!findRE.MatchString(row.ID) || seen[row.ID] || !slices.Contains([]string{"open", "fixed"}, row.Status), "finding-set", row.ID, "unexpected/duplicate id or invalid status")
		seen[row.ID] = true
		is.check(len(row.Invariants) == 0, "finding-map", row.ID, "finding has no invariant")
		for _, id := range row.Invariants {
			inv, found := invs[id]
			is.check(!found || !slices.Contains(inv.Findings, row.ID), "finding-map", row.ID, id+" lacks reciprocal mapping")
		}
	}
}
