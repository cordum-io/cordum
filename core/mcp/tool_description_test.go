package mcp

import (
	"sort"
	"strings"
	"testing"
)

// TestReadOnlyToolDescriptions_EvaluatorSmoke confirms that a natural
// operator question deterministically picks the right read-only tool
// via a keyword-match scoring function. The scorer is deliberately
// simple — token overlap between the prompt and the tool's name +
// description — so the test is stable across CI runs and doesn't need
// a real LLM. The threshold (>=90% match rate) is the floor; a failing
// prompt exposes an LLM-hostile description that needs rewording.
//
// This is the "evaluator smoke test" from the plan. Each prompt maps
// to the tool an LLM planner should pick; any mismatch indicates the
// description fails the outcome-first + "Use this when..." pattern.
func TestReadOnlyToolDescriptions_EvaluatorSmoke(t *testing.T) {
	// Build the catalogue once. Uses a mockServiceBridge because the
	// scorer only reads Name + Description; handlers are never invoked.
	catalogue := readOnlyToolSpecs(&mockServiceBridge{})
	index := make(map[string]string) // id → name+description text
	for _, spec := range catalogue {
		index[spec.tool.Name] = strings.ToLower(spec.tool.Name + " " + spec.tool.Description)
	}

	// Natural-language prompts an operator might ask an LLM.
	// wantTool is the tool the description matching should produce.
	// We measure "top-1 accuracy" — planner must pick the right tool
	// as its highest-scoring match.
	cases := []struct {
		prompt   string
		wantTool string
	}{
		{"what jobs failed in the last hour", ToolListJobs},
		{"show me all jobs from today", ToolListJobs},
		{"why did job abc-123 fail", ToolGetJob},
		{"show me job details", ToolGetJob},
		{"which workflows are running now", ToolListRuns},
		{"give me the state of run r-42", ToolGetRun},
		{"where did run r-42 get stuck", ToolRunTimeline},
		{"show the timeline for this run", ToolRunTimeline},
		{"what workflows do I have available", ToolListWorkflows},
		{"list my installed integration packs", ToolListPacks},
		{"what topics can I submit to", ToolListTopics},
		{"which agents are online right now", ToolListWorkers},
		{"which identities can call this tool", ToolListAgents},
		{"what needs my approval", ToolListPendingApprovals},
		{"show pending approvals", ToolListPendingApprovals},
		{"search audit events for policy changes", ToolAuditQuery},
		{"who changed policy last week", ToolAuditQuery},
		{"verify the audit chain is intact", ToolAuditVerify},
		{"is our audit log compromised", ToolAuditVerify},
		{"is cordum healthy", ToolStatus},
	}
	if len(cases) < 20 {
		t.Fatalf("plan requires >=20 prompts, have %d", len(cases))
	}

	correct := 0
	for _, c := range cases {
		got := pickBestTool(strings.ToLower(c.prompt), index)
		if got == c.wantTool {
			correct++
			continue
		}
		t.Logf("prompt=%q picked %q (want %q)", c.prompt, got, c.wantTool)
	}
	rate := float64(correct) / float64(len(cases))
	if rate < 0.90 {
		t.Errorf("descriptor match rate %.0f%% below 90%% threshold (%d/%d correct)",
			rate*100, correct, len(cases))
	}
}

// pickBestTool scores every tool by the count of prompt tokens that
// appear verbatim in the tool's name+description text, then returns
// the highest-scoring tool id. Ties break by alphabetical tool id so
// the result is deterministic. Tokens shorter than 4 chars are
// discarded — they're usually articles / prepositions that don't
// discriminate between tools.
func pickBestTool(prompt string, index map[string]string) string {
	tokens := tokenize(prompt)
	scores := make(map[string]int, len(index))
	for id, text := range index {
		scores[id] = scoreOverlap(tokens, text)
	}
	best := ""
	bestScore := -1
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if scores[id] > bestScore {
			bestScore = scores[id]
			best = id
		}
	}
	return best
}

func tokenize(s string) []string {
	out := strings.FieldsFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	filtered := out[:0]
	for _, t := range out {
		if len(t) >= 4 {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func scoreOverlap(tokens []string, text string) int {
	score := 0
	for _, t := range tokens {
		if strings.Contains(text, t) {
			score++
		}
	}
	return score
}
