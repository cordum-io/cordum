package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordum/cordum/core/model"
	sdk "github.com/cordum/cordum/sdk/client"
)

func runEvalsCmd(args []string) {
	if len(args) < 1 {
		evalsUsage()
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "extract":
		err = runEvalsExtract(args[1:])
	case "run":
		err = runEvalsRun(args[1:])
	default:
		evalsUsage()
		os.Exit(1)
	}
	if err != nil {
		fail(err.Error())
	}
}

func evalsUsage() {
	fmt.Fprintln(os.Stderr, `Usage: cordumctl evals <command>

Commands:
  extract --name <dataset>                         Extract incidents into an immutable eval dataset
  run --dataset <id> [--use-current] [--wait]     Replay an eval dataset against the active/candidate policy

Flags for extract:
  --since YYYY-MM-DD                               Inclusive lower date bound
  --until YYYY-MM-DD                               Inclusive upper date bound
  --topic <pattern>                                Topic filter (exact, glob, or re:regex)
  --rule <id>                                      Rule ID filter
  --verdicts deny,require_approval                 Lowercase wire verdicts
  --agent-id <id>                                  Agent ID filter
  --max-entries <n>                                Max deduplicated entries (default server-side 1000)
  --name <dataset>                                 Dataset name (required)
  --description <text>                             Dataset description
  --dry-run                                        Preview without writing

Flags for run:
  --dataset <id>                                   Dataset ID (required)
  --use-current                                    Evaluate against the active policy
  --candidate-bundle <id>                          Candidate bundle ID
  --candidate-content <yaml|@file>                 Candidate policy content inline or @file
  --max-entries <n>                                Cap entries evaluated by the server
  --wait                                           Poll until async runs complete and print the summary`)
}

func runEvalsExtract(args []string) error {
	fs := newFlagSet("evals extract")
	sinceRaw := fs.String("since", "", "inclusive lower date bound (YYYY-MM-DD)")
	untilRaw := fs.String("until", "", "inclusive upper date bound (YYYY-MM-DD)")
	topic := fs.String("topic", "", "topic filter (exact, glob, or re:regex)")
	ruleID := fs.String("rule", "", "rule id filter")
	verdictsRaw := fs.String("verdicts", "", "comma-separated lowercase wire verdicts")
	agentID := fs.String("agent-id", "", "agent id filter")
	maxEntries := fs.Int("max-entries", 0, "max deduplicated entries (default server-side 1000)")
	name := fs.String("name", "", "dataset name")
	description := fs.String("description", "", "dataset description")
	dryRun := fs.Bool("dry-run", false, "preview without writing")
	fs.ParseArgs(args)

	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	datasetName := strings.TrimSpace(*name)
	if datasetName == "" {
		return fmt.Errorf("--name is required")
	}

	since, err := parseEvalDate(*sinceRaw, false)
	if err != nil {
		return err
	}
	until, err := parseEvalDate(*untilRaw, true)
	if err != nil {
		return err
	}
	if since != nil && until != nil && until.Before(*since) {
		return fmt.Errorf("until must be >= since")
	}

	verdicts, err := parseEvalVerdicts(*verdictsRaw)
	if err != nil {
		return err
	}

	if *maxEntries < 0 {
		return fmt.Errorf("--max-entries must be >= 0")
	}
	if *maxEntries > model.MaxEvalDatasetEntries {
		return fmt.Errorf("--max-entries must be <= %d", model.MaxEvalDatasetEntries)
	}

	req := &sdk.ExtractIncidentsRequest{
		Topic:       strings.TrimSpace(*topic),
		RuleID:      strings.TrimSpace(*ruleID),
		Verdicts:    verdicts,
		AgentID:     strings.TrimSpace(*agentID),
		MaxEntries:  *maxEntries,
		Name:        datasetName,
		Description: strings.TrimSpace(*description),
		DryRun:      *dryRun,
	}
	if since != nil {
		req.Since = since.UTC().Format(time.RFC3339Nano)
	}
	if until != nil {
		req.Until = until.UTC().Format(time.RFC3339Nano)
	}

	client := newClientFromFlags(fs)
	resp, err := client.ExtractEvalDatasetFromIncidents(context.Background(), req)
	if err != nil {
		return err
	}

	printEvalExtractionSummary(resp, *dryRun)
	return nil
}

func parseEvalVerdicts(raw string) ([]string, error) {
	values := splitComma(raw)
	if len(values) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		parsed, err := model.ParseDecisionLogVerdict(value)
		if err != nil {
			return nil, err
		}
		wire, err := parsed.DecisionLogWireValue()
		if err != nil {
			return nil, err
		}
		if _, ok := seen[wire]; ok {
			continue
		}
		seen[wire] = struct{}{}
		out = append(out, wire)
	}
	return out, nil
}

func parseEvalDate(raw string, endOfDay bool) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, fmt.Errorf("parse eval date %q: %w", raw, err)
	}
	if endOfDay {
		value := parsed.Add(24*time.Hour - time.Millisecond)
		return &value, nil
	}
	return &parsed, nil
}

func printEvalExtractionSummary(resp *sdk.ExtractIncidentsResponse, dryRun bool) {
	if resp == nil {
		return
	}

	mode := "created"
	if dryRun {
		mode = "preview"
	}
	fmt.Printf("mode: %s\n", mode)
	fmt.Printf("name: %s\n", resp.Name)
	fmt.Printf("entry_count: %d\n", resp.EntryCount)
	fmt.Printf("deduped_count: %d\n", resp.DedupedCount)
	fmt.Printf("scanned_decisions: %d\n", resp.ScannedDecisions)
	if resp.Version > 0 {
		fmt.Printf("version: %d\n", resp.Version)
	}
	if datasetID := strings.TrimSpace(resp.DatasetID); datasetID != "" {
		fmt.Printf("dataset_id: %s\n", datasetID)
	}
	if len(resp.Warnings) > 0 {
		fmt.Println("warnings:")
		for _, warning := range resp.Warnings {
			fmt.Printf("  - %s\n", warning)
		}
	}
}

func runEvalsRun(args []string) error {
	fs := newFlagSet("evals run")
	datasetID := fs.String("dataset", "", "dataset id")
	useCurrent := fs.Bool("use-current", false, "evaluate against the active policy")
	candidateBundle := fs.String("candidate-bundle", "", "candidate bundle id")
	candidateContentRaw := fs.String("candidate-content", "", "candidate content inline or @file")
	maxEntries := fs.Int("max-entries", 0, "maximum entries to evaluate")
	wait := fs.Bool("wait", false, "poll until async runs complete")
	fs.ParseArgs(args)

	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(*datasetID) == "" {
		return fmt.Errorf("--dataset is required")
	}
	if *maxEntries < 0 {
		return fmt.Errorf("--max-entries must be >= 0")
	}
	if *maxEntries > model.MaxEvalDatasetEntries {
		return fmt.Errorf("--max-entries must be <= %d", model.MaxEvalDatasetEntries)
	}

	candidateContent, err := resolveEvalCandidateContent(*candidateContentRaw)
	if err != nil {
		return err
	}

	req := &sdk.EvalRunRequest{
		UseCurrentPolicy:  *useCurrent,
		CandidateBundleID: strings.TrimSpace(*candidateBundle),
		CandidateContent:  candidateContent,
		MaxEntries:        *maxEntries,
	}
	if !req.UseCurrentPolicy && req.CandidateBundleID == "" && req.CandidateContent == "" {
		req.UseCurrentPolicy = true
	}

	client := newClientFromFlags(fs)
	resp, err := client.RunEvalDataset(context.Background(), strings.TrimSpace(*datasetID), req)
	if err != nil {
		return err
	}

	if *wait {
		for resp.Pending() {
			time.Sleep(250 * time.Millisecond)
			resp, err = client.GetEvalRun(context.Background(), resp.RunID)
			if err != nil {
				return err
			}
		}
	}

	if resp.Failed() {
		return fmt.Errorf("eval run %s failed: %s", resp.RunID, strings.TrimSpace(resp.Error))
	}

	printEvalRunSummary(resp)
	if resp.Summary != nil && resp.Summary.Regressions > 0 {
		return fmt.Errorf("eval run %s detected %d regression(s)", resp.RunID, resp.Summary.Regressions)
	}
	return nil
}

func resolveEvalCandidateContent(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.HasPrefix(raw, "@") {
		return raw, nil
	}
	path := strings.TrimSpace(strings.TrimPrefix(raw, "@"))
	if path == "" {
		return "", fmt.Errorf("--candidate-content @file requires a path")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- @file is an explicit CLI input contract.
	if err != nil {
		return "", fmt.Errorf("read candidate content file %q: %w", path, err)
	}
	return string(data), nil
}

func printEvalRunSummary(resp *sdk.EvalRunResponse) {
	if resp == nil {
		return
	}
	if resp.RunID != "" {
		fmt.Printf("run_id: %s\n", resp.RunID)
	}
	if resp.Pending() {
		fmt.Printf("status: %s\n", resp.Status)
		if resp.PollURL != "" {
			fmt.Printf("poll_url: %s\n", resp.PollURL)
		}
		return
	}
	if resp.Failed() {
		fmt.Printf("status: %s\n", resp.Status)
		if msg := strings.TrimSpace(resp.Error); msg != "" {
			fmt.Printf("error: %s\n", msg)
		}
		return
	}
	if resp.DatasetID != "" {
		fmt.Printf("dataset_id: %s\n", resp.DatasetID)
	}
	if resp.DatasetName != "" {
		fmt.Printf("dataset_name: %s\n", resp.DatasetName)
	}
	if resp.DatasetVersion > 0 {
		fmt.Printf("dataset_version: %d\n", resp.DatasetVersion)
	}
	if resp.PolicySnapshot != "" {
		fmt.Printf("policy_snapshot: %s\n", resp.PolicySnapshot)
	}
	if resp.StartedAt != "" {
		fmt.Printf("started_at: %s\n", resp.StartedAt)
	}
	if resp.CompletedAt != "" {
		fmt.Printf("completed_at: %s\n", resp.CompletedAt)
	}
	if resp.Summary == nil {
		return
	}
	fmt.Printf("total: %d\n", resp.Summary.Total)
	fmt.Printf("passed: %d\n", resp.Summary.Passed)
	fmt.Printf("failed: %d\n", resp.Summary.Failed)
	fmt.Printf("regressions: %d\n", resp.Summary.Regressions)
	fmt.Printf("errored: %d\n", resp.Summary.Errored)
	if resp.Summary.ScorePercent != nil {
		fmt.Printf("score_percent: %.2f\n", *resp.Summary.ScorePercent)
	}
}
