package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	sdk "github.com/cordum/cordum/sdk/client"
)

const (
	demoQuickstartID         = "quickstart"
	demoQuickstartWorkflow   = "demo-quickstart.hello"
	demoQuickstartTimeoutDef = 30 * time.Second
)

// runDemoCmd dispatches `cordumctl demo <subcommand>`.
func runDemoCmd(args []string) {
	if len(args) < 1 {
		fail("usage: cordumctl demo run <demo_id>")
	}
	switch args[0] {
	case "run":
		runDemoRun(args[1:])
	default:
		fail(fmt.Sprintf("unknown demo subcommand %q (only 'run' is supported)", args[0]))
	}
}

func runDemoRun(args []string) {
	fs := newFlagSet("demo run")
	timeoutSec := fs.Int("timeout", int(demoQuickstartTimeoutDef.Seconds()), "max seconds to wait for the demo run to finish")
	jsonOut := fs.Bool("json", false, "print the raw verdict map as JSON instead of the table")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		fail("usage: cordumctl demo run <demo_id>")
	}
	demoID := strings.TrimSpace(fs.Arg(0))
	if demoID != demoQuickstartID {
		fail(fmt.Sprintf("unknown demo %q (only %q is currently supported)", demoID, demoQuickstartID))
	}
	if *timeoutSec <= 0 {
		fail("--timeout must be > 0")
	}
	client := newClientFromFlags(fs)

	verdicts, err := runDemoQuickstart(context.Background(), client, time.Duration(*timeoutSec)*time.Second)
	if err != nil {
		fail(err.Error())
	}
	if *jsonOut {
		printJSON(verdicts)
	} else {
		renderVerdictTable(os.Stdout, verdicts)
	}
	if !allVerdictsObserved(verdicts) {
		// Demo's promise is to observe all three verdicts. If we timed out
		// without seeing one, surface it as a non-zero exit so CI catches
		// regressions while keeping the table visible.
		os.Exit(2)
	}
}

// demoVerdict is the per-step row rendered to the user. JobID is captured
// for the REQUIRE_APPROVAL row so we can print the approval command.
type demoVerdict struct {
	StepID  string `json:"step_id"`
	Topic   string `json:"topic"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	JobID   string `json:"job_id,omitempty"`
}

// expectedQuickstartSteps maps the workflow step IDs to the topic the
// step submits to. Order is the order rendered to the user.
var expectedQuickstartSteps = []struct {
	StepID string
	Topic  string
}{
	{"greet", "job.demo.greet"},
	{"attempt_delete", "job.demo.delete-all"},
	{"escalate_admin", "job.demo.admin"},
}

// runDemoQuickstart starts demo-quickstart.hello, polls for verdicts on
// each of the three worker steps, and returns the rendered rows. Returns
// after timeout even if not every verdict was observed — the table is
// still useful for diagnosis.
func runDemoQuickstart(ctx context.Context, client *sdk.Client, timeout time.Duration) ([]demoVerdict, error) {
	startCtx, startCancel := context.WithTimeout(ctx, 10*time.Second)
	defer startCancel()
	runID, err := client.StartRun(startCtx, demoQuickstartWorkflow, demoQuickstartInput())
	if err != nil {
		return nil, fmt.Errorf("start workflow %s: %w", demoQuickstartWorkflow, err)
	}

	rows := make([]demoVerdict, 0, len(expectedQuickstartSteps))
	for _, step := range expectedQuickstartSteps {
		rows = append(rows, demoVerdict{StepID: step.StepID, Topic: step.Topic})
	}
	rowByStep := func(stepID string) *demoVerdict {
		for i := range rows {
			if rows[i].StepID == stepID {
				return &rows[i]
			}
		}
		return nil
	}

	deadline := time.Now().Add(timeout)
	pollInterval := 1 * time.Second
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return rows, ctx.Err()
		}
		timelineCtx, timelineCancel := context.WithTimeout(ctx, 5*time.Second)
		events, err := client.GetRunTimeline(timelineCtx, runID)
		timelineCancel()
		if err == nil {
			updateRowsFromTimeline(ctx, client, events, rowByStep)
			if allVerdictsObserved(rows) {
				return rows, nil
			}
		}
		select {
		case <-ctx.Done():
			return rows, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	return rows, nil
}

// updateRowsFromTimeline scans the timeline for step→job mappings and, for
// each step missing a verdict, fetches the underlying job to read its
// safety_decision/safety_reason fields.
func updateRowsFromTimeline(ctx context.Context, client *sdk.Client, events []sdk.TimelineEvent, lookup func(string) *demoVerdict) {
	jobByStep := map[string]string{}
	for _, ev := range events {
		if ev.StepID == "" || ev.JobID == "" {
			continue
		}
		if _, ok := jobByStep[ev.StepID]; ok {
			continue
		}
		jobByStep[ev.StepID] = ev.JobID
	}
	for stepID, jobID := range jobByStep {
		row := lookup(stepID)
		if row == nil || row.Verdict != "" {
			continue
		}
		jobCtx, jobCancel := context.WithTimeout(ctx, 5*time.Second)
		job, err := client.GetJob(jobCtx, jobID)
		jobCancel()
		if err != nil || job == nil {
			continue
		}
		decision, _ := job["safety_decision"].(string)
		reason, _ := job["safety_reason"].(string)
		if strings.TrimSpace(decision) == "" {
			continue
		}
		row.Verdict = strings.ToUpper(decision)
		row.Reason = reason
		row.JobID = jobID
	}
}

func allVerdictsObserved(rows []demoVerdict) bool {
	for _, row := range rows {
		if row.Verdict == "" {
			return false
		}
	}
	return true
}

// demoQuickstartInput is the workflow's top-level input. The hello.yaml
// transform substitution map drives each worker step from these values.
func demoQuickstartInput() map[string]any {
	return map[string]any{
		"name":         "operator",
		"target":       "/etc/passwd",
		"admin_action": "rotate-secrets",
	}
}

// renderVerdictTable prints a fixed-width, ASCII-only verdict table plus
// an approval-command line for each REQUIRE_APPROVAL row. ASCII-only so
// MSYS/cmd on Windows render correctly regardless of code page.
func renderVerdictTable(out io.Writer, rows []demoVerdict) {
	const (
		stepW    = 18
		topicW   = 24
		verdictW = 18
		reasonW  = 50
	)
	hr := "  +" +
		strings.Repeat("-", stepW+2) + "+" +
		strings.Repeat("-", topicW+2) + "+" +
		strings.Repeat("-", verdictW+2) + "+" +
		strings.Repeat("-", reasonW+2) + "+"

	// Render in the deterministic order defined by expectedQuickstartSteps.
	rendered := make([]demoVerdict, 0, len(rows))
	for _, expected := range expectedQuickstartSteps {
		for _, row := range rows {
			if row.StepID == expected.StepID {
				rendered = append(rendered, row)
				break
			}
		}
	}
	if len(rendered) == 0 {
		// Defensive: if expected list drifted out of sync, fall back to the
		// rows order without re-sorting.
		rendered = append(rendered, rows...)
		sort.SliceStable(rendered, func(i, j int) bool {
			return rendered[i].StepID < rendered[j].StepID
		})
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "  Demo verdicts")
	_, _ = fmt.Fprintln(out, hr)
	_, _ = fmt.Fprintf(out, "  | %-*s | %-*s | %-*s | %-*s |\n",
		stepW, "Step", topicW, "Topic", verdictW, "Verdict", reasonW, "Reason")
	_, _ = fmt.Fprintln(out, hr)
	for _, row := range rendered {
		verdict := row.Verdict
		if verdict == "" {
			verdict = "PENDING"
		}
		_, _ = fmt.Fprintf(out, "  | %-*s | %-*s | %-*s | %-*s |\n",
			stepW, demoTruncate(row.StepID, stepW),
			topicW, demoTruncate(row.Topic, topicW),
			verdictW, demoTruncate(verdict, verdictW),
			reasonW, demoTruncate(row.Reason, reasonW))
	}
	_, _ = fmt.Fprintln(out, hr)

	for _, row := range rendered {
		if row.Verdict == "REQUIRE_APPROVAL" && row.JobID != "" {
			_, _ = fmt.Fprintf(out, "\n  To approve %s: cordumctl approval job %s --approve\n",
				row.StepID, row.JobID)
		}
	}
}

func demoTruncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}

// the existing flag.NewFlagSet helpers in main.go (newFlagSet/newClientFromFlags)
// are imported via package scope, so demo.go doesn't need its own. The compile
// check below silences an unused-import linter when called from tests.
var _ = flag.ContinueOnError
