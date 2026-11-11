package contract

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const publicTable = "| File | Entries |\n| --- | --- |\n"
const publicCordum = "# Fixture\nProse is ignored.\n" + publicTable +
	"| README.md | 1 CLM-001 overstated R15; 1 CLM-002 accurate; 2 not-a-claim nav |\n"
const publicSite = publicTable +
	"| site/src/app/page.tsx | 47 CLM-002 unverifiable ev:test; 50 none:bench unverifiable ev:bench |\n"

func publicFixture(t *testing.T, crlf bool) (string, *ClaimRegister) {
	t.Helper()
	dir := t.TempDir()
	for file, body := range map[string]string{
		"cordum-x.md": publicCordum, "cordum-x.hits": "README.md:1\nREADME.md:2\n",
		"site-y.md": publicSite, "site-y.hits": "site/src/app/page.tsx:47\nsite/src/app/page.tsx:50\n",
	} {
		if crlf {
			body = strings.ReplaceAll(body, "\n", "\r\n")
		}
		write(t, filepath.Join(dir, file), body)
	}
	reg, err := loadClaims(t, baseRegister)
	if err != nil {
		t.Fatal(err)
	}
	return dir, reg
}

func TestPublicClaimsValidatorAcceptsBaseline(t *testing.T) {
	dir, reg := publicFixture(t, false)
	is, counts, err := ValidatePublicClaims(dir, reg)
	want := map[string]map[string]int{
		"cordum-x": {"hits": 2, "accurate": 1, "overstated": 1, "unverifiable": 0, "not-a-claim": 1},
		"site-y":   {"hits": 2, "accurate": 0, "overstated": 0, "unverifiable": 2, "not-a-claim": 0},
	}
	if err != nil || len(is) != 0 || !reflect.DeepEqual(counts, want) {
		t.Fatalf("issues=%+v counts=%v error=%v; want %v", is, counts, err, want)
	}
}

func TestPublicClaimsValidatorAcceptsCRLF(t *testing.T) {
	dir, reg := publicFixture(t, true)
	if is, _, err := ValidatePublicClaims(dir, reg); err != nil || len(is) != 0 {
		t.Fatalf("CRLF: issues=%+v error=%v", is, err)
	}
}

type publicRejectCase struct{ name, rule, claim, location, old, replacement string }

func publicRejectCases() []publicRejectCase {
	return []publicRejectCase{
		{"pair", "surface-pair", "", "site-y.hits", "", ""},
		{"name", "surface-name", "", "other-x", "", ""},
		{"none", "surface-none", "", "", "", ""},
		{"third_cell", "row-format", "", "README.md", " nav |", " nav | extra |"},
		{"path", "row-format", "", "../README.md", "README.md", "../README.md"},
		{"zero", "entry-format", "CLM-001", "README.md:0", "1 CLM-001", "0 CLM-001"},
		{"short_ref", "entry-format", "", "README.md:1", "CLM-001", "CLM-1"},
		{"class", "entry-format", "CLM-001", "README.md:1", "overstated", "wrong"},
		{"code", "entry-format", "CLM-001", "README.md:1", "R15", "R16"},
		{"reason", "entry-format", "", "README.md:2", "nav", "misc"},
		{"empty_entry", "entry-format", "", "README.md", "R15;", "R15;;"},
		{"unknown", "claim-unknown", "CLM-009", "README.md:1", "CLM-001", "CLM-009"},
		{"missing_code", "class-codes", "CLM-001", "README.md:1", "overstated R15", "overstated"},
		{"accurate_code", "class-codes", "CLM-002", "README.md:1", "CLM-002 accurate", "CLM-002 accurate R15"},
		{"duplicate", "entry-duplicate", "CLM-001", "README.md:1", "R15;", "R15; 1 CLM-001 overstated R15;"},
		{"not_claim_mix", "entry-duplicate", "CLM-001", "README.md:2", "nav |", "nav; 2 CLM-001 overstated R15 |"},
		{"missing", "hit-missing", "", "README.md:2", "; 2 not-a-claim nav", ""},
		{"extra", "hit-extra", "", "README.md:3", "nav |", "nav; 3 not-a-claim nav |"},
		{"hits", "hits-format", "", "README.md", "README.md:1", "README.md"},
		{"unfilled", "source-unfilled", "CLM-001", "site/src/app/page.tsx:50", "none:bench", "CLM-001"},
		{"unmatched", "source-unmatched", "CLM-002", "README.md:1", "1 CLM-002 accurate; ", ""},
	}
}

func changePublicFixture(t *testing.T, dir string, tc publicRejectCase) string {
	t.Helper()
	if tc.name == "none" {
		return t.TempDir()
	}
	if tc.name == "pair" {
		if err := os.Remove(filepath.Join(dir, "site-y.hits")); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	if tc.name == "name" {
		for _, ext := range []string{".md", ".hits"} {
			if err := os.Rename(filepath.Join(dir, "cordum-x"+ext), filepath.Join(dir, "other-x"+ext)); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	file, body := "cordum-x.md", publicCordum
	if tc.name == "hits" {
		file, body = "cordum-x.hits", "README.md:1\nREADME.md:2\n"
	} else if tc.name == "unfilled" {
		file, body = "site-y.md", publicSite
	}
	write(t, filepath.Join(dir, file), mutate(t, body, tc.old, tc.replacement))
	return dir
}

func TestPublicClaimsValidatorRejects(t *testing.T) {
	for _, tc := range publicRejectCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir, reg := publicFixture(t, false)
			dir = changePublicFixture(t, dir, tc)
			got, _, err := ValidatePublicClaims(dir, reg)
			if err != nil {
				t.Fatal(err)
			}
			surface := "cordum-x"
			switch tc.name {
			case "pair", "unfilled":
				surface = "site-y"
			case "name":
				surface = "other-x"
			case "none":
				surface = "public-claims"
			}
			for _, issue := range got {
				if issue.Rule == tc.rule && issue.ClaimID == tc.claim && strings.HasPrefix(issue.Detail, surface+": ") && strings.Contains(issue.Detail, tc.location) {
					return
				}
			}
			t.Fatalf("want %s/%s at %s: %s; got %+v", tc.rule, tc.claim, surface, tc.location, got)
		})
	}
}

func TestPublicClaimsBaseline(t *testing.T) {
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
	is, counts, err := ValidatePublicClaims(filepath.Join(root, "docs", "contract", "public-claims"), reg)
	if err != nil || len(is) != 0 {
		t.Fatalf("public baseline: error=%v issues=%+v", err, is)
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		c := counts[name]
		t.Logf("%s: %d hits; accurate %d, overstated %d, unverifiable %d, not-a-claim %d",
			name, c["hits"], c["accurate"], c["overstated"], c["unverifiable"], c["not-a-claim"])
	}
}
