package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// staticEvaluator maps `topic → (decision, ruleID)` so a test can fully
// control what each entry's evaluation returns.
type staticEvaluator struct {
	rules     map[string]staticRule
	errorOnID map[string]error
}

type staticRule struct {
	decision string
	ruleID   string
	reason   string
}

func (s staticEvaluator) Evaluate(req *pb.JobRequest) (decision, ruleID, reason string, err error) {
	if e, ok := s.errorOnID[req.GetJobId()]; ok {
		return "", "", "", e
	}
	rule, ok := s.rules[req.GetTopic()]
	if !ok {
		return "ALLOW", "", "no match", nil
	}
	return rule.decision, rule.ruleID, rule.reason, nil
}

func buildDataset(t *testing.T, entries ...model.EvalEntry) model.EvalDataset {
	t.Helper()
	return model.EvalDataset{
		ID:      "ds-test",
		Name:    "unit",
		Version: 1,
		Tenant:  "acme",
		Entries: entries,
	}
}

func entry(id, topic string, expected model.SafetyDecision) model.EvalEntry {
	snap := map[string]any{"tenant": "acme", "topic": topic, "agent_id": "agent-a"}
	raw, _ := json.Marshal(snap)
	return model.EvalEntry{
		ID:               id,
		Input:            raw,
		ExpectedDecision: expected,
	}
}

func fixedClock() func() time.Time {
	calls := 0
	return func() time.Time {
		calls++
		return time.Date(2026, 4, 20, 12, 0, calls, 0, time.UTC)
	}
}

func runCtx() RunContext {
	return RunContext{
		RunID:          "run-1",
		Tenant:         "acme",
		DatasetName:    "unit",
		DatasetVersion: 1,
		PolicySnapshot: "snap-1",
		Clock:          fixedClock(),
	}
}

func TestRunAllPass(t *testing.T) {
	ev := staticEvaluator{rules: map[string]staticRule{
		"job.a": {decision: "ALLOW"},
		"job.b": {decision: "DENY"},
		"job.c": {decision: "REQUIRE_APPROVAL"},
	}}
	ds := buildDataset(t,
		entry("e-a", "job.a", model.SafetyAllow),
		entry("e-b", "job.b", model.SafetyDeny),
		entry("e-c", "job.c", model.SafetyRequireApproval),
	)

	res, err := Run(context.Background(), runCtx(), ds, ev, RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Total != 3 || res.Summary.Passed != 3 {
		t.Fatalf("expected 3 passes, got %+v", res.Summary)
	}
	if res.Summary.Failed != 0 || res.Summary.Regressions != 0 || res.Summary.Errored != 0 {
		t.Fatalf("expected zero failures/regressions/errors, got %+v", res.Summary)
	}
	if res.Summary.ScorePercent == nil || *res.Summary.ScorePercent != 100.0 {
		t.Fatalf("expected score 100.00, got %+v", res.Summary.ScorePercent)
	}
	for _, r := range res.Entries {
		if r.Status != StatusPass {
			t.Fatalf("entry %s expected pass, got %s", r.EntryID, r.Status)
		}
		if r.DriftDirection != DriftUnchanged {
			t.Fatalf("entry %s expected unchanged drift, got %s", r.EntryID, r.DriftDirection)
		}
	}
}

func TestRunRegressionAndFailureMix(t *testing.T) {
	ev := staticEvaluator{rules: map[string]staticRule{
		"job.was-denied":   {decision: "ALLOW", ruleID: "r-relaxed"},
		"job.now-denied":   {decision: "DENY"},
		"job.now-throttle": {decision: "THROTTLE"},
		"job.ok":           {decision: "ALLOW"},
	}}
	ds := buildDataset(t,
		entry("e1", "job.was-denied", model.SafetyDeny),
		entry("e2", "job.now-denied", model.SafetyAllow),
		entry("e3", "job.now-throttle", model.SafetyRequireApproval),
		entry("e4", "job.ok", model.SafetyAllow),
	)

	res, err := Run(context.Background(), runCtx(), ds, ev, RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Total != 4 {
		t.Fatalf("total = %d, want 4", res.Summary.Total)
	}
	if res.Summary.Regressions != 1 {
		t.Fatalf("regressions = %d, want 1 (entry e1)", res.Summary.Regressions)
	}
	if res.Summary.Failed != 2 {
		t.Fatalf("failed = %d, want 2 (e2 + e3)", res.Summary.Failed)
	}
	if res.Summary.Passed != 1 {
		t.Fatalf("passed = %d, want 1 (e4)", res.Summary.Passed)
	}
	if res.Summary.ScorePercent == nil || *res.Summary.ScorePercent != 25.0 {
		t.Fatalf("score = %+v, want 25.00", res.Summary.ScorePercent)
	}

	byID := map[string]EntryResult{}
	for _, r := range res.Entries {
		byID[r.EntryID] = r
	}
	if byID["e1"].Status != StatusRegression {
		t.Fatalf("e1 should be regression, got %s", byID["e1"].Status)
	}
	if byID["e1"].DriftDirection != DriftRelaxed {
		t.Fatalf("e1 drift should be relaxed, got %s", byID["e1"].DriftDirection)
	}
	if byID["e2"].Status != StatusFail {
		t.Fatalf("e2 should be fail, got %s", byID["e2"].Status)
	}
	if byID["e2"].DriftDirection != DriftEscalated {
		t.Fatalf("e2 drift should be escalated, got %s", byID["e2"].DriftDirection)
	}
	if byID["e3"].Status != StatusFail {
		t.Fatalf("e3 should be fail, got %s", byID["e3"].Status)
	}
	if byID["e4"].Status != StatusPass {
		t.Fatalf("e4 should be pass, got %s", byID["e4"].Status)
	}
}

func TestRunEvalErrorMidRun(t *testing.T) {
	ev := staticEvaluator{
		rules:     map[string]staticRule{"job.ok": {decision: "ALLOW"}},
		errorOnID: map[string]error{"e-boom": errors.New("evaluator fell over")},
	}
	ds := buildDataset(t,
		entry("e-ok1", "job.ok", model.SafetyAllow),
		entry("e-boom", "job.ok", model.SafetyAllow),
		entry("e-ok2", "job.ok", model.SafetyAllow),
	)

	res, err := Run(context.Background(), runCtx(), ds, ev, RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Errored != 1 || res.Summary.Passed != 2 {
		t.Fatalf("expected 2 passed + 1 errored, got %+v", res.Summary)
	}
	var boom EntryResult
	for _, r := range res.Entries {
		if r.EntryID == "e-boom" {
			boom = r
			break
		}
	}
	if boom.Status != StatusError {
		t.Fatalf("e-boom status = %s, want error", boom.Status)
	}
	if !strings.Contains(boom.Error, "evaluator fell over") {
		t.Fatalf("e-boom error text lost: %q", boom.Error)
	}
}

func TestRunScoreRoundingBoundary(t *testing.T) {
	rules := map[string]staticRule{"job.ok": {decision: "ALLOW"}}
	ev := staticEvaluator{rules: rules}
	entries := make([]model.EvalEntry, 0, 1000)
	for i := 0; i < 1000; i++ {
		expected := model.SafetyAllow
		if i == 0 {
			expected = model.SafetyDeny
		}
		entries = append(entries, entry(fmt.Sprintf("e%04d", i), "job.ok", expected))
	}
	ds := buildDataset(t, entries...)

	res, err := Run(context.Background(), runCtx(), ds, ev, RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.ScorePercent == nil || *res.Summary.ScorePercent != 99.9 {
		t.Fatalf("score = %+v, want 99.90", res.Summary.ScorePercent)
	}
}

func TestRunCaseInsensitiveDecisionCompare(t *testing.T) {
	ev := staticEvaluator{rules: map[string]staticRule{
		"job.x": {decision: "deny"},
	}}
	ds := buildDataset(t, entry("e1", "job.x", model.SafetyDeny))

	res, err := Run(context.Background(), runCtx(), ds, ev, RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Passed != 1 {
		t.Fatalf("expected 1 pass on case-insensitive compare, got %+v", res.Summary)
	}
}

func TestRunEmptyDatasetReturnsNilScore(t *testing.T) {
	ev := staticEvaluator{}
	ds := buildDataset(t)

	res, err := Run(context.Background(), runCtx(), ds, ev, RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Total != 0 {
		t.Fatalf("total = %d, want 0", res.Summary.Total)
	}
	if res.Summary.ScorePercent != nil {
		t.Fatalf("ScorePercent must be nil for Total=0, got %+v", res.Summary.ScorePercent)
	}
	if len(res.Entries) != 0 {
		t.Fatalf("Entries should be empty, got %d", len(res.Entries))
	}
}

func TestRunRespectsMaxEntries(t *testing.T) {
	ev := staticEvaluator{rules: map[string]staticRule{"job.ok": {decision: "ALLOW"}}}
	ds := buildDataset(t,
		entry("e1", "job.ok", model.SafetyAllow),
		entry("e2", "job.ok", model.SafetyAllow),
		entry("e3", "job.ok", model.SafetyAllow),
	)

	res, err := Run(context.Background(), runCtx(), ds, ev, RunRequest{MaxEntries: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Total != 2 {
		t.Fatalf("expected total=2 from MaxEntries cap, got %d", res.Summary.Total)
	}
}

func TestRunRequiresEvaluator(t *testing.T) {
	ds := buildDataset(t, entry("e1", "job.a", model.SafetyAllow))
	if _, err := Run(context.Background(), runCtx(), ds, nil, RunRequest{}); err == nil {
		t.Fatal("expected error for nil evaluator")
	}
}

func TestRunMalformedInputRecordsError(t *testing.T) {
	ev := staticEvaluator{}
	ds := model.EvalDataset{
		ID:      "ds-bad",
		Name:    "unit",
		Version: 1,
		Tenant:  "acme",
		Entries: []model.EvalEntry{{
			ID:               "e-bad",
			Input:            json.RawMessage(`not json`),
			ExpectedDecision: model.SafetyAllow,
		}},
	}
	res, err := Run(context.Background(), runCtx(), ds, ev, RunRequest{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Summary.Errored != 1 || res.Summary.Total != 1 {
		t.Fatalf("expected 1 error / 1 total, got %+v", res.Summary)
	}
	if !strings.Contains(res.Entries[0].Error, "parse input snapshot") {
		t.Fatalf("expected parse error text, got %q", res.Entries[0].Error)
	}
}

func TestRunContextCancellation(t *testing.T) {
	ev := staticEvaluator{rules: map[string]staticRule{"job.ok": {decision: "ALLOW"}}}
	ds := buildDataset(t,
		entry("e1", "job.ok", model.SafetyAllow),
		entry("e2", "job.ok", model.SafetyAllow),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, runCtx(), ds, ev, RunRequest{}); err == nil {
		t.Fatal("expected ctx.Err() to abort run")
	}
}
