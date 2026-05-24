package gateway

import "testing"

// Edge session create accepts agent_name / principal_display_name only as
// human-facing display labels: the Gateway secret-redacts then collapses/bounds
// them. Principal identity is never derived from these (covered elsewhere).
func TestRedactEdgeSessionCreateRequestSanitizesDisplayLabels(t *testing.T) {
	out, err := redactEdgeSessionCreateRequest(edgeSessionCreateRequest{
		AgentName:            "  Claude  Code  ",
		PrincipalDisplayName: "Yaron\tT",
	})
	if err != nil {
		t.Fatalf("redactEdgeSessionCreateRequest: %v", err)
	}
	if out.AgentName != "Claude Code" {
		t.Fatalf("AgentName = %q, want sanitized %q", out.AgentName, "Claude Code")
	}
	if out.PrincipalDisplayName != "Yaron T" {
		t.Fatalf("PrincipalDisplayName = %q, want sanitized %q", out.PrincipalDisplayName, "Yaron T")
	}
}
