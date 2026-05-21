package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge/shadow"
)

// lifecycleFindingFixture returns a minimally-valid EDGE-141
// ShadowAgentFinding JSON for the CLI to consume.
func lifecycleFindingFixture(t *testing.T, mut func(*shadow.ShadowAgentFinding)) []byte {
	t.Helper()
	f := shadow.ShadowAgentFinding{
		FindingID:        "edge_shadow_cli_1",
		TenantID:         "tenant-alpha",
		OwnerPrincipalID: "owner@cordum.test",
		PrincipalID:      "scanner",
		AgentProduct:     "claude-code",
		Risk:             shadow.FindingRiskMedium,
		Status:           shadow.FindingStatusDetected,
		EvidenceType:     shadow.EvidenceConfigFile,
		EvidenceSummary:  "1 mcp servers configured",
		RedactedPath:     "~/.claude/settings.json",
		DetectedAt:       time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
		SignalSet:        []string{"unmanaged_claude_settings"},
	}
	if mut != nil {
		mut(&f)
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal lifecycle finding: %v", err)
	}
	return b
}

// scannerFindingFixture returns a minimally-valid EDGE-140 scanner
// Finding JSON.
func scannerFindingFixture(t *testing.T, mut func(*shadow.Finding)) []byte {
	t.Helper()
	f := shadow.Finding{
		TenantID:              "tenant-beta",
		PrincipalID:           "alice",
		Hostname:              "dev-mac",
		Product:               "claude-code",
		EvidenceType:          shadow.EvidenceConfigFile,
		RedactedPath:          "~/.claude/settings.json",
		RedactedConfigSummary: "1 mcp servers configured",
		Risk:                  shadow.RiskMedium,
		Status:                shadow.StatusObserved,
		ObservedAt:            time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
	}
	if mut != nil {
		mut(&f)
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal scanner finding: %v", err)
	}
	return b
}

func writeFixtureToTemp(t *testing.T, payload []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "finding.json")
	if err := os.WriteFile(p, payload, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p
}

func TestShadowRemediateCmd_LifecycleFile_TextOutput(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	path := writeFixtureToTemp(t, lifecycleFindingFixture(t, nil))

	code := runShadowRemediateCmd([]string{"--finding-file", path, "--audience", "dev"}, nil, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Cordum Edge shadow remediation plan",
		"Action kind:",
		"Audience:         dev",
		"Severity:         medium",
		"cordumctl edge claude",
		"Recommended action",
		"Safety notes",
		"Steps",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q; got %s", want, out)
		}
	}
}

func TestShadowRemediateCmd_LifecycleFile_JSONOutput(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	path := writeFixtureToTemp(t, lifecycleFindingFixture(t, nil))

	code := runShadowRemediateCmd([]string{"--finding-file", path, "--audience", "enterprise", "--json"}, nil, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d stderr=%s", code, stderr.String())
	}
	var plan shadow.RemediationPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("unmarshal JSON output: %v body=%s", err, stdout.String())
	}
	if plan.Audience != shadow.RemediationAudienceEnterprise {
		t.Errorf("audience: want enterprise, got %q", plan.Audience)
	}
	if plan.ActionKind != shadow.RemediationDeployManagedSettings {
		t.Errorf("action kind: want %q, got %q", shadow.RemediationDeployManagedSettings, plan.ActionKind)
	}
	if plan.GeneratorVersion == "" {
		t.Error("GeneratorVersion missing in JSON output")
	}
}

func TestShadowRemediateCmd_ScannerFinding(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	path := writeFixtureToTemp(t, scannerFindingFixture(t, nil))

	code := runShadowRemediateCmd([]string{"--finding-file", path, "--json"}, nil, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d stderr=%s", code, stderr.String())
	}
	var plan shadow.RemediationPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("unmarshal JSON output: %v body=%s", err, stdout.String())
	}
	if plan.FindingID != "" {
		t.Errorf("scanner finding has no persistent ID; want empty, got %q", plan.FindingID)
	}
	if plan.TenantID != "tenant-beta" {
		t.Errorf("TenantID: want tenant-beta, got %q", plan.TenantID)
	}
}

func TestShadowRemediateCmd_Stdin(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	stdin := bytes.NewReader(lifecycleFindingFixture(t, nil))

	code := runShadowRemediateCmd([]string{"--finding-file", "-", "--audience", "dev", "--json"}, stdin, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d stderr=%s", code, stderr.String())
	}
	var plan shadow.RemediationPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("unmarshal JSON output: %v body=%s", err, stdout.String())
	}
	if plan.Audience != shadow.RemediationAudienceDev {
		t.Errorf("audience: want dev, got %q", plan.Audience)
	}
}

func TestShadowRemediateCmd_DeterministicText(t *testing.T) {
	path := writeFixtureToTemp(t, lifecycleFindingFixture(t, nil))

	run := func() string {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := runShadowRemediateCmd([]string{"--finding-file", path, "--audience", "dev"}, nil, stdout, stderr)
		if code != 0 {
			t.Fatalf("exit code: want 0, got %d stderr=%s", code, stderr.String())
		}
		// Drop the GeneratedAt timestamp line so the test is wall-clock
		// independent.
		lines := strings.Split(stdout.String(), "\n")
		out := make([]string, 0, len(lines))
		for _, l := range lines {
			if strings.HasPrefix(l, "Generated at:") {
				continue
			}
			out = append(out, l)
		}
		return strings.Join(out, "\n")
	}
	first := run()
	second := run()
	if first != second {
		t.Errorf("text output is non-deterministic across repeated invocations:\nfirst=%q\nsecond=%q", first, second)
	}
}

func TestShadowRemediateCmd_InvalidJSON(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	path := writeFixtureToTemp(t, []byte("{ this is not json"))

	code := runShadowRemediateCmd([]string{"--finding-file", path}, nil, stdout, stderr)
	if code != 2 {
		t.Errorf("exit code: want 2, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "shadow remediate:") {
		t.Errorf("stderr must carry shadow remediate prefix; got %q", stderr.String())
	}
}

func TestShadowRemediateCmd_MissingFlag(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := runShadowRemediateCmd([]string{}, nil, stdout, stderr)
	if code != 2 {
		t.Errorf("exit code for missing --finding-file: want 2, got %d", code)
	}
}

func TestShadowRemediateCmd_InvalidAudience(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	path := writeFixtureToTemp(t, lifecycleFindingFixture(t, nil))

	code := runShadowRemediateCmd([]string{"--finding-file", path, "--audience", "bogus"}, nil, stdout, stderr)
	if code != 2 {
		t.Errorf("exit code: want 2, got %d", code)
	}
}

func TestShadowRemediateCmd_UnknownFlag(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	path := writeFixtureToTemp(t, lifecycleFindingFixture(t, nil))

	code := runShadowRemediateCmd([]string{"--finding-file", path, "--what"}, nil, stdout, stderr)
	if code != 2 {
		t.Errorf("exit code for unknown flag: want 2, got %d", code)
	}
}

func TestShadowRemediateCmd_NoSecretLeakage(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	path := writeFixtureToTemp(t, lifecycleFindingFixture(t, func(f *shadow.ShadowAgentFinding) {
		f.EvidenceSummary = "leaked cordum_fake_sk-ant-realsecret0123456789 in summary"
		f.SignalSet = []string{"unmanaged_claude_settings"}
		f.Metadata = map[string]string{
			"home_path": "/Users/realdev/secrets",
			"raw":       "Authorization: Bearer cordum_fake_ghp_abcdef1234567890",
		}
	}))

	code := runShadowRemediateCmd([]string{"--finding-file", path, "--json"}, nil, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d stderr=%s", code, stderr.String())
	}
	for _, needle := range []string{
		"sk-ant-realsecret",
		"ghp_abcdef",
		"/Users/realdev",
		"Bearer ghp_",
	} {
		if strings.Contains(stdout.String(), needle) {
			t.Errorf("CLI output leaked %q; body=%s", needle, stdout.String())
		}
	}
}

func TestShadowRemediateCmd_OmitCommandsFlag(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	path := writeFixtureToTemp(t, lifecycleFindingFixture(t, nil))

	code := runShadowRemediateCmd([]string{"--finding-file", path, "--audience", "dev", "--omit-commands", "--json"}, nil, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d stderr=%s", code, stderr.String())
	}
	var plan shadow.RemediationPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, step := range plan.Steps {
		if step.Command != "" {
			t.Errorf("--omit-commands should strip Command; step %q got %q", step.ID, step.Command)
		}
	}
}
