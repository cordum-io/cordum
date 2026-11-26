package scripts_test

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Rules for the keyless signature of the binary release manifest: release.yml
// sign-manifest signs SHA256SUMS, checks it with install.sh before the upload,
// and docker.yml publish-binaries requires and publishes the bundle.
const (
	signJob         = "sign-manifest"
	signBlobCommand = "cosign sign-blob --yes --bundle release/SHA256SUMS.sigstore.json release/SHA256SUMS"
	pinIdentityIf   = "github.repository != 'cordum-io/cordum' || !startsWith(github.ref, 'refs/tags/v')"
	identityEnv     = "CORDUM_COSIGN_IDENTITY_REGEXP"
	bundleFile      = "release/SHA256SUMS.sigstore.json"
	manifestGate    = "Require signed manifest on canonical release"
	canonicalRepo   = "github.repository == 'cordum-io/cordum'"
)

var (
	signingPermissions = map[string]string{"contents": "read", "id-token": "write"}
	// cosignCommandPattern matches cosign run as a command, not cosign-keyless.
	cosignCommandPattern = regexp.MustCompile(`(^|[\s;&|(/])cosign(\s|$)`)
	signingOrder         = []string{"Generate SHA256SUMS", "Install cosign", "sign-blob", "pin-signer-identity", "verify-release", "refuse-tampered"}
	installOverrides     = []string{"--install-dir", "CORDUM_INSTALL_DIR", "--insecure-allow-missing-cosign", "--dev-allow-unsigned", "--rollback-operator-override", identityEnv, "CORDUM_COSIGN_BIN"}
)

// exactPermissions is true when node grants exactly want.
func exactPermissions(node yaml.Node, want map[string]string) bool {
	var perms map[string]string
	return node.Kind == yaml.MappingNode && node.Decode(&perms) == nil && maps.Equal(perms, want)
}

// checkSigningSteps: sign-manifest signs, pins, verifies and refuses in order,
// before the bundle upload, and none of it can be skipped or ignored.
func checkSigningSteps(job releaseWorkflowJob) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, "BINARY_SIGNING "+fmt.Sprintf(format, a...)) }
	if normalizeCondition(job.If) != "always()" || ignoresFailure(job.ContinueOnError) {
		add("sign-manifest must keep if: always() and must not ignore failure")
	}
	if !exactPermissions(job.Permissions, signingPermissions) {
		add("sign-manifest must grant exactly contents read and id-token write")
	}
	last := -1
	for _, key := range append(slices.Clone(signingOrder), "release-bundle") {
		i := findStep(job, key)
		if key == "release-bundle" {
			i = artifactStep(job, uploadAction, key)
		}
		if i <= last {
			add("%s missing or out of order", key)
			continue
		}
		last = i
		want := ""
		if key == "pin-signer-identity" {
			want = pinIdentityIf
		}
		if normalizeCondition(job.Steps[i].If.Value) != want || ignoresFailure(job.Steps[i].ContinueOnError) {
			add("%s must run with if %q and must not ignore failure", key, want)
		}
	}
	if i := artifactStep(job, uploadAction, "release-bundle"); i >= 0 {
		paths := strings.Fields(job.Steps[i].With["path"].Value)
		if !slices.Contains(paths, bundleFile) || !slices.Contains(paths, "release/SHA256SUMS") {
			add("release-bundle must upload SHA256SUMS and %s", bundleFile)
		}
	}
	return append(out, checkSigningInstall(job)...)
}

// checkSigningInstall: install.sh runs on the release in verify-only mode with
// the built-in or pinned identity, and on the tampered copy with an install dir.
func checkSigningInstall(job releaseWorkflowJob) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, "BINARY_SIGNING "+fmt.Sprintf(format, a...)) }
	if i := findStep(job, "verify-release"); i >= 0 {
		text := stepText(job.Steps[i])
		if !strings.Contains(text, "bash tools/scripts/install.sh --release-dir release") || !strings.Contains(text, `CORDUM_BINARY_FLOOR_FILE="$RUNNER_TEMP/`) {
			add("verify-release must run install.sh on release with a floor under RUNNER_TEMP")
		}
		for _, flag := range installOverrides {
			if strings.Contains(text, flag) {
				add("verify-release passes %s", flag)
			}
		}
	}
	if i := findStep(job, "refuse-tampered"); i >= 0 {
		text := stepText(job.Steps[i])
		if !strings.Contains(text, "tools/scripts/install.sh") || !strings.Contains(text, "--install-dir") || !strings.Contains(text, "BINARY-VERIFY-FAIL: hash mismatch") {
			add("refuse-tampered must run install.sh with an install dir and expect a hash mismatch")
		}
		for _, flag := range installOverrides[1:] {
			if strings.Contains(text, flag) {
				add("refuse-tampered passes %s", flag)
			}
		}
	}
	if i := findStep(job, "pin-signer-identity"); i >= 0 {
		run := job.Steps[i].Run
		for _, text := range []string{"/.github/workflows/release.yml@${GITHUB_REF}", identityEnv + "=^", `>> "$GITHUB_ENV"`} {
			if !strings.Contains(run, text) {
				add("pin-signer-identity missing %s", text)
			}
		}
	}
	return out
}

// checkSigningCommands: sign-blob is release.yml's only cosign command, the
// installer is docker.yml's pin, and only pin-signer-identity sets the identity.
func checkSigningCommands(docker, binary releaseWorkflow) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, "BINARY_SIGNING "+fmt.Sprintf(format, a...)) }
	installers := map[string]bool{}
	for w, wf := range []releaseWorkflow{docker, binary} {
		if _, ok := wf.Env[identityEnv]; ok {
			add("workflow env sets %s", identityEnv)
		}
		for name, job := range wf.Jobs {
			if _, ok := job.Env[identityEnv]; ok {
				add("%s env sets %s", name, identityEnv)
			}
			for _, step := range job.Steps {
				pin := w == 1 && name == signJob && step.ID == "pin-signer-identity"
				if _, ok := step.Env[identityEnv]; ok || (strings.Contains(step.Run, identityEnv) && !pin) {
					add("%s/%s sets %s", name, stepLabel(step), identityEnv)
				}
				if strings.HasPrefix(step.Uses, "sigstore/cosign-installer@") {
					installers[step.Uses] = true
				}
			}
		}
	}
	uses := ""
	if i := findStep(binary.Jobs[signJob], "Install cosign"); i >= 0 {
		uses = binary.Jobs[signJob].Steps[i].Uses
	}
	if len(installers) != 1 || !installers[uses] {
		add("sign-manifest must install cosign with docker.yml's pin, got %q of %v", uses, slices.Sorted(maps.Keys(installers)))
	}
	for name, job := range binary.Jobs {
		for _, step := range job.Steps {
			if stepRunMatches(step, cosignCommandPattern) && (name != signJob || step.ID != "sign-blob") {
				add("%s/%s runs cosign; only sign-blob may", name, stepLabel(step))
			}
		}
	}
	if i := findStep(binary.Jobs[signJob], "sign-blob"); i >= 0 && strings.TrimSpace(binary.Jobs[signJob].Steps[i].Run) != signBlobCommand {
		add("sign-blob must run exactly %q", signBlobCommand)
	}
	return out
}

// checkSigningPublish: publication needs a real tag and, on the canonical
// repository, the bundle; create-release uploads it.
func checkSigningPublish(docker releaseWorkflow) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, "BINARY_SIGNING "+fmt.Sprintf(format, a...)) }
	job := docker.Jobs["publish-binaries"]
	if !requiresRealRelease(job.If) {
		add("publish-binaries must require %s", realReleaseCondition)
	}
	gate, create := findStep(job, manifestGate), findStep(job, "create-release")
	if gate < 0 || create < gate {
		return append(out, "BINARY_SIGNING the bundle check must precede create-release")
	}
	step := job.Steps[gate]
	if normalizeCondition(step.If.Value) != canonicalRepo || ignoresFailure(step.ContinueOnError) || !strings.Contains(step.Run, "[ ! -f "+bundleFile+" ]") {
		add("%s must require %s on %s", manifestGate, bundleFile, canonicalRepo)
	}
	if !strings.Contains(job.Steps[create].Run, bundleFile) {
		add("create-release must publish %s", bundleFile)
	}
	return out
}

func signingViolations(docker, binary releaseWorkflow) []string {
	out := checkSigningSteps(binary.Jobs[signJob])
	out = append(out, checkSigningCommands(docker, binary)...)
	out = append(out, checkSigningPublish(docker)...)
	return append(out, checkBinaryCallers(docker)...)
}

func TestReleaseBinarySigningRules(t *testing.T) {
	_, docker, binary := loadBinaryWorkflow(t)
	assertNoReleaseViolations(t, signingViolations(docker, binary))
}

func removeStep(t *testing.T, wf *releaseWorkflow, job, key string) {
	t.Helper()
	mutateBinaryJob(t, wf, job, func(j *releaseWorkflowJob) {
		i := findStep(*j, key)
		if i < 0 {
			t.Fatalf("mutation target %s/%s missing", job, key)
		}
		j.Steps = slices.Delete(j.Steps, i, i+1)
	})
}

func replaceInRun(t *testing.T, wf *releaseWorkflow, job, key, old, replacement string) {
	t.Helper()
	step := mutableStep(t, wf, job, key)
	if !strings.Contains(step.Run, old) {
		t.Fatalf("mutation target %s/%s has no %q", job, key, old)
	}
	step.Run = strings.Replace(step.Run, old, replacement, 1)
}

func TestReleaseBinarySigningMutationsAreCaught(t *testing.T) {
	yes := yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	readOnly := yaml.Node{}
	if err := readOnly.Encode(map[string]string{"contents": "read"}); err != nil {
		t.Fatal(err)
	}
	dropIDToken := func(wf func(d, b *releaseWorkflow) *releaseWorkflow, job string) func(*testing.T, *releaseWorkflow, *releaseWorkflow) {
		return func(t *testing.T, d, b *releaseWorkflow) {
			mutateBinaryJob(t, wf(d, b), job, func(j *releaseWorkflowJob) { j.Permissions = readOnly })
		}
	}
	docker := func(d, _ *releaseWorkflow) *releaseWorkflow { return d }
	binary := func(_, b *releaseWorkflow) *releaseWorkflow { return b }
	mutations := []struct {
		name   string
		mutate func(*testing.T, *releaseWorkflow, *releaseWorkflow)
	}{
		{"sign-blob removed", func(t *testing.T, d, b *releaseWorkflow) { removeStep(t, b, signJob, "sign-blob") }},
		{"sign-blob continue-on-error", func(t *testing.T, d, b *releaseWorkflow) {
			mutableStep(t, b, signJob, "sign-blob").ContinueOnError = yes
		}},
		{"sign-blob limited to real tags", func(t *testing.T, d, b *releaseWorkflow) {
			mutableStep(t, b, signJob, "sign-blob").If = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: realReleaseCondition}
		}},
		{"sign-blob writes no bundle", func(t *testing.T, d, b *releaseWorkflow) {
			replaceInRun(t, b, signJob, "sign-blob", " --bundle release/SHA256SUMS.sigstore.json", "")
		}},
		{"sign-manifest continue-on-error", func(t *testing.T, d, b *releaseWorkflow) {
			mutateBinaryJob(t, b, signJob, func(j *releaseWorkflowJob) { j.ContinueOnError = yes })
		}},
		{"sign-manifest id-token dropped", dropIDToken(binary, signJob)},
		{"binaries id-token dropped", dropIDToken(docker, "binaries")},
		{"binaries-dry-run id-token dropped", dropIDToken(docker, "binaries-dry-run")},
		{"verify-release removed", func(t *testing.T, d, b *releaseWorkflow) { removeStep(t, b, signJob, "verify-release") }},
		{"verify-release before sign-blob", func(t *testing.T, d, b *releaseWorkflow) {
			moveStepBefore(t, b, signJob, "verify-release", "sign-blob")
		}},
		{"verify-release installs", func(t *testing.T, d, b *releaseWorkflow) {
			replaceInRun(t, b, signJob, "verify-release", "--release-dir release", `--release-dir release --install-dir "$RUNNER_TEMP/bin"`)
		}},
		{"refuse-tampered removed", func(t *testing.T, d, b *releaseWorkflow) { removeStep(t, b, signJob, "refuse-tampered") }},
		{"bundle not uploaded", func(t *testing.T, d, b *releaseWorkflow) {
			job := b.Jobs[signJob]
			i := artifactStep(job, uploadAction, "release-bundle")
			if i < 0 {
				t.Fatal("missing release-bundle upload")
			}
			path := job.Steps[i].With["path"]
			if !strings.Contains(path.Value, bundleFile+"\n") {
				t.Fatalf("release-bundle path has no %s", bundleFile)
			}
			path.Value = strings.Replace(path.Value, bundleFile+"\n", "", 1)
			job.Steps[i].With["path"] = path
		}},
		{"pin-signer-identity unconditional", func(t *testing.T, d, b *releaseWorkflow) {
			mutableStep(t, b, signJob, "pin-signer-identity").If = yaml.Node{}
		}},
		{"identity override in job env", func(t *testing.T, d, b *releaseWorkflow) {
			mutateBinaryJob(t, b, signJob, func(j *releaseWorkflowJob) { j.Env = map[string]string{identityEnv: ".*"} })
		}},
		{"second cosign command", func(t *testing.T, d, b *releaseWorkflow) {
			step := mutableStep(t, b, signJob, "refuse-tampered")
			step.Run += "\ncosign verify-blob --bundle x release/SHA256SUMS\n"
		}},
		{"cosign installer pin differs", func(t *testing.T, d, b *releaseWorkflow) {
			mutableStep(t, b, signJob, "Install cosign").Uses = "sigstore/cosign-installer@v4"
		}},
		{"publish bundle check removed", func(t *testing.T, d, b *releaseWorkflow) { removeStep(t, d, "publish-binaries", manifestGate) }},
		{"publish reachable from a dry run", func(t *testing.T, d, b *releaseWorkflow) {
			dropConjunct(t, d, "publish-binaries", realReleaseCondition)
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			_, docker, binary := loadBinaryWorkflow(t)
			if got := signingViolations(docker, binary); len(got) > 0 {
				t.Fatalf("baseline: %v", got)
			}
			mutation.mutate(t, &docker, &binary)
			if got := signingViolations(docker, binary); len(got) == 0 {
				t.Fatal("mutation not caught")
			}
		})
	}
}

// TestReleaseBinarySigningPublishRequiresBundle runs the manifest gate and, when
// it passes, create-release, as publish-binaries does.
func TestReleaseBinarySigningPublishRequiresBundle(t *testing.T) {
	root, docker, binary := loadBinaryWorkflow(t)
	qualify := requiredBinaryStep(t, binary, signJob, "qualify-binaries")
	gate := requiredBinaryStep(t, docker, "publish-binaries", manifestGate)
	create := requiredBinaryStep(t, docker, "publish-binaries", "create-release")
	cases := []struct {
		name   string
		remove []string
		bundle bool
	}{
		{"asc without bundle", nil, false},
		{"bundle without manifest", []string{"SHA256SUMS", "SHA256SUMS.asc"}, true},
		{"bundle without asc", []string{"SHA256SUMS.asc"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newBinaryWorkspace(t, root, binary)
			w.qualify(t, qualify)
			for _, name := range tc.remove {
				if err := os.Remove(filepath.Join(w.dir, "release", name)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.bundle {
				writeHarnessFile(t, filepath.Join(w.dir, bundleFile), []byte("bundle"), 0600)
			}
			code, output := w.run(t, gate)
			if code == 0 {
				code, output = w.run(t, create)
			}
			if tc.name != "bundle without asc" {
				if _, err := os.Stat(w.marker); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("gh ran or marker inaccessible: %v", err)
				}
				if code == 0 {
					t.Fatalf("published: %s", output)
				}
				return
			}
			if code != 0 {
				t.Fatalf("rc=%d: %s", code, output)
			}
			data, err := os.ReadFile(w.marker)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(string(data), "\n")
			if !slices.Contains(args, bundleFile) || !slices.Contains(args, "release/SHA256SUMS") || slices.Contains(args, "release/SHA256SUMS.asc") {
				t.Fatalf("gh args: %q", args)
			}
		})
	}
}

// TestReleaseBinarySigningPinsExactIdentity runs pin-signer-identity and checks
// the regexp it exports with Go's engine, which cosign uses.
func TestReleaseBinarySigningPinsExactIdentity(t *testing.T) {
	root, _, binary := loadBinaryWorkflow(t)
	step := requiredBinaryStep(t, binary, signJob, "pin-signer-identity")
	w := newBinaryWorkspace(t, root, binary)
	ref := "refs/heads/release-dry-run/a.b+c(d)"
	identity := "https://github.com/cordum-io/cordum/.github/workflows/release.yml@" + ref
	env := filepath.Join(w.dir, "github-env")
	w.values["GITHUB_ENV"], w.values["GITHUB_SERVER_URL"], w.values["GITHUB_REF"] = filepath.ToSlash(env), "https://github.com", ref
	if code, output := w.run(t, step); code != 0 {
		t.Fatalf("rc=%d: %s", code, output)
	}
	data, err := os.ReadFile(env)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := strings.CutPrefix(strings.TrimRight(string(data), "\r\n"), identityEnv+"=")
	if !ok || strings.ContainsAny(value, "\r\n") {
		t.Fatalf("GITHUB_ENV = %q", data)
	}
	re, err := regexp.Compile(value)
	if err != nil {
		t.Fatal(err)
	}
	for candidate, want := range map[string]bool{
		identity: true,
		strings.Replace(identity, "a.b", "aXb", 1):        false,
		strings.Replace(identity, "b+c", "bbc", 1):        false,
		strings.Replace(identity, "(d)", "d", 1):          false,
		strings.Replace(identity, "/heads/", "/tags/", 1): false,
		identity + "x": false,
		"x" + identity: false,
	} {
		if re.MatchString(candidate) != want {
			t.Errorf("%q matches %q: got %v, want %v", value, candidate, !want, want)
		}
	}
}
