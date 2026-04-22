package model

import (
	"testing"
	"time"
)

func TestDecisionQueryNormalize(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		query   DecisionQuery
		check   func(t *testing.T, got DecisionQuery)
		wantErr string
	}{
		{
			name:  "defaults last day and limit",
			query: DecisionQuery{Tenant: "tenant-a"},
			check: func(t *testing.T, got DecisionQuery) {
				t.Helper()
				if got.Limit != DefaultDecisionQueryLimit {
					t.Fatalf("Limit=%d want %d", got.Limit, DefaultDecisionQueryLimit)
				}
				if got.Until != now.UnixMilli() {
					t.Fatalf("Until=%d want %d", got.Until, now.UnixMilli())
				}
				if got.Since != now.Add(-24*time.Hour).UnixMilli() {
					t.Fatalf("Since=%d want %d", got.Since, now.Add(-24*time.Hour).UnixMilli())
				}
			},
		},
		{
			name: "fills missing until with now",
			query: DecisionQuery{
				Tenant: "tenant-a",
				Since:  now.Add(-2 * time.Hour).UnixMilli(),
				Limit:  25,
			},
			check: func(t *testing.T, got DecisionQuery) {
				t.Helper()
				if got.Until != now.UnixMilli() {
					t.Fatalf("Until=%d want %d", got.Until, now.UnixMilli())
				}
				if got.Since != now.Add(-2*time.Hour).UnixMilli() {
					t.Fatalf("Since=%d want %d", got.Since, now.Add(-2*time.Hour).UnixMilli())
				}
				if got.Limit != 25 {
					t.Fatalf("Limit=%d want 25", got.Limit)
				}
			},
		},
		{
			name: "fills missing since from until",
			query: DecisionQuery{
				Tenant: "tenant-a",
				Until:  now.Add(-3 * time.Hour).UnixMilli(),
			},
			check: func(t *testing.T, got DecisionQuery) {
				t.Helper()
				wantSince := now.Add(-27 * time.Hour).UnixMilli()
				if got.Since != wantSince {
					t.Fatalf("Since=%d want %d", got.Since, wantSince)
				}
			},
		},
		{
			name: "accepts supported verdict",
			query: DecisionQuery{
				Tenant:  "tenant-a",
				Verdict: SafetyAllowWithConstraints,
			},
			check: func(t *testing.T, got DecisionQuery) {
				t.Helper()
				if got.Verdict != SafetyAllowWithConstraints {
					t.Fatalf("Verdict=%q want %q", got.Verdict, SafetyAllowWithConstraints)
				}
			},
		},
		{
			name: "accepts valid cursor",
			query: DecisionQuery{
				Tenant: "tenant-a",
				Cursor: EncodeDecisionCursor(now.UnixMilli(), "job-1"),
			},
			check: func(t *testing.T, got DecisionQuery) {
				t.Helper()
				if got.Cursor == "" {
					t.Fatal("Cursor unexpectedly empty")
				}
			},
		},
		{
			name:    "requires tenant",
			query:   DecisionQuery{},
			wantErr: "tenant is required",
		},
		{
			name: "rejects until before since",
			query: DecisionQuery{
				Tenant: "tenant-a",
				Since:  now.UnixMilli(),
				Until:  now.Add(-time.Minute).UnixMilli(),
			},
			wantErr: "until must be >= since",
		},
		{
			name: "rejects limit above max",
			query: DecisionQuery{
				Tenant: "tenant-a",
				Limit:  MaxDecisionQueryLimit + 1,
			},
			wantErr: "limit must be <=",
		},
		{
			name: "rejects unknown verdict",
			query: DecisionQuery{
				Tenant:  "tenant-a",
				Verdict: SafetyDecision("maybe"),
			},
			wantErr: "unknown decision verdict",
		},
		{
			name: "rejects bad cursor",
			query: DecisionQuery{
				Tenant: "tenant-a",
				Cursor: "not-base64",
			},
			wantErr: "decode decision cursor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.query.Normalize(now)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !contains(err.Error(), tt.wantErr) {
					t.Fatalf("error=%q want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestParseDecisionLogVerdict(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    SafetyDecision
		wantErr string
	}{
		{name: "empty", input: "", want: ""},
		{name: "allow", input: "allow", want: SafetyAllow},
		{name: "deny uppercase", input: "DENY", want: SafetyDeny},
		{name: "constrain", input: "constrain", want: SafetyAllowWithConstraints},
		{name: "require approval", input: "require_approval", want: SafetyRequireApproval},
		{name: "throttle", input: "throttle", want: SafetyThrottle},
		{name: "reject unknown", input: "shadowban", wantErr: "unknown decision verdict"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDecisionLogVerdict(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !contains(err.Error(), tt.wantErr) {
					t.Fatalf("error=%q want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDecisionLogVerdict() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseDecisionLogVerdict()=%q want %q", got, tt.want)
			}
		})
	}
}

func TestDecisionCursorRoundTrip(t *testing.T) {
	timestamp := int64(1713614400000)
	cursor := EncodeDecisionCursor(timestamp, "decision-1")
	gotTS, gotID, err := DecodeDecisionCursor(cursor)
	if err != nil {
		t.Fatalf("DecodeDecisionCursor() error = %v", err)
	}
	if gotTS != timestamp {
		t.Fatalf("timestamp=%d want %d", gotTS, timestamp)
	}
	if gotID != "decision-1" {
		t.Fatalf("id=%q want decision-1", gotID)
	}
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
