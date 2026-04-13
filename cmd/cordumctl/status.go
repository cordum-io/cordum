package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cordum/cordum/core/licensing"
)

type gatewayStatusResponse struct {
	Time          string                 `json:"time"`
	UptimeSeconds int64                  `json:"uptime_seconds"`
	Build         gatewayBuildResponse   `json:"build"`
	NATS          gatewayNATSResponse    `json:"nats"`
	Redis         gatewayRedisResponse   `json:"redis"`
	Workers       gatewayWorkersSummary  `json:"workers"`
	License       *licensing.LicenseInfo `json:"license,omitempty"`
}

type gatewayBuildResponse struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type gatewayNATSResponse struct {
	Connected bool   `json:"connected"`
	Status    string `json:"status"`
}

type gatewayRedisResponse struct {
	OK bool `json:"ok"`
}

type gatewayWorkersSummary struct {
	Count int64 `json:"count"`
}

type licenseUsageSummary struct {
	TenantID string                 `json:"tenant_id"`
	Plan     string                 `json:"plan"`
	License  *licensing.LicenseInfo `json:"license,omitempty"`
	Usage    licenseUsageMetrics    `json:"usage"`
}

type licenseUsageMetrics struct {
	Workers           numericUsageMetric `json:"workers"`
	ConcurrentJobs    numericUsageMetric `json:"concurrent_jobs"`
	ActiveWorkflows   numericUsageMetric `json:"active_workflows"`
	WorkflowSteps     numericUsageMetric `json:"workflow_steps"`
	Schemas           numericUsageMetric `json:"schemas"`
	PolicyBundles     numericUsageMetric `json:"policy_bundles"`
	RequestsPerSecond numericUsageMetric `json:"requests_per_second"`
	PromptChars       numericUsageMetric `json:"prompt_chars"`
	BodyBytes         numericUsageMetric `json:"body_bytes"`
	ApprovalMode      stringUsageMetric  `json:"approval_mode"`
}

type numericUsageMetric struct {
	Current    *int64 `json:"current,omitempty"`
	Allowed    *int64 `json:"allowed,omitempty"`
	Registered *int64 `json:"registered,omitempty"`
	Connected  *int64 `json:"connected,omitempty"`
}

type stringUsageMetric struct {
	Current *string `json:"current,omitempty"`
	Allowed *string `json:"allowed,omitempty"`
}

func runStatusCmd(args []string) {
	if err := runStatusCmdE(args); err != nil {
		fail(err.Error())
	}
}

func runStatusCmdE(args []string) error {
	fs := newFlagSet("status")
	jsonOutput := fs.Bool("json", false, "emit raw JSON enriched with license usage")
	fs.ParseArgs(args)

	client := restClientFromFlags(fs)
	ctx := context.Background()

	var status gatewayStatusResponse
	if err := client.doJSON(ctx, http.MethodGet, "/api/v1/status", nil, &status); err != nil {
		return err
	}

	var usage licenseUsageSummary
	usageErr := client.doJSON(ctx, http.MethodGet, "/api/v1/license/usage", nil, &usage)

	if *jsonOutput {
		payload := map[string]any{
			"status": status,
		}
		if usageErr == nil {
			payload["license_usage"] = usage
		} else {
			payload["license_usage_error"] = usageErr.Error()
		}
		printJSON(payload)
		return nil
	}

	printStatusOverview(os.Stdout, strings.TrimRight(*fs.gateway, "/"), status, chooseStatusLicense(status, usage, usageErr))
	if usageErr != nil {
		_, _ = fmt.Fprintf(os.Stdout, "\nUsage vs limits unavailable: %v\n", usageErr)
		return nil
	}

	printStatusUsage(os.Stdout, usage)
	return nil
}

func chooseStatusLicense(status gatewayStatusResponse, usage licenseUsageSummary, usageErr error) *licensing.LicenseInfo {
	if usageErr == nil && usage.License != nil {
		return usage.License
	}
	return status.License
}

func printStatusOverview(w *os.File, gateway string, status gatewayStatusResponse, licenseInfo *licensing.LicenseInfo) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "Gateway\t"+valueOrDash(gateway))
	_, _ = fmt.Fprintln(tw, "Build\t"+valueOrDash(status.Build.Version))
	_, _ = fmt.Fprintln(tw, "Commit\t"+valueOrDash(status.Build.Commit))
	_, _ = fmt.Fprintln(tw, "Time\t"+valueOrDash(status.Time))
	_, _ = fmt.Fprintln(tw, "Uptime\t"+formatUptime(status.UptimeSeconds))
	_, _ = fmt.Fprintln(tw, "Workers\t"+formatInt(status.Workers.Count))
	_, _ = fmt.Fprintln(tw, "NATS\t"+valueOrDash(strings.ToLower(status.NATS.Status)))
	_, _ = fmt.Fprintln(tw, "Redis\t"+formatBoolState(status.Redis.OK, "ok", "unavailable"))
	_, _ = fmt.Fprintln(tw, "Tier\t"+displayPlanName(statusTier(status, licenseInfo)))
	_, _ = fmt.Fprintln(tw, "Expiry\t"+formatStatusExpiry(licenseInfo))
	_ = tw.Flush()
}

func printStatusUsage(w *os.File, summary licenseUsageSummary) {
	_, _ = fmt.Fprintf(w, "\nUsage vs limits (tenant: %s)\n", valueOrDash(summary.TenantID))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "METRIC\tCURRENT\tLIMIT\tDETAILS")
	writeNumericUsageRow(tw, "Workers", summary.Usage.Workers, "", false)
	writeNumericUsageRow(tw, "Concurrent jobs", summary.Usage.ConcurrentJobs, "", false)
	writeNumericUsageRow(tw, "Active workflows", summary.Usage.ActiveWorkflows, "", false)
	writeNumericUsageRow(tw, "Workflow steps / run", summary.Usage.WorkflowSteps, "", false)
	writeNumericUsageRow(tw, "Schemas", summary.Usage.Schemas, "", false)
	writeNumericUsageRow(tw, "Policy bundles", summary.Usage.PolicyBundles, "", false)
	writeStringUsageRow(tw, "Approval mode", summary.Usage.ApprovalMode)
	writeNumericUsageRow(tw, "Requests / second", summary.Usage.RequestsPerSecond, "", false)
	writeNumericUsageRow(tw, "Prompt chars", summary.Usage.PromptChars, "", false)
	writeNumericUsageRow(tw, "JSON body size", summary.Usage.BodyBytes, "", true)
	_ = tw.Flush()
}

func writeNumericUsageRow(tw *tabwriter.Writer, label string, metric numericUsageMetric, detail string, bytes bool) {
	current := formatOptionalInt(metric.Current)
	limit := formatOptionalLimit(metric.Allowed, bytes)
	if metric.Registered != nil || metric.Connected != nil {
		detail = strings.TrimSpace(strings.Join([]string{
			formatDetail("registered", metric.Registered),
			formatDetail("connected", metric.Connected),
		}, " · "))
	}
	if detail == "" {
		detail = "—"
	}
	_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", label, current, limit, detail)
}

func writeStringUsageRow(tw *tabwriter.Writer, label string, metric stringUsageMetric) {
	current := "—"
	if metric.Current != nil && strings.TrimSpace(*metric.Current) != "" {
		current = strings.TrimSpace(*metric.Current)
	}
	limit := "—"
	if metric.Allowed != nil && strings.TrimSpace(*metric.Allowed) != "" {
		limit = strings.TrimSpace(*metric.Allowed)
	}
	_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", label, current, limit, "—")
}

func formatDetail(label string, value *int64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%s %s", formatInt(*value), label)
}

func formatOptionalInt(value *int64) string {
	if value == nil {
		return "—"
	}
	return formatInt(*value)
}

func formatOptionalLimit(value *int64, bytes bool) string {
	if value == nil {
		return "—"
	}
	if bytes {
		return formatBytesValue(*value)
	}
	if *value < 0 {
		return "unlimited"
	}
	return formatInt(*value)
}

func formatBytesValue(value int64) string {
	if value < 0 {
		return "unlimited"
	}
	if value == 0 {
		return "0 B"
	}
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case value >= gib:
		return fmt.Sprintf("%.1f GB", float64(value)/float64(gib))
	case value >= mib:
		return fmt.Sprintf("%.1f MB", float64(value)/float64(mib))
	case value >= kib:
		return fmt.Sprintf("%.1f KB", float64(value)/float64(kib))
	default:
		return fmt.Sprintf("%d B", value)
	}
}

func formatBoolState(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

func formatInt(value int64) string {
	return fmt.Sprintf("%d", value)
}

func formatUptime(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	return (time.Duration(seconds) * time.Second).String()
}

func statusTier(status gatewayStatusResponse, licenseInfo *licensing.LicenseInfo) string {
	if licenseInfo != nil && strings.TrimSpace(licenseInfo.Plan) != "" {
		return licenseInfo.Plan
	}
	return licensing.PlanCommunity.DisplayName()
}

func displayPlanName(plan string) string {
	return licensing.ParsePlan(plan).DisplayName()
}

func formatStatusExpiry(info *licensing.LicenseInfo) string {
	if info == nil {
		return "Community defaults active"
	}
	expiresAt := strings.TrimSpace(info.ExpiresAt)
	status := strings.TrimSpace(info.Status)
	switch {
	case expiresAt == "" && status == "":
		return "No expiry set"
	case expiresAt == "":
		return status
	case status == "":
		return expiresAt
	default:
		return fmt.Sprintf("%s (%s)", expiresAt, status)
	}
}
