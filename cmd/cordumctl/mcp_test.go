package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/cordum/cordum/sdk/client"
)

// TestRenderMCPToolList_Empty pins the "no tools visible" message so
// operator scripts can grep for it.
func TestRenderMCPToolList_Empty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	renderMCPToolList(&buf, &sdk.MCPToolList{Tools: nil, Filtered: true, AgentID: "agent-xyz"})
	if !strings.Contains(buf.String(), "No MCP tools visible") {
		t.Errorf("missing empty-state line:\n%s", buf.String())
	}
}

// TestRenderMCPToolList_Rows verifies tabular output emits each tool
// with its risk tier, classifications, and approval marker.
func TestRenderMCPToolList_Rows(t *testing.T) {
	t.Parallel()
	list := &sdk.MCPToolList{
		Filtered: true,
		AgentID:  "agent-xyz",
		Tools: []sdk.MCPToolInfo{
			{Name: "fs.read", RiskTier: "low"},
			{Name: "pii.export", RiskTier: "high", DataClassifications: []string{"pii"}, RequiresApproval: true},
		},
	}
	var buf bytes.Buffer
	renderMCPToolList(&buf, list)
	out := buf.String()

	for _, want := range []string{"fs.read", "pii.export", "low", "high", "pii", "required", "Filtered for agent agent-xyz", "2 tool(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

// TestFilterMCPToolList_SubstringMatch verifies the --filter flag
// behaviour — substring match is case-insensitive and scans both
// name and description.
func TestFilterMCPToolList_SubstringMatch(t *testing.T) {
	t.Parallel()
	list := &sdk.MCPToolList{
		Filtered: false,
		Tools: []sdk.MCPToolInfo{
			{Name: "cordum_list_jobs", Description: "List jobs..."},
			{Name: "cordum_get_job", Description: "Fetch job..."},
			{Name: "cordum_status", Description: "Platform health..."},
		},
	}
	got := filterMCPToolList(list, "job")
	if len(got.Tools) != 2 {
		t.Fatalf("expected 2 job-matching tools, got %d", len(got.Tools))
	}
	got = filterMCPToolList(list, "HEALTH")
	if len(got.Tools) != 1 || got.Tools[0].Name != "cordum_status" {
		t.Errorf("case-insensitive description match failed: %+v", got.Tools)
	}
	got = filterMCPToolList(list, "nonexistent")
	if len(got.Tools) != 0 {
		t.Errorf("no-match should yield empty list, got %d", len(got.Tools))
	}
}

func TestFilterMCPToolList_NilSafe(t *testing.T) {
	t.Parallel()
	if got := filterMCPToolList(nil, "anything"); got != nil {
		t.Errorf("nil list should stay nil, got %+v", got)
	}
}

// TestRenderMCPToolList_UnfilteredHeader pins the admin-catalogue
// header text when no agent id is supplied.
func TestRenderMCPToolList_UnfilteredHeader(t *testing.T) {
	t.Parallel()
	list := &sdk.MCPToolList{
		Filtered: false,
		Tools:    []sdk.MCPToolInfo{{Name: "fs.read", RiskTier: "low"}},
	}
	var buf bytes.Buffer
	renderMCPToolList(&buf, list)
	if !strings.Contains(buf.String(), "Full registry") {
		t.Errorf("expected Full registry header:\n%s", buf.String())
	}
}

// TestRenderMCPApprovalListHandlesEmpty pins the user-facing message
// for an empty list — CI/automation greps for the "No MCP approvals"
// string.
func TestRenderMCPApprovalListHandlesEmpty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	renderMCPApprovalList(&buf, nil)
	if !strings.Contains(buf.String(), "No MCP approvals") {
		t.Errorf("missing empty-state line:\n%s", buf.String())
	}
}

// TestRenderMCPApprovalListShowsAllRows verifies tabular output emits
// one body row per record + the expected header columns.
func TestRenderMCPApprovalListShowsAllRows(t *testing.T) {
	t.Parallel()
	items := []sdk.MCPApproval{
		{ID: "app-1234567890ab", ToolName: "files.delete", AgentID: "agent-1", Status: "pending", ArgsHash: "deadbeef0123"},
		{ID: "app-2222", ToolName: "very.long.tool.name.that.will.truncate", AgentID: "agent-2", Status: "approved", ArgsHash: "1234abcd"},
	}
	var buf bytes.Buffer
	renderMCPApprovalList(&buf, items)
	out := buf.String()

	for _, want := range []string{
		"Approval ID", "Tool", "Agent", "Status", "Args hash",
		"app-1234567890ab",
		"agent-1",
		"pending",
		"approved",
		"deadbeef",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

// TestTruncateASCIIBoundaries pins behaviour at the edges so future
// edits don't regress the column-width contract the table relies on.
func TestTruncateASCIIBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactlyten", 10, "exactlyten"},
		{"more-than-ten", 10, "more-th..."},
		{"abc", 3, "abc"},
		{"abcdef", 3, "abc"},
		{"", 5, ""},
	}
	for _, tc := range cases {
		if got := truncateASCII(tc.in, tc.n); got != tc.want {
			t.Errorf("truncateASCII(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

// TestShortHexClipsToPrefix is an obvious unit test but pins the
// args-hash column rendering that operators use to disambiguate.
func TestShortHexClipsToPrefix(t *testing.T) {
	t.Parallel()
	if got := shortHex("deadbeef0123", 8); got != "deadbeef" {
		t.Errorf("shortHex prefix = %q", got)
	}
	if got := shortHex("abc", 8); got != "abc" {
		t.Errorf("shortHex short = %q", got)
	}
}

// TestErrorClassifiers verifies the SDK-error sniffers used by
// runMCPResolve to translate server responses into clean exit codes.
func TestErrorClassifiers(t *testing.T) {
	t.Parallel()
	if !isMCPSelfApproval(errors.New("api: 403 self_approval_denied — body=...")) {
		t.Error("isMCPSelfApproval should match self_approval_denied substring")
	}
	if isMCPSelfApproval(errors.New("api: 500 internal")) {
		t.Error("isMCPSelfApproval matched a generic error")
	}
	if !isMCPNotFound(errors.New("api: 404 approval_not_found")) {
		t.Error("isMCPNotFound should match approval_not_found")
	}
	if !isMCPNotFound(fmt.Errorf("wrapped: %w", errors.New("no such mcp approval"))) {
		t.Error("isMCPNotFound should match the human-readable variant")
	}
	if isMCPNotFound(nil) {
		t.Error("isMCPNotFound(nil) must be false")
	}
}
