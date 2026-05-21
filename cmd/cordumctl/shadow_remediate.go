package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cordum/cordum/core/edge/shadow"
)

// runShadowRemediateCmd is the public entry point for
// `cordumctl shadow remediate`. Reads one finding JSON from
// --finding-file (or stdin via "-"), generates an advisory remediation
// plan, and writes either human-readable text or machine-readable JSON
// to stdout. Exit codes: 0 success, 2 parse/validation/unsupported flags.
//
// The command is offline by default: it never talks to the Cordum
// Gateway and never requires API keys. Operators preview guidance
// against scanner JSONL or stored-finding JSON without dispatching
// remote calls.
func runShadowRemediateCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("shadow remediate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	findingFile := fs.String("finding-file", "", "path to finding JSON (use - for stdin)")
	audience := fs.String("audience", string(shadow.RemediationAudienceBoth), "audience: dev, enterprise, or both")
	emitJSON := fs.Bool("json", false, "emit machine-readable JSON instead of human text")
	omitCommands := fs.Bool("omit-commands", false, "suppress command and api_request.body fields")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *findingFile == "" {
		_, _ = fmt.Fprintln(stderr, "shadow remediate: --finding-file required (use - for stdin)")
		return 2
	}

	aud, ok := parseCLIAudience(*audience)
	if !ok {
		_, _ = fmt.Fprintln(stderr, "shadow remediate: --audience must be one of dev|enterprise|both")
		return 2
	}

	raw, err := readFindingInput(*findingFile, stdin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "shadow remediate: read finding: %s\n", err)
		return 2
	}

	plan, err := generatePlanFromRaw(raw, shadow.GeneratorOptions{
		Audience:     aud,
		OmitCommands: *omitCommands,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "shadow remediate: %s\n", err)
		return 2
	}

	if *emitJSON {
		return writeJSONPlan(stdout, stderr, plan)
	}
	return writeTextPlan(stdout, stderr, plan)
}

// parseCLIAudience normalises the --audience flag.
func parseCLIAudience(v string) (shadow.RemediationAudience, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "dev":
		return shadow.RemediationAudienceDev, true
	case "enterprise":
		return shadow.RemediationAudienceEnterprise, true
	case "both", "":
		return shadow.RemediationAudienceBoth, true
	default:
		return "", false
	}
}

// readFindingInput reads the finding payload from disk or stdin.
// The "-" sentinel mirrors the cordumctl scan output convention.
func readFindingInput(path string, stdin io.Reader) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(io.LimitReader(stdin, 64*1024))
	}
	return os.ReadFile(path)
}

// generatePlanFromRaw inspects the JSON payload to choose between the
// EDGE-141 lifecycle shape and the EDGE-140 scanner shape, then calls
// the appropriate pure generator.
func generatePlanFromRaw(raw []byte, opts shadow.GeneratorOptions) (*shadow.RemediationPlan, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("finding input is empty")
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("finding input is not valid JSON")
	}

	// Peek at known discriminator fields to choose the right shape.
	// finding_id / agent_product → EDGE-141; product → EDGE-140 scanner.
	var probe struct {
		FindingID    string `json:"finding_id"`
		AgentProduct string `json:"agent_product"`
		Product      string `json:"product"`
	}
	_ = json.Unmarshal(raw, &probe)

	if probe.FindingID != "" || probe.AgentProduct != "" {
		var f shadow.ShadowAgentFinding
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("decode shadow finding: %w", err)
		}
		return shadow.GenerateForFinding(&f, opts)
	}

	if probe.Product != "" {
		var sf shadow.Finding
		if err := json.Unmarshal(raw, &sf); err != nil {
			return nil, fmt.Errorf("decode scanner finding: %w", err)
		}
		return shadow.GenerateForScannerFinding(&sf, opts)
	}

	// No discriminator — best effort: try lifecycle first, then scanner.
	var lifecycle shadow.ShadowAgentFinding
	if err := json.Unmarshal(raw, &lifecycle); err == nil {
		return shadow.GenerateForFinding(&lifecycle, opts)
	}
	var scanner shadow.Finding
	if err := json.Unmarshal(raw, &scanner); err == nil {
		return shadow.GenerateForScannerFinding(&scanner, opts)
	}
	return nil, fmt.Errorf("could not classify finding payload as lifecycle or scanner shape")
}

// writeJSONPlan emits the canonical JSON representation. Uses an
// encoder with html-escape disabled and 2-space indentation so the
// output is human-skimmable but still byte-stable for tests.
func writeJSONPlan(stdout, stderr io.Writer, plan *shadow.RemediationPlan) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(plan); err != nil {
		_, _ = fmt.Fprintf(stderr, "shadow remediate: encode plan: %s\n", err)
		return 2
	}
	return 0
}

// writeTextPlan renders the plan as a deterministic human-readable
// block. Includes the action kind, severity, summary, risk
// explanation, recommended action, safety notes, and ordered steps.
func writeTextPlan(stdout, stderr io.Writer, plan *shadow.RemediationPlan) int {
	var b strings.Builder
	fmt.Fprintf(&b, "Cordum Edge shadow remediation plan\n")
	fmt.Fprintf(&b, "===================================\n")
	if plan.FindingID != "" {
		fmt.Fprintf(&b, "Finding:          %s\n", plan.FindingID)
	}
	if plan.TenantID != "" {
		fmt.Fprintf(&b, "Tenant:           %s\n", plan.TenantID)
	}
	fmt.Fprintf(&b, "Action kind:      %s\n", plan.ActionKind)
	fmt.Fprintf(&b, "Audience:         %s\n", plan.Audience)
	fmt.Fprintf(&b, "Severity:         %s\n", plan.Severity)
	fmt.Fprintf(&b, "Advisory only:    %t\n", plan.AdvisoryOnly)
	fmt.Fprintf(&b, "Generator:        %s\n", plan.GeneratorVersion)
	fmt.Fprintf(&b, "\nSummary\n-------\n%s\n", plan.Summary)
	fmt.Fprintf(&b, "\nRisk\n----\n%s\n", plan.RiskExplanation)
	fmt.Fprintf(&b, "\nRecommended action\n------------------\n%s\n", plan.RecommendedAction)
	if len(plan.SafetyNotes) > 0 {
		fmt.Fprintln(&b, "\nSafety notes")
		fmt.Fprintln(&b, "------------")
		for _, n := range plan.SafetyNotes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}
	fmt.Fprintln(&b, "\nSteps")
	fmt.Fprintln(&b, "-----")
	if len(plan.Steps) == 0 {
		fmt.Fprintln(&b, "  (no steps generated; consult the runbook)")
	}
	for i, step := range plan.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, step.Title)
		fmt.Fprintf(&b, "   id:           %s\n", step.ID)
		fmt.Fprintf(&b, "   kind:         %s\n", step.Kind)
		if step.PreviewOnly || step.RequiresBackup || step.Destructive {
			fmt.Fprintf(&b, "   safety:       preview_only=%t backup=%t destructive=%t\n",
				step.PreviewOnly, step.RequiresBackup, step.Destructive)
		}
		if step.Command != "" {
			fmt.Fprintf(&b, "   command:      %s\n", step.Command)
		}
		if step.APIRequest != nil {
			fmt.Fprintf(&b, "   api request:  %s %s\n", step.APIRequest.Method, step.APIRequest.Path)
		}
		if step.DocsURL != "" {
			fmt.Fprintf(&b, "   docs:         %s\n", step.DocsURL)
		}
		for _, c := range step.Conditions {
			fmt.Fprintf(&b, "   - %s\n", c)
		}
	}
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		_, _ = fmt.Fprintf(stderr, "shadow remediate: write text: %s\n", err)
		return 2
	}
	return 0
}
