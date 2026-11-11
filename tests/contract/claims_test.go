package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pts = `enforcement_points: [{path: pkg/gate.go, symbol: Gate.Check}, {path: conf/policy.yaml, symbol: "default_decision: deny"}]`

// baseRegister is the positive control: CLM-001 is a plain supported claim that
// names a finding, CLM-002 is a fully evidenced one that resolves in the fixture.
const baseRegister = `schema_version: 1
sources_reviewed_at: 0123456789abcdef0123456789abcdef01234567
claims:
  - id: CLM-001
    statement: s1
    boundary: b1
    status: supported
    invariants: []
    findings: [R15]
    enforcement_points: []
    tests: []
    evidence: e1
    public_sources: ["cordum:README.md:1"]
  - id: CLM-002
    statement: s2
    boundary: b2
    status: verified
    invariants: [I02]
    findings: []
    ` + pts + `
    tests: [{package: pkg, name: TestEnforce}]
    evidence: e2
    public_sources: ["cordum:README.md:1", "Cordum-site:site/src/app/page.tsx:47"]
`

func claimsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"pkg/gate.go": "package pkg\n\nfunc Enforce() {}\n\ntype Gate struct{}\n\nfunc (g *Gate) Check() {}\n\n" +
			"type Box[T any] struct{}\n\nfunc (b Box[T]) Get() {}\n\nvar Limit = 1\n\nconst Mode = \"x\"\n",
		"pkg/gate_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestEnforce(t *testing.T) {}\n",
		"conf/policy.yaml": "default_decision: deny\n",
		"README.md":        "readme\n",
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func loadClaims(t *testing.T, text string) (*ClaimRegister, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claims.yaml")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadClaimRegister(p)
}

// mutate applies old/new pairs once each and fails if a pair does not match.
func mutate(t *testing.T, base string, edits ...string) string {
	t.Helper()
	for i := 0; i < len(edits); i += 2 {
		if !strings.Contains(base, edits[i]) {
			t.Fatalf("edit %q does not match the base register", edits[i])
		}
		base = strings.Replace(base, edits[i], edits[i+1], 1)
	}
	return base
}

func TestClaimsValidatorAcceptsValidRegister(t *testing.T) {
	reg, err := loadClaims(t, baseRegister)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Validate(claimsFixture(t)); len(got) != 0 {
		t.Fatalf("positive control produced issues: %+v", got)
	}
}

func TestClaimsValidatorRejects(t *testing.T) {
	root := claimsFixture(t)
	cases := []struct {
		name, rule, claim string
		edits             []string
	}{
		{"duplicate_id", "id-duplicate", "CLM-001", []string{"id: CLM-002", "id: CLM-001"}},
		{"verified_without_test", "test-required", "CLM-002", []string{"tests: [{package: pkg, name: TestEnforce}]", "tests: []"}},
		{"enforced_missing_file", "enforcement-missing-file", "CLM-002", []string{"status: verified", "status: enforced", "pkg/gate.go", "pkg/nope.go"}},
		{"enforced_missing_symbol", "enforcement-missing-symbol", "CLM-002", []string{"status: verified", "status: enforced", "Gate.Check", "Gate.Nope"}},
		{"missing_required_field", "field-missing", "CLM-002", []string{"    boundary: b2\n", ""}},
		{"missing_list_key", "field-missing", "CLM-002", []string{"    findings: []\n", ""}},
		{"bad_status", "status-invalid", "CLM-002", []string{"status: verified", "status: shipped"}},
		{"finding_on_verified", "finding-on-verified", "CLM-002", []string{"findings: []", "findings: [R03]"}},
		{"enforced_without_point", "enforcement-required", "CLM-002", []string{pts, "enforcement_points: []"}},
		{"enforced_without_invariant", "invariant-required", "CLM-002", []string{"invariants: [I02]", "invariants: []"}},
		{"dangling_test", "test-missing", "CLM-002", []string{"name: TestEnforce", "name: TestNope"}},
		{"path_traversal", "path-invalid", "CLM-002", []string{"pkg/gate.go", "../x.go"}},
		{"backslash_path", "path-invalid", "CLM-002", []string{"pkg/gate.go", "pkg\\gate.go"}},
		{"test_file_as_enforcement_point", "enforcement-missing-file", "CLM-002", []string{"pkg/gate.go", "pkg/gate_test.go"}},
		{"invariant_out_of_range", "invariant-format", "CLM-002", []string{"[I02]", "[I11]"}},
		{"finding_out_of_range", "finding-format", "CLM-001", []string{"[R15]", "[R16]"}},
		{"source_without_repo_prefix", "source-format", "CLM-002", []string{`"Cordum-site:site/src/app/page.tsx:47"`, `"site/src/app/page.tsx:47"`}},
		{"missing_cordum_source_file", "source-missing-file", "CLM-001", []string{"cordum:README.md:1", "cordum:NOPE.md:1"}},
		{"short_id", "id-format", "CLM-1", []string{"id: CLM-001", "id: CLM-1"}},
		{"schema_version_2", "schema-version", "", []string{"schema_version: 1", "schema_version: 2"}},
		{"short_sha", "sources-sha", "", []string{"0123456789abcdef0123456789abcdef01234567", "0123456"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := loadClaims(t, mutate(t, baseRegister, tc.edits...))
			if err != nil {
				t.Fatal(err)
			}
			got := reg.Validate(root)
			if len(got) != 1 || got[0].Rule != tc.rule || got[0].ClaimID != tc.claim {
				t.Fatalf("want exactly one %q issue for claim %q, got %+v", tc.rule, tc.claim, got)
			}
		})
	}
}

func TestClaimsLoadRejects(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"unknown_key", mutate(t, baseRegister, "    invariants: [I02]", "    invarients: [I02]"), "invarients"},
		{"malformed_yaml", "claims: [", ""},
		{"second_document", baseRegister + "---\nschema_version: 1\n", ""},
		{"empty_file", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadClaims(t, tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestClaimsReferenceHelpers(t *testing.T) {
	root := claimsFixture(t)
	for sym, want := range map[string]bool{
		"Enforce": true, "Gate": true, "Gate.Check": true, "Box": true, "Box.Get": true,
		"Limit": true, "Mode": true, "Nope": false, "Gate.Nope": false, "Check": false,
	} {
		if got := hasSymbol(root, "pkg/gate.go", sym); got != want {
			t.Errorf("hasSymbol(%q) = %v, want %v", sym, got, want)
		}
	}
	if hasSymbol(root, "pkg/gate_test.go", "TestEnforce") {
		t.Error("a symbol in a _test.go file must not resolve")
	}
	if !hasSymbol(root, "conf/policy.yaml", "default_decision: deny") || hasSymbol(root, "conf/policy.yaml", "default_decision: allow") {
		t.Error("a non-Go anchor must resolve only when the literal text is present")
	}
	if !hasTest(root, "pkg", "TestEnforce") || hasTest(root, "pkg", "Enforce") || hasTest(root, "pkg", "TestNope") || hasTest(root, "nodir", "TestEnforce") {
		t.Error("hasTest must find only a declared Test function in that directory")
	}
	for _, bad := range []string{"", "/abs", "C:/x", "C:x", `a\b`, "../x", "a/../b", "a//b", "./a", "a/"} {
		if _, err := repoFile(root, bad); err == nil {
			t.Errorf("repoFile(%q) must be rejected", bad)
		}
	}
	if _, err := repoFile(root, "pkg/gate.go"); err != nil {
		t.Errorf("repoFile rejected a clean path: %v", err)
	}
}

func TestClaimsRegister(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := LoadClaimRegister(filepath.Join(root, "docs", "contract", "claims.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Validate(root); len(got) != 0 {
		t.Fatalf("docs/contract/claims.yaml has issues: %+v", got)
	}
}
