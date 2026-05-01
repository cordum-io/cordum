package edge

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestGenerateApprovalRefIsURLSafeRandomAndPrefixed(t *testing.T) {
	allowed := regexp.MustCompile(`^edge_appr_[A-Za-z0-9_-]+$`)
	seen := make(map[string]bool, 128)
	for i := 0; i < 128; i++ {
		ref, err := GenerateApprovalRef()
		if err != nil {
			t.Fatalf("GenerateApprovalRef: %v", err)
		}
		if !allowed.MatchString(ref) {
			t.Fatalf("approval ref %q is not URL-safe edge_appr_ prefixed", ref)
		}
		if strings.ContainsAny(ref, "/+=") {
			t.Fatalf("approval ref %q contains a non-URL-safe base64 character", ref)
		}
		if seen[ref] {
			t.Fatalf("GenerateApprovalRef produced duplicate ref %q", ref)
		}
		seen[ref] = true
	}
}

func TestEdgeApprovalValidateRequiresBindingFieldsAndTerminalState(t *testing.T) {
	started := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	base := validApprovalContract(started)

	validPending := base
	if err := validPending.Validate(); err != nil {
		t.Fatalf("pending approval Validate: %v", err)
	}

	for _, tc := range []struct {
		name    string
		mutate  func(*EdgeApproval)
		wantErr string
	}{
		{name: "action hash", mutate: func(a *EdgeApproval) { a.ActionHash = "" }, wantErr: "action_hash"},
		{name: "policy snapshot", mutate: func(a *EdgeApproval) { a.PolicySnapshot = "" }, wantErr: "policy_snapshot"},
		{name: "requester", mutate: func(a *EdgeApproval) { a.Requester = "" }, wantErr: "requester"},
		{name: "principal", mutate: func(a *EdgeApproval) { a.PrincipalID = "" }, wantErr: "principal_id"},
		{name: "pending decision", mutate: func(a *EdgeApproval) { a.Decision = ApprovalDecisionApprove }, wantErr: "decision"},
		{name: "pending resolver", mutate: func(a *EdgeApproval) { a.ResolverID = "resolver-a" }, wantErr: "resolver_id"},
		{name: "consumed pending", mutate: func(a *EdgeApproval) { consumed := started.Add(time.Minute); a.ConsumedAt = &consumed }, wantErr: "consumed_at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := base
			tc.mutate(&next)
			err := next.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate error = %v, want field %q", err, tc.wantErr)
			}
		})
	}

	terminal := base
	resolvedAt := started.Add(2 * time.Minute)
	consumedAt := started.Add(3 * time.Minute)
	terminal.Status = ApprovalStatusApproved
	terminal.Decision = ApprovalDecisionApprove
	terminal.ResolverID = "principal-reviewer"
	terminal.ResolvedBy = "reviewer@example.invalid"
	terminal.ResolutionReason = "approved for test"
	terminal.ResolvedAt = &resolvedAt
	terminal.ConsumedAt = &consumedAt
	if err := terminal.Validate(); err != nil {
		t.Fatalf("terminal consumed approval Validate: %v", err)
	}

	missingResolver := terminal
	missingResolver.ResolverID = ""
	if err := missingResolver.Validate(); err == nil || !strings.Contains(err.Error(), "resolver_id") {
		t.Fatalf("terminal missing resolver error = %v, want resolver_id", err)
	}
}

func validApprovalContract(started time.Time) EdgeApproval {
	expires := started.Add(5 * time.Minute)
	return EdgeApproval{
		ApprovalRef:    "edge_appr_testContract",
		TenantID:       "tenant-a",
		SessionID:      "session-a",
		ExecutionID:    "execution-a",
		EventID:        "event-a",
		PrincipalID:    "principal-a",
		Requester:      "principal-a",
		Status:         ApprovalStatusPending,
		Reason:         "destructive action requires approval",
		RuleID:         "claude-code.require-approval-for-edits",
		PolicySnapshot: "policy-v1",
		ActionHash:     "actionhash-a",
		InputHash:      "sha256:event-a",
		CreatedAt:      started.UTC(),
		ExpiresAt:      &expires,
		Labels:         Labels{"env": "test"},
		Metadata:       Metadata{"source": "unit"},
	}
}
