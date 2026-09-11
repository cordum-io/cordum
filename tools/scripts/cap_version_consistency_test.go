package scripts_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// knownCAPTagCommits binds every deliberately promoted stable CAP tag to its
// immutable release commit. Extend this table (one line) on each future CAP
// promotion; the consistency test then keeps proving that the configured
// tag/commit pair is a published one rather than an aspirational or drifted
// combination.
var knownCAPTagCommits = map[string]string{
	"v2.17.0": "e580c670d54a7563c749835c7dd09d81f116c823",
}

var (
	capRequirePattern = regexp.MustCompile(`(?m)^\s*(?:require\s+)?github\.com/cordum-io/cap/v2\s+(\S+?)\s*(?://.*)?$`)
	capReplacePattern = regexp.MustCompile(`(?m)^\s*(?:replace\s+)?github\.com/cordum-io/cap/v2(?:\s+\S+)?\s*=>`)
	stableTagPattern  = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	capRevPattern     = regexp.MustCompile(`(?m)^[ \t]+CAP_REV:\s*(\S+)\s*$`)
	capShaPattern     = regexp.MustCompile(`(?m)^[ \t]+CAP_HANDSHAKE_CAP_SHA:\s*(\S+)\s*$`)
	fullShaPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type capConsistencyInput struct {
	rootGoMod []byte
	sdkGoMod  []byte
	ciYAML    []byte
}

// checkCAPVersionConsistency validates that the root module, the nested SDK
// module, and the CI CAP checkout all agree on one exact, stable, published
// CAP version. It returns a list of human-readable problems; an empty list
// means the configuration is consistent.
func checkCAPVersionConsistency(in capConsistencyInput) []string {
	var problems []string

	rootVersion, ok := capRequireVersion(in.rootGoMod)
	if !ok {
		problems = append(problems, "root go.mod: missing github.com/cordum-io/cap/v2 requirement")
	}
	sdkVersion, ok := capRequireVersion(in.sdkGoMod)
	if !ok {
		problems = append(problems, "sdk/go.mod: missing github.com/cordum-io/cap/v2 requirement")
	}
	for name, version := range map[string]string{"root go.mod": rootVersion, "sdk/go.mod": sdkVersion} {
		if version != "" && !stableTagPattern.MatchString(version) {
			problems = append(problems, fmt.Sprintf("%s: CAP version %q is not an exact stable tag (pseudo-versions and pre-releases are forbidden)", name, version))
		}
	}
	if rootVersion != "" && sdkVersion != "" && rootVersion != sdkVersion {
		problems = append(problems, fmt.Sprintf("root go.mod pins CAP %s but sdk/go.mod pins %s; both modules must pin the same released tag", rootVersion, sdkVersion))
	}
	if capReplacePattern.Match(normalizeEOL(in.rootGoMod)) {
		problems = append(problems, "root go.mod: forbidden replace directive for github.com/cordum-io/cap/v2")
	}
	if capReplacePattern.Match(normalizeEOL(in.sdkGoMod)) {
		problems = append(problems, "sdk/go.mod: forbidden replace directive for github.com/cordum-io/cap/v2")
	}

	capRev, ok := singleMatch(capRevPattern, in.ciYAML)
	if !ok {
		problems = append(problems, "ci.yml: missing or ambiguous CAP_REV")
	}
	capSha, ok := singleMatch(capShaPattern, in.ciYAML)
	if !ok {
		problems = append(problems, "ci.yml: missing or ambiguous CAP_HANDSHAKE_CAP_SHA")
	}
	for name, sha := range map[string]string{"CAP_REV": capRev, "CAP_HANDSHAKE_CAP_SHA": capSha} {
		if sha != "" && !fullShaPattern.MatchString(sha) {
			problems = append(problems, fmt.Sprintf("ci.yml: %s %q is not one full lowercase 40-hex commit SHA", name, sha))
		}
	}
	if capRev != "" && capSha != "" && capRev != capSha {
		problems = append(problems, fmt.Sprintf("ci.yml: CAP_REV %s != CAP_HANDSHAKE_CAP_SHA %s", capRev, capSha))
	}

	if rootVersion != "" && stableTagPattern.MatchString(rootVersion) {
		wantCommit, known := knownCAPTagCommits[rootVersion]
		if !known {
			problems = append(problems, fmt.Sprintf("CAP tag %s is not in the known promoted tag/commit table; add it only for a genuinely published release", rootVersion))
		} else if capRev != "" && capRev != wantCommit {
			problems = append(problems, fmt.Sprintf("ci.yml CAP_REV %s does not match the %s release commit %s", capRev, rootVersion, wantCommit))
		}
	}

	return problems
}

func capRequireVersion(goMod []byte) (string, bool) {
	match := capRequirePattern.FindSubmatch(normalizeEOL(goMod))
	if len(match) != 2 {
		return "", false
	}
	return string(match[1]), true
}

func singleMatch(pattern *regexp.Regexp, data []byte) (string, bool) {
	matches := pattern.FindAllSubmatch(normalizeEOL(data), -1)
	if len(matches) != 1 {
		return "", false
	}
	return string(matches[0][1]), true
}

// normalizeEOL strips carriage returns so the line-anchored patterns behave
// identically on LF and CRLF working trees (autocrlf checkouts on Windows).
func normalizeEOL(data []byte) []byte {
	return []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
}

func validCAPFixture() capConsistencyInput {
	return capConsistencyInput{
		rootGoMod: []byte("module github.com/cordum/cordum\n\ngo 1.26.3\n\nrequire (\n\tgithub.com/cordum-io/cap/v2 v2.17.0\n\tgithub.com/other/dep v1.0.0\n)\n\nreplace github.com/cordum/cordum/sdk => ./sdk\n"),
		sdkGoMod:  []byte("module github.com/cordum/cordum/sdk\n\ngo 1.26.3\n\nrequire (\n\tgithub.com/cordum-io/cap/v2 v2.17.0\n)\n"),
		ciYAML:    []byte("jobs:\n  handshake:\n    env:\n      CAP_REV: e580c670d54a7563c749835c7dd09d81f116c823\n      CAP_HANDSHAKE_CAP_SHA: e580c670d54a7563c749835c7dd09d81f116c823\n"),
	}
}

func assertSingleProblemContains(t *testing.T, problems []string, want string) {
	t.Helper()
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly one mentioning %q", problems, want)
	}
	if !strings.Contains(problems[0], want) {
		t.Fatalf("problem %q does not mention %q", problems[0], want)
	}
}

func TestCAPConsistencyAcceptsPromotedState(t *testing.T) {
	if problems := checkCAPVersionConsistency(validCAPFixture()); len(problems) != 0 {
		t.Fatalf("valid promoted fixture reported problems: %v", problems)
	}
}

func TestCAPConsistencyHandlesCRLF(t *testing.T) {
	in := validCAPFixture()
	in.rootGoMod = []byte(strings.ReplaceAll(string(in.rootGoMod), "\n", "\r\n"))
	in.sdkGoMod = []byte(strings.ReplaceAll(string(in.sdkGoMod), "\n", "\r\n"))
	in.ciYAML = []byte(strings.ReplaceAll(string(in.ciYAML), "\n", "\r\n"))
	if problems := checkCAPVersionConsistency(in); len(problems) != 0 {
		t.Fatalf("CRLF fixture reported problems: %v", problems)
	}
}

func TestCAPConsistencyDetectsPseudoVersion(t *testing.T) {
	in := validCAPFixture()
	in.rootGoMod = []byte(strings.ReplaceAll(string(in.rootGoMod), "v2.17.0", "v2.16.2-0.20260722152314-e580c670d54a"))
	in.sdkGoMod = []byte(strings.ReplaceAll(string(in.sdkGoMod), "v2.17.0", "v2.16.2-0.20260722152314-e580c670d54a"))
	problems := checkCAPVersionConsistency(in)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want two pseudo-version problems", problems)
	}
	for _, p := range problems {
		if !strings.Contains(p, "not an exact stable tag") {
			t.Fatalf("problem %q does not flag the pseudo-version", p)
		}
	}
}

func TestCAPConsistencyDetectsRootSdkSkew(t *testing.T) {
	in := validCAPFixture()
	in.sdkGoMod = []byte(strings.ReplaceAll(string(in.sdkGoMod), "v2.17.0", "v2.15.1"))
	problems := checkCAPVersionConsistency(in)
	found := false
	for _, p := range problems {
		if strings.Contains(p, "both modules must pin the same released tag") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want a root/sdk skew problem", problems)
	}
}

func TestCAPConsistencyDetectsCapReplace(t *testing.T) {
	in := validCAPFixture()
	in.rootGoMod = append(in.rootGoMod, []byte("\nreplace github.com/cordum-io/cap/v2 => ../cap\n")...)
	assertSingleProblemContains(t, checkCAPVersionConsistency(in), "forbidden replace directive")
}

func TestCAPConsistencyDetectsCIShaMismatch(t *testing.T) {
	in := validCAPFixture()
	in.ciYAML = []byte(strings.Replace(string(in.ciYAML), "CAP_HANDSHAKE_CAP_SHA: e580c670d54a7563c749835c7dd09d81f116c823", "CAP_HANDSHAKE_CAP_SHA: 32b9db9670c597685344808272f9b246026091ba", 1))
	problems := checkCAPVersionConsistency(in)
	found := false
	for _, p := range problems {
		if strings.Contains(p, "CAP_REV e580c670d54a7563c749835c7dd09d81f116c823 != CAP_HANDSHAKE_CAP_SHA 32b9db9670c597685344808272f9b246026091ba") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want a CAP_REV/CAP_HANDSHAKE_CAP_SHA mismatch problem", problems)
	}
}

func TestCAPConsistencyDetectsUnknownTag(t *testing.T) {
	in := validCAPFixture()
	in.rootGoMod = []byte(strings.ReplaceAll(string(in.rootGoMod), "v2.17.0", "v2.99.0"))
	in.sdkGoMod = []byte(strings.ReplaceAll(string(in.sdkGoMod), "v2.17.0", "v2.99.0"))
	problems := checkCAPVersionConsistency(in)
	found := false
	for _, p := range problems {
		if strings.Contains(p, "not in the known promoted tag/commit table") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want an unknown-tag problem", problems)
	}
}

func TestCAPConsistencyDetectsCICommitNotMatchingTag(t *testing.T) {
	in := validCAPFixture()
	in.ciYAML = []byte(strings.ReplaceAll(string(in.ciYAML), "e580c670d54a7563c749835c7dd09d81f116c823", "32b9db9670c597685344808272f9b246026091ba"))
	problems := checkCAPVersionConsistency(in)
	found := false
	for _, p := range problems {
		if strings.Contains(p, "does not match the v2.17.0 release commit") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want a tag/commit binding problem", problems)
	}
}

func TestCAPConsistencySupportsSingleLineRequire(t *testing.T) {
	in := validCAPFixture()
	in.sdkGoMod = []byte("module github.com/cordum/cordum/sdk\n\ngo 1.26.3\n\nrequire github.com/cordum-io/cap/v2 v2.17.0\n")
	if problems := checkCAPVersionConsistency(in); len(problems) != 0 {
		t.Fatalf("single-line require fixture reported problems: %v", problems)
	}
}

func TestCAPConsistencyDetectsMissingRequirement(t *testing.T) {
	in := validCAPFixture()
	in.sdkGoMod = []byte("module github.com/cordum/cordum/sdk\n\ngo 1.26.3\n")
	problems := checkCAPVersionConsistency(in)
	found := false
	for _, p := range problems {
		if strings.Contains(p, "sdk/go.mod: missing github.com/cordum-io/cap/v2 requirement") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want a missing-requirement problem", problems)
	}
}

// TestRepositoryCAPVersionConsistency runs the checker against the real
// repository files: the root module, the nested SDK module, and the CI
// workflow that checks out CAP for the handshake interop gate.
func TestRepositoryCAPVersionConsistency(t *testing.T) {
	root := repositoryRoot(t)
	read := func(rel string) []byte {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return data
	}
	in := capConsistencyInput{
		rootGoMod: read("go.mod"),
		sdkGoMod:  read("sdk/go.mod"),
		ciYAML:    read(".github/workflows/ci.yml"),
	}
	if problems := checkCAPVersionConsistency(in); len(problems) != 0 {
		t.Fatalf("repository CAP version consistency problems:\n  %s", strings.Join(problems, "\n  "))
	}
}
