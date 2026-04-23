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
  --dry-run                                        Preview without writing`)
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
