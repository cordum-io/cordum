package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cordum/cordum/core/licensing"
)

const licenseFileEnvName = "CORDUM_LICENSE_FILE"

func runLicenseCmd(args []string) {
	if len(args) < 1 {
		licenseUsage()
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "install":
		err = runLicenseInstallE(args[1:])
	case "info":
		err = runLicenseInfoE(args[1:])
	case "reload":
		err = runLicenseReloadE(args[1:])
	default:
		licenseUsage()
		os.Exit(1)
	}
	if err != nil {
		fail(err.Error())
	}
}

func licenseUsage() {
	fmt.Fprintln(os.Stderr, `Usage: cordumctl license <command>

Commands:
  install <path>                                  Copy a signed license into the standard config location
  info [--json]                                   Show plan, entitlements, rights, and expiry metadata
  reload                                          Hot-reload the license on a running gateway (no restart)`)
}

func runLicenseInstallE(args []string) error {
	fs := newFlagSet("license install")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: cordumctl license install <path>")
	}

	source := filepath.Clean(strings.TrimSpace(fs.Arg(0)))
	if source == "" {
		return fmt.Errorf("license file path required")
	}
	if _, err := licensing.LoadFile(source); err != nil {
		return err
	}

	destination, err := licensing.PreferredLicenseFilePath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read license file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create license directory: %w", err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return fmt.Errorf("write license file: %w", err)
	}

	fmt.Printf("Installed license to %s\n", destination)
	if envPath := strings.TrimSpace(os.Getenv(licenseFileEnvName)); envPath != "" {
		fmt.Printf("%s is set, so Cordum will read %s\n", licenseFileEnvName, filepath.Clean(envPath))
	}
	return nil
}

func runLicenseInfoE(args []string) error {
	fs := newFlagSet("license info")
	jsonOutput := fs.Bool("json", false, "emit raw parsed license JSON")
	fs.ParseArgs(args)

	license, err := licensing.LoadFromEnv()
	if err != nil {
		return err
	}
	if license == nil {
		fmt.Println("No signed license found. Cordum is enforcing Community defaults.")
		if path, pathErr := licensing.PreferredLicenseFilePath(); pathErr == nil {
			fmt.Printf("Install one with: cordumctl license install <path> (default destination: %s)\n", path)
		}
		return nil
	}

	if *jsonOutput {
		printJSON(license)
		return nil
	}

	printLicenseInfo(os.Stdout, license)
	return nil
}

func runLicenseReloadE(args []string) error {
	fs := newFlagSet("license reload")
	fs.ParseArgs(args)

	client := restClientFromFlags(fs)
	ctx := context.Background()

	var result map[string]any
	if err := client.doJSON(ctx, http.MethodPost, "/api/v1/license/reload", nil, &result); err != nil {
		return err
	}

	plan, _ := result["plan"].(string)
	status, _ := result["status"].(string)
	fmt.Printf("License reloaded: plan=%s status=%s\n", plan, status)

	if licenseInfo, ok := result["license"].(map[string]any); ok {
		if expiresAt, _ := licenseInfo["expires_at"].(string); expiresAt != "" {
			fmt.Printf("Expires: %s\n", expiresAt)
		}
		if licStatus, _ := licenseInfo["status"].(string); licStatus != "" {
			fmt.Printf("License status: %s\n", licStatus)
		}
	}
	return nil
}

func printLicenseInfo(w *os.File, license *licensing.License) {
	if license == nil {
		return
	}

	verification, graceUntil := verifyLicenseState(license)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "FIELD\tVALUE")
	_, _ = fmt.Fprintln(tw, "Plan\t"+displayPlanName(license.Payload.Plan))
	_, _ = fmt.Fprintln(tw, "Org ID\t"+valueOrDash(license.Payload.OrgID))
	_, _ = fmt.Fprintln(tw, "License ID\t"+valueOrDash(license.Payload.LicenseID))
	_, _ = fmt.Fprintln(tw, "Deployment\t"+valueOrDash(license.Payload.DeploymentType))
	_, _ = fmt.Fprintln(tw, "Issued\t"+valueOrDash(license.Payload.IssuedAt))
	_, _ = fmt.Fprintln(tw, "Valid from\t"+valueOrDash(license.Payload.NotBefore))
	_, _ = fmt.Fprintln(tw, "Expires\t"+valueOrDash(license.Payload.ExpiresAt))
	_, _ = fmt.Fprintln(tw, "Verification\t"+verification)
	if graceUntil != "" {
		_, _ = fmt.Fprintln(tw, "Grace until\t"+graceUntil)
	}
	_ = tw.Flush()

	_, _ = fmt.Fprintln(w, "\nEntitlements")
	entitlements := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range licenseEntitlementRows(license.Payload.Entitlements) {
		_, _ = fmt.Fprintf(entitlements, "%s\t%s\n", row.label, row.value)
	}
	_ = entitlements.Flush()

	_, _ = fmt.Fprintln(w, "\nCapabilities")
	capabilities := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range licenseCapabilityRows(license.Payload.Entitlements) {
		_, _ = fmt.Fprintf(capabilities, "%s\t%s\n", row.label, row.value)
	}
	_ = capabilities.Flush()

	_, _ = fmt.Fprintln(w, "\nCommercial rights")
	rights := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range licenseRightsRows(license.Payload.Rights) {
		_, _ = fmt.Fprintf(rights, "%s\t%s\n", row.label, row.value)
	}
	_ = rights.Flush()
}

func verifyLicenseState(license *licensing.License) (string, string) {
	pub, err := licensing.PublicKeyFromEnv()
	if err != nil {
		if errors.Is(err, licensing.ErrLicensePublicKeyMissing) {
			return "signature not verified (no public key available)", ""
		}
		return "signature not verified: " + err.Error(), ""
	}

	err = license.Verify(pub, time.Now().UTC())
	graceUntil := ""
	if license.Grace != nil {
		graceUntil = license.Grace.GraceUntil.UTC().Format(time.RFC3339)
	}

	switch {
	case err == nil:
		return string(license.ExpiryState), graceUntil
	case errors.Is(err, licensing.ErrLicenseExpired):
		return string(license.ExpiryState) + " (" + err.Error() + ")", graceUntil
	case errors.Is(err, licensing.ErrLicenseNotActive):
		return "not active yet", graceUntil
	default:
		return err.Error(), graceUntil
	}
}

type licenseRow struct {
	label string
	value string
}

func licenseEntitlementRows(entitlements *licensing.Entitlements) []licenseRow {
	if entitlements == nil {
		return []licenseRow{{label: "Mode", value: "Community defaults"}}
	}

	rows := []licenseRow{
		{label: "Approval mode", value: valueOrDash(strings.TrimSpace(entitlements.ApprovalMode))},
		{label: "Telemetry mode", value: valueOrDash(strings.TrimSpace(entitlements.TelemetryMode))},
		{label: "Max workers", value: formatLimitValue(entitlements.MaxWorkers, false)},
		{label: "Concurrent jobs", value: formatLimitValue(entitlements.MaxConcurrentJobs, false)},
		{label: "Active workflows", value: formatLimitValue(entitlements.MaxActiveWorkflows, false)},
		{label: "Workflow steps / run", value: formatLimitValue(entitlements.MaxWorkflowSteps, false)},
		{label: "Schemas", value: formatLimitValue(entitlements.MaxSchemaCount, false)},
		{label: "Policy bundles", value: formatLimitValue(entitlements.MaxPolicyBundles, false)},
		{label: "Requests / second", value: formatLimitValue(entitlements.RequestsPerSecond, false)},
		{label: "Prompt chars", value: formatLimitValue(entitlements.MaxPromptChars, false)},
		{label: "JSON body size", value: formatLimitValue(entitlements.MaxBodyBytes, true)},
		{label: "Artifact size", value: formatLimitValue(entitlements.MaxArtifactBytes, true)},
		{label: "Audit retention", value: formatDays(entitlements.AuditRetentionDays)},
	}
	rows = append(rows, dynamicLimitRows(entitlements.Limits)...)
	return rows
}

func dynamicLimitRows(limits map[string]int64) []licenseRow {
	if len(limits) == 0 {
		return nil
	}
	keys := make([]string, 0, len(limits))
	for key := range limits {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]licenseRow, 0, len(keys))
	for _, key := range keys {
		if isCoreEntitlementLimit(key) {
			continue
		}
		rows = append(rows, licenseRow{
			label: strings.ReplaceAll(key, "_", " "),
			value: formatLimitValue(limits[key], false),
		})
	}
	return rows
}

func isCoreEntitlementLimit(key string) bool {
	switch strings.TrimSpace(key) {
	case "max_workers",
		"max_concurrent_jobs",
		"max_active_workflows",
		"max_workflow_steps",
		"max_schema_count",
		"max_policy_bundles",
		"requests_per_second",
		"max_prompt_chars",
		"max_body_bytes",
		"max_artifact_bytes",
		"audit_retention_days":
		return true
	default:
		return false
	}
}

func licenseCapabilityRows(entitlements *licensing.Entitlements) []licenseRow {
	rows := []licenseRow{
		{label: "Single sign-on", value: formatEnabled(entitlements != nil && entitlements.SSO)},
		{label: "SAML", value: formatEnabled(entitlements != nil && entitlements.SAML)},
		{label: "SCIM", value: formatEnabled(entitlements != nil && entitlements.SCIM)},
		{label: "Advanced RBAC", value: formatEnabled(entitlements != nil && entitlements.RBAC)},
		{label: "Audit trail", value: formatEnabled(entitlements != nil && entitlements.Audit)},
		{label: "Audit export", value: formatEnabled(entitlements != nil && entitlements.AuditExport)},
		{label: "SIEM export", value: formatEnabled(entitlements != nil && entitlements.SIEMExport)},
		{label: "Legal hold", value: formatEnabled(entitlements != nil && entitlements.LegalHold)},
		{label: "Velocity rules", value: formatEnabled(entitlements != nil && entitlements.VelocityRules)},
		{label: "Break-glass admin", value: formatEnabled(entitlements != nil && entitlements.BreakGlassAdmin)},
	}
	if entitlements == nil || len(entitlements.Features) == 0 {
		return rows
	}

	keys := make([]string, 0, len(entitlements.Features))
	for key := range entitlements.Features {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		rows = append(rows, licenseRow{
			label: strings.ReplaceAll(key, "_", " "),
			value: formatEnabled(entitlements.Features[key]),
		})
	}
	return rows
}

func licenseRightsRows(rights *licensing.Rights) []licenseRow {
	return []licenseRow{
		{label: "Hosted service", value: formatEnabled(rights != nil && rights.HostedService)},
		{label: "Embedding", value: formatEnabled(rights != nil && rights.Embedding)},
		{label: "Resale", value: formatEnabled(rights != nil && rights.Resale)},
		{label: "White label", value: formatEnabled(rights != nil && rights.WhiteLabel)},
		{label: "Support SLA", value: formatEnabled(rights != nil && rights.SupportSLA)},
	}
}

func formatEnabled(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "not included"
}

func formatLimitValue(value int64, bytes bool) string {
	if value < 0 {
		return "unlimited"
	}
	if bytes {
		return formatBytesValue(value)
	}
	return formatInt(value)
}

func formatDays(value int64) string {
	if value < 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%s days", formatInt(value))
}
