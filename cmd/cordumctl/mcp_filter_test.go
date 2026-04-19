package main

import (
	"testing"
	"time"

	sdk "github.com/cordum/cordum/sdk/client"
)

func TestIsMutatingMCPTool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		// Mutating family (task-2d989055).
		{"cordum_create_workflow", true},
		{"cordum_install_pack", true},
		{"cordum_uninstall_pack", true},
		{"cordum_register_agent", true},
		{"cordum_update_policy_bundle", true},
		{"cordum_revoke_worker_session", true},
		{"cordum_set_agent_scope", true},
		// Read-only family (task-466b6a6a) — NOT mutating.
		{"cordum_list_jobs", false},
		{"cordum_get_job", false},
		{"cordum_audit_verify", false},
		// Original action family — NOT mutating in the "creates
		// persistent state" sense tracked here.
		{"cordum_submit_job", false},
		{"cordum_approve_job", false},
		// Unknown / misspelled → NOT mutating (fail-closed for the
		// bulk-review filter — operator gets back a smaller list
		// rather than a mistakenly-broad one).
		{"cordum_unknown_tool", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMutatingMCPTool(tc.name); got != tc.want {
				t.Errorf("isMutatingMCPTool(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestParseSinceTimestamp(t *testing.T) {
	t.Parallel()
	// Unix-ms integer.
	if v, err := parseSinceTimestamp("1700000000000"); err != nil || v != 1700000000000 {
		t.Errorf("unix-ms parse: got (%d, %v), want (1700000000000, nil)", v, err)
	}
	// RFC3339 string.
	want := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC).UnixMilli()
	if v, err := parseSinceTimestamp("2026-04-19T12:00:00Z"); err != nil || v != want {
		t.Errorf("rfc3339 parse: got (%d, %v), want (%d, nil)", v, err, want)
	}
	// Empty.
	if v, err := parseSinceTimestamp(""); err != nil || v != 0 {
		t.Errorf("empty parse: got (%d, %v)", v, err)
	}
	// Garbage.
	if _, err := parseSinceTimestamp("not-a-time"); err == nil {
		t.Errorf("garbage parse should error")
	}
}

func TestFilterMCPPending_NoFilters_PassThrough(t *testing.T) {
	t.Parallel()
	items := []sdk.MCPApproval{
		{ID: "a", ToolName: "cordum_list_jobs"},
		{ID: "b", ToolName: "cordum_install_pack"},
	}
	got := filterMCPPending(items, filterMCPPendingOpts{})
	if len(got) != 2 {
		t.Fatalf("pass-through failed: %d", len(got))
	}
}

func TestFilterMCPPending_MutatingOnly(t *testing.T) {
	t.Parallel()
	items := []sdk.MCPApproval{
		{ID: "a", ToolName: "cordum_list_jobs"},
		{ID: "b", ToolName: "cordum_install_pack"},
		{ID: "c", ToolName: "cordum_get_job"},
		{ID: "d", ToolName: "cordum_update_policy_bundle"},
	}
	got := filterMCPPending(items, filterMCPPendingOpts{mutatingOnly: true})
	if len(got) != 2 {
		t.Fatalf("mutating-only: got %d, want 2", len(got))
	}
	for _, it := range got {
		if !isMutatingMCPTool(it.ToolName) {
			t.Errorf("non-mutating tool leaked through: %s", it.ToolName)
		}
	}
}

func TestFilterMCPPending_ToolName(t *testing.T) {
	t.Parallel()
	items := []sdk.MCPApproval{
		{ID: "a", ToolName: "cordum_install_pack"},
		{ID: "b", ToolName: "cordum_uninstall_pack"},
		{ID: "c", ToolName: "cordum_install_pack"},
	}
	got := filterMCPPending(items, filterMCPPendingOpts{toolName: "cordum_install_pack"})
	if len(got) != 2 {
		t.Fatalf("tool-name filter: got %d, want 2", len(got))
	}
}

func TestFilterMCPPending_Since(t *testing.T) {
	t.Parallel()
	// Gateway records use microseconds (UnixMicro). The filter
	// converts its --since (ms) to compare against /1000.
	oldMicro := time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC).UnixMicro()
	newMicro := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC).UnixMicro()
	items := []sdk.MCPApproval{
		{ID: "old", ToolName: "cordum_install_pack", CreatedAt: oldMicro},
		{ID: "new", ToolName: "cordum_install_pack", CreatedAt: newMicro},
	}
	got := filterMCPPending(items, filterMCPPendingOpts{
		since: "2026-04-19T00:00:00Z",
	})
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("since filter: got %+v, want [new]", got)
	}
}

func TestFilterMCPPending_CombinedFilters(t *testing.T) {
	t.Parallel()
	now := time.Now().UnixMicro()
	items := []sdk.MCPApproval{
		// matches all three.
		{ID: "match", ToolName: "cordum_install_pack", CreatedAt: now},
		// wrong tool.
		{ID: "wrong-tool", ToolName: "cordum_list_jobs", CreatedAt: now},
		// too old.
		{ID: "too-old", ToolName: "cordum_install_pack", CreatedAt: now - int64(48*time.Hour/time.Microsecond)},
	}
	got := filterMCPPending(items, filterMCPPendingOpts{
		mutatingOnly: true,
		toolName:     "cordum_install_pack",
		since:        time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	})
	if len(got) != 1 || got[0].ID != "match" {
		t.Fatalf("combined: got %+v, want [match]", got)
	}
}
