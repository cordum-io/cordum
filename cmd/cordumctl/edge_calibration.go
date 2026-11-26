package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// renderEdgeCalibrationReport prints the report as ASCII text.
func renderEdgeCalibrationReport(out io.Writer, r calibration.Report) {
	_, _ = fmt.Fprintf(out, "Calibration report %s\n", r.ID)
}
