package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cordum/cordum/core/edge/calibration"
	sdk "github.com/cordum/cordum/sdk/client"
)

const (
	edgeCalibrationPath          = "/api/v1/edge/calibration-reports"
	edgeCalibrationDefaultWindow = 7 * 24 * time.Hour
	edgeCalibrationUsage         = "usage: cordumctl edge calibration-report [--since T] [--until T] [--repos a,b] [--labels k=v,k2=v2] [--json]\n       cordumctl edge calibration-report get <id> [--json]"
)

// edgeCalibrationRequest is the POST body of /api/v1/edge/calibration-reports.
type edgeCalibrationRequest struct {
	Since  time.Time         `json:"since"`
	Until  time.Time         `json:"until"`
	Repos  []string          `json:"repos,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// runEdgeCalibrationReportCmd creates a calibration report over a window and
// a scope, or fetches a stored one with `get <id>`. It exits 0 on success, 1
// when the request fails and 2 on a usage error, which sends no request.
func runEdgeCalibrationReportCmd(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("edge calibration-report")
	since := fs.String("since", "", "window start, RFC 3339 (default: --until minus 7 days)")
	until := fs.String("until", "", "window end, RFC 3339 (default: now)")
	repos := fs.String("repos", "", "comma-separated repositories to include")
	labels := fs.String("labels", "", "comma-separated k=v session labels to include")
	jsonOut := fs.Bool("json", false, "print the server's JSON unchanged")
	fs.ParseArgs(args)

	method, path, body, err := edgeCalibrationCall(fs.Args(), *since, *until, *repos, *labels)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cordumctl edge calibration-report: %s\n%s\n", err, edgeCalibrationUsage)
		return 2
	}
	transport, err := sdk.BuildTLSTransportErr(fs.tlsOptions())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cordumctl edge calibration-report: tls configuration error: %v\n", err)
		return 2
	}
	var raw json.RawMessage
	client := newRestClient(*fs.gateway, *fs.apiKey, *fs.tenant, transport)
	if err := client.doJSON(context.Background(), method, path, body, &raw); err != nil {
		_, _ = fmt.Fprintf(stderr, "cordumctl edge calibration-report: %s\n", err)
		return 1
	}
	if *jsonOut {
		_, _ = stdout.Write(raw)
		return 0
	}
	var report calibration.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		_, _ = fmt.Fprintf(stderr, "cordumctl edge calibration-report: decode report: %s\n", err)
		return 1
	}
	renderEdgeCalibrationReport(stdout, report)
	return 0
}

// edgeCalibrationCall turns the positional args and the flags into the call
// to make: a GET for `get <id>`, otherwise a POST of the window and scope.
func edgeCalibrationCall(args []string, since, until, repos, labels string) (string, string, any, error) {
	if len(args) > 0 && args[0] == "get" {
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			return "", "", nil, fmt.Errorf("get needs exactly one report id")
		}
		return http.MethodGet, edgeCalibrationPath + "/" + url.PathEscape(args[1]), nil, nil
	}
	if len(args) != 0 {
		return "", "", nil, fmt.Errorf("unexpected argument %q", args[0])
	}
	body, err := edgeCalibrationBody(since, until, repos, labels, time.Now().UTC())
	if err != nil {
		return "", "", nil, err
	}
	return http.MethodPost, edgeCalibrationPath, body, nil
}

// edgeCalibrationBody parses the window and scope flags. until defaults to
// now and since to until minus 7 days.
func edgeCalibrationBody(since, until, repos, labels string, now time.Time) (edgeCalibrationRequest, error) {
	body := edgeCalibrationRequest{Until: now}
	var err error
	if until != "" {
		if body.Until, err = time.Parse(time.RFC3339, until); err != nil {
			return body, fmt.Errorf("--until: %w", err)
		}
	}
	body.Since = body.Until.Add(-edgeCalibrationDefaultWindow)
	if since != "" {
		if body.Since, err = time.Parse(time.RFC3339, since); err != nil {
			return body, fmt.Errorf("--since: %w", err)
		}
	}
	for _, repo := range strings.Split(repos, ",") {
		if repo = strings.TrimSpace(repo); repo != "" {
			body.Repos = append(body.Repos, repo)
		}
	}
	for _, pair := range strings.Split(labels, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if key = strings.TrimSpace(key); !ok || key == "" {
			return body, fmt.Errorf("--labels: %q is not k=v", pair)
		}
		if body.Labels == nil {
			body.Labels = map[string]string{}
		}
		body.Labels[key] = strings.TrimSpace(value)
	}
	return body, nil
}

// renderEdgeCalibrationReport prints the report as ASCII text. The Edge
// counts are decisions recorded in observe-mode sessions; only the shadow
// section says what a candidate bundle would decide.
func renderEdgeCalibrationReport(out io.Writer, r calibration.Report) {
	line := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format+"\n", a...) }
	section := func(heading string, entries []string) {
		line("%s", heading)
		if len(entries) == 0 {
			entries = []string{"(none)"}
		}
		for _, entry := range entries {
			line("  %s", entry)
		}
	}
	truncated := "no"
	if r.Truncated {
		truncated = "yes"
	}
	line("Calibration report %s", r.ID)
	line("Tenant: %s  Window: %s to %s  Truncated: %s", r.TenantID, r.Since.Format(time.RFC3339), r.Until.Format(time.RFC3339), truncated)
	var rules []string
	for _, rule := range r.Rules {
		rules = append(rules, fmt.Sprintf("%s  deny=%d  require_approval=%d", edgeCalibrationRuleName(rule.RuleID), rule.Deny, rule.RequireApproval))
	}
	if r.SessionsObserved == 0 {
		rules = []string{"No observe-mode sessions matched this window and scope."}
	}
	section(fmt.Sprintf("Policy decisions recorded in observe-mode sessions (%d sessions, %d events):", r.SessionsObserved, r.EventsScanned), rules)
	section("Rules with no matching Edge event:", r.NeverFired)
	section("Most affected tools:", edgeCalibrationNameCounts(r.TopTools))
	section("Most affected command classes:", edgeCalibrationNameCounts(r.TopCommandClasses))
	var days []string
	total := 0
	for _, day := range r.ApprovalsPerDay {
		days = append(days, fmt.Sprintf("%s  %d", day.Day, day.Count))
		total += day.Count
	}
	mean := 0.0
	if len(days) > 0 {
		mean = float64(total) / float64(len(days))
	}
	section(fmt.Sprintf("Approval requests per UTC day (mean %.1f):", mean), days)
	var shadow []string
	for _, rule := range r.ShadowRules {
		shadow = append(shadow, fmt.Sprintf("%s/%s  deny=%d  require_approval=%d", rule.BundleID, edgeCalibrationRuleName(rule.RuleID), rule.Deny, rule.RequireApproval))
	}
	section("Candidate shadow bundles, what each would decide (tenant-wide, not filtered by scope):", shadow)
	var assurance []string
	for _, state := range slices.Sorted(maps.Keys(r.Assurance)) {
		assurance = append(assurance, fmt.Sprintf("%s  %d", state, r.Assurance[state]))
	}
	section("Assurance state of observed sessions (at report time):", assurance)
}

func edgeCalibrationRuleName(id string) string {
	if id == "" {
		return "(no rule)"
	}
	return id
}

func edgeCalibrationNameCounts(list []calibration.NameCount) []string {
	entries := make([]string, 0, len(list))
	for _, item := range list {
		entries = append(entries, fmt.Sprintf("%s  %d", item.Name, item.Count))
	}
	return entries
}
