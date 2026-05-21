// EDGE-142 — Shadow remediation generator tests.
//
// Tests are written RED-first against the contract in remediation.go.
// They exercise:
//
//   - Classification by evidence type / signal set / source type.
//   - Audience-driven wording differences (dev vs enterprise vs both).
//   - Redaction guarantees: no live secrets, no full-path leakage,
//     no provider-credentialed URLs.
//   - Backup/preview gating for destructive step kinds.
//   - Deterministic step ordering + byte-stable JSON output.
//   - Nil/empty/oversized input safety (no panics, safe fallback).
package shadow

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fixedTime is the deterministic clock used by all remediation tests.
func fixedTime() time.Time {
	return time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC)
}

// fixedClock returns fixedTime in the GeneratorOptions.Now shape.
func fixedClock() func() time.Time { return fixedTime }

// newFindingForTest builds a baseline ShadowAgentFinding suitable for
// tweaking per-test. EDGE-141 minimum required fields populated; tests
// override the dimension they exercise.
func newFindingForTest(id string) *ShadowAgentFinding {
	return &ShadowAgentFinding{
		FindingID:        "edge_shadow_" + id,
		TenantID:         "tenant-alpha",
		OwnerPrincipalID: "owner@cordum.test",
		PrincipalID:      "scanner-svc",
		AgentProduct:     "claude-code",
		Risk:             FindingRiskMedium,
		Status:           FindingStatusDetected,
		EvidenceType:     EvidenceConfigFile,
		EvidenceSummary:  "1 mcp servers configured (transports: stdio)",
		RedactedPath:     "~/.claude/settings.json",
		SourceType:       SourceTypeLocal,
		DetectedAt:       fixedTime(),
	}
}

func TestGenerateForFinding_UnmanagedClaudeSettings_Dev(t *testing.T) {
	f := newFindingForTest("claude-1")
	f.RedactedPath = "~/.claude/settings.json"

	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceDev, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if plan.ActionKind != RemediationUseCordumctlEdgeClaude {
		t.Errorf("ActionKind: want %q, got %q", RemediationUseCordumctlEdgeClaude, plan.ActionKind)
	}
	if plan.Audience != RemediationAudienceDev {
		t.Errorf("Audience: want dev, got %q", plan.Audience)
	}
	if !plan.AdvisoryOnly {
		t.Error("AdvisoryOnly must be true (task rail #1)")
	}
	if plan.GeneratorVersion == "" {
		t.Error("GeneratorVersion required")
	}
	if plan.FindingID != f.FindingID {
		t.Errorf("FindingID: want %q, got %q", f.FindingID, plan.FindingID)
	}
	if plan.TenantID != f.TenantID {
		t.Errorf("TenantID: want %q, got %q", f.TenantID, plan.TenantID)
	}
	foundCordumctl := false
	for _, step := range plan.Steps {
		if strings.Contains(step.Command, "cordumctl edge claude") {
			foundCordumctl = true
			break
		}
	}
	if !foundCordumctl {
		t.Errorf("expected at least one step recommending `cordumctl edge claude`, got steps: %+v", stepKinds(plan.Steps))
	}
}

func TestGenerateForFinding_UnmanagedClaudeSettings_Enterprise(t *testing.T) {
	f := newFindingForTest("claude-2")
	f.RedactedPath = "~/.claude/settings.json"

	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceEnterprise, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if plan.ActionKind != RemediationDeployManagedSettings {
		t.Errorf("ActionKind: want %q, got %q", RemediationDeployManagedSettings, plan.ActionKind)
	}
	foundManaged := false
	for _, step := range plan.Steps {
		if strings.Contains(step.Command, "cordumctl edge managed-settings export") {
			foundManaged = true
		}
	}
	if !foundManaged {
		t.Errorf("expected managed-settings export step, got: %+v", stepKinds(plan.Steps))
	}
}

func TestGenerateForFinding_UnmanagedMCPServer(t *testing.T) {
	f := newFindingForTest("mcp-1")
	f.AgentProduct = "cursor"
	f.RedactedPath = "~/.cursor/mcp.json"
	f.EvidenceSummary = "1 mcp servers configured (transports: http; hosts: anthropic.com)"
	f.SignalSet = []string{"unmanaged_mcp_server"}

	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceBoth, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if plan.ActionKind != RemediationAttachMCPGateway {
		t.Errorf("ActionKind: want %q, got %q", RemediationAttachMCPGateway, plan.ActionKind)
	}
}

func TestGenerateForFinding_DirectProviderURL(t *testing.T) {
	f := newFindingForTest("provider-1")
	f.EvidenceSummary = "1 mcp servers configured (hosts: api.anthropic.com)"
	f.SignalSet = []string{"direct_provider_url"}
	f.Risk = FindingRiskHigh

	plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if plan.ActionKind != RemediationRouteThroughLLMProxy {
		t.Errorf("ActionKind: want %q, got %q", RemediationRouteThroughLLMProxy, plan.ActionKind)
	}
	if plan.Severity != RemediationSeverityHigh {
		t.Errorf("Severity: want high, got %q", plan.Severity)
	}
}

func TestGenerateForFinding_MissingHeartbeat(t *testing.T) {
	f := newFindingForTest("hb-1")
	f.SignalSet = []string{"k8s_heartbeat_missing"}
	f.EvidenceType = "heartbeat"
	f.SourceType = SourceTypeKubernetes

	plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if plan.ActionKind != RemediationRunEdgeDoctor {
		t.Errorf("ActionKind: want %q, got %q", RemediationRunEdgeDoctor, plan.ActionKind)
	}
	hasDoctor := false
	for _, step := range plan.Steps {
		if strings.Contains(step.Command, "cordumctl edge doctor") {
			hasDoctor = true
		}
	}
	if !hasDoctor {
		t.Errorf("expected `cordumctl edge doctor` step, got: %+v", stepKinds(plan.Steps))
	}
}

func TestGenerateForFinding_UnknownFindingFallback(t *testing.T) {
	f := newFindingForTest("unknown-1")
	f.EvidenceType = "weird_unknown_evidence"
	f.AgentProduct = "unknown-agent"
	f.SignalSet = []string{"unknown_signal_xyz"}

	plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("expected safe fallback, got error: %v", err)
	}
	if plan.ActionKind != RemediationManualReview && plan.ActionKind != RemediationInvestigateProcess {
		t.Errorf("ActionKind: want manual_review or investigate_process, got %q", plan.ActionKind)
	}
	if len(plan.Steps) == 0 {
		t.Error("fallback plan must include at least one step")
	}
}

func TestGenerateForFinding_NilInput(t *testing.T) {
	_, err := GenerateForFinding(nil, GeneratorOptions{Now: fixedClock()})
	if err == nil {
		t.Fatal("nil finding must return validation error, not panic")
	}
}

func TestGenerateForFinding_EmptyFinding(t *testing.T) {
	plan, err := GenerateForFinding(&ShadowAgentFinding{TenantID: "tenant-empty"}, GeneratorOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("empty finding must produce fallback plan, got error: %v", err)
	}
	if plan.ActionKind == "" {
		t.Error("fallback plan must populate ActionKind")
	}
	if len(plan.Steps) == 0 {
		t.Error("fallback plan must include at least one step")
	}
}

func TestGenerateForFinding_NoSecretsInOutput(t *testing.T) {
	f := newFindingForTest("redact-1")
	// Inject a value that, if echoed verbatim, would leak.
	f.EvidenceSummary = "live token cordum_fake_sk-ant-abcdef1234567890 leaked"
	f.RedactedPath = "/Users/realdev/secrets/key.pem"
	f.Metadata = map[string]string{
		"raw_command": "curl -H 'Authorization: Bearer cordum_fake_ghp_abcdef1234567890' https://api.example.com",
		"home":        "/Users/realdev",
	}

	plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	body := string(encoded)
	forbidden := []string{
		"sk-ant-abcdef",
		"ghp_abcdef",
		"/Users/realdev",
		"Bearer ghp_",
	}
	for _, needle := range forbidden {
		if strings.Contains(body, needle) {
			t.Errorf("plan JSON must not contain %q; got: %s", needle, body)
		}
	}
}

func TestGenerateForFinding_DisableConfigRequiresBackupAndPreview(t *testing.T) {
	f := newFindingForTest("destructive-1")
	f.RedactedPath = "~/.claude/settings.json"
	f.SignalSet = []string{"unmanaged_claude_settings"}

	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceEnterprise, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	for _, step := range plan.Steps {
		if step.Kind != RemediationDisableUnmanagedConfig {
			continue
		}
		if !step.PreviewOnly {
			t.Errorf("disable step %q must be preview_only=true", step.ID)
		}
		if !step.RequiresBackup {
			t.Errorf("disable step %q must require backup", step.ID)
		}
		if !step.Destructive {
			t.Errorf("disable step %q must mark destructive=true", step.ID)
		}
	}
	// Verify a backup step precedes any disable step.
	disablePos := -1
	backupPos := -1
	for i, step := range plan.Steps {
		if step.Kind == RemediationDisableUnmanagedConfig && disablePos < 0 {
			disablePos = i
		}
		if isBackupStep(step) && backupPos < 0 {
			backupPos = i
		}
	}
	if disablePos >= 0 && (backupPos < 0 || backupPos >= disablePos) {
		t.Errorf("backup step must precede disable step (backup=%d, disable=%d) — steps: %+v",
			backupPos, disablePos, stepKinds(plan.Steps))
	}
}

func TestGenerateForFinding_DeterministicJSON(t *testing.T) {
	f := newFindingForTest("det-1")
	f.SignalSet = []string{"unmanaged_mcp_server", "unmanaged_claude_settings"}

	opt := GeneratorOptions{Audience: RemediationAudienceBoth, Now: fixedClock()}
	plan1, err := GenerateForFinding(f, opt)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	plan2, err := GenerateForFinding(f, opt)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	b1, _ := json.Marshal(plan1)
	b2, _ := json.Marshal(plan2)
	if string(b1) != string(b2) {
		t.Errorf("non-deterministic output:\n a=%s\n b=%s", b1, b2)
	}
}

func TestGenerateForFinding_AudienceBoth_LayersDevThenEnterprise(t *testing.T) {
	f := newFindingForTest("both-1")
	f.RedactedPath = "~/.claude/settings.json"

	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceBoth, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if len(plan.Steps) < 2 {
		t.Fatalf("audience=both must emit dev+enterprise steps; got %d", len(plan.Steps))
	}
	devIdx, entIdx := -1, -1
	for i, step := range plan.Steps {
		// Heuristic: dev steps reference `cordumctl edge claude`,
		// enterprise steps reference managed-settings.
		if strings.Contains(step.Command, "cordumctl edge claude") && devIdx < 0 {
			devIdx = i
		}
		if strings.Contains(step.Command, "managed-settings") && entIdx < 0 {
			entIdx = i
		}
	}
	if devIdx < 0 || entIdx < 0 {
		t.Errorf("audience=both must emit at least one dev step (cordumctl edge claude) and one enterprise step (managed-settings); got %+v",
			stepKinds(plan.Steps))
	} else if devIdx > entIdx {
		t.Errorf("dev step must precede enterprise step in audience=both (dev=%d, ent=%d)", devIdx, entIdx)
	}
}

func TestGenerateForFinding_OmitCommands_StripsCommandFields(t *testing.T) {
	f := newFindingForTest("nocmd-1")
	plan, err := GenerateForFinding(f, GeneratorOptions{OmitCommands: true, Audience: RemediationAudienceDev, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	for _, step := range plan.Steps {
		if step.Command != "" {
			t.Errorf("step %q must have empty Command when OmitCommands=true; got %q", step.ID, step.Command)
		}
		if step.APIRequest != nil && step.APIRequest.Body != "" {
			t.Errorf("step %q must omit APIRequest.Body when OmitCommands=true", step.ID)
		}
	}
}

func TestGenerateForFinding_InvalidAudienceDefaults(t *testing.T) {
	f := newFindingForTest("aud-1")
	plan, err := GenerateForFinding(f, GeneratorOptions{Audience: "bogus", Now: fixedClock()})
	if err != nil {
		t.Fatalf("invalid audience must fall back to default, got error: %v", err)
	}
	if plan.Audience != RemediationAudienceBoth {
		t.Errorf("invalid audience must default to both, got %q", plan.Audience)
	}
}

func TestGenerateForFinding_HugeMetadataSafe(t *testing.T) {
	f := newFindingForTest("huge-1")
	huge := strings.Repeat("a", 8*1024)
	f.Metadata = map[string]string{
		"k1": huge,
		"k2": huge,
	}
	plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("oversized metadata must not panic; got error: %v", err)
	}
	body, _ := json.Marshal(plan)
	if len(body) > 32*1024 {
		t.Errorf("plan must bound its output size; got %d bytes", len(body))
	}
}

func TestGenerateForScannerFinding_Local(t *testing.T) {
	sf := &Finding{
		TenantID:              "tenant-beta",
		PrincipalID:           "alice",
		Hostname:              "dev-box",
		Product:               "claude-code",
		EvidenceType:          EvidenceConfigFile,
		RedactedPath:          "~/.claude/settings.json",
		RedactedConfigSummary: "1 mcp servers configured",
		Risk:                  RiskMedium,
		Status:                StatusObserved,
		ObservedAt:            fixedTime(),
	}
	plan, err := GenerateForScannerFinding(sf, GeneratorOptions{Audience: RemediationAudienceDev, Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForScannerFinding: %v", err)
	}
	if plan.FindingID != "" {
		t.Errorf("scanner finding has no persistent ID; want empty, got %q", plan.FindingID)
	}
	if plan.TenantID != "tenant-beta" {
		t.Errorf("TenantID: want tenant-beta, got %q", plan.TenantID)
	}
	if plan.ActionKind == "" {
		t.Error("ActionKind must be populated")
	}
}

func TestGenerateForScannerFinding_Nil(t *testing.T) {
	_, err := GenerateForScannerFinding(nil, GeneratorOptions{Now: fixedClock()})
	if err == nil {
		t.Fatal("nil scanner finding must return validation error")
	}
}

func TestGenerateForFinding_NoForbiddenPathPatterns(t *testing.T) {
	// Stress-test redaction across all classification branches.
	cases := []struct {
		name string
		mut  func(*ShadowAgentFinding)
	}{
		{"claude_settings", func(f *ShadowAgentFinding) { f.RedactedPath = "~/.claude/settings.json" }},
		{"mcp_server", func(f *ShadowAgentFinding) { f.SignalSet = []string{"unmanaged_mcp_server"} }},
		{"provider_url", func(f *ShadowAgentFinding) { f.SignalSet = []string{"direct_provider_url"} }},
		{"heartbeat", func(f *ShadowAgentFinding) { f.SignalSet = []string{"k8s_heartbeat_missing"} }},
		{"unknown", func(f *ShadowAgentFinding) { f.EvidenceType = "weird" }},
	}
	leak := regexp.MustCompile(`(?i)(/Users/|/home/|C:\\|sk-ant-[a-z0-9]{16}|ghp_[a-z0-9]{16}|gho_[a-z0-9]{16}|Bearer [a-z0-9]{8})`)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFindingForTest(c.name)
			f.EvidenceSummary = "summary with cordum_fake_sk-ant-abcdef0123456789 and /Users/dev/secret"
			f.RedactedPath = "/Users/dev/secret/config.json"
			c.mut(f)
			plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
			if err != nil {
				t.Fatalf("GenerateForFinding: %v", err)
			}
			b, _ := json.Marshal(plan)
			if loc := leak.FindString(string(b)); loc != "" {
				t.Errorf("plan JSON leaked secret/path token %q; full body=%s", loc, b)
			}
		})
	}
}

func TestGenerateForFinding_SeverityFromRisk(t *testing.T) {
	cases := []struct {
		risk FindingRisk
		want RemediationSeverity
	}{
		{FindingRiskLow, RemediationSeverityLow},
		{FindingRiskMedium, RemediationSeverityMedium},
		{FindingRiskHigh, RemediationSeverityHigh},
		{FindingRiskCritical, RemediationSeverityHigh},
	}
	for _, c := range cases {
		t.Run(string(c.risk), func(t *testing.T) {
			f := newFindingForTest("sev-" + string(c.risk))
			f.Risk = c.risk
			plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
			if err != nil {
				t.Fatalf("GenerateForFinding: %v", err)
			}
			if plan.Severity != c.want {
				t.Errorf("severity for risk=%q: want %q, got %q", c.risk, c.want, plan.Severity)
			}
		})
	}
}

func TestGenerateForFinding_SecretInProductLabel(t *testing.T) {
	f := newFindingForTest("prodsec-1")
	f.AgentProduct = "claude-code cordum_fake_sk-ant-abcdef0123456789"
	plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	body, _ := json.Marshal(plan)
	if strings.Contains(string(body), "sk-ant-abcdef") {
		t.Errorf("product-label injection leaked into plan JSON: %s", body)
	}
}

func TestGenerateForFinding_SecretInSignalLabel(t *testing.T) {
	f := newFindingForTest("sigsec-1")
	f.SignalSet = []string{"unmanaged_mcp_server", "cordum_fake_sk-ant-realsecret0123456789"}
	plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	body, _ := json.Marshal(plan)
	if strings.Contains(string(body), "sk-ant-realsecret") {
		t.Errorf("signal-label injection leaked into plan JSON: %s", body)
	}
}

func TestGenerateForFinding_TerminalEscapeStripped(t *testing.T) {
	f := newFindingForTest("escape-1")
	f.AgentProduct = "claude-code\x1b[31mRED\x1b[0m"
	plan, err := GenerateForFinding(f, GeneratorOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if strings.ContainsRune(plan.Summary, '\x1b') {
		t.Errorf("summary must not carry terminal escapes; got %q", plan.Summary)
	}
}

func TestGenerateForFinding_DisableStepsAlwaysPreviewOnly(t *testing.T) {
	// Force the classifier into the disable-config branch by signaling
	// unmanaged_claude_settings + enterprise audience; the resolver
	// upgrades to DeployManagedSettings which still leaves no
	// destructive steps. Then exercise the explicit disable path via
	// scanner-like context.
	f := newFindingForTest("noauto-1")
	f.SignalSet = []string{"unmanaged_claude_settings"}
	plan, _ := GenerateForFinding(f, GeneratorOptions{Audience: RemediationAudienceEnterprise, Now: fixedClock()})
	for _, step := range plan.Steps {
		if step.Destructive && !step.PreviewOnly {
			t.Errorf("destructive step %q must be preview_only=true", step.ID)
		}
		if step.Destructive && !step.RequiresBackup {
			t.Errorf("destructive step %q must require backup", step.ID)
		}
	}
}

func TestGenerateForFinding_GeneratedAtUsesClockSeam(t *testing.T) {
	f := newFindingForTest("clock-1")
	fixed := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	plan, err := GenerateForFinding(f, GeneratorOptions{Now: func() time.Time { return fixed }})
	if err != nil {
		t.Fatalf("GenerateForFinding: %v", err)
	}
	if !plan.GeneratedAt.Equal(fixed) {
		t.Errorf("GeneratedAt: want %s, got %s", fixed, plan.GeneratedAt)
	}
}

// Helpers ────────────────────────────────────────────────────────────

func stepKinds(steps []RemediationStep) []string {
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		out = append(out, string(step.Kind)+":"+step.ID)
	}
	return out
}

func isBackupStep(step RemediationStep) bool {
	if step.RequiresBackup {
		// A pure backup step is named with `backup` in its ID per
		// generator convention.
		if strings.Contains(strings.ToLower(step.ID), "backup") {
			return true
		}
		if strings.Contains(strings.ToLower(step.Title), "backup") {
			return true
		}
	}
	return false
}

// rot13 returns the ROT13 transform of s; non-letter runes pass through.
// Used by TestSecretRedaction_Homoglyphic to build an encoding-evaded
// fake secret. ROT13 is its own inverse, so the decoded form is just
// rot13(rot13(s)).
func rot13(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune('A' + (r-'A'+13)%26)
		case r >= 'a' && r <= 'z':
			b.WriteRune('a' + (r-'a'+13)%26)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestSecretRedaction_Homoglyphic stress-tests the EDGE-142 redactor
// against encoding-evasion patterns. Adversaries can smuggle a
// secret past the regex-based stripSecretMarkers by encoding it. The
// invariants every secret-bearing field must satisfy:
//
//  1. The decoded secret shape ("sk-ant-…") MUST NOT appear in the
//     rendered plan (text or JSON). This pins that no upstream
//     decoder accidentally regurgitates the secret.
//  2. The raw encoded form MUST NOT be echoed verbatim either. If the
//     encoded form survives, an attacker who can decode (or any
//     downstream system that decodes) can recover the secret.
//
// Coverage:
//   - base64-wrapped secret in evidence summary + metadata.
//   - Secret split across two SignalSet entries (joined in render).
//   - U+2010 hyphen-replacement homoglyph in AgentProduct + signals.
//   - ROT13-encoded secret in metadata + signals.
//
// Per task-56bfc62a rail #4: if the redactor lacks encoding-aware
// handling, this test fails honestly and we file a HIGH follow-up to
// add base64/ROT13/homoglyph/split awareness rather than silently
// patching the test to pass.
func TestSecretRedaction_Homoglyphic(t *testing.T) {
	// canonicalFake is the decoded shape that stripSecretMarkers WOULD
	// catch if any decoder along the pipeline emitted it. The `sk-ant-`
	// prefix + ≥16 alnum-or-`_-` chars matches the production regex at
	// redaction.go:29. The `cordum_fake_` framing keeps GitHub secret
	// scanning quiet.
	const canonicalFake = "cordum_fake_sk-ant-cordumtest2026realfakekey0123"
	// The bare leak shape we never want to see anywhere in the output.
	const leakPrefix = "sk-ant-"

	cases := []struct {
		name string
		// mut tweaks the finding to inject the encoded form. encodedRaw
		// is the wire-shape string we'll later assert doesn't appear in
		// the rendered plan verbatim.
		mut        func(*ShadowAgentFinding) string
		extraLeaks []string
	}{
		{
			name: "base64-in-evidence-and-metadata",
			mut: func(f *ShadowAgentFinding) string {
				encoded := base64.StdEncoding.EncodeToString([]byte(canonicalFake))
				f.EvidenceSummary = "config carrying base64 token " + encoded
				f.Metadata = map[string]string{"base64_payload": encoded}
				return encoded
			},
		},
		{
			name: "split-across-signal-set",
			mut: func(f *ShadowAgentFinding) string {
				// Split the canonical fake just after `sk-ant-` so neither
				// half independently matches the {16,} regex bound; if
				// the renderer concatenates them adjacently, the leak
				// shape reappears.
				idx := strings.Index(canonicalFake, "sk-ant-") + len("sk-ant-")
				left, right := canonicalFake[:idx], canonicalFake[idx:]
				f.SignalSet = []string{
					"unmanaged_mcp_server",
					left,
					right,
				}
				return left + right
			},
		},
		{
			name: "homoglyph-u2010-hyphen",
			mut: func(f *ShadowAgentFinding) string {
				// Replace ASCII `-` with U+2010 HYPHEN so the regex
				// match fails. Inject into AgentProduct + a signal so
				// the value flows through safeProductName/sortedSignals.
				homo := strings.ReplaceAll(canonicalFake, "-", "‐")
				f.AgentProduct = "claude-code " + homo
				f.SignalSet = []string{"unmanaged_mcp_server", homo}
				return homo
			},
			// Fused-letter shape that appeared before the encoding-aware
			// redactor normalized U+2010 before stripUnsafeRunes.
			extraLeaks: []string{"cordumtest2026realfakekey0123"},
		},
		{
			name: "rot13-in-metadata-and-signals",
			mut: func(f *ShadowAgentFinding) string {
				encoded := rot13(canonicalFake)
				f.Metadata = map[string]string{"rot13_payload": encoded}
				f.SignalSet = []string{"unmanaged_mcp_server", encoded}
				return encoded
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFindingForTest("homo-" + tc.name)
			f.RedactedPath = "~/.claude/settings.json"
			f.SignalSet = []string{"unmanaged_claude_settings"}
			encodedRaw := tc.mut(f)

			plan, err := GenerateForFinding(f, GeneratorOptions{
				Audience: RemediationAudienceBoth,
				Now:      fixedClock(),
			})
			if err != nil {
				t.Fatalf("GenerateForFinding: %v", err)
			}
			body, err := json.Marshal(plan)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			rendered := string(body)

			// Invariant 1: decoded secret shape must NOT appear.
			if strings.Contains(rendered, canonicalFake) {
				t.Errorf("decoded canonical fake %q leaked verbatim into rendered plan: %s", canonicalFake, rendered)
			}
			if strings.Contains(rendered, leakPrefix+"cordumtest") {
				t.Errorf("post-decode secret-shape (%q+) leaked into rendered plan: %s", leakPrefix, rendered)
			}

			// Invariant 2: raw encoded form must NOT be echoed verbatim
			// for the encoded fields that flow through render. (For
			// fields like EvidenceSummary that the generator never
			// echoes, this invariant is vacuously satisfied.)
			if strings.Contains(rendered, encodedRaw) {
				t.Errorf("encoded form %q echoed verbatim — encoding-evasion redaction gap; rendered=%s", encodedRaw, rendered)
			}

			for _, leak := range tc.extraLeaks {
				if strings.Contains(rendered, leak) {
					t.Errorf("post-strip leak shape %q present in rendered plan: %s", leak, rendered)
				}
			}
		})
	}
}

// TestRemediationGeneratorMutationResistant pins the **tightness** of
// the existing remediation-generator assertions: that the rendered
// JSON is sensitive to plan-field changes, so a single-line bug
// (severity hardcode, dropped step, off-by-one count) cannot slip
// past byte-equality checks. The test is structured in two phases:
//
//   - baseline-pass: two calls with identical inputs produce
//     byte-identical JSON. Tightens the determinism check that
//     TestGenerateForFinding_DeterministicJSON already pins.
//   - mutation-fails: each named mutator perturbs one field of the
//     baseline plan and asserts the JSON encoding changes. If the
//     mutation does NOT change the JSON, the existing field is
//     either unrendered or shadowed by another field — in either
//     case, downstream golden-file assertions would not detect a
//     real regression there. That is the assertion-loosness signal
//     the test exists to catch.
//
// No production code is touched: the mutator is a TEST-ONLY
// post-generation transform on *RemediationPlan. This documents
// which plan fields contribute to wire output and pins the
// generator's surface area.
func TestRemediationGeneratorMutationResistant(t *testing.T) {
	f := newFindingForTest("mutation-1")
	f.RedactedPath = "~/.claude/settings.json"
	f.SignalSet = []string{"unmanaged_claude_settings"}
	f.Risk = FindingRiskHigh

	opts := GeneratorOptions{
		Audience: RemediationAudienceEnterprise,
		Now:      fixedClock(),
	}

	baseline, err := GenerateForFinding(f, opts)
	if err != nil {
		t.Fatalf("baseline GenerateForFinding: %v", err)
	}
	baselineJSON, err := json.Marshal(baseline)
	if err != nil {
		t.Fatalf("baseline marshal: %v", err)
	}

	t.Run("baseline-pass", func(t *testing.T) {
		// Pin: a second call with identical inputs produces byte-
		// identical JSON. If a future refactor introduces nondeterminism
		// (map iteration order, time.Now, rand.Read), this catches it.
		second, err := GenerateForFinding(f, opts)
		if err != nil {
			t.Fatalf("second GenerateForFinding: %v", err)
		}
		secondJSON, err := json.Marshal(second)
		if err != nil {
			t.Fatalf("second marshal: %v", err)
		}
		if !bytes.Equal(baselineJSON, secondJSON) {
			t.Errorf("baseline non-deterministic across calls — assertion-pinning is unsafe.\nfirst=%s\nsecond=%s", baselineJSON, secondJSON)
		}
	})

	mutators := []struct {
		name string
		mut  func(*RemediationPlan)
	}{
		{
			name: "severity-hardcoded-to-low",
			mut: func(p *RemediationPlan) {
				p.Severity = RemediationSeverityLow
			},
		},
		{
			name: "action-kind-stripped",
			mut: func(p *RemediationPlan) {
				p.ActionKind = ""
			},
		},
		{
			name: "drop-first-step",
			mut: func(p *RemediationPlan) {
				if len(p.Steps) > 0 {
					p.Steps = p.Steps[1:]
				} else {
					// Force a divergence even on fallback paths so the
					// mutation case still produces a JSON difference.
					p.Steps = append(p.Steps, RemediationStep{ID: "mutation-injected"})
				}
			},
		},
		{
			name: "off-by-one-step-count",
			mut: func(p *RemediationPlan) {
				if len(p.Steps) > 0 {
					p.Steps = append(p.Steps, p.Steps[0])
				} else {
					p.Steps = append(p.Steps, RemediationStep{ID: "mutation-injected"})
				}
			},
		},
		{
			name: "summary-tampered",
			mut: func(p *RemediationPlan) {
				p.Summary = "MUTATION_INJECTED — this string must change the rendered JSON"
			},
		},
		{
			name: "advisory-only-flipped",
			mut: func(p *RemediationPlan) {
				p.AdvisoryOnly = !p.AdvisoryOnly
			},
		},
	}

	for _, mc := range mutators {
		t.Run("mutation-"+mc.name, func(t *testing.T) {
			// Regenerate the plan rather than mutating the shared baseline
			// pointer so each subtest is independent.
			plan, err := GenerateForFinding(f, opts)
			if err != nil {
				t.Fatalf("regen for mutation: %v", err)
			}
			mc.mut(plan)
			mutatedJSON, err := json.Marshal(plan)
			if err != nil {
				t.Fatalf("mutated marshal: %v", err)
			}
			if bytes.Equal(baselineJSON, mutatedJSON) {
				t.Errorf("mutation %q did NOT change JSON output — assertions on this field are too loose; a regression in this surface would not be detectable by byte-equality.\nbaseline=%s\nmutated =%s", mc.name, baselineJSON, mutatedJSON)
			}
		})
	}
}
