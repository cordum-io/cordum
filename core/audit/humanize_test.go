package audit

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// secretCanary is a fake secret used to prove humanization never leaks
// secret-shaped content into summaries/labels.
const secretCanary = "sk-test-DEADBEEFdeadbeef0123456789ABCDEF"

func TestHumanSummary_RepresentativeRows(t *testing.T) {
	cases := []struct {
		name        string
		ev          SIEMEvent
		mustContain []string // case-insensitive substrings
		mustReject  []string
	}{
		{
			name: "policy decision deny",
			ev: SIEMEvent{
				EventType: EventSafetyDecision, Action: "job.submit", Decision: "deny",
				MatchedRule: "no-prod-writes", Reason: "destructive write to prod",
				AgentID: "agent-7", AgentName: "Billing Bot",
			},
			mustContain: []string{"Billing Bot", "deny", "no-prod-writes", "destructive write to prod"},
		},
		{
			name: "approval resolved",
			ev: SIEMEvent{
				EventType: EventSafetyApproval, Action: "approval.resolve", Decision: "approve",
				Identity: "user:alice", Extra: map[string]string{"approval_ref": "appr-42", "outcome": "approved"},
			},
			mustContain: []string{"alice", "approv"},
		},
		{
			name: "edge action denied",
			ev: SIEMEvent{
				EventType: EventEdgeActionDenied, Action: "edge.action", Decision: "deny",
				AgentName: "Claude Code", Reason: "blocked path",
				Extra: map[string]string{"tool_name": "Bash", "action_target_summary": "rm -rf /etc", "command_family": "filesystem"},
			},
			mustContain: []string{"Claude Code", "Bash", "deni", "rm -rf /etc"},
		},
		{
			name: "edge approval requested",
			ev: SIEMEvent{
				EventType: EventEdgeApprovalRequested, Action: "edge.approval",
				AgentName: "Claude Code",
				Extra:     map[string]string{"tool_name": "Write", "action_target_summary": "/etc/hosts"},
			},
			mustContain: []string{"Claude Code", "approval", "Write"},
		},
		{
			name: "mcp tool invocation",
			ev: SIEMEvent{
				EventType: EventMCPToolInvocation, Action: "mcp.invoke", Decision: "allow",
				Identity: "agent:deploy-bot", Extra: map[string]string{"tool_name": "github.create_pr"},
			},
			mustContain: []string{"deploy-bot", "github.create_pr", "mcp"},
		},
		{
			name: "mcp tool denied",
			ev: SIEMEvent{
				EventType: EventMCPToolDenied, Action: "mcp.invoke", Decision: "deny",
				Identity: "agent:deploy-bot", Reason: "tool not allowlisted",
				Extra: map[string]string{"tool_name": "shell.exec"},
			},
			mustContain: []string{"shell.exec", "deni", "tool not allowlisted"},
		},
		{
			name: "worker auth event",
			ev: SIEMEvent{
				EventType: EventSystemAuth, Action: "auth.login", Decision: "allow",
				Identity: "user:ops", Reason: "api key valid",
			},
			mustContain: []string{"ops", "auth.login"},
		},
		{
			name: "minimal unknown row",
			ev: SIEMEvent{
				EventType: "some.future.event", Action: "did.something",
			},
			mustContain: []string{"did.something"},
		},
		{
			name: "secret-shaped extra keys never leak",
			ev: SIEMEvent{
				EventType: EventEdgeActionDenied, Action: "edge.action", Decision: "deny",
				AgentName: "Claude Code",
				Extra: map[string]string{
					"tool_name":     "Bash",
					"api_key":       secretCanary,                 // not allowlisted → never read
					"authorization": "Bearer " + secretCanary,     // not allowlisted
					"password":      "hunter2",                    // not allowlisted
				},
			},
			mustContain: []string{"Claude Code", "Bash"},
			mustReject:  []string{secretCanary, "hunter2", "Bearer", "api_key", "password"},
		},
		{
			name: "secret-shaped value in allowlisted key is redacted",
			ev: SIEMEvent{
				EventType: EventEdgeActionDenied, Action: "edge.action", Decision: "deny",
				// action_target_summary IS allowlisted, but a secret-looking value must be scrubbed.
				Extra: map[string]string{"action_target_summary": secretCanary},
			},
			mustReject: []string{secretCanary},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HumanSummary(tc.ev)
			if got == "" {
				t.Fatalf("HumanSummary returned empty for %s", tc.name)
			}
			if n := utf8.RuneCountInString(got); n > maxSummaryLen {
				t.Fatalf("summary exceeds bound %d: %d runes: %q", maxSummaryLen, n, got)
			}
			low := strings.ToLower(got)
			for _, want := range tc.mustContain {
				if !strings.Contains(low, strings.ToLower(want)) {
					t.Errorf("summary %q missing %q", got, want)
				}
			}
			for _, bad := range tc.mustReject {
				// Case-insensitive: a case-flipped leak ("BEARER xxx") must
				// still trip this gate. `low` is got lowercased above.
				if strings.Contains(low, strings.ToLower(bad)) {
					t.Errorf("summary %q leaked forbidden %q", got, bad)
				}
			}
			// Determinism: same input → same output.
			if again := HumanSummary(tc.ev); again != got {
				t.Errorf("non-deterministic: %q != %q", again, got)
			}
		})
	}
}

func TestBoundedPreview(t *testing.T) {
	// Secret-shaped previews collapse to a redaction marker.
	if got := BoundedPreview(secretCanary, 200); got != "[redacted]" {
		t.Errorf("secret preview = %q, want [redacted]", got)
	}
	if got := BoundedPreview("Bearer "+secretCanary, 200); got != "[redacted]" {
		t.Errorf("bearer preview = %q, want [redacted]", got)
	}
	// Ordinary, already-redacted content passes through (trimmed).
	if got := BoundedPreview("  command allowed: read file  ", 200); got != "command allowed: read file" {
		t.Errorf("normal preview = %q", got)
	}
	// Long content is rune-bounded.
	if got := BoundedPreview(strings.Repeat("a", 500), 64); utf8.RuneCountInString(got) > 64 {
		t.Errorf("preview not bounded: %d runes", utf8.RuneCountInString(got))
	}
	// ResourceLabel reads the Edge-emitted target_summary key too.
	rl := ResourceLabel(SIEMEvent{Extra: map[string]string{"target_summary": "shell:destructive/filesystem_delete"}})
	if !strings.Contains(rl, "shell:destructive") {
		t.Errorf("ResourceLabel(target_summary) = %q", rl)
	}
}

func TestActorAgentResourceLabels(t *testing.T) {
	ev := SIEMEvent{
		Identity: "user:alice", AgentID: "agent-7", AgentName: "Billing Bot",
		Extra: map[string]string{
			"principal_display_name": "Alice Ops",
			"resource_name":          "prod-db",
			"resource_type":          "database",
			"agent_product":          "claude-code",
		},
	}
	if got := ActorLabel(ev); got != "Alice Ops" {
		t.Errorf("ActorLabel = %q, want principal_display_name %q", got, "Alice Ops")
	}
	if got := AgentLabel(ev); got != "Billing Bot" {
		t.Errorf("AgentLabel = %q, want %q", got, "Billing Bot")
	}
	if got := ResourceLabel(ev); !strings.Contains(got, "prod-db") {
		t.Errorf("ResourceLabel = %q, want to contain %q", got, "prod-db")
	}

	// Fallbacks: no display name → identity; no agent name → product; empty → safe defaults.
	ev2 := SIEMEvent{Identity: "user:bob", AgentID: "agent-9", Extra: map[string]string{"agent_product": "claude-code"}}
	if got := ActorLabel(ev2); !strings.Contains(got, "bob") {
		t.Errorf("ActorLabel fallback = %q, want to contain identity", got)
	}
	if got := AgentLabel(ev2); got != "claude-code" {
		t.Errorf("AgentLabel fallback = %q, want product %q", got, "claude-code")
	}

	empty := SIEMEvent{}
	if got := ActorLabel(empty); got == "" {
		t.Error("ActorLabel must return a non-empty safe default for an empty event")
	}
}

func TestPivots_OnlyAllowlistedExtra(t *testing.T) {
	ev := SIEMEvent{
		JobID: "job-1",
		Extra: map[string]string{
			"session_id":   "sess-2",
			"execution_id": "exec-3",
			"resource_id":  "res-4",
			"api_key":      secretCanary, // not a pivot, never surfaced
		},
	}
	p := Pivots(ev)
	if p.JobID != "job-1" || p.SessionID != "sess-2" || p.ExecutionID != "exec-3" || p.ResourceID != "res-4" {
		t.Fatalf("Pivots = %+v", p)
	}
}

func TestAllowedExtra_RejectsNonAllowlistedKeys(t *testing.T) {
	ev := SIEMEvent{Extra: map[string]string{"api_key": secretCanary, "session_id": "ok"}}
	if got := allowedExtra(ev, "api_key"); got != "" {
		t.Errorf("allowedExtra(api_key) = %q, want empty (not allowlisted)", got)
	}
	if got := allowedExtra(ev, "session_id"); got != "ok" {
		t.Errorf("allowedExtra(session_id) = %q, want %q", got, "ok")
	}
}
