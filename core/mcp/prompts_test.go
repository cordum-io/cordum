package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPromptRegistry_RegisterAndList(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	if err := RegisterAllPrompts(r); err != nil {
		t.Fatalf("RegisterAllPrompts: %v", err)
	}
	prompts := r.List(context.Background())
	if len(prompts) != 4 {
		t.Fatalf("expected 4 prompts, got %d", len(prompts))
	}
	names := map[string]bool{}
	for _, p := range prompts {
		names[p.Name] = true
	}
	for _, want := range []string{
		draftSafetyRulePromptName,
		explainDenialPromptName,
		summarizeApprovalsPromptName,
		policyMigrationHelperPromptName,
	} {
		if !names[want] {
			t.Errorf("missing prompt %q in catalogue: %+v", want, names)
		}
	}
	// List order is sorted so catalogue output stays deterministic.
	for i := 1; i < len(prompts); i++ {
		if prompts[i-1].Name > prompts[i].Name {
			t.Fatalf("list not sorted: %+v", prompts)
		}
	}
}

func TestPromptRegistry_RequiresRender(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	err := r.Register(PromptEntry{Descriptor: Prompt{Name: "no-render"}})
	if err == nil {
		t.Fatal("expected error when Render is nil")
	}
}

func TestPromptRegistry_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_, err := r.Render(context.Background(), "missing", nil)
	if !errors.Is(err, ErrPromptNotFound) {
		t.Fatalf("expected ErrPromptNotFound, got %v", err)
	}
}

func TestPromptRegistry_MissingRequiredArg(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	if err := RegisterAllPrompts(r); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := r.Render(context.Background(), draftSafetyRulePromptName, map[string]string{})
	if !errors.Is(err, ErrPromptInvalidArgs) {
		t.Fatalf("expected invalid-args, got %v", err)
	}
}

func TestDraftSafetyRule_RendersYAMLAndDisclaimer(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_ = RegisterAllPrompts(r)
	res, err := r.Render(context.Background(), draftSafetyRulePromptName, map[string]string{
		"scenario":   "block agents from calling the internal billing API without approval",
		"topic":      "job.billing.*",
		"risk_level": "high",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("expected system + user messages, got %d", len(res.Messages))
	}
	system := res.Messages[0]
	if system.Role != "system" {
		t.Fatalf("first message role=%q", system.Role)
	}
	// Simulate-before-apply disclaimer must appear verbatim — operators
	// grep for this exact line in rendered output.
	if !strings.Contains(system.Content.Text, draftSafetyRuleDisclaimer) {
		t.Fatalf("system prompt missing verbatim disclaimer:\n%s", system.Content.Text)
	}
	// Topic must be echoed in the user turn so the LLM has scope.
	if !strings.Contains(res.Messages[1].Content.Text, "job.billing.*") {
		t.Fatalf("user prompt missing topic:\n%s", res.Messages[1].Content.Text)
	}
}

func TestDraftSafetyRule_RejectsBadRiskLevel(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_ = RegisterAllPrompts(r)
	_, err := r.Render(context.Background(), draftSafetyRulePromptName, map[string]string{
		"scenario":   "x",
		"risk_level": "extreme",
	})
	if !errors.Is(err, ErrPromptInvalidArgs) {
		t.Fatalf("expected invalid-args, got %v", err)
	}
}

func TestExplainDenial_FetcherWiredEmbedsContext(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_ = RegisterAllPrompts(r)
	fetcher := func(_ context.Context, jobID string) (DenialContext, error) {
		if jobID != "job-42" {
			t.Fatalf("fetcher called with wrong id %q", jobID)
		}
		return DenialContext{
			JobID:    jobID,
			Decision: "deny",
			RuleID:   "rule-block-billing",
			Reason:   "agent attempted restricted billing op",
			Tenant:   "acme",
			Topic:    "job.billing.charge",
			AgentID:  "agent-99",
			RiskTags: []string{"pii", "finance"},
		}, nil
	}
	ctx := WithDenialContextFetcher(context.Background(), fetcher)
	res, err := r.Render(ctx, explainDenialPromptName, map[string]string{"job_id": "job-42"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	user := res.Messages[1].Content.Text
	for _, expect := range []string{"rule-block-billing", "restricted billing op", "job.billing.charge", "agent-99", "pii, finance"} {
		if !strings.Contains(user, expect) {
			t.Fatalf("user prompt missing %q:\n%s", expect, user)
		}
	}
}

func TestExplainDenial_NoFetcherAsksForContext(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_ = RegisterAllPrompts(r)
	res, err := r.Render(context.Background(), explainDenialPromptName, map[string]string{"job_id": "j1"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	user := res.Messages[1].Content.Text
	if !strings.Contains(user, "no fetcher wired") {
		t.Fatalf("expected no-fetcher hint:\n%s", user)
	}
}

func TestExplainDenial_FetcherErrorSurfaces(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_ = RegisterAllPrompts(r)
	fetcher := func(_ context.Context, _ string) (DenialContext, error) {
		return DenialContext{}, errors.New("upstream down")
	}
	ctx := WithDenialContextFetcher(context.Background(), fetcher)
	res, err := r.Render(ctx, explainDenialPromptName, map[string]string{"job_id": "j1"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(res.Messages[1].Content.Text, "decision_lookup: failed") {
		t.Fatal("expected fetch-failed marker")
	}
}

func TestSummarizeApprovals_RendersCounts(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_ = RegisterAllPrompts(r)
	src := func(_ context.Context, _ string, _ string) (ApprovalsSummary, error) {
		return ApprovalsSummary{
			Window: "24h", Tenant: "acme",
			Pending: 3, Approved: 10, Rejected: 2, Expired: 1,
			ByRule:    map[string]int{"rule-billing": 5, "rule-pii": 3, "rule-throttle": 1},
			Approvers: map[string]int{"alice": 6, "bob": 4},
		}, nil
	}
	ctx := WithApprovalsSummarySource(context.Background(), src)
	res, err := r.Render(ctx, summarizeApprovalsPromptName, map[string]string{"window": "24h"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	user := res.Messages[1].Content.Text
	for _, expect := range []string{"pending=3", "approved=10", "rule-billing: 5", "alice: 6"} {
		if !strings.Contains(user, expect) {
			t.Fatalf("missing %q:\n%s", expect, user)
		}
	}
}

func TestSummarizeApprovals_WindowValidation(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_ = RegisterAllPrompts(r)
	cases := map[string]bool{
		"":     true, // empty defaults to 24h
		"24h":  true,
		"7d":   true,
		"30d":  true,
		"720h": true,
		"31d":  false,
		"abc":  false,
		"5":    false,
		"-3h":  false,
	}
	for window, ok := range cases {
		args := map[string]string{"window": window}
		_, err := r.Render(context.Background(), summarizeApprovalsPromptName, args)
		if ok {
			if err != nil {
				t.Errorf("window=%q expected success, got %v", window, err)
			}
		} else {
			if !errors.Is(err, ErrPromptInvalidArgs) {
				t.Errorf("window=%q expected invalid-args, got %v", window, err)
			}
		}
	}
}

func TestSummarizeApprovals_NoSourceAsks(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_ = RegisterAllPrompts(r)
	res, _ := r.Render(context.Background(), summarizeApprovalsPromptName, map[string]string{})
	if !strings.Contains(res.Messages[1].Content.Text, "no source wired") {
		t.Fatal("expected no-source hint")
	}
}

func TestPolicyMigrationHelper_Rejects_SameVersions(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_ = RegisterAllPrompts(r)
	_, err := r.Render(context.Background(), policyMigrationHelperPromptName, map[string]string{
		"from_version": "2025-01-01",
		"to_version":   "2025-01-01",
	})
	if !errors.Is(err, ErrPromptInvalidArgs) {
		t.Fatalf("expected invalid-args on same-version, got %v", err)
	}
}

func TestPolicyMigrationHelper_EmbedsDiff(t *testing.T) {
	t.Parallel()
	r := NewPromptRegistry()
	_ = RegisterAllPrompts(r)
	diff := "RENAME: match.risk → match.risk_tags\nREMOVED: remediation.run_script"
	src := func(_ context.Context, from, to string) (string, error) {
		if from != "2024-09-01" || to != "2025-03-01" {
			t.Fatalf("src called with %q → %q", from, to)
		}
		return diff, nil
	}
	ctx := WithPolicyGrammarDiffSource(context.Background(), src)
	res, err := r.Render(ctx, policyMigrationHelperPromptName, map[string]string{
		"from_version": "2024-09-01",
		"to_version":   "2025-03-01",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(res.Messages[1].Content.Text, "RENAME: match.risk") {
		t.Fatalf("expected diff embedded:\n%s", res.Messages[1].Content.Text)
	}
	if !strings.Contains(res.Messages[0].Content.Text, "re-sign + simulate") {
		t.Fatal("system prompt must remind operator to re-sign + simulate")
	}
}

func TestIsValidApprovalsWindow_Table(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"":     false,
		"h":    false,
		"d":    false,
		"0h":   false,
		"-1h":  false,
		"1h":   true,
		"1d":   true,
		"24h":  true,
		"30d":  true,
		"31d":  false,
		"720h": true,
		"721h": false,
		"abc":  false,
	}
	for in, want := range cases {
		if got := isValidApprovalsWindow(in); got != want {
			t.Errorf("isValidApprovalsWindow(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTopCountLines_StableOrder(t *testing.T) {
	t.Parallel()
	in := map[string]int{"b": 3, "a": 3, "c": 5, "d": 1}
	got := topCountLines(in, 3)
	want := []string{"c: 5", "a: 3", "b: 3"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d: %v", len(got), len(want), got)
	}
	for i, line := range want {
		if got[i] != line {
			t.Errorf("line %d: %q want %q", i, got[i], line)
		}
	}
}
