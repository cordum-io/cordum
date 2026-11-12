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
	switch tc.name {
	case "hits":
		file, body = "cordum-x.hits", "README.md:1\nREADME.md:2\n"
	case "unfilled":
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

func TestPublicClaimsValidatorEdges(t *testing.T) {
	cases := []struct{ name, old, replacement, rule string }{
		{"pipe", "R15", "R15 |", "row-format"},
		{"tabs", "1 CLM-001 overstated R15", "  1\tCLM-001\t overstated  R15  ", ""},
		{"repeated_code", "R15", "R15 R15", ""},
		{"lowercase_code", "R15", "r05", "entry-format"},
		{"no_table", publicCordum, "Only prose.\n", "hit-missing"},
		{"reverse_mix", "2 not-a-claim nav", "2 CLM-001 accurate; 2 not-a-claim nav", "entry-duplicate"},
		{"trailing_cell", "nav |", "nav | garbage", "row-format"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, reg := publicFixture(t, false)
			write(t, filepath.Join(dir, "cordum-x.md"), mutate(t, publicCordum, tc.old, tc.replacement))
			got, _, err := ValidatePublicClaims(dir, reg)
			if err != nil || (tc.rule == "" && len(got) != 0) {
				t.Fatalf("issues=%+v error=%v", got, err)
			}
			if tc.rule != "" && !slices.ContainsFunc(got, func(i Issue) bool { return i.Rule == tc.rule }) {
				t.Fatalf("missing %s: %+v", tc.rule, got)
			}
		})
	}
}

func TestPublicClaimsValidatorPathsAndEmpty(t *testing.T) {
	for _, path := range []string{"site/src/(marketing)/[slug]/a b.md", "plain.md"} {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "site-z.hits"), path+":1\r\n\r\n")
		write(t, filepath.Join(dir, "site-z.md"), "| "+path+" | 1 not-a-claim nav |\r\n")
		write(t, filepath.Join(dir, "ignored.txt"), "not a surface")
		if got, _, err := ValidatePublicClaims(dir, nil); err != nil || len(got) != 0 {
			t.Fatalf("path %s: %+v, %v", path, got, err)
		}
		write(t, filepath.Join(dir, "site-z.md"), "")
		write(t, filepath.Join(dir, "site-z.hits"), "")
		got, counts, err := ValidatePublicClaims(dir, &ClaimRegister{})
		if err != nil || len(got) != 0 || counts["site-z"]["hits"] != 0 || counts["site-z"]["not-a-claim"] != 0 {
			t.Fatalf("empty pair: %+v, %v, %v", got, counts, err)
		}
	}
}

func TestPublicClaimsValidatorOrderAndNil(t *testing.T) {
	dir, reg := publicFixture(t, false)
	reg.Claims = nil
	for _, r := range []*ClaimRegister{nil, reg} {
		first, _, err := ValidatePublicClaims(dir, r)
		if err != nil || len(first) != 3 || first[0].Rule != "claim-unknown" {
			t.Fatalf("nil claims: %+v, %v", first, err)
		}
		for range 3 {
			next, _, err := ValidatePublicClaims(dir, r)
			if err != nil || !reflect.DeepEqual(first, next) {
				t.Fatalf("unstable issues: %+v vs %+v, %v", first, next, err)
			}
		}
	}
}

func TestPublicClaimsValidatorIO(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ValidatePublicClaims(filepath.Join(dir, "missing"), nil); err == nil {
		t.Fatal("missing directory must be an I/O error")
	}
	for _, ext := range []string{".hits", ".md"} {
		if _, err := readPublicSurface(dir, "cordum-x", nil); err == nil {
			t.Fatalf("missing %s must be an I/O error", ext)
		}
		write(t, filepath.Join(dir, "cordum-x"+ext), "")
	}
}
