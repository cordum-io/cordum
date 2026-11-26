package contract

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// ClaimRegister is docs/contract/claims.yaml. Claim list fields are pointers, so
// a missing key is told apart from an empty list.
type ClaimRegister struct {
	SchemaVersion     int    `yaml:"schema_version"`
	SourcesReviewedAt string `yaml:"sources_reviewed_at"`
	Claims            []Claim
}

// Claim is one register entry; every key must be present.
type Claim struct {
	ID, Statement, Boundary, Status, Evidence string
	Invariants, Findings                      *[]string
	EnforcementPoints                         *[]EnforcementPoint `yaml:"enforcement_points"`
	Tests                                     *[]TestRef
	PublicSources                             *[]string `yaml:"public_sources"`
}

// EnforcementPoint is a repo-relative file plus a symbol: a top-level
// declaration or Type.Method in a Go file, else a literal anchor in the file.
type EnforcementPoint struct{ Path, Symbol string }

// TestRef is a package directory plus a top-level Test function in it.
type TestRef struct{ Package, Name string }

// Issue is one rule violation; ClaimID is empty for register-level rules.
type Issue struct{ Rule, ClaimID, Detail string }

type issues []Issue

func (is *issues) add(rule, id, detail string) { *is = append(*is, Issue{rule, id, detail}) }

var (
	statuses = []string{"planned", "supported", "enforced", "verified", "unsupported"}
	idRE     = regexp.MustCompile(`^CLM-[0-9]{3}$`)
	invRE    = regexp.MustCompile(`^I(0[1-9]|10)$`)
	findRE   = regexp.MustCompile(`^R(0[1-9]|1[0-5])$`)
	shaRE    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	srcRE    = regexp.MustCompile(`^(cordum|Cordum-site):([^:]+):[1-9][0-9]*$`)
)

// LoadClaimRegister reads one YAML document and rejects unknown keys, an empty
// file and any further document.
func LoadClaimRegister(file string) (*ClaimRegister, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var r ClaimRegister
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if err := dec.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: more than one YAML document", file)
	}
	return &r, nil
}

// Validate checks the register against the tree at root and returns the issues
// in register order. Every listed reference must resolve whatever the status;
// the status only decides which lists must be non-empty, and a claim that names
// a finding can never be verified.
func (r *ClaimRegister) Validate(root string) []Issue {
	var is issues
	if r.SchemaVersion != 1 {
		is.add("schema-version", "", fmt.Sprintf("schema_version is %d, want 1", r.SchemaVersion))
	}
	if !shaRE.MatchString(r.SourcesReviewedAt) {
		is.add("sources-sha", "", "sources_reviewed_at must be 40 lowercase hex characters")
	}
	if r.Claims == nil {
		is.add("field-missing", "", "claims must be a list (possibly empty)")
	}
	seen := map[string]bool{}
	for i := range r.Claims {
		c := &r.Claims[i]
		if !idRE.MatchString(c.ID) {
			is.add("id-format", c.ID, "id must match CLM-NNN")
		} else if seen[c.ID] {
			is.add("id-duplicate", c.ID, "id is used by an earlier claim")
		}
		seen[c.ID] = true
		c.validate(root, &is)
	}
	return is
}

func deref[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}

func (c *Claim) validate(root string, is *issues) {
	for _, f := range []struct {
		name   string
		absent bool
	}{
		{"statement", c.Statement == ""}, {"boundary", c.Boundary == ""}, {"evidence", c.Evidence == ""},
		{"invariants", c.Invariants == nil}, {"findings", c.Findings == nil}, {"tests", c.Tests == nil},
		{"enforcement_points", c.EnforcementPoints == nil}, {"public_sources", len(deref(c.PublicSources)) == 0},
	} {
		if f.absent {
			is.add("field-missing", c.ID, f.name+" is missing or empty")
		}
	}
	if !slices.Contains(statuses, c.Status) {
		is.add("status-invalid", c.ID, fmt.Sprintf("status %q is not one of %s", c.Status, strings.Join(statuses, ", ")))
	}
	strong := c.Status == "enforced" || c.Status == "verified"
	checkIDs(is, c.ID, "invariant", invRE, deref(c.Invariants), strong)
	checkIDs(is, c.ID, "finding", findRE, deref(c.Findings), false)
	if c.Status == "verified" && len(deref(c.Findings)) > 0 {
		is.add("finding-on-verified", c.ID, "a claim that names a finding cannot be verified")
	}
	if strong && len(deref(c.EnforcementPoints)) == 0 {
		is.add("enforcement-required", c.ID, c.Status+" needs an enforcement point")
	}
	for _, p := range deref(c.EnforcementPoints) {
		checkPoint(is, root, c.ID, p)
	}
	if c.Status == "verified" && len(deref(c.Tests)) == 0 {
		is.add("test-required", c.ID, "verified needs a test")
	}
	for _, t := range deref(c.Tests) {
		checkTest(is, root, c.ID, t)
	}
	for _, s := range deref(c.PublicSources) {
		checkSource(is, root, c.ID, s)
	}
}

// checkIDs checks id formats; required also demands a non-empty list.
func checkIDs(is *issues, id, kind string, re *regexp.Regexp, vals []string, required bool) {
	if required && len(vals) == 0 {
		is.add(kind+"-required", id, "at least one "+kind+" id is required")
	}
	for _, v := range vals {
		if !re.MatchString(v) {
			is.add(kind+"-format", id, fmt.Sprintf("%q is not a valid %s id", v, kind))
		}
	}
}

func checkPoint(is *issues, root, id string, p EnforcementPoint) {
	full, err := repoFile(root, p.Path)
	if err != nil {
		is.add("path-invalid", id, err.Error())
	} else if st, err := os.Stat(full); err != nil || st.IsDir() || strings.HasSuffix(p.Path, "_test.go") {
		is.add("enforcement-missing-file", id, p.Path+" is not an existing non-test file")
	} else if !hasSymbol(root, p.Path, p.Symbol) {
		is.add("enforcement-missing-symbol", id, fmt.Sprintf("%q not found in %s", p.Symbol, p.Path))
	}
}

func checkTest(is *issues, root, id string, t TestRef) {
	if _, err := repoFile(root, t.Package); err != nil {
		is.add("path-invalid", id, err.Error())
	} else if !hasTest(root, t.Package, t.Name) {
		is.add("test-missing", id, fmt.Sprintf("no func %s(*testing.T) in %s", t.Name, t.Package))
	}
}

// checkSource checks "<repo>:<path>:<line>" and that a cordum path exists;
// Cordum-site paths cannot be read from this repository.
func checkSource(is *issues, root, id, s string) {
	m := srcRE.FindStringSubmatch(s)
	if m == nil {
		is.add("source-format", id, fmt.Sprintf("%q is not <cordum|Cordum-site>:<path>:<line>", s))
	} else if full, err := repoFile(root, m[2]); err != nil {
		is.add("path-invalid", id, err.Error())
	} else if st, err := os.Stat(full); m[1] == "cordum" && (err != nil || st.IsDir()) {
		is.add("source-missing-file", id, m[2]+" does not exist")
	}
}
