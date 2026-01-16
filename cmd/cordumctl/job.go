package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	sdk "github.com/cordum/cordum/sdk/client"
)

func runJobCmd(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	switch args[0] {
	case "submit":
		runJobSubmit(args[1:])
	case "status":
		runJobStatus(args[1:])
	case "logs":
		runJobLogs(args[1:])
	default:
		usage()
		os.Exit(1)
	}
}

func runJobSubmit(args []string) {
	fs := newFlagSet("job submit")
	topic := fs.String("topic", "", "job topic (job.*)")
	prompt := fs.String("prompt", "", "job prompt")
	input := fs.String("input", "", "input JSON (inline or path)")
	idempotencyKey := fs.String("idempotency-key", "", "idempotency key")
	capability := fs.String("capability", "", "job capability")
	packID := fs.String("pack-id", "", "pack id")
	labels := fs.String("labels", "", "labels JSON object")
	riskTags := fs.String("risk-tags", "", "comma-separated risk tags")
	requires := fs.String("requires", "", "comma-separated requires")
	orgID := fs.String("org", "", "org/tenant id")
	actorID := fs.String("actor-id", "", "actor id")
	actorType := fs.String("actor-type", "", "actor type (human|service)")
	jsonOut := fs.Bool("json", false, "output JSON response")
	fs.ParseArgs(args)

	if strings.TrimSpace(*topic) == "" {
		fail("job topic required")
	}
	promptValue := strings.TrimSpace(*prompt)
	if promptValue == "" {
		if strings.TrimSpace(*input) == "" {
			fail("prompt required (use --prompt or --input)")
		}
		promptValue = "cordumctl job submit"
	}

	var contextValue any
	if strings.TrimSpace(*input) != "" {
		decoded, err := parseJSONArg(*input)
		check(err)
		contextValue = decoded
	}

	labelMap := map[string]string{}
	if strings.TrimSpace(*labels) != "" {
		decoded, err := parseJSONArg(*labels)
		check(err)
		typed, ok := decoded.(map[string]any)
		if !ok {
			fail("labels must be a JSON object")
		}
		for key, val := range typed {
			str, ok := val.(string)
			if !ok {
				fail("labels values must be strings")
			}
			labelMap[key] = str
		}
	}
	if len(labelMap) == 0 {
		labelMap = nil
	}

	req := &sdk.JobSubmitRequest{
		Prompt:         promptValue,
		Topic:          *topic,
		Context:        contextValue,
		OrgID:          strings.TrimSpace(*orgID),
		ActorID:        strings.TrimSpace(*actorID),
		ActorType:      strings.TrimSpace(*actorType),
		IdempotencyKey: strings.TrimSpace(*idempotencyKey),
		PackID:         strings.TrimSpace(*packID),
		Capability:     strings.TrimSpace(*capability),
		RiskTags:       splitComma(*riskTags),
		Requires:       splitComma(*requires),
		Labels:         labelMap,
	}

	client := newClient(*fs.gateway, *fs.apiKey)
	resp, err := client.SubmitJob(context.Background(), req)
	check(err)
	if *jsonOut {
		printJSON(resp)
		return
	}
	fmt.Println(resp.JobID)
}

func runJobStatus(args []string) {
	fs := newFlagSet("job status")
	jsonOut := fs.Bool("json", false, "output full job JSON")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		fail("job id required")
	}
	client := newClient(*fs.gateway, *fs.apiKey)
	job, err := client.GetJob(context.Background(), fs.Arg(0))
	check(err)
	if *jsonOut {
		printJSON(job)
		return
	}
	if state, ok := job["state"].(string); ok && state != "" {
		fmt.Println(state)
		return
	}
	printJSON(job)
}

func runJobLogs(args []string) {
	fs := newFlagSet("job logs")
	fs.ParseArgs(args)
	if fs.NArg() < 1 {
		fail("job id required")
	}
	client := newClient(*fs.gateway, *fs.apiKey)
	job, err := client.GetJob(context.Background(), fs.Arg(0))
	check(err)
	if result, ok := job["result"]; ok && result != nil {
		printJSON(result)
		return
	}
	if msg, ok := job["error_message"].(string); ok && strings.TrimSpace(msg) != "" {
		fmt.Fprintln(os.Stderr, msg)
		return
	}
	printJSON(job)
}

func parseJSONArg(value string) (any, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if _, err := os.Stat(value); err == nil {
		// #nosec G304 -- CLI explicitly reads local files provided by the operator.
		data, err := os.ReadFile(value)
		if err != nil {
			return nil, err
		}
		return parseJSONBytes(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return parseJSONBytes([]byte(value))
}

func parseJSONBytes(data []byte) (any, error) {
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	return out, nil
}

func splitComma(value string) []string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
