package model

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeAgentName(t *testing.T) {
	long := strings.Repeat("a", MaxAgentNameLen+50)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   \t\n ", ""},
		{"trims surrounding whitespace", "  Billing Bot  ", "Billing Bot"},
		{"preserves unicode em-dash", "Claude Code — Billing", "Claude Code — Billing"},
		{"collapses internal whitespace runs", "Claude    Code  Billing", "Claude Code Billing"},
		{"newline and tab become single space", "Claude\n\tCode", "Claude Code"},
		{"drops non-space control chars", "Bad\x00\x1bName", "BadName"},
		{"truncates to MaxAgentNameLen", long, strings.Repeat("a", MaxAgentNameLen)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeAgentName(tc.in)
			if got != tc.want {
				t.Fatalf("SanitizeAgentName(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if n := utf8.RuneCountInString(got); n > MaxAgentNameLen {
				t.Fatalf("result exceeds MaxAgentNameLen (%d): %d runes", MaxAgentNameLen, n)
			}
		})
	}
}
