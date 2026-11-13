package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordum/cordum/tools/releasequal"
)

const (
	helperEnv     = "RELEASE_QUAL_HELPER"
	helperExitEnv = "RELEASE_QUAL_HELPER_EXIT"
	runURL        = "https://example.invalid/run/1"
	reason        = "The production gate is not bound to these digests."
	binName       = "cordum-hook-linux-amd64"
)

var (
	imgDigest   = "sha256:" + strings.Repeat("1", 64)
	otherDigest = "sha256:" + strings.Repeat("2", 64)
	sourceSHA   = strings.Repeat("c", 40)
)

// TestHelperProcess is the child of the publish tests. It records that it was
// started by creating the marker file named by its last argument.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	if err := os.WriteFile(os.Args[len(os.Args)-1], []byte("started"), 0o600); err != nil {
		os.Exit(99)
	}
	code, _ := strconv.Atoi(os.Getenv(helperExitEnv))
	os.Exit(code)
}

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func mustRun(t *testing.T, want int, args ...string) (stdout, stderr string) {
	t.Helper()
	code, stdout, stderr := runCLI(args...)
	if code != want {
		t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, stdout, stderr)
	}
	return stdout, stderr
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s exists or is unreadable (err=%v), want it absent", path, err)
	}
}

type fixture struct {
	dir, record, binPath, binDigest string
	binBytes                        []byte
}

// newFixture builds a record through the command: one image and one binary.
func newFixture(t *testing.T, trivy string) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{dir: dir, record: filepath.Join(dir, "rec.json"), binBytes: []byte("qualified binary bytes\x00\x01")}
	f.binPath = writeFile(t, filepath.Join(dir, binName), f.binBytes)
	f.binDigest = digestOf(f.binBytes)
	img, bin := filepath.Join(dir, "img.json"), filepath.Join(dir, "bin.json")
	mustRun(t, 0, "entry", "--kind", "image", "--name", "api-gateway", "--ref", "ghcr.io/cordum-io/cordum/api-gateway",
		"--digest", imgDigest, "--gate", "image-smoke=pass", "--gate", "trivy-os-linux-amd64="+trivy, "--log", runURL,
		"--exception", "production-gate-not-digest-bound="+reason, "--out", img)
	mustRun(t, 0, "entry", "--kind", "binary", "--file", f.binPath, "--exception", "binary-no-release-gate="+reason,
		"--exception", "production-gate-not-digest-bound="+reason, "--out", bin)
	mustRun(t, 0, "assemble", "--out", f.record, "--ref", "refs/tags/v9.9.9", "--run-url", runURL,
		"--repository", "cordum-io/cordum", "--sha", sourceSHA, img, bin)
	return f
}

func TestEntryAssembleValidateEndToEnd(t *testing.T) {
	f := newFixture(t, "pass")
	stdout, _ := mustRun(t, 0, "validate", f.record)
	if want := "VALID 2 artifacts (1 image, 1 binary), 2 gates, 2 exceptions, source " + sourceSHA + ", ref refs/tags/v9.9.9, dry_run false"; !strings.Contains(stdout, want) {
		t.Fatalf("validate output %q lacks %q", stdout, want)
	}
	data, err := os.ReadFile(f.record)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := releasequal.Parse(data)
	if err != nil {
		t.Fatalf("assembled record does not parse: %v", err)
	}
	if rec.Source.SHA != sourceSHA {
		t.Fatalf("source sha %q, want %q", rec.Source.SHA, sourceSHA)
	}
	got := map[string]string{}
	for _, a := range rec.Artifacts {
		got[a.Name] = a.Digest
	}
	if len(rec.Artifacts) != 2 || got["api-gateway"] != imgDigest || got[binName] != f.binDigest {
		t.Fatalf("artifacts %+v, want api-gateway=%s and %s=%s", rec.Artifacts, imgDigest, binName, f.binDigest)
	}
	if want := digestOf(f.binBytes); got[binName] != want {
		t.Fatalf("binary digest %s is not the sha256 of the file bytes %s", got[binName], want)
	}
	if len(rec.Gates) != 2 || rec.Gates[0].Digest != imgDigest || rec.Gates[0].Log != runURL {
		t.Fatalf("gates %+v, want two gates bound to the image digest with the log url", rec.Gates)
	}
	if len(rec.Exceptions) != 2 || len(rec.Exceptions[1].Artifacts) != 2 {
		t.Fatalf("exceptions %+v, want the shared exception merged across both artifacts", rec.Exceptions)
	}
}

func TestEntryFragmentEncoding(t *testing.T) {
	out := filepath.Join(t.TempDir(), "frag.json")
	tricky := "a=b <c> & d"
	mustRun(t, 0, "entry", "--kind", "image", "--name", "api-gateway", "--ref", "r", "--digest", imgDigest,
		"--exception", "x-exception="+tricky, "--out", out)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	frag, err := releasequal.ParseFragment(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(frag.Exceptions) != 1 || frag.Exceptions[0].Reason != tricky || frag.Exceptions[0].ID != "x-exception" {
		t.Fatalf("exceptions %+v, want the reason kept whole after the first '='", frag.Exceptions)
	}
	if !bytes.Contains(data, []byte(tricky)) || !bytes.HasSuffix(data, []byte("}\n")) || !bytes.Contains(data, []byte("\n  \"")) {
		t.Fatalf("fragment is not unescaped, indented, newline-terminated JSON:\n%s", data)
	}
	if !bytes.Contains(data, []byte(`"gates": []`)) {
		t.Fatalf("empty gates must encode as [], got:\n%s", data)
	}
}

func TestEntryRefusesInvalidFragment(t *testing.T) {
	dir := t.TempDir()
	upper := writeFile(t, filepath.Join(dir, "Bad_NAME"), []byte("x"))
	ok := writeFile(t, filepath.Join(dir, binName), []byte("x"))
	cases := map[string][]string{
		"binary with neither gate nor exception": {"--kind", "binary", "--file", ok},
		"image with malformed digest":            {"--kind", "image", "--name", "a", "--ref", "r", "--digest", "sha256:abc", "--exception", "e=r"},
		"binary with a name the schema refuses":  {"--kind", "binary", "--file", upper, "--exception", "e=r"},
		"gate result that is not pass or fail":   {"--kind", "binary", "--file", ok, "--gate", "g=maybe", "--log", runURL},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "frag.json")
			mustRun(t, 1, append([]string{"entry"}, append(args, "--out", out)...)...)
			absent(t, out)
		})
	}
}

func TestEntryUsageErrors(t *testing.T) {
	dir := t.TempDir()
	ok := writeFile(t, filepath.Join(dir, binName), []byte("x"))
	out := filepath.Join(dir, "frag.json")
	cases := map[string][]string{
		"image without name":          {"--kind", "image", "--ref", "r", "--digest", imgDigest, "--exception", "e=r", "--out", out},
		"image with a file":           {"--kind", "image", "--name", "a", "--ref", "r", "--digest", imgDigest, "--file", ok, "--exception", "e=r", "--out", out},
		"binary without a file":       {"--kind", "binary", "--exception", "e=r", "--out", out},
		"binary with a digest":        {"--kind", "binary", "--file", ok, "--digest", imgDigest, "--exception", "e=r", "--out", out},
		"binary with a name":          {"--kind", "binary", "--file", ok, "--name", "a", "--exception", "e=r", "--out", out},
		"binary file that is missing": {"--kind", "binary", "--file", filepath.Join(dir, "missing"), "--exception", "e=r", "--out", out},
		"unknown kind":                {"--kind", "tarball", "--out", out},
		"gate without an '='":         {"--kind", "binary", "--file", ok, "--gate", "nogate", "--log", runURL, "--out", out},
		"gate with an empty id":       {"--kind", "binary", "--file", ok, "--gate", "=pass", "--log", runURL, "--out", out},
		"gate without a log":          {"--kind", "binary", "--file", ok, "--gate", "g=pass", "--out", out},
		"exception with an empty id":  {"--kind", "binary", "--file", ok, "--exception", "=why", "--out", out},
		"missing --out":               {"--kind", "binary", "--file", ok, "--exception", "e=r"},
		"unknown flag":                {"--kind", "binary", "--bogus", "1"},
		"stray positional argument":   {"--kind", "binary", "--file", ok, "--exception", "e=r", "--out", out, "extra"},
		"no arguments at all":         {},
		"image without ref":           {"--kind", "image", "--name", "a", "--digest", imgDigest, "--exception", "e=r", "--out", out},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mustRun(t, 2, append([]string{"entry"}, args...)...)
			absent(t, out)
		})
	}
}

func TestAssembleAndValidate(t *testing.T) {
	f := newFixture(t, "pass")
	img, bin := filepath.Join(f.dir, "img.json"), filepath.Join(f.dir, "bin.json")
	base := []string{"--ref", "refs/tags/v9.9.9", "--run-url", runURL, "--repository", "cordum-io/cordum", "--sha", sourceSHA}
	conflict := writeFile(t, filepath.Join(f.dir, "conflict.json"),
		[]byte(`{"artifacts":[],"gates":[],"exceptions":[{"id":"production-gate-not-digest-bound","artifacts":["api-gateway"],"reason":"a different reason"}]}`))
	unknown := writeFile(t, filepath.Join(f.dir, "unknown.json"), []byte(`{"artifacts":[],"gates":[],"exceptions":[],"extra":1}`))
	with := func(extra ...string) []string { return append(append([]string{"assemble"}, base...), extra...) }
	t.Run("conflicting exception reasons exit 1 and write nothing", func(t *testing.T) {
		out := filepath.Join(f.dir, "o1.json")
		mustRun(t, 1, with("--out", out, img, bin, conflict)...)
		absent(t, out)
	})
	t.Run("fragment with an unknown field exits 1", func(t *testing.T) {
		out := filepath.Join(f.dir, "o2.json")
		mustRun(t, 1, with("--out", out, img, unknown)...)
		absent(t, out)
	})
	t.Run("non-dry-run branch ref exits 1", func(t *testing.T) {
		out := filepath.Join(f.dir, "o3.json")
		mustRun(t, 1, "assemble", "--ref", "refs/heads/main", "--run-url", runURL, "--repository", "cordum-io/cordum", "--sha", sourceSHA, "--out", out, img, bin)
		absent(t, out)
	})
	t.Run("dry-run branch ref is recorded as a dry run", func(t *testing.T) {
		out := filepath.Join(f.dir, "o4.json")
		mustRun(t, 0, "assemble", "--dry-run", "--ref", "refs/heads/release-dry-run/x", "--run-url", runURL, "--repository", "cordum-io/cordum", "--sha", sourceSHA, "--out", out, img, bin)
		stdout, _ := mustRun(t, 0, "validate", out)
		if !strings.Contains(stdout, "dry_run true") {
			t.Fatalf("validate output %q does not report dry_run true", stdout)
		}
	})
	usage := map[string][]string{
		"no fragments":         with("--out", filepath.Join(f.dir, "o5.json")),
		"missing --out":        with(img),
		"missing --sha":        {"assemble", "--ref", "refs/tags/v1", "--run-url", runURL, "--repository", "a/b", "--out", filepath.Join(f.dir, "o6.json"), img},
		"unreadable fragment":  with("--out", filepath.Join(f.dir, "o7.json"), filepath.Join(f.dir, "missing.json")),
		"validate no argument": {"validate"},
		"validate two files":   {"validate", f.record, f.record},
		"validate unreadable":  {"validate", filepath.Join(f.dir, "missing.json")},
	}
	for name, args := range usage {
		t.Run(name, func(t *testing.T) { mustRun(t, 2, args...) })
	}
	t.Run("validate refuses a record that fails Parse with exit 1", func(t *testing.T) {
		bad := writeFile(t, filepath.Join(f.dir, "bad.json"), []byte(`{}`))
		mustRun(t, 1, "validate", bad)
	})
}

func TestCheckExitCodes(t *testing.T) {
	f := newFixture(t, "pass")
	failing := newFixture(t, "fail")
	differing := writeFile(t, filepath.Join(t.TempDir(), binName), []byte("not the qualified bytes"))
	invalid := writeFile(t, filepath.Join(f.dir, "invalid.json"), []byte(`{"schema_version":1}`))
	art := func(s string) []string { return []string{"--artifact", s} }
	rec := func(path string, rest ...string) []string {
		return append([]string{"check", "--record", path}, rest...)
	}
	cases := []struct {
		name string
		args []string
		want int
		out  string // "|"-separated substrings of stdout (exit 0) or stderr (exit 3)
	}{
		{"qualified artifact", rec(f.record, art("api-gateway="+imgDigest)...), 0, "QUALIFIED api-gateway " + imgDigest},
		{"qualified file", rec(f.record, "--file", f.binPath), 0, "QUALIFIED " + binName + " " + f.binDigest},
		{"unknown artifact", rec(f.record, art("nope="+imgDigest)...), 3, "REFUSED nope"},
		{"different digest", rec(f.record, art("api-gateway="+otherDigest)...), 3, "REFUSED api-gateway"},
		{"name matched case-sensitively", rec(f.record, art("API-GATEWAY="+imgDigest)...), 3, "REFUSED API-GATEWAY"},
		{"file bytes differ from the recorded digest", rec(f.record, "--file", differing), 3, "REFUSED " + binName},
		{"artifact with a failing gate", rec(failing.record, art("api-gateway="+imgDigest)...), 3, "trivy-os-linux-amd64"},
		{"every refusal is named", rec(f.record, append(art("nope="+imgDigest), art("api-gateway="+otherDigest)...)...), 3, "REFUSED nope|REFUSED api-gateway"},
		{"one refusal among qualified items", rec(f.record, append(art("api-gateway="+imgDigest), "--file", differing)...), 3, "REFUSED " + binName},
		{"invalid record refuses everything", rec(invalid, art("api-gateway="+imgDigest)...), 3, "REFUSED record"},
		{"no items", rec(f.record), 2, ""},
		{"artifact without '='", rec(f.record, art("api-gateway")...), 2, ""},
		{"artifact with an empty digest", rec(f.record, art("api-gateway=")...), 2, ""},
		{"artifact with an empty name", rec(f.record, art("="+imgDigest)...), 2, ""},
		{"missing --record", []string{"check", "--artifact", "api-gateway=" + imgDigest}, 2, ""},
		{"unreadable record", rec(filepath.Join(f.dir, "missing.json"), art("api-gateway="+imgDigest)...), 2, ""},
		{"unreadable file", rec(f.record, "--file", filepath.Join(f.dir, "missing")), 2, ""},
		{"unknown flag", rec(f.record, "--bogus", "x"), 2, ""},
		{"stray positional argument", rec(f.record, append(art("api-gateway="+imgDigest), "extra")...), 2, ""},
		{"unknown subcommand", []string{"frobnicate"}, 2, ""},
		{"no subcommand", nil, 2, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(c.args...)
			if code != c.want {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, c.want, stdout, stderr)
			}
			got := stdout
			if c.want == 3 {
				got = stderr
			}
			for _, want := range strings.Split(c.out, "|") {
				if !strings.Contains(got, want) {
					t.Fatalf("output %q lacks %q", got, want)
				}
			}
		})
	}
}

// helperCmd re-runs this test binary as the helper child, which creates marker.
// Extra arguments go before the marker, which stays last.
func helperCmd(marker string, extra ...string) []string {
	return append(append([]string{os.Args[0], "-test.run=TestHelperProcess", "--"}, extra...), marker)
}

func publishArgs(record, marker string, items ...string) []string {
	args := append([]string{"publish", "--record", record}, items...)
	return append(append(args, "--"), helperCmd(marker)...)
}

func TestPublishRefusesUnqualifiedDigestWithoutStartingCommand(t *testing.T) {
	f := newFixture(t, "pass")
	failing := newFixture(t, "fail")
	raw, err := os.ReadFile(f.record)
	if err != nil {
		t.Fatal(err)
	}
	altered := strings.Replace(string(raw), f.binDigest, otherDigest, 1)
	if altered == string(raw) {
		t.Fatal("test bug: the binary digest was not altered")
	}
	alteredRec := writeFile(t, filepath.Join(f.dir, "altered.json"), []byte(altered))
	if _, err := releasequal.Parse([]byte(altered)); err != nil {
		t.Fatalf("test bug: the altered record must stay valid so only the digest check can refuse it: %v", err)
	}
	brokenRec := writeFile(t, filepath.Join(f.dir, "broken.json"), []byte(`{"schema_version":1`))
	invalidRec := writeFile(t, filepath.Join(f.dir, "invalid.json"), []byte(`{"schema_version":1}`))
	differing := writeFile(t, filepath.Join(t.TempDir(), binName), []byte("different bytes"))
	cases := []struct {
		name   string
		record string
		items  []string
	}{
		{"a digest that is not the recorded one", f.record, []string{"--artifact", "api-gateway=" + otherDigest}},
		{"a record in which one digest was altered", alteredRec, []string{"--file", f.binPath}},
		{"file bytes that differ from the recorded binary", f.record, []string{"--file", differing}},
		{"an artifact that is not in the record", f.record, []string{"--artifact", "nope=" + imgDigest}},
		{"an artifact with a failing gate", failing.record, []string{"--artifact", "api-gateway=" + imgDigest}},
		{"one unqualified item among qualified ones", f.record, []string{"--artifact", "api-gateway=" + imgDigest, "--file", differing}},
		{"a record that is not JSON", brokenRec, []string{"--artifact", "api-gateway=" + imgDigest}},
		{"a record that fails Parse", invalidRec, []string{"--artifact", "api-gateway=" + imgDigest}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(helperEnv, "1")
			marker := filepath.Join(t.TempDir(), "marker")
			mustRun(t, 3, publishArgs(c.record, marker, c.items...)...)
			absent(t, marker)
		})
	}
	t.Run("positive control: the qualified digests start the command", func(t *testing.T) {
		t.Setenv(helperEnv, "1")
		marker := filepath.Join(t.TempDir(), "marker")
		mustRun(t, 0, publishArgs(f.record, marker, "--artifact", "api-gateway="+imgDigest, "--file", f.binPath)...)
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("qualified publish did not start the command: %v", err)
		}
	})
}

func TestPublishReturnsCommandExitCode(t *testing.T) {
	f := newFixture(t, "pass")
	t.Setenv(helperEnv, "1")
	t.Setenv(helperExitEnv, "7")
	marker := filepath.Join(t.TempDir(), "marker")
	mustRun(t, 7, publishArgs(f.record, marker, "--artifact", "api-gateway="+imgDigest)...)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the command was not started: %v", err)
	}
}

func TestPublishUsageErrors(t *testing.T) {
	f := newFixture(t, "pass")
	item := []string{"--artifact", "api-gateway=" + imgDigest}
	pub := append([]string{"publish", "--record", f.record}, item...)
	cmd := helperCmd(filepath.Join(f.dir, "marker"))
	cases := map[string][]string{
		"no -- separator":                     append(append([]string{}, pub...), cmd...),
		"nothing after --":                    append(append([]string{}, pub...), "--"),
		"a command that cannot be started":    append(append([]string{}, pub...), "--", filepath.Join(f.dir, "no-such-command")),
		"no items before --":                  append([]string{"publish", "--record", f.record, "--"}, cmd...),
		"unreadable record":                   append([]string{"publish", "--record", filepath.Join(f.dir, "missing.json"), "--artifact", "api-gateway=" + imgDigest, "--"}, cmd...),
		"a command flag is not read as ours":  append([]string{"publish", "--"}, helperCmd("m", "--record", f.record)...),
		"unknown release-qual flag before --": append(append([]string{"publish", "--bogus", "1"}, pub[1:]...), append([]string{"--"}, cmd...)...),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mustRun(t, 2, args...)
			absent(t, filepath.Join(f.dir, "marker"))
		})
	}
}

// The CLI is the file boundary: it must bound what it hands the library. A valid
// record padded with whitespace parses fine, so only the size cap can refuse it.
func TestJSONInputsAreSizeBounded(t *testing.T) {
	f := newFixture(t, "pass")
	pad := bytes.Repeat([]byte(" "), 5<<20)
	padded := func(name string) string {
		data, err := os.ReadFile(filepath.Join(f.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return writeFile(t, filepath.Join(f.dir, "big-"+name), append(data, pad...))
	}
	bigRec, bigFrag := padded("rec.json"), padded("img.json")
	mustRun(t, 2, "validate", bigRec)
	mustRun(t, 2, "check", "--record", bigRec, "--artifact", "api-gateway="+imgDigest)
	mustRun(t, 2, "assemble", "--out", filepath.Join(f.dir, "o.json"), "--ref", "refs/tags/v9.9.9", "--run-url", runURL,
		"--repository", "cordum-io/cordum", "--sha", sourceSHA, bigFrag)
	absent(t, filepath.Join(f.dir, "o.json"))
}
