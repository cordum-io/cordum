package scripts_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type releaseWorkflowStep struct {
	Name            string               `yaml:"name"`
	ID              string               `yaml:"id"`
	If              yaml.Node            `yaml:"if"`
	Uses            string               `yaml:"uses"`
	Run             string               `yaml:"run"`
	With            map[string]yaml.Node `yaml:"with"`
	Env             map[string]string    `yaml:"env"`
	ContinueOnError yaml.Node            `yaml:"continue-on-error"`
}

type releaseWorkflowJob struct {
	Uses            string            `yaml:"uses"`
	With            yaml.Node         `yaml:"with"`
	Permissions     yaml.Node         `yaml:"permissions"`
	Needs           yaml.Node         `yaml:"needs"`
	If              string            `yaml:"if"`
	ContinueOnError yaml.Node         `yaml:"continue-on-error"`
	Env             map[string]string `yaml:"env"`
	Secrets         yaml.Node         `yaml:"secrets"`
	Strategy        struct {
		Matrix struct {
			Include []map[string]string `yaml:"include"`
			Binary  []string            `yaml:"binary"`
			Target  []map[string]string `yaml:"target"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []releaseWorkflowStep `yaml:"steps"`
}

type releaseWorkflow struct {
	Permissions yaml.Node                     `yaml:"permissions"`
	On          map[string]yaml.Node          `yaml:"on"`
	Env         map[string]string             `yaml:"env"`
	Jobs        map[string]releaseWorkflowJob `yaml:"jobs"`
}

const (
	buildPushAction = "docker/build-push-action@"
	trivyAction     = "aquasecurity/trivy-action@"
	uploadAction    = "actions/upload-artifact@"
	downloadAction  = "actions/download-artifact@"
	buildDigestRef  = "steps.build.outputs.digest"
	recordJob       = "qualification-record"
	// realReleaseCondition is the only predicate that marks a v* tag push.
	realReleaseCondition = "startsWith(github.ref, 'refs/tags/v')"
)

var (
	releaseImageJobs = []string{"control-plane", "dashboard", "demo-quickstart-worker"}

	buildCommandPattern = regexp.MustCompile(`\bdocker\s+(buildx\s+)?build\b|\bbuildx\s+bake\b`)
	// taggingPattern matches commands that write a tag or push to a registry.
	taggingPattern = regexp.MustCompile(`\bimagetools\s+create\b|\bdocker\s+(image\s+)?(tag|push)\b|` +
		`\bdocker\s+manifest\s+(create|push)\b|\bcrane(@\S+)?\s+(tag|copy|cp|push|append|mutate)\b|` +
		`\boras\s+(tag|push|copy|cp)\b|\bregctl\b|\bskopeo\s+copy\b`)
	signingPattern = regexp.MustCompile(`\bcosign\s+(sign|attest|attach|copy)\b`)
	entryPattern   = regexp.MustCompile(`"\$RELEASE_QUAL"\s+entry\b`)
	publishPattern = regexp.MustCompile(`"\$RELEASE_QUAL"\s+publish\b`)
	// realOnlyRunPattern matches run lines that log in, sign, or write to GitHub or a chart registry.
	realOnlyRunPattern = regexp.MustCompile(`\b(docker|helm|crane|oras|regctl)\b.*\blogin\b|\bcosign\b|` +
		`\bgh\s+(issue|label|release|pr|workflow|secret|variable|repo)\b|\bhelm\s+push\b|` +
		`\bgh\s+api\b.*(\s(-X|--method)[\s=]*(POST|PUT|PATCH|DELETE)\b|\s(-f|-F|--field|--raw-field|--input)\b)`)
	realOnlyActions = []string{"docker/login-action@", "sigstore/cosign-installer@", "github/codeql-action/upload-sarif@"}
)

// loadReleaseWorkflow returns the cordum repo root and the parsed
// .github/workflows/docker.yml.
func loadReleaseWorkflow(t *testing.T) (string, releaseWorkflow) {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	root := filepath.Join(filepath.Dir(current), "..", "..")
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "docker.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	var workflow releaseWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse release workflow: %v", err)
	}
	return root, workflow
}

// normalizeCondition trims an if: expression, strips one ${{ }} wrapper and
// collapses whitespace.
func normalizeCondition(cond string) string {
	c := strings.TrimSpace(cond)
	if strings.HasPrefix(c, "${{") && strings.HasSuffix(c, "}}") {
		c = c[3 : len(c)-2]
	}
	return strings.Join(strings.Fields(c), " ")
}

// requiresRealRelease is true only when cond has no || and one of its
// top-level && conjuncts is exactly realReleaseCondition, so the condition can
// only hold on a v* tag push.
func requiresRealRelease(cond string) bool {
	c := normalizeCondition(cond)
	if strings.Contains(c, "||") {
		return false
	}
	return slices.Contains(topLevelConjuncts(c), realReleaseCondition)
}

// topLevelConjuncts splits c at && outside parentheses and quoted strings.
func topLevelConjuncts(c string) []string {
	var parts []string
	depth, quoted, start := 0, false, 0
	for i := 0; i < len(c); i++ {
		switch {
		case c[i] == '\'':
			quoted = !quoted
		case quoted:
		case c[i] == '(':
			depth++
		case c[i] == ')':
			depth--
		case depth == 0 && strings.HasPrefix(c[i:], "&&"):
			parts = append(parts, strings.TrimSpace(c[start:i]))
			start = i + 2
			i++
		}
	}
	return append(parts, strings.TrimSpace(c[start:]))
}

// logicalLines joins backslash-newline continuations, drops comment lines and
// collapses whitespace, so each entry is one shell command line.
func logicalLines(run string) []string {
	var lines []string
	var pending strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(run, "\r\n", "\n"), "\n") {
		if strings.HasSuffix(line, "\\") {
			pending.WriteString(strings.TrimSuffix(line, "\\") + " ")
			continue
		}
		pending.WriteString(line)
		full := strings.Join(strings.Fields(pending.String()), " ")
		pending.Reset()
		if full != "" && !strings.HasPrefix(full, "#") {
			lines = append(lines, full)
		}
	}
	if rest := strings.Join(strings.Fields(pending.String()), " "); rest != "" {
		lines = append(lines, rest)
	}
	return lines
}

func stepLabel(step releaseWorkflowStep) string {
	if step.ID != "" {
		return step.ID
	}
	return step.Name
}

func violation(code, job string, step *releaseWorkflowStep, format string, a ...any) string {
	where := job
	if step != nil {
		where += "/" + stepLabel(*step)
	}
	return fmt.Sprintf("%s %s: %s", code, where, fmt.Sprintf(format, a...))
}

// findStep returns the index of the step whose id or name is key, or -1.
func findStep(job releaseWorkflowJob, key string) int {
	return slices.IndexFunc(job.Steps, func(s releaseWorkflowStep) bool { return s.ID == key || s.Name == key })
}

func stepRunMatches(step releaseWorkflowStep, re *regexp.Regexp) bool {
	return slices.ContainsFunc(logicalLines(step.Run), re.MatchString)
}

// stepText is everything a step passes to a command: env, with and run.
func stepText(step releaseWorkflowStep) string {
	var b strings.Builder
	for _, key := range slices.Sorted(maps.Keys(step.Env)) {
		b.WriteString(step.Env[key] + "\n")
	}
	for _, key := range slices.Sorted(maps.Keys(step.With)) {
		b.WriteString(step.With[key].Value + "\n")
	}
	return b.String() + strings.Join(logicalLines(step.Run), "\n")
}

func ignoresFailure(value yaml.Node) bool {
	return value.Kind != 0 && (value.Tag != "!!bool" || value.Value != "false")
}

func jobNeeds(job releaseWorkflowJob) []string {
	switch job.Needs.Kind {
	case yaml.ScalarNode:
		return []string{job.Needs.Value}
	case yaml.SequenceNode:
		var needs []string
		if err := job.Needs.Decode(&needs); err == nil {
			return needs
		}
	}
	return nil
}

// checkBuildOnce (rule a): one image build per job, pushed by digest only.
func checkBuildOnce(name string, job releaseWorkflowJob) []string {
	var out []string
	builds := 0
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, buildPushAction) {
			builds++
		}
		for _, line := range logicalLines(step.Run) {
			builds += len(buildCommandPattern.FindAllString(line, -1))
		}
	}
	if builds != 1 {
		out = append(out, violation("BUILD_NOT_ONCE", name, nil, "want exactly one image build, found %d", builds))
	}
	i := findStep(job, "build")
	if i < 0 || !strings.HasPrefix(job.Steps[i].Uses, buildPushAction) {
		return append(out, violation("BUILD_NOT_BY_DIGEST", name, nil, "no docker/build-push-action step with id build"))
	}
	build := &job.Steps[i]
	if _, ok := build.With["tags"]; ok {
		out = append(out, violation("BUILD_NOT_BY_DIGEST", name, build, "the build must not apply tags"))
	}
	if strings.EqualFold(build.With["push"].Value, "true") {
		out = append(out, violation("BUILD_NOT_BY_DIGEST", name, build, "push: true pushes tags; push through outputs by digest"))
	}
	fields := map[string]bool{}
	for _, field := range strings.Split(build.With["outputs"].Value, ",") {
		fields[strings.Trim(strings.TrimSpace(field), `"`)] = true
	}
	if !fields["push-by-digest=true"] || !fields["push=true"] {
		out = append(out, violation("BUILD_NOT_BY_DIGEST", name, build, "outputs %q lacks push-by-digest=true and push=true", build.With["outputs"].Value))
	}
	if build.If.Kind != 0 || ignoresFailure(build.ContinueOnError) {
		out = append(out, violation("FAILURE_IGNORED", name, build, "the build must carry no if and no continue-on-error"))
	}
	return out
}

// checkGatesOnDigest (rule b): the smoke check and the single Trivy step scan
// the pushed digest, and nothing lets the job continue past them. Returns the
// index after which tags, signatures and entries may run.
func checkGatesOnDigest(name string, job releaseWorkflowJob) (int, []string) {
	var out []string
	build, gates := findStep(job, "build"), len(job.Steps)
	smoke := findStep(job, "smoke")
	if smoke < 0 {
		out = append(out, violation("GATE_NOT_ON_DIGEST", name, nil, "no step with id smoke"))
	} else {
		step := &job.Steps[smoke]
		text := stepText(*step)
		if smoke < build || !strings.Contains(text, buildDigestRef) || strings.Contains(text, ":verify") {
			out = append(out, violation("GATE_NOT_ON_DIGEST", name, step, "the smoke check must follow build and use %s, never :verify", buildDigestRef))
		}
		if step.If.Kind != 0 || ignoresFailure(step.ContinueOnError) {
			out = append(out, violation("FAILURE_IGNORED", name, step, "the smoke check must carry no if and no continue-on-error"))
		}
	}
	trivy := -1
	for i, step := range job.Steps {
		if strings.HasPrefix(step.Uses, trivyAction) {
			if trivy >= 0 {
				out = append(out, violation("TRIVY_CONFIG", name, &job.Steps[i], "want exactly one Trivy step"))
			}
			trivy = i
		}
	}
	if trivy < 0 {
		return gates, append(out, violation("TRIVY_CONFIG", name, nil, "no Trivy step"))
	}
	out = append(out, checkTrivyStep(name, job, trivy, build)...)
	if smoke >= 0 {
		gates = max(smoke, trivy)
	}
	return gates, out
}

// checkTrivyStep keeps the Trivy gate's strength. With format: sarif,
// trivy-action v0.36.0 (ed142fd0) entrypoint.sh:75-82 unsets TRIVY_SEVERITY
// unless limit-severities-for-sarif is "true", so the effective gate is every
// fixed OS-package finding at any severity (ignore-unfixed: true, vuln-type:
// os), not only the `severity: CRITICAL` the step names (verified in
// task-278cbdd7). Setting that input, ignoring findings, or letting the job
// continue past the scan would weaken the gate.
func checkTrivyStep(name string, job releaseWorkflowJob, trivy, build int) []string {
	var out []string
	step := &job.Steps[trivy]
	ref := step.With["image-ref"].Value
	if trivy < build || !strings.HasSuffix(ref, "@${{ "+buildDigestRef+" }}") || strings.Contains(ref, ":verify") {
		out = append(out, violation("GATE_NOT_ON_DIGEST", name, step, "image-ref %q must follow build and end with @${{ %s }}", ref, buildDigestRef))
	}
	if got := step.With["exit-code"].Value; got != "1" {
		out = append(out, violation("TRIVY_CONFIG", name, step, "exit-code = %q, want \"1\"", got))
	}
	if limit, ok := step.With["limit-severities-for-sarif"]; ok && strings.EqualFold(limit.Value, "true") {
		out = append(out, violation("TRIVY_CONFIG", name, step, "limit-severities-for-sarif: true narrows the gate"))
	}
	if _, ok := step.With["trivyignores"]; ok {
		out = append(out, violation("TRIVY_CONFIG", name, step, "trivyignores would ignore findings"))
	}
	if step.If.Kind != 0 {
		out = append(out, violation("TRIVY_CONFIG", name, step, "the Trivy step must not carry an if: condition"))
	}
	if ignoresFailure(step.ContinueOnError) {
		out = append(out, violation("FAILURE_IGNORED", name, step, "the Trivy step must not continue on error"))
	}
	return out
}

// checkAfterGates (rules c, e, f): tagging, signing and the qualification entry
// run only after both gates; the entry records both gates and the exceptions;
// published tags are resolved after the last tagging step.
func checkAfterGates(name string, job releaseWorkflowJob, gates int) []string {
	var out []string
	lastTag, entry := -1, -1
	for i := range job.Steps {
		step := &job.Steps[i]
		for _, rule := range []struct {
			code string
			re   *regexp.Regexp
		}{{"TAG_BEFORE_GATES", taggingPattern}, {"SIGN_BEFORE_GATES", signingPattern}, {"ENTRY_BEFORE_GATES", entryPattern}} {
			if stepRunMatches(*step, rule.re) && i <= gates {
				out = append(out, violation(rule.code, name, step, "runs before both the smoke and the Trivy gate"))
			}
		}
		if stepRunMatches(*step, taggingPattern) {
			lastTag = i
		}
		if stepRunMatches(*step, entryPattern) && entry < 0 {
			entry = i
		}
	}
	if lastTag < 0 {
		out = append(out, violation("TAG_BYPASSES_PUBLISH", name, nil, "no tagging step"))
	}
	out = append(out, checkEntry(name, job, entry)...)
	verify := findStep(job, "verify-tags")
	if verify <= lastTag || job.Steps[verify].If.Kind != 0 || !stepRunMatches(job.Steps[verify], regexp.MustCompile(`\bimagetools\s+inspect\b`)) {
		out = append(out, violation("TAGS_NOT_VERIFIED", name, nil, "verify-tags must follow the last tagging step, run unconditionally and use imagetools inspect"))
	}
	return out
}

func checkEntry(name string, job releaseWorkflowJob, entry int) []string {
	if entry < 0 {
		return []string{violation("ENTRY_CONTENT", name, nil, "no step runs \"$RELEASE_QUAL\" entry")}
	}
	var out []string
	step := &job.Steps[entry]
	if step.If.Kind != 0 || ignoresFailure(step.ContinueOnError) {
		out = append(out, violation("ENTRY_CONTENT", name, step, "the entry records pass, so it must carry no if and no continue-on-error"))
	}
	run := strings.Join(logicalLines(step.Run), "\n")
	for _, want := range []string{"--gate image-smoke=pass", "--gate trivy-os-linux-amd64=pass",
		"--exception non-amd64-variant-not-gated=", "--exception production-gate-not-digest-bound="} {
		if !strings.Contains(run, want) {
			out = append(out, violation("ENTRY_CONTENT", name, step, "missing %q", want))
		}
	}
	uploaded := slices.ContainsFunc(job.Steps[entry+1:], func(s releaseWorkflowStep) bool {
		return strings.HasPrefix(s.Uses, uploadAction) && strings.HasPrefix(s.With["name"].Value, "qual-entry-")
	})
	if !uploaded {
		out = append(out, violation("ENTRY_CONTENT", name, step, "no qual-entry-* upload follows the entry"))
	}
	return out
}

func checkImageJobs(wf releaseWorkflow) []string {
	var out []string
	for _, name := range releaseImageJobs {
		job, ok := wf.Jobs[name]
		if !ok {
			out = append(out, violation("JOB_NEEDS", name, nil, "required image job missing"))
			continue
		}
		out = append(out, checkBuildOnce(name, job)...)
		gates, gateViolations := checkGatesOnDigest(name, job)
		out = append(out, gateViolations...)
		out = append(out, checkAfterGates(name, job, gates)...)
	}
	return out
}

// isPublishCommand reports whether the command matched at offset at is the CMD
// of the closest preceding "$RELEASE_QUAL" publish ... -- on the same line,
// with no shell separator or substitution in between.
func isPublishCommand(line string, at int) bool {
	locs := publishPattern.FindAllStringIndex(line[:at], -1)
	if len(locs) == 0 {
		return false
	}
	between := line[locs[len(locs)-1][0]:at]
	return strings.Contains(between+" ", " -- ") && !strings.ContainsAny(between, ";&|`") && !strings.Contains(between, "$(")
}

// checkPublishGate (rule d): every tagging or signing command in every job is
// release-qual publish's CMD.
func checkPublishGate(wf releaseWorkflow) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(wf.Jobs)) {
		for i := range wf.Jobs[name].Steps {
			step := &wf.Jobs[name].Steps[i]
			for _, line := range logicalLines(step.Run) {
				for code, re := range map[string]*regexp.Regexp{"TAG_BYPASSES_PUBLISH": taggingPattern, "SIGN_BYPASSES_PUBLISH": signingPattern} {
					for _, m := range re.FindAllStringIndex(line, -1) {
						if !isPublishCommand(line, m[0]) {
							out = append(out, violation(code, name, step, "%q is not the CMD of \"$RELEASE_QUAL\" publish ... --", line))
						}
					}
				}
			}
		}
	}
	return out
}

func isRealOnlyStep(step releaseWorkflowStep) bool {
	if slices.ContainsFunc(realOnlyActions, func(prefix string) bool { return strings.HasPrefix(step.Uses, prefix) }) {
		return true
	}
	return stepRunMatches(step, realOnlyRunPattern) || strings.Contains(stepText(step), "secrets.")
}

// checkDryRunIsolation (rule g): only v* tags and release-dry-run/** branch
// pushes trigger the workflow, and everything that logs in, signs, writes to
// GitHub, pushes the chart or reads a secret runs only on a v* tag.
func checkDryRunIsolation(wf releaseWorkflow) []string {
	var out []string
	var push map[string][]string
	want := map[string][]string{"tags": {"v*"}, "branches": {"release-dry-run/**"}}
	node, ok := wf.On["push"]
	if len(wf.On) != 1 || !ok || node.Decode(&push) != nil || !maps.EqualFunc(push, want, slices.Equal) {
		out = append(out, violation("TRIGGER", "on", nil, "want exactly push tags [v*] and branches [release-dry-run/**]"))
	}
	for key, value := range wf.Env {
		if strings.Contains(value, "secrets.") {
			out = append(out, violation("DRY_RUN_REACHES_REAL", "env", nil, "workflow env %s reads a secret in every job", key))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(wf.Jobs)) {
		job := wf.Jobs[name]
		jobReal := requiresRealRelease(job.If)
		envSecret := slices.ContainsFunc(slices.Collect(maps.Values(job.Env)), func(v string) bool { return strings.Contains(v, "secrets.") })
		if !jobReal && (job.Secrets.Kind != 0 || envSecret) {
			out = append(out, violation("DRY_RUN_REACHES_REAL", name, nil, "job secrets or env secrets without %s", realReleaseCondition))
		}
		for i := range job.Steps {
			step := &job.Steps[i]
			if isRealOnlyStep(*step) && !jobReal && !requiresRealRelease(step.If.Value) {
				out = append(out, violation("DRY_RUN_REACHES_REAL", name, step, "real-only step without a %s conjunct", realReleaseCondition))
			}
		}
	}
	return out
}

// checkJobGraph (rule h): gated jobs keep GitHub's default success condition,
// the published-image smoke and the chart run only on v* tags, and the chart
// waits for the smoke and the record.
func checkJobGraph(wf releaseWorkflow) []string {
	var out []string
	for _, name := range releaseImageJobs {
		if job, ok := wf.Jobs[name]; ok && job.If != "" {
			out = append(out, violation("JOB_CONDITION", name, nil, "must keep the default success condition, got if: %q", job.If))
		}
	}
	for _, name := range []string{"smoke-published-images", "helm-chart"} {
		if job, ok := wf.Jobs[name]; ok && normalizeCondition(job.If) != realReleaseCondition {
			out = append(out, violation("JOB_CONDITION", name, nil, "if must be exactly %s, got %q", realReleaseCondition, job.If))
		}
	}
	for name, required := range map[string][]string{
		"smoke-published-images": releaseImageJobs,
		recordJob:                releaseImageJobs,
		"helm-chart":             {"smoke-published-images", recordJob},
	} {
		job, ok := wf.Jobs[name]
		if !ok {
			out = append(out, violation("JOB_NEEDS", name, nil, "required job missing"))
			continue
		}
		for _, dependency := range required {
			if !slices.Contains(jobNeeds(job), dependency) {
				out = append(out, violation("JOB_NEEDS", name, nil, "must need %s", dependency))
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(wf.Jobs)) {
		job := wf.Jobs[name]
		if ignoresFailure(job.ContinueOnError) {
			out = append(out, violation("FAILURE_IGNORED", name, nil, "job continue-on-error"))
		}
		for i := range job.Steps {
			if ignoresFailure(job.Steps[i].ContinueOnError) {
				out = append(out, violation("FAILURE_IGNORED", name, &job.Steps[i], "step continue-on-error"))
			}
		}
	}
	return out
}

// checkRecordJob (rule i): the record job expects exactly the images the image
// jobs build, assembles and validates their entries, and uploads the record.
func checkRecordJob(wf releaseWorkflow) []string {
	job, ok := wf.Jobs[recordJob]
	if !ok {
		return []string{violation("RECORD_JOB", recordJob, nil, "job missing")}
	}
	var out []string
	var expected []string
	for _, entry := range wf.Jobs["control-plane"].Strategy.Matrix.Include {
		expected = append(expected, entry["name"])
	}
	expected = append(expected, "dashboard", "demo-quickstart-worker")
	sort.Strings(expected)
	if got := job.Env["EXPECTED_IMAGES"]; got != strings.Join(expected, " ") {
		out = append(out, violation("RECORD_JOB", recordJob, nil, "EXPECTED_IMAGES = %q, want %q", got, strings.Join(expected, " ")))
	}
	download := slices.IndexFunc(job.Steps, func(s releaseWorkflowStep) bool {
		return strings.HasPrefix(s.Uses, downloadAction) && s.With["pattern"].Value == "qual-entry-*"
	})
	assemble := slices.IndexFunc(job.Steps, func(s releaseWorkflowStep) bool {
		run := strings.Join(logicalLines(s.Run), "\n")
		return strings.Contains(run, `"$RELEASE_QUAL" assemble`) && strings.Contains(run, `"$RELEASE_QUAL" validate`) &&
			strings.Contains(run, "EXPECTED_IMAGES")
	})
	upload := slices.IndexFunc(job.Steps, func(s releaseWorkflowStep) bool {
		return strings.HasPrefix(s.Uses, uploadAction) && s.With["name"].Value == recordJob
	})
	if download < 0 || assemble < download || upload < assemble {
		out = append(out, violation("RECORD_JOB", recordJob, nil,
			"want download qual-entry-*, then assemble+validate+EXPECTED_IMAGES check, then upload %s (got %d, %d, %d)", recordJob, download, assemble, upload))
	}
	return out
}

func allReleaseViolations(wf releaseWorkflow) []string {
	var out []string
	for _, check := range []func(releaseWorkflow) []string{checkImageJobs, checkPublishGate, checkDryRunIsolation, checkJobGraph, checkRecordJob, checkTrivyCoverageReason} {
		out = append(out, check(wf)...)
	}
	return out
}

// Keep the record's coverage claim bound to the SARIF gate described above
// checkTrivyStep. Any change to the claim or these inputs needs a joint review.
func checkTrivyCoverageReason(wf releaseWorkflow) []string {
	const want = "The Trivy gate fails on fixed OS-package findings at any severity " +
		"(SARIF without limit-severities-for-sarif); unfixed findings are ignored " +
		"(ignore-unfixed), and application dependencies such as Go modules and npm " +
		"packages are not scanned (vuln-type os)."
	var out []string
	if wf.Env["QUAL_REASON_TRIVY_OS_ONLY"] != want {
		out = append(out, violation("TRIVY_COVERAGE", "env", nil, "QUAL_REASON_TRIVY_OS_ONLY must describe fixed OS findings at any severity, not a CRITICAL-only gate"))
	}
	for _, name := range releaseImageJobs {
		for i := range wf.Jobs[name].Steps {
			step := &wf.Jobs[name].Steps[i]
			if !strings.HasPrefix(step.Uses, trivyAction) {
				continue
			}
			for key, value := range map[string]string{"format": "sarif", "ignore-unfixed": "true", "vuln-type": "os"} {
				if step.With[key].Value != value {
					out = append(out, violation("TRIVY_COVERAGE", name, step, "%s must be %q to match the coverage reason", key, value))
				}
			}
		}
	}
	return out
}

func assertNoReleaseViolations(t *testing.T, violations []string) {
	t.Helper()
	for _, v := range violations {
		t.Error(v)
	}
}

func TestReleaseChartWaitsForPublishedImages(t *testing.T) {
	_, wf := loadReleaseWorkflow(t)
	assertNoReleaseViolations(t, checkJobGraph(wf))
}

func TestReleaseGatesRunOnPushedDigest(t *testing.T) {
	_, wf := loadReleaseWorkflow(t)
	assertNoReleaseViolations(t, checkImageJobs(wf))
}

func TestReleaseTaggingGoesThroughPublishGate(t *testing.T) {
	_, wf := loadReleaseWorkflow(t)
	assertNoReleaseViolations(t, checkPublishGate(wf))
}

func TestReleaseDryRunCannotReachRealServices(t *testing.T) {
	_, wf := loadReleaseWorkflow(t)
	assertNoReleaseViolations(t, checkDryRunIsolation(wf))
}

func TestReleaseQualificationRecordCoversEveryImage(t *testing.T) {
	_, wf := loadReleaseWorkflow(t)
	assertNoReleaseViolations(t, checkRecordJob(wf))
}

func TestReleaseTrivyCoverageReasonMatchesGate(t *testing.T) {
	_, wf := loadReleaseWorkflow(t)
	assertNoReleaseViolations(t, checkTrivyCoverageReason(wf))
}

func TestReleaseConditionAndCommandHelpers(t *testing.T) {
	for cond, want := range map[string]bool{
		"startsWith(github.ref, 'refs/tags/v')":                                     true,
		"${{  startsWith(github.ref,  'refs/tags/v') }}":                            true,
		"always() && startsWith(github.ref, 'refs/tags/v') && hashFiles('x') != ''": true,
		"": false,
		"startsWith(github.ref, 'refs/tags/v') || startsWith(github.ref, 'refs/heads/release-dry-run/')": false,
		"!startsWith(github.ref, 'refs/tags/v')":                                                         false,
		"(always() || startsWith(github.ref, 'refs/tags/v'))":                                            false,
		"format('{0}', startsWith(github.ref, 'refs/tags/v') && true)":                                   false,
		"startsWith(github.ref, 'refs/heads/release-dry-run/')":                                          false,
	} {
		if got := requiresRealRelease(cond); got != want {
			t.Errorf("requiresRealRelease(%q) = %t, want %t", cond, got, want)
		}
	}
	for line, want := range map[string]bool{
		`"$RELEASE_QUAL" publish --record r --artifact a=d -- docker buildx imagetools create --tag t s`: true,
		`"$RELEASE_QUAL" publish --record r --artifact a=d -- cosign sign --yes r@d`:                     true,
		`docker buildx imagetools create --tag t s`:                                                      false,
		`"$RELEASE_QUAL" publish --record r --artifact a=d -- true; cosign sign --yes r@d`:               false,
		`"$RELEASE_QUAL" publish --record r --artifact a=d -- true && docker push r:t`:                   false,
		`"$RELEASE_QUAL" publish --record r --artifact a=d -- $(cosign sign --yes r@d)`:                  false,
		`"$RELEASE_QUAL" publish --record r --artifact a=d cosign sign --yes r@d`:                        false,
	} {
		lines := logicalLines(line)
		m := regexp.MustCompile(taggingPattern.String()+"|"+signingPattern.String()).FindAllStringIndex(lines[0], -1)
		if len(m) == 0 {
			t.Fatalf("fixture %q has no tagging or signing command", line)
		}
		if got := isPublishCommand(lines[0], m[len(m)-1][0]); got != want {
			t.Errorf("isPublishCommand(%q) = %t, want %t", line, got, want)
		}
	}
	if got := logicalLines("# cosign sign x\n  docker buildx imagetools create \\\n    --tag t s\r\n"); !slices.Equal(got, []string{"docker buildx imagetools create --tag t s"}) {
		t.Errorf("logicalLines = %q", got)
	}
}

// mutableStep returns the step with id or name key in job, failing the test
// when it is missing so no mutation can pass vacuously.
func mutableStep(t *testing.T, wf *releaseWorkflow, job, key string) *releaseWorkflowStep {
	t.Helper()
	i := findStep(wf.Jobs[job], key)
	if i < 0 {
		t.Fatalf("mutation target %s/%s missing", job, key)
	}
	return &wf.Jobs[job].Steps[i]
}

func moveStepBefore(t *testing.T, wf *releaseWorkflow, job, key, before string) {
	t.Helper()
	j := wf.Jobs[job]
	from, to := findStep(j, key), findStep(j, before)
	if from < 0 || to < 0 || from < to {
		t.Fatalf("mutation target %s/%s (%d) must follow %s (%d)", job, key, from, before, to)
	}
	step := j.Steps[from]
	j.Steps = slices.Insert(slices.Delete(j.Steps, from, from+1), to, step)
	wf.Jobs[job] = j
}

func stripPublishWrapper(t *testing.T, wf *releaseWorkflow, job, key string) {
	t.Helper()
	step := mutableStep(t, wf, job, key)
	stripped := regexp.MustCompile(`"\$RELEASE_QUAL" publish [^\n]*? -- `).ReplaceAllString(step.Run, "")
	if stripped == step.Run {
		t.Fatalf("mutation target %s/%s has no publish wrapper", job, key)
	}
	step.Run = stripped
}

func TestReleaseWorkflowMutationsAreCaught(t *testing.T) {
	testBinaryWorkflowMutations(t)
	scalar := func(v string) yaml.Node { return yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
	for _, tc := range []struct {
		name   string
		want   string
		mutate func(t *testing.T, wf *releaseWorkflow)
	}{
		{"sign before Trivy (control-plane)", "SIGN_BEFORE_GATES", func(t *testing.T, wf *releaseWorkflow) {
			moveStepBefore(t, wf, "control-plane", "sign", "trivy")
		}},
		{"publish before smoke (dashboard)", "TAG_BEFORE_GATES", func(t *testing.T, wf *releaseWorkflow) {
			moveStepBefore(t, wf, "dashboard", "publish", "smoke")
		}},
		{"publish wrapper stripped (demo-quickstart-worker)", "TAG_BYPASSES_PUBLISH", func(t *testing.T, wf *releaseWorkflow) {
			stripPublishWrapper(t, wf, "demo-quickstart-worker", "publish")
		}},
		{"sign wrapper stripped (control-plane)", "SIGN_BYPASSES_PUBLISH", func(t *testing.T, wf *releaseWorkflow) {
			stripPublishWrapper(t, wf, "control-plane", "sign")
		}},
		{"build applies tags (control-plane)", "BUILD_NOT_BY_DIGEST", func(t *testing.T, wf *releaseWorkflow) {
			mutableStep(t, wf, "control-plane", "build").With["tags"] = scalar("${{ steps.meta.outputs.tags }}")
		}},
		{"GHCR login if cleared (dashboard)", "DRY_RUN_REACHES_REAL", func(t *testing.T, wf *releaseWorkflow) {
			mutableStep(t, wf, "dashboard", "Login to GHCR").If = yaml.Node{}
		}},
		{"sign if cleared (demo-quickstart-worker)", "DRY_RUN_REACHES_REAL", func(t *testing.T, wf *releaseWorkflow) {
			mutableStep(t, wf, "demo-quickstart-worker", "sign").If = yaml.Node{}
		}},
		{"Docker Hub login if widened (control-plane)", "DRY_RUN_REACHES_REAL", func(t *testing.T, wf *releaseWorkflow) {
			step := mutableStep(t, wf, "control-plane", "Login to Docker Hub")
			step.If = scalar(step.If.Value + " || startsWith(github.ref, 'refs/heads/release-dry-run/')")
		}},
		{"helm-chart if cleared", "DRY_RUN_REACHES_REAL", func(t *testing.T, wf *releaseWorkflow) {
			job := wf.Jobs["helm-chart"]
			if job.If == "" {
				t.Fatal("mutation target helm-chart has no if")
			}
			job.If = ""
			wf.Jobs["helm-chart"] = job
		}},
		{"Trivy scans the :verify rebuild (dashboard)", "GATE_NOT_ON_DIGEST", func(t *testing.T, wf *releaseWorkflow) {
			mutableStep(t, wf, "dashboard", "trivy").With["image-ref"] = scalar("cordum/dashboard:verify")
		}},
		{"Trivy reason claims CRITICAL only", "TRIVY_COVERAGE", func(t *testing.T, wf *releaseWorkflow) {
			wf.Env["QUAL_REASON_TRIVY_OS_ONLY"] = "The Trivy gate fails only on CRITICAL findings; lower severities do not fail it."
		}},
		{"Trivy no longer uses SARIF", "TRIVY_COVERAGE", func(t *testing.T, wf *releaseWorkflow) {
			mutableStep(t, wf, "dashboard", "trivy").With["format"] = scalar("table")
		}},
		{"Trivy includes unfixed findings", "TRIVY_COVERAGE", func(t *testing.T, wf *releaseWorkflow) {
			mutableStep(t, wf, "dashboard", "trivy").With["ignore-unfixed"] = scalar("false")
		}},
		{"Trivy includes application dependencies", "TRIVY_COVERAGE", func(t *testing.T, wf *releaseWorkflow) {
			mutableStep(t, wf, "dashboard", "trivy").With["vuln-type"] = scalar("os,library")
		}},
		{"mcp dropped from EXPECTED_IMAGES", "RECORD_JOB", func(t *testing.T, wf *releaseWorkflow) {
			env := wf.Jobs[recordJob].Env
			dropped := strings.Join(slices.DeleteFunc(strings.Fields(env["EXPECTED_IMAGES"]), func(s string) bool { return s == "mcp" }), " ")
			if dropped == env["EXPECTED_IMAGES"] {
				t.Fatal("mutation target EXPECTED_IMAGES has no mcp")
			}
			env["EXPECTED_IMAGES"] = dropped
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, wf := loadReleaseWorkflow(t)
			if baseline := allReleaseViolations(wf); len(baseline) > 0 {
				t.Fatalf("unmutated workflow is not clean: %s", strings.Join(baseline, "; "))
			}
			tc.mutate(t, &wf)
			got := allReleaseViolations(wf)
			if !slices.ContainsFunc(got, func(v string) bool { return strings.HasPrefix(v, tc.want+" ") }) {
				t.Fatalf("mutation not caught: want %s, got %q", tc.want, got)
			}
		})
	}
}

// checkFinalStageUpgradesAlpine reports an error when the final stage of a
// Dockerfile is based on an Alpine image but never runs
// `apk upgrade --no-cache`. Without it the image ships the base layer's
// packages as they were when the base was published (task-278cbdd7: the
// demo image shipped alpine:3.21's zlib 1.3.2-r1, CVE-2026-4738).
//
// Any `<` in the Dockerfile fails closed (task-0a00a0dd). dockerfileInstructions
// knows nothing of heredocs, so it would read heredoc data as instructions:
// after `RUN cat <<'EOF'`, a data line `RUN apk upgrade --no-cache` would
// count as the upgrade, and a data line `FROM scratch` would hide the Alpine
// final stage. Rejecting only `<<` is not enough, because BuildKit joins
// continuation lines before it looks for a heredoc: `RUN cat <\` followed by
// a line `<'EOF'` opens one too.
func checkFinalStageUpgradesAlpine(dockerfile string) error {
	if i := strings.IndexByte(dockerfile, '<'); i >= 0 {
		return fmt.Errorf("line %d has `<`: heredoc data cannot be told apart from instructions, so keep `<` out of this Dockerfile", strings.Count(dockerfile[:i], "\n")+1)
	}
	instructions := dockerfileInstructions(dockerfile)

	last := -1
	for i, instruction := range instructions {
		if strings.HasPrefix(strings.ToUpper(instruction), "FROM ") {
			last = i
		}
	}
	if last < 0 {
		return errors.New("no FROM instruction")
	}
	base := ""
	for _, token := range strings.Fields(instructions[last])[1:] {
		if !strings.HasPrefix(token, "--") {
			base = token
			break
		}
	}
	if !strings.Contains(strings.ToLower(base), "alpine") {
		return nil
	}
	for _, instruction := range instructions[last+1:] {
		if strings.HasPrefix(strings.ToUpper(instruction), "RUN ") && runExecutesApkUpgrade(instruction[len("RUN "):]) {
			return nil
		}
	}
	return fmt.Errorf("final stage %s does not run `apk upgrade --no-cache`", base)
}

var (
	// quotedShellText masks a single-quoted argument, a double-quoted argument
	// (in which a backslash escapes the next character) and a backslash-escaped
	// character outside quotes, as sh reads them.
	quotedShellText = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"|\\.`)
	shellComment    = regexp.MustCompile(`(^|\s)#.*$`)
)

// shellLeavers are words that let a command end the shell with status 0
// before a later command in the same && list runs (`exit 0`, `exec true`,
// `return 0`), or run text this check cannot see (`.`, source, eval).
var shellLeavers = []string{"exit", "exec", "return", ".", "source", "eval"}

// runExecutesApkUpgrade reports whether the shell form of a RUN instruction
// (continuation lines already joined) runs `apk upgrade --no-cache` so that
// the RUN can succeed only if the upgrade ran and succeeded. The text does not
// count when it sits in a shell comment or in a quoted argument such as
// `echo 'apk upgrade --no-cache'` or `echo "\"; apk upgrade --no-cache; #"`;
// a quote left unbalanced after masking fails closed. Only a plain `&&` list
// is recognised: any `;`, `|`, `||` or `&` left after masking fails closed,
// because those forms can skip the upgrade (`true || apk upgrade`,
// `false && apk upgrade; true`), swallow its failure (`apk upgrade || true`,
// `apk upgrade | cat`) or run it in the background. Exec-form and heredoc
// RUNs are not recognised either.
//
// The upgrade command must be exactly `apk upgrade --no-cache`: another
// argument can make it change nothing (`--simulate`, `-s`) or upgrade only
// some packages. A command before it in the list that has a shellLeavers word
// anywhere fails closed, so a prefix such as `X=1 exit 0` cannot hide one.
// This is a token check, not a shell parser: keep the release Dockerfiles in
// the plain `RUN apk upgrade --no-cache && apk add ...` form.
func runExecutesApkUpgrade(script string) bool {
	script = quotedShellText.ReplaceAllString(script, "Q")
	script = shellComment.ReplaceAllString(script, "")
	if strings.ContainsAny(script, `'"`) {
		return false
	}
	commands := strings.Split(script, "&&")
	for _, command := range commands {
		if strings.ContainsAny(command, ";|&") {
			return false
		}
	}
	for _, command := range commands {
		words := strings.Fields(command)
		if slices.Equal(words, []string{"apk", "upgrade", "--no-cache"}) {
			return true
		}
		for _, word := range words {
			if slices.Contains(shellLeavers, word) {
				return false
			}
		}
	}
	return false
}

func TestReleaseFinalStageUpgradeFixtures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dockerfile string
		wantErr    bool
	}{
		{
			name: "pre-#531 demo final stage",
			dockerfile: "FROM golang:1.26-alpine AS builder\nRUN go build -o /out/worker ./demo\n" +
				"FROM alpine:3.21\nRUN apk add --no-cache ca-certificates wget\nUSER cordum\n",
			wantErr: true,
		},
		{
			name: "upgrade only in builder stage",
			dockerfile: "FROM golang:1.26-alpine AS builder\nRUN apk upgrade --no-cache\n" +
				"FROM alpine:3.24\nRUN apk add --no-cache ca-certificates\n",
			wantErr: true,
		},
		{
			name: "upgrade line commented out",
			dockerfile: "FROM alpine:3.24\n# RUN apk upgrade --no-cache && apk add --no-cache ca-certificates\n" +
				"RUN apk add --no-cache ca-certificates\n",
			wantErr: true,
		},
		{
			name:       "upgrade only in a trailing shell comment",
			dockerfile: "FROM alpine:3.24\nRUN apk add --no-cache ca-certificates # apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade only as an echo argument",
			dockerfile: "FROM alpine:3.24\nRUN echo 'apk upgrade --no-cache'\n",
			wantErr:    true,
		},
		{
			name:       "upgrade only in a double-quoted argument",
			dockerfile: "FROM alpine:3.24\nRUN apk add --no-cache ca-certificates && echo \"apk upgrade --no-cache\"\n",
			wantErr:    true,
		},
		{
			name:       "upgrade after a continued line that opens a comment",
			dockerfile: "FROM alpine:3.24\nRUN apk add --no-cache ca-certificates # then \\\n    && apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade only as a RUN line in heredoc data",
			dockerfile: "FROM alpine:3.24\nRUN cat <<'EOF'\nRUN apk upgrade --no-cache\nEOF\n",
			wantErr:    true,
		},
		{
			name:       "Alpine final stage hidden by a FROM line in heredoc data",
			dockerfile: "FROM alpine:3.24\nRUN cat <<'EOF'\nFROM scratch\nEOF\n",
			wantErr:    true,
		},
		{
			name:       "upgrade only in heredoc data opened across a continued line",
			dockerfile: "FROM alpine:3.24\nRUN cat <\\\n<'EOF'\nRUN apk upgrade --no-cache\nEOF\n",
			wantErr:    true,
		},
		{
			name:       "upgrade skipped by a short-circuit or list",
			dockerfile: "FROM alpine:3.24\nRUN true || apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade behind a failing and list that an or list rescues",
			dockerfile: "FROM alpine:3.24\nRUN false && apk upgrade --no-cache || true\n",
			wantErr:    true,
		},
		{
			name:       "upgrade failure swallowed by an or list",
			dockerfile: "FROM alpine:3.24\nRUN apk upgrade --no-cache || true\n",
			wantErr:    true,
		},
		{
			name:       "upgrade only in a double-quoted argument with an escaped quote",
			dockerfile: "FROM alpine:3.24\nRUN echo \"\\\"; apk upgrade --no-cache; #\"\n",
			wantErr:    true,
		},
		{
			name:       "upgrade only in a double-quoted and list with escaped quotes",
			dockerfile: "FROM alpine:3.24\nRUN echo \"\\\" && apk upgrade --no-cache && echo \\\"\"\n",
			wantErr:    true,
		},
		{
			name:       "upgrade after an unbalanced quote",
			dockerfile: "FROM alpine:3.24\nRUN echo \"unterminated && apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade skipped by an and list that a later command rescues",
			dockerfile: "FROM alpine:3.24\nRUN false && apk upgrade --no-cache; true\n",
			wantErr:    true,
		},
		{
			name:       "upgrade failure swallowed by a later command",
			dockerfile: "FROM alpine:3.24\nRUN apk upgrade --no-cache ; apk add --no-cache ca-certificates\n",
			wantErr:    true,
		},
		{
			name:       "upgrade failure hidden by a pipeline",
			dockerfile: "FROM alpine:3.24\nRUN apk upgrade --no-cache | cat\n",
			wantErr:    true,
		},
		{
			name:       "upgrade run in the background",
			dockerfile: "FROM alpine:3.24\nRUN apk upgrade --no-cache & wait\n",
			wantErr:    true,
		},
		{
			name:       "upgrade after a command that exits the shell",
			dockerfile: "FROM alpine:3.24\nRUN exit 0 && apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade after a command that replaces the shell",
			dockerfile: "FROM alpine:3.24\nRUN exec true && apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade after a top-level return",
			dockerfile: "FROM alpine:3.24\nRUN return 0 && apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade after a dot-sourced file",
			dockerfile: "FROM alpine:3.24\nRUN . /etc/profile && apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade after a sourced file",
			dockerfile: "FROM alpine:3.24\nRUN source /etc/profile && apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade after an eval",
			dockerfile: "FROM alpine:3.24\nRUN eval true && apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade after an exit behind a variable assignment",
			dockerfile: "FROM alpine:3.24\nRUN X=1 exit 0 && apk upgrade --no-cache\n",
			wantErr:    true,
		},
		{
			name:       "upgrade only simulated",
			dockerfile: "FROM alpine:3.24\nRUN apk upgrade --no-cache --simulate\n",
			wantErr:    true,
		},
		{
			name:       "upgrade only simulated with the short flag",
			dockerfile: "FROM alpine:3.24\nRUN apk upgrade --no-cache -s\n",
			wantErr:    true,
		},
		{
			name:       "upgrade limited to one package",
			dockerfile: "FROM alpine:3.24\nRUN apk upgrade --no-cache zlib\n",
			wantErr:    true,
		},
		{
			name:       "upgrade skipped inside a command substitution",
			dockerfile: "FROM alpine:3.24\nRUN echo $(false && apk upgrade --no-cache && true)\n",
			wantErr:    true,
		},
		{
			name:       "upgrade skipped inside a backtick command substitution",
			dockerfile: "FROM alpine:3.24\nRUN echo `false && apk upgrade --no-cache && true`\n",
			wantErr:    true,
		},
		{
			name:       "upgrade skipped inside a command substitution in double quotes",
			dockerfile: "FROM alpine:3.24\nRUN echo \"$(echo \" && apk upgrade --no-cache && \")\"\n",
			wantErr:    true,
		},
		{
			name:       "upgrade skipped inside a parameter expansion",
			dockerfile: "FROM alpine:3.24\nRUN echo ${X:-false && apk upgrade --no-cache && true}\n",
			wantErr:    true,
		},
		{
			name:       "upgrade skipped inside a process substitution",
			dockerfile: "FROM alpine:3.24\nRUN echo >(false && apk upgrade --no-cache && true)\n",
			wantErr:    true,
		},
		{
			name:       "upgrade skipped inside a negated subshell",
			dockerfile: "FROM alpine:3.24\nRUN ! (false && apk upgrade --no-cache && true)\n",
			wantErr:    true,
		},
		{
			name:       "upgrade only inside an ANSI-C quoted argument",
			dockerfile: "FROM alpine:3.24\nRUN echo $'\\' && apk upgrade --no-cache && true '\\'\n",
			wantErr:    true,
		},
		{
			name:       "upgrade after an escaped quote outside quotes",
			dockerfile: "FROM alpine:3.24\nRUN echo \\\" && apk upgrade --no-cache\n",
		},
		{
			name:       "upgrade before an echo with escaped quotes",
			dockerfile: "FROM alpine:3.24\nRUN apk upgrade --no-cache && echo \"say \\\"hi\\\"\"\n",
		},
		{
			name:       "upgrade chained after another command",
			dockerfile: "FROM alpine:3.24\nRUN apk add --no-cache ca-certificates && apk upgrade --no-cache\n",
		},
		{
			name:       "upgrade before a command that exits the shell",
			dockerfile: "FROM alpine:3.24\nRUN apk upgrade --no-cache && exit 0\n",
		},
		{
			name:       "root Dockerfile style",
			dockerfile: "FROM golang:1.26-alpine AS builder\r\nFROM alpine:3.24\r\nRUN apk upgrade --no-cache \\\r\n    && apk add --no-cache ca-certificates\r\nUSER cordum\r\n",
		},
		{
			name:       "non-alpine final stage",
			dockerfile: "FROM golang:1.26-alpine AS builder\nFROM gcr.io/distroless/static-debian12:nonroot\nCOPY --from=builder /out/app /app\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkFinalStageUpgradesAlpine(tc.dockerfile)
			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
		})
	}
}

const dryReleaseCondition = "startsWith(github.ref, 'refs/heads/release-dry-run/')"

func loadBinaryWorkflow(t *testing.T) (string, releaseWorkflow, releaseWorkflow) {
	t.Helper()
	root, docker := loadReleaseWorkflow(t)
	return root, docker, readWorkflow(t, filepath.Join(root, ".github/workflows/release.yml"))
}

func readWorkflow(t *testing.T, file string) releaseWorkflow {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var wf releaseWorkflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	return wf
}

func contentsPermission(node yaml.Node, value string) bool {
	var perms map[string]string
	return node.Kind == yaml.MappingNode && node.Decode(&perms) == nil && maps.Equal(perms, map[string]string{"contents": value})
}

func checkBinaryWiring(docker, binary releaseWorkflow) []string {
	var out []string
	add := func(message string) { out = append(out, "BINARY_WIRING "+message) }
	if _, ok := binary.On["workflow_call"]; !ok || len(binary.On) != 1 {
		add("release.yml must only accept workflow_call")
	}
	if !contentsPermission(binary.Permissions, "read") {
		add("called workflow must grant only contents read")
	}
	for name, job := range binary.Jobs {
		if job.Permissions.Kind != 0 {
			add(name + " declares job permissions")
		}
		for _, step := range job.Steps {
			for _, command := range []string{"gh release", "gh api", "git push", "docker login", "docker push", "cosign", "helm push"} {
				if strings.Contains(step.Run, command) {
					add(name + " contains " + command)
				}
			}
		}
	}
	for _, name := range []string{"Resolve pushed and prior tags", "Enforce monotonicity"} {
		job := binary.Jobs["version-monotonicity"]
		i := findStep(job, name)
		if i < 0 || normalizeCondition(job.Steps[i].If.Value) != realReleaseCondition {
			add(name + " must require a real tag")
		}
	}
	return append(out, checkBinaryCallers(docker)...)
}

func checkBinaryCallers(docker releaseWorkflow) []string {
	var out []string
	add := func(message string) { out = append(out, "BINARY_WIRING "+message) }
	callers := 0
	for name, job := range docker.Jobs {
		if job.Uses != "./.github/workflows/release.yml" {
			continue
		}
		callers++
		want := realReleaseCondition
		if name == "binaries-dry-run" {
			want = dryReleaseCondition
		} else if name != "binaries" {
			add("unexpected caller " + name)
		}
		if normalizeCondition(job.If) != want || !contentsPermission(job.Permissions, "read") || job.With.Kind != 0 {
			add(name + " caller configuration")
		}
		if name == "binaries-dry-run" && job.Secrets.Kind != 0 {
			add("dry caller passes secrets")
		}
		if name == "binaries" && (job.Secrets.Kind != yaml.ScalarNode || job.Secrets.Value != "inherit") {
			add("real caller must inherit secrets")
		}
	}
	if callers != 2 {
		add("want exactly two callers")
	}
	return out
}

func binaryNames(wf releaseWorkflow) []string {
	var names []string
	matrix := wf.Jobs["build"].Strategy.Matrix
	for _, bin := range matrix.Binary {
		for _, target := range matrix.Target {
			names = append(names, bin+"-"+target["goos"]+"-"+target["goarch"]+target["ext"])
		}
	}
	sort.Strings(names)
	return names
}

func artifactStep(job releaseWorkflowJob, action, name string) int {
	return slices.IndexFunc(job.Steps, func(s releaseWorkflowStep) bool {
		return strings.HasPrefix(s.Uses, action) && s.With["name"].Value == name
	})
}

func checkBinaryRecord(docker, binary releaseWorkflow) []string {
	var out []string
	add := func(message string) { out = append(out, "BINARY_RECORD "+message) }
	want := binaryNames(binary)
	if len(want) != 15 || len(slices.Compact(slices.Clone(want))) != 15 {
		add("matrix must build 15 distinct binaries")
	}
	for _, names := range []string{binary.Env["EXPECTED_BINARIES"], docker.Jobs[recordJob].Env["EXPECTED_BINARIES"]} {
		got := strings.Fields(names)
		sort.Strings(got)
		if !slices.Equal(got, want) {
			add("EXPECTED_BINARIES differs from build matrix")
		}
	}
	out = append(out, checkBinaryEntry(binary.Jobs["sign-manifest"])...)
	record := docker.Jobs[recordJob]
	if record.If != "${{ !cancelled() && !failure() }}" {
		add("record must tolerate the skipped caller without ignoring failure")
	}
	for _, need := range append(slices.Clone(releaseImageJobs), "binaries", "binaries-dry-run") {
		if !slices.Contains(jobNeeds(record), need) {
			add("record must need " + need)
		}
	}
	if !slices.ContainsFunc(record.Steps, func(s releaseWorkflowStep) bool {
		return strings.Contains(s.Run, `select(.kind == "binary")`) && strings.Contains(s.Run, `"$EXPECTED_BINARIES"`)
	}) {
		add("record must check binary names")
	}
	return out
}

func checkBinaryEntry(job releaseWorkflowJob) []string {
	var out []string
	add := func(message string) { out = append(out, "BINARY_RECORD "+message) }
	stage, qualify, bundle := findStep(job, "Stage binaries"), findStep(job, "qualify-binaries"), artifactStep(job, uploadAction, "release-bundle")
	if stage < 0 || qualify <= stage || bundle <= qualify {
		add("qualification must follow staging and precede bundle upload")
	}
	if qualify >= 0 {
		step := job.Steps[qualify]
		if step.If.Kind != 0 || ignoresFailure(step.ContinueOnError) {
			add("qualification must run and succeed")
		}
		for _, text := range []string{`"$RELEASE_QUAL" entry --kind binary`, `--exception "binary-no-release-gate=`} {
			if !strings.Contains(step.Run, text) {
				add("entry missing " + text)
			}
		}
	}
	upload := artifactStep(job, uploadAction, "qual-entry-binaries")
	if upload <= qualify || upload < 0 || job.Steps[upload].With["overwrite"].Value != "true" || job.Steps[upload].With["if-no-files-found"].Value != "error" {
		add("entry upload must follow qualification and replace its artifact")
	}
	return out
}

var createReleasePattern = regexp.MustCompile(`\bgh\s+release\s+create\b`)

func checkBinaryPublish(docker releaseWorkflow) []string {
	var out []string
	add := func(message string) { out = append(out, "BINARY_PUBLISH "+message) }
	job, ok := docker.Jobs["publish-binaries"]
	if !ok {
		return []string{"BINARY_PUBLISH missing job"}
	}
	if !requiresRealRelease(job.If) || !slices.Contains(topLevelConjuncts(normalizeCondition(job.If)), "!contains(github.ref, '-rc')") {
		add("publication must require a non-rc real tag")
	}
	if !contentsPermission(job.Permissions, "write") {
		add("publication needs contents write only")
	}
	for _, need := range []string{"binaries", recordJob} {
		if !slices.Contains(jobNeeds(job), need) {
			add("publication must need " + need)
		}
	}
	create, manifest := findStep(job, "create-release"), findStep(job, "Require signed manifest on canonical release")
	if create < 0 {
		return append(out, "BINARY_PUBLISH missing create-release")
	}
	if manifest < 0 || manifest >= create {
		add("signed manifest check must precede publication")
	}
	for _, name := range []string{"release-bundle", recordJob} {
		i := artifactStep(job, downloadAction, name)
		if i < 0 || i >= create {
			add("publication must download " + name)
		}
	}
	return append(out, checkBinaryCreateCommand(job.Steps[create])...)
}

func checkBinaryCreateCommand(step releaseWorkflowStep) []string {
	var out []string
	add := func(message string) { out = append(out, "BINARY_PUBLISH "+message) }
	if strings.Contains(step.Run, "${{") {
		add("expressions belong in env")
	}
	commands := 0
	for _, line := range logicalLines(step.Run) {
		for _, loc := range createReleasePattern.FindAllStringIndex(line, -1) {
			commands++
			if !isPublishCommand(line, loc[0]) || !strings.Contains(line[:loc[0]], `--record "$RUNNER_TEMP/qualification-record.json"`) {
				add("create must be the record gate's command")
			}
		}
	}
	if commands != 1 {
		add("expected one create command")
	}
	return out
}

func TestReleaseBinariesRunOnlyFromDockerWorkflow(t *testing.T) {
	root, docker, binary := loadBinaryWorkflow(t)
	assertNoReleaseViolations(t, checkBinaryWiring(docker, binary))
	files, err := filepath.Glob(filepath.Join(root, ".github/workflows/*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Base(file) == "docker.yml" {
			continue
		}
		for name, job := range readWorkflow(t, file).Jobs {
			if strings.Contains(job.Uses, "/release.yml") {
				t.Errorf("unexpected caller %s/%s", file, name)
			}
		}
	}
}

func TestReleaseQualificationRecordCoversEveryBinary(t *testing.T) {
	_, docker, binary := loadBinaryWorkflow(t)
	assertNoReleaseViolations(t, checkBinaryRecord(docker, binary))
}

func TestReleaseBinaryPublishGoesThroughGate(t *testing.T) {
	root, docker, _ := loadBinaryWorkflow(t)
	assertNoReleaseViolations(t, checkBinaryPublish(docker))
	files, err := filepath.Glob(filepath.Join(root, ".github/workflows/*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, file := range files {
		for name, job := range readWorkflow(t, file).Jobs {
			for _, step := range job.Steps {
				if !stepRunMatches(step, createReleasePattern) {
					continue
				}
				count++
				if filepath.Base(file) != "docker.yml" || name != "publish-binaries" || step.ID != "create-release" {
					t.Errorf("unexpected publication %s/%s/%s", file, name, step.ID)
				}
			}
		}
	}
	if count != 1 {
		t.Errorf("create steps = %d, want 1", count)
	}
}

func binaryViolations(docker, binary releaseWorkflow) []string {
	out := checkBinaryWiring(docker, binary)
	out = append(out, checkBinaryRecord(docker, binary)...)
	return append(out, checkBinaryPublish(docker)...)
}

func mutateBinaryJob(t *testing.T, wf *releaseWorkflow, name string, change func(*releaseWorkflowJob)) {
	t.Helper()
	job, ok := wf.Jobs[name]
	if !ok {
		t.Fatalf("missing mutation target %s", name)
	}
	change(&job)
	wf.Jobs[name] = job
}

func removeBinaryNeed(t *testing.T, wf *releaseWorkflow, name, need string) {
	t.Helper()
	mutateBinaryJob(t, wf, name, func(job *releaseWorkflowJob) {
		needs := jobNeeds(*job)
		if !slices.Contains(needs, need) {
			t.Fatalf("%s does not need %s", name, need)
		}
		needs = slices.DeleteFunc(needs, func(n string) bool { return n == need })
		if err := job.Needs.Encode(needs); err != nil {
			t.Fatal(err)
		}
	})
}

func testBinaryWorkflowMutations(t *testing.T) {
	t.Helper()
	scalar := func(v string) yaml.Node { return yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
	write := yaml.Node{}
	if err := write.Encode(map[string]string{"contents": "write"}); err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		mutate func(*testing.T, *releaseWorkflow, *releaseWorkflow)
	}{
		{"dry caller secrets", func(t *testing.T, d, b *releaseWorkflow) {
			mutateBinaryJob(t, d, "binaries-dry-run", func(j *releaseWorkflowJob) { j.Secrets = scalar("inherit") })
		}},
		{"dry caller write token", func(t *testing.T, d, b *releaseWorkflow) {
			mutateBinaryJob(t, d, "binaries-dry-run", func(j *releaseWorkflowJob) { j.Permissions = write })
		}},
		{"real caller dry predicate", func(t *testing.T, d, b *releaseWorkflow) {
			mutateBinaryJob(t, d, "binaries", func(j *releaseWorkflowJob) { j.If = dryReleaseCondition })
		}},
		{"called workflow tag trigger", func(t *testing.T, d, b *releaseWorkflow) { b.On["push"] = scalar("tags: [v*]") }},
		{"called workflow write token", func(t *testing.T, d, b *releaseWorkflow) { b.Permissions = write }},
		{"binary create wrapper removed", func(t *testing.T, d, b *releaseWorkflow) {
			stripPublishWrapper(t, d, "publish-binaries", "create-release")
		}},
		{"binary publish predicate removed", func(t *testing.T, d, b *releaseWorkflow) {
			mutateBinaryJob(t, d, "publish-binaries", func(j *releaseWorkflowJob) { j.If = "!contains(github.ref, '-rc')" })
		}},
		{"binary publish record dependency removed", func(t *testing.T, d, b *releaseWorkflow) { removeBinaryNeed(t, d, "publish-binaries", recordJob) }},
		{"record dry caller dependency removed", func(t *testing.T, d, b *releaseWorkflow) { removeBinaryNeed(t, d, recordJob, "binaries-dry-run") }},
		{"record binary omitted", func(t *testing.T, d, b *releaseWorkflow) {
			names := strings.Fields(d.Jobs[recordJob].Env["EXPECTED_BINARIES"])
			if len(names) != 15 {
				t.Fatal("missing names")
			}
			d.Jobs[recordJob].Env["EXPECTED_BINARIES"] = strings.Join(names[1:], " ")
		}},
		{"binary entry removed", func(t *testing.T, d, b *releaseWorkflow) {
			mutateBinaryJob(t, b, "sign-manifest", func(j *releaseWorkflowJob) {
				i := findStep(*j, "qualify-binaries")
				if i < 0 {
					t.Fatal("missing entry")
				}
				j.Steps = slices.Delete(j.Steps, i, i+1)
			})
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			_, docker, binary := loadBinaryWorkflow(t)
			if got := binaryViolations(docker, binary); len(got) > 0 {
				t.Fatalf("baseline: %v", got)
			}
			mutation.mutate(t, &docker, &binary)
			if got := binaryViolations(docker, binary); len(got) == 0 {
				t.Fatal("mutation not caught")
			}
		})
	}
}

var releaseHarnessBuild struct {
	once     sync.Once
	dir, bin string
	err      error
}

func TestMain(m *testing.M) {
	if marker := os.Getenv("RELEASE_WORKFLOW_FAKE_GH_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte(strings.Join(os.Args[1:], "\n")), 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	code := m.Run()
	if releaseHarnessBuild.dir != "" {
		if err := os.RemoveAll(releaseHarnessBuild.dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	os.Exit(code)
}

func releaseHarnessBinary(t *testing.T, root string) string {
	t.Helper()
	releaseHarnessBuild.once.Do(func() {
		releaseHarnessBuild.dir, releaseHarnessBuild.err = os.MkdirTemp("", "release-workflow-build-")
		if releaseHarnessBuild.err != nil {
			return
		}
		releaseHarnessBuild.bin = filepath.Join(releaseHarnessBuild.dir, "release-qual"+testExecutableExtension())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", releaseHarnessBuild.bin, "./tools/releasequal/cmd/release-qual")
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			releaseHarnessBuild.err = fmt.Errorf("build release-qual: %w\n%s", err, output)
		}
	})
	if releaseHarnessBuild.err != nil {
		t.Fatal(releaseHarnessBuild.err)
	}
	return releaseHarnessBuild.bin
}

func testExecutableExtension() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func releaseHarnessBash(t *testing.T) string {
	t.Helper()
	bash := os.Getenv("RELEASE_WORKFLOW_TEST_BASH")
	if bash == "" && runtime.GOOS == "windows" {
		bash = filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe")
	}
	if bash == "" {
		bash = "bash"
	}
	resolved, err := exec.LookPath(bash)
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("Git Bash unavailable: %v", err)
		}
		t.Fatal(err)
	}
	return resolved
}

type binaryWorkspace struct {
	dir, bash, bin, marker string
	values                 map[string]string
}

func writeHarnessFile(t *testing.T, file string, content []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, content, mode); err != nil {
		t.Fatal(err)
	}
}

func newBinaryWorkspace(t *testing.T, root string, binary releaseWorkflow) binaryWorkspace {
	t.Helper()
	w := binaryWorkspace{dir: t.TempDir(), bash: releaseHarnessBash(t), bin: releaseHarnessBinary(t, root)}
	w.marker = filepath.Join(w.dir, "gh-marker")
	for i, name := range binaryNames(binary) {
		writeHarnessFile(t, filepath.Join(w.dir, "release", name), []byte(fmt.Sprintf("binary %d %s", i, name)), 0600)
	}
	for _, name := range []string{"SHA256SUMS", "SHA256SUMS.asc"} {
		writeHarnessFile(t, filepath.Join(w.dir, "release", name), []byte(name), 0600)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	writeHarnessFile(t, filepath.Join(w.dir, "fakebin", "gh"+testExecutableExtension()), data, 0755)
	if err := os.Mkdir(filepath.Join(w.dir, "gh-config"), 0700); err != nil {
		t.Fatal(err)
	}
	w.values = map[string]string{
		"RELEASE_QUAL": filepath.ToSlash(w.bin), "RUNNER_TEMP": filepath.ToSlash(w.dir),
		"GITHUB_REF": "refs/tags/v0.0.0-qualtest", "GITHUB_SHA": strings.Repeat("a", 40), "GITHUB_REPOSITORY": "cordum-io/cordum",
		"DRY_RUN": "false", "RUN_URL": "https://example.invalid/run/1", "GH_TOKEN": "", "GITHUB_TOKEN": "",
		"GH_CONFIG_DIR": filepath.ToSlash(filepath.Join(w.dir, "gh-config")), "GH_HOST": "example.invalid", "GH_REPO": "example.invalid/none/none",
		"RELEASE_WORKFLOW_FAKE_GH_MARKER": w.marker,
	}
	for key, value := range binary.Env {
		if key != "EXPECTED_BINARIES" && key != "BINARY_NO_RELEASE_GATE_REASON" {
			t.Fatalf("unknown workflow env %s", key)
		}
		w.values[key] = value
	}
	return w
}

func (w binaryWorkspace) run(t *testing.T, step releaseWorkflowStep) (int, string) {
	t.Helper()
	for key := range step.Env {
		if _, ok := w.values[key]; !ok {
			t.Fatalf("unknown step env %s", key)
		}
	}
	script := filepath.Join(w.dir, "step.sh")
	writeHarnessFile(t, script, []byte(strings.ReplaceAll(step.Run, "\r\n", "\n")), 0600)
	values := maps.Clone(w.values)
	values["PATH"] = filepath.Join(w.dir, "fakebin") + ":/usr/bin:/bin"
	if runtime.GOOS == "windows" {
		values["PATH"] = filepath.Join(w.dir, "fakebin") + ";" + filepath.Join(filepath.Dir(filepath.Dir(w.bash)), "usr", "bin")
		for _, key := range []string{"SystemRoot", "TEMP", "TMP"} {
			values[key] = os.Getenv(key)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.bash, "--noprofile", "--norc", "-eo", "pipefail", filepath.ToSlash(script))
	cmd.Dir = w.dir
	for _, key := range slices.Sorted(maps.Keys(values)) {
		cmd.Env = append(cmd.Env, key+"="+values[key])
	}
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("step timed out: %s", output)
	}
	if err == nil {
		return 0, string(output)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("start bash: %v", err)
	}
	return exit.ExitCode(), string(output)
}

func requiredBinaryStep(t *testing.T, wf releaseWorkflow, job, key string) releaseWorkflowStep {
	t.Helper()
	i := findStep(wf.Jobs[job], key)
	if i < 0 {
		t.Fatalf("missing %s/%s", job, key)
	}
	return wf.Jobs[job].Steps[i]
}

func (w binaryWorkspace) qualify(t *testing.T, step releaseWorkflowStep) {
	t.Helper()
	if code, output := w.run(t, step); code != 0 {
		t.Fatalf("qualification rc=%d: %s", code, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.bin, "assemble", "--out", filepath.Join(w.dir, "qualification-record.json"), "--ref", w.values["GITHUB_REF"], "--run-url", w.values["RUN_URL"], "--repository", w.values["GITHUB_REPOSITORY"], "--sha", w.values["GITHUB_SHA"], filepath.Join(w.dir, "qual", "entry-binaries.json"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("assemble: %v\n%s", err, output)
	}
}

func TestReleaseBinaryPublishRefusesChangedBytes(t *testing.T) {
	root, docker, binary := loadBinaryWorkflow(t)
	qualify := requiredBinaryStep(t, binary, "sign-manifest", "qualify-binaries")
	publish := requiredBinaryStep(t, docker, "publish-binaries", "create-release")
	for _, name := range []string{"changed bytes", "unrecorded file", "positive control"} {
		t.Run(name, func(t *testing.T) {
			w := newBinaryWorkspace(t, root, binary)
			w.qualify(t, qualify)
			if name == "changed bytes" {
				file := filepath.Join(w.dir, "release", "cordum-hook-linux-amd64")
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				writeHarnessFile(t, file, append(data, 'x'), 0600)
			}
			if name == "unrecorded file" {
				writeHarnessFile(t, filepath.Join(w.dir, "release", "cordum-zz-unrecorded"), []byte("extra"), 0600)
			}
			code, output := w.run(t, publish)
			if name != "positive control" {
				if _, err := os.Stat(w.marker); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("gh marker exists or inaccessible: %v", err)
				}
				if code != 3 {
					t.Fatalf("rc=%d, want3: %s", code, output)
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
			if len(args) < 3 || !slices.Equal(args[:3], []string{"release", "create", "v0.0.0-qualtest"}) {
				t.Fatalf("gh args: %q", args)
			}
			for _, asset := range append(binaryNames(binary), "SHA256SUMS", "SHA256SUMS.asc") {
				if !slices.Contains(args, "release/"+asset) {
					t.Errorf("missing asset %s: %q", asset, args)
				}
			}
		})
	}
}

func TestReleaseBinaryEntryRequiresEveryExpectedBinary(t *testing.T) {
	root, _, binary := loadBinaryWorkflow(t)
	step := requiredBinaryStep(t, binary, "sign-manifest", "qualify-binaries")
	for _, name := range []string{"missing binary", "extra binary"} {
		t.Run(name, func(t *testing.T) {
			w := newBinaryWorkspace(t, root, binary)
			if name == "missing binary" {
				if err := os.Remove(filepath.Join(w.dir, "release", "cordum-hook-linux-amd64")); err != nil {
					t.Fatal(err)
				}
			} else {
				writeHarnessFile(t, filepath.Join(w.dir, "release", "cordum-zz-extra"), []byte("extra"), 0600)
			}
			if code, output := w.run(t, step); code == 0 {
				t.Fatalf("accepted incomplete set: %s", output)
			}
			if _, err := os.Stat(filepath.Join(w.dir, "qual", "entry-binaries.json")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("fragment exists or inaccessible: %v", err)
			}
		})
	}
}

func TestReleaseImagesUpgradeAlpinePackages(t *testing.T) {
	root, wf := loadReleaseWorkflow(t)
	files := map[string]bool{}
	for _, job := range wf.Jobs {
		scanned := false
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, trivyAction) {
				scanned = true
			}
		}
		if !scanned {
			continue
		}
		for _, step := range job.Steps {
			if file, ok := step.With["file"]; ok && strings.HasPrefix(step.Uses, buildPushAction) {
				files[path.Clean(file.Value)] = true
			}
		}
	}
	for _, want := range []string{"Dockerfile", "dashboard/Dockerfile", "demo/quickstart/Dockerfile"} {
		if !files[want] {
			t.Fatalf("scanned image jobs do not build %s; found %v", want, files)
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			if err := checkFinalStageUpgradesAlpine(string(data)); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		})
	}
}
