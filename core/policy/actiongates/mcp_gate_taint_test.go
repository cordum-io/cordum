package actiongates

import (
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// mcpDestructiveIdentity allows the demo server + every tool so gate evaluation
// reaches the taint check (the full identity used elsewhere only allows read_*/
// list_*, which would deny delete_items at the tool allowlist first).
func mcpDestructiveIdentity() *mcp.AgentIdentity {
	return &mcp.AgentIdentity{
		ID:             mcpAgentA,
		AllowedServers: []string{"monday"},
		AllowedTools:   []string{"*"},
	}
}

// TestMCPGate_SessionTaintDeniesDestructiveOnly is the content-aware deny: the
// gate DENIES a destructive tool ONLY when the session is tainted (tainted AND
// destructive), citing the injected snippet in Extra. A clean session's delete
// is NOT denied by taint (DoD#3) and a benign tool while tainted still flows
// (DoD#4) -- proving this is not a bare "deny deletes" metadata rule.
func TestMCPGate_SessionTaintDeniesDestructiveOnly(t *testing.T) {
	t.Parallel()
	taint := mcpTaintForTest("get_board")
	cases := []struct {
		name     string
		tool     string
		tainted  bool
		wantDeny bool
	}{
		{"destructive_tainted_denies", "delete_items", true, true},
		{"destructive_clean_allows", "delete_items", false, false},
		{"benign_tainted_allows", "get_board", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := newMCPGateWithIdentity(mcpDestructiveIdentity())
			in := withAgentIDLabel(mcpInputAction("monday", tc.tool), mcpAgentA)
			if tc.tainted {
				stampMCPTaintForTest(in.Action, taint)
			}
			dec := gate.Evaluate(mcpAuthCtx(), in)
			assertTaintDecision(t, dec, tc.wantDeny, taint, tc.tool)
		})
	}
}

func TestMCPGate_SessionTaintDeniesAllMondayApiDeleteMutation(t *testing.T) {
	t.Parallel()
	taint := mcpTaintForTest("get_board_items_page")
	cases := []struct {
		name      string
		tool      string
		query     string
		tainted   bool
		wantDeny  bool
		wantMatch string
	}{
		{
			name:      "tainted_delete_item_mutation_denies",
			tool:      "all_monday_api",
			query:     `mutation($itemId: ID!){delete_item(item_id:$itemId){id}}`,
			tainted:   true,
			wantDeny:  true,
			wantMatch: "delete_item",
		},
		{
			name:     "clean_delete_item_mutation_allows",
			tool:     "all_monday_api",
			query:    `mutation($itemId: ID!){delete_item(item_id:$itemId){id}}`,
			tainted:  false,
			wantDeny: false,
		},
		{
			name:     "tainted_graphql_query_allows",
			tool:     "all_monday_api",
			query:    `query{boards{id}}`,
			tainted:  true,
			wantDeny: false,
		},
		{
			name:     "tainted_benign_mutation_allows",
			tool:     "all_monday_api",
			query:    `mutation{change_column_value(board_id:1,item_id:2,column_id:"s",value:"x"){id}}`,
			tainted:  true,
			wantDeny: false,
		},
		{
			name:      "tainted_delete_items_mutation_denies",
			tool:      "all_monday_api",
			query:     `mutation{delete_items(item_ids:[0]){id}}`,
			tainted:   true,
			wantDeny:  true,
			wantMatch: "delete_items",
		},
		{
			name:      "tainted_delete_board_mutation_denies",
			tool:      "all_monday_api",
			query:     `mutation{delete_board(board_id:0){id}}`,
			tainted:   true,
			wantDeny:  true,
			wantMatch: "delete_board",
		},
		{
			name:      "tainted_delete_items_tool_name_denies",
			tool:      "delete_items",
			query:     "",
			tainted:   true,
			wantDeny:  true,
			wantMatch: "delete_items",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := newMCPGateWithIdentity(mcpDestructiveIdentity())
			in := withAgentIDLabel(mcpInputAction("monday", tc.tool), mcpAgentA)
			if tc.query != "" {
				in.Action.Args = map[string]any{"query": tc.query}
			}
			if tc.tainted {
				stampMCPTaintForTest(in.Action, taint)
			}
			dec := gate.Evaluate(mcpAuthCtx(), in)
			assertTaintDecision(t, dec, tc.wantDeny, taint, tc.wantMatch)
		})
	}
}

func mcpTaintForTest(sourceTool string) *config.ActionSessionTaint {
	return &config.ActionSessionTaint{
		Pattern:    "system override directive",
		Snippet:    "SYSTEM OVERRIDE:",
		SourceTool: sourceTool,
		Severity:   "high",
	}
}

func stampMCPTaintForTest(action *config.ActionDescriptor, taint *config.ActionSessionTaint) {
	action.RiskTags = []string{config.RiskTagSessionPromptInjection}
	action.SessionTaint = taint
}

func assertTaintDecision(t *testing.T, dec ActionGateDecision, wantDeny bool, taint *config.ActionSessionTaint, wantMatch string) {
	t.Helper()
	if wantDeny {
		if dec.Decision != pb.DecisionType_DECISION_TYPE_DENY {
			t.Fatalf("got %v, want DENY", dec.Decision)
		}
		if dec.Code != CodeAccessDenied {
			t.Fatalf("got code %q, want %q", dec.Code, CodeAccessDenied)
		}
		if dec.SubReason != "session_tainted_prompt_injection" {
			t.Fatalf("got subReason %q, want session_tainted_prompt_injection", dec.SubReason)
		}
		if dec.Extra["taint_snippet"] != taint.Snippet {
			t.Fatalf("Extra[taint_snippet] = %q, want %q", dec.Extra["taint_snippet"], taint.Snippet)
		}
		if dec.Extra["taint_pattern"] != taint.Pattern {
			t.Fatalf("Extra[taint_pattern] = %q, want %q", dec.Extra["taint_pattern"], taint.Pattern)
		}
		match := dec.Extra["taint_destructive_match"]
		if match == "" {
			t.Fatalf("missing Extra[taint_destructive_match]")
		}
		if wantMatch != "" && !strings.Contains(match, wantMatch) {
			t.Fatalf("Extra[taint_destructive_match] = %q, want it to contain %q", match, wantMatch)
		}
		return
	}
	if dec.Decision != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("got %v / %q, want ALLOW", dec.Decision, dec.SubReason)
	}
	if dec.SubReason == "session_tainted_prompt_injection" {
		t.Fatalf("unexpected taint deny")
	}
}

func TestMatchesDestructiveMutationArgs(t *testing.T) {
	t.Parallel()
	oversize := "mutation{delete_item(item_id:0){id}}" + strings.Repeat("x", maxArgMutationScanBytes+1)
	cases := []struct {
		name      string
		args      map[string]any
		wantField string
		wantOK    bool
	}{
		{name: "nil_args", args: nil, wantOK: false},
		{name: "non_string_query", args: map[string]any{"query": 123}, wantOK: false},
		{name: "query_without_mutation", args: map[string]any{"query": "query{items{id}}"}, wantOK: false},
		{name: "delete_item", args: map[string]any{"query": "mutation{delete_item(item_id:0){id}}"}, wantField: "delete_item", wantOK: true},
		{name: "aliased_delete_item", args: map[string]any{"query": "mutation{d:delete_item(item_id:0){id}}"}, wantField: "delete_item", wantOK: true},
		{name: "uppercase_delete_item", args: map[string]any{"query": "MUTATION{DELETE_ITEM(item_id:0){id}}"}, wantField: "delete_item", wantOK: true},
		{name: "oversize_delete_item", args: map[string]any{"query": oversize}, wantField: "delete_item", wantOK: true},
		{name: "archive_item", args: map[string]any{"query": "mutation{archive_item(item_id:0){id}}"}, wantField: "archive_item", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field, ok := matchesDestructiveMutationArgs(tc.args, defaultDestructiveMutationArgKeys, defaultDestructiveMutationFieldGlobs)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (field=%q)", ok, tc.wantOK, field)
			}
			if field != tc.wantField {
				t.Fatalf("field = %q, want %q", field, tc.wantField)
			}
		})
	}
}
