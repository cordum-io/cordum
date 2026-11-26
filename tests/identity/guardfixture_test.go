package identity

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestGuardDetectsNewSites runs both guards' scanners on a fixture that
// plants one new site of each kind and each misuse of Run, and pins the exact
// problems they report.
func TestGuardDetectsNewSites(t *testing.T) {
	root := filepath.Join("testdata", "guardfixture")
	calls, probs := scanRuns(parseTree(t, root, func(rel string) bool { return strings.HasSuffix(rel, "_test.go") }))
	consts := map[string]string{"HTTPAPI": string(HTTPAPI), "EdgeAPI": string(EdgeAPI)}
	probs = append(probs, coverageProblems(calls, consts, []Entry{HTTPAPI, EdgeAPI})...)
	dot := `package dot

import . "github.com/cordum/cordum/tests/identity"
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "dot_test.go", dot, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	_, dotProbs := scanRuns([]goFile{{fset, f, []byte(dot)}})
	sites := productSites(t, root)
	stale := "core/fake/fake.go|Serve|listen|net.Listen"
	probs = append(append(probs, dotProbs...), inventoryProblems(sites, map[string]use{stale: {n: 1}})...)
	want := []string{
		"core/fake/fake_test.go:9: Run is used as a value, not called",
		"core/fake/fake_test.go:11: Run names WorkloadAPI, which is not an Entry in Entries",
		"core/fake/fake_test.go:19: Run's entry argument is not a constant selector idm.<Entry>",
		"core/fake/fake_test.go:23: Run is called outside a top-level TestAudienceMatrix test",
		`entry "http_api" has 2 runners, want 1: core/fake/fake_test.go:13, core/fake/fake_test.go:15`,
		`entry "edge_api" has no runner`,
		"dot_test.go:3: dot-import of tests/identity hides its Run calls from this guard",
		"unmapped site core/fake/fake.go|IssueWorkloadToken|issuer|IssueWorkloadToken at core/fake/fake.go:16",
		"unmapped site core/fake/fake.go|Serve|subscribe|sys.fake.subject at core/fake/fake.go:21",
		"unmapped site core/fake/fake.go|Serve|listen|grpc.NewServer at core/fake/fake.go:22",
		"unmapped site core/fake/fake.go|Serve|listen|http.Server{} at core/fake/fake.go:22",
		"stale inventory key " + stale + ": no such site",
		"inventory key " + stale + " must name its Entries, its Credentials (for a source) or a reason, and only one",
	}
	slices.Sort(probs)
	slices.Sort(want)
	if !slices.Equal(probs, want) {
		t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(probs, "\n  "), strings.Join(want, "\n  "))
	}
	sub := "core/fake/fake.go|Serve|subscribe|sys.fake.subject"
	miscount := "site " + sub + " is found 1 time(s) at core/fake/fake.go:21, the inventory says 2"
	if got := inventoryProblems(sites, map[string]use{sub: serves(2, BusWorker)}); !slices.Contains(got, miscount) {
		t.Errorf("count mismatch not reported: %q", got)
	}
}
