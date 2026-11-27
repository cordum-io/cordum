package tenancy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var boardKeyRE = regexp.MustCompile(`^open-finding:[A-Z][A-Z0-9]*-[0-9A-Z]+(-[0-9A-Z]+)*$`)
var coverageTestRE = regexp.MustCompile(`^Test[A-Za-z0-9_]+$`)

// checkManifestComplete returns one message per entry that keeps the
// manifest from being complete: a pending owner, an open finding that is not
// a bare board key, a covering test declared in zero or several package
// directories, any other Coverage form, or an invalid Binding.
func checkManifestComplete(entries []storeKeyEntry, testDirs map[string][]string) []string {
	if len(entries) == 0 {
		return []string{"manifest is empty"}
	}
	var messages []string
	for _, entry := range entries {
		label := entry.Package + " " + entry.Pattern
		switch coverage := entry.Coverage; {
		case strings.HasPrefix(coverage, "pending:"):
			messages = append(messages, "pending coverage "+label+": "+coverage)
		case strings.HasPrefix(coverage, "open-finding:"):
			if !boardKeyRE.MatchString(coverage) {
				messages = append(messages, "open finding is not a board key "+label+": "+coverage)
			}
		case coverageTestRE.MatchString(coverage):
			if n := len(testDirs[coverage]); n != 1 {
				messages = append(messages, fmt.Sprintf("coverage test %s for %s declared in %d package directories", coverage, label, n))
			}
		default:
			messages = append(messages, "unknown coverage form "+label+": "+coverage)
		}
		if !bindingRE.MatchString(entry.Binding) {
			messages = append(messages, "invalid binding "+label+": "+entry.Binding)
		}
	}
	return messages
}

type coveredTest struct {
	Dir  string `json:"dir"`
	Test string `json:"test"`
}

func TestTenantIsolationManifestComplete(t *testing.T) {
	root := filepath.Join("..", "..")
	testDirs, err := testFuncDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range checkManifestComplete(storeKeyManifest, testDirs) {
		t.Error(message)
	}
	out := os.Getenv("TENANCY_COVERAGE_OUT")
	if out == "" || t.Failed() {
		return
	}
	var covered []coveredTest
	for _, entry := range storeKeyManifest {
		if dirs := testDirs[entry.Coverage]; coverageTestRE.MatchString(entry.Coverage) && len(dirs) == 1 {
			covered = append(covered, coveredTest{Dir: dirs[0], Test: entry.Coverage})
		}
	}
	slices.SortFunc(covered, func(a, b coveredTest) int {
		return strings.Compare(a.Dir+"\x00"+a.Test, b.Dir+"\x00"+b.Test)
	})
	covered = slices.Compact(covered)
	data, err := json.MarshalIndent(covered, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTenantIsolationManifestCompleteRejects(t *testing.T) {
	testDirs := map[string][]string{
		"TestOne":   {"core/a"},
		"TestTwice": {"core/a", "core/b"},
	}
	entry := func(binding, coverage string) []storeKeyEntry {
		return []storeKeyEntry{{Pattern: "k:{id}", Package: "core/a", Binding: binding, Coverage: coverage}}
	}
	cases := []struct {
		name    string
		entries []storeKeyEntry
		want    []string
	}{
		{"clean", []storeKeyEntry{
			{Pattern: "k:{id}", Package: "core/a", Binding: "tenant-in-key", Coverage: "TestOne"},
			{Pattern: "v:{id}", Package: "core/a", Binding: "tenant-in-value, enforced at store.Get", Coverage: "open-finding:PR1-03-F1"},
			{Pattern: "g:{id}", Package: "core/a", Binding: "global-id", Coverage: "TestOne"},
		}, nil},
		{"empty manifest", nil, []string{"manifest is empty"}},
		{"pending", entry("tenant-in-key", "pending:PR1-03B"), []string{"pending coverage core/a k:{id}: pending:PR1-03B"}},
		{"open finding prose", entry("tenant-in-key", "open-finding:see the private record"),
			[]string{"open finding is not a board key core/a k:{id}: open-finding:see the private record"}},
		{"test in no package", entry("tenant-in-key", "TestMissing"),
			[]string{"coverage test TestMissing for core/a k:{id} declared in 0 package directories"}},
		{"test in two packages", entry("tenant-in-key", "TestTwice"),
			[]string{"coverage test TestTwice for core/a k:{id} declared in 2 package directories"}},
		{"subtest", entry("tenant-in-key", "TestOne/sub"), []string{"unknown coverage form core/a k:{id}: TestOne/sub"}},
		{"empty coverage", entry("tenant-in-key", ""), []string{"unknown coverage form core/a k:{id}: "}},
		{"unknown binding", entry("tenant-scoped", "TestOne"), []string{"invalid binding core/a k:{id}: tenant-scoped"}},
		{"empty enforced at", entry("tenant-in-key, enforced at ", "TestOne"),
			[]string{"invalid binding core/a k:{id}: tenant-in-key, enforced at "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkManifestComplete(tc.entries, testDirs); !slices.Equal(got, tc.want) {
				t.Fatalf("checkManifestComplete() = %q, want %q", got, tc.want)
			}
		})
	}
}
