package shadow_test

import (
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge/shadow"
)

// TestFindingRedactionStripsSecretMarkers — DoD #4 'no raw secret collection'.
// Input config containing a Bearer/sk-/Anthropic-style key marker must NOT
// appear in the produced redacted-summary bytes.
func TestFindingRedactionStripsSecretMarkers(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"sk-prefix", `{"mcpServers":{"x":{"command":"foo","authToken":"sk-leaked-12345"}}}`},
		{"anthropic", `{"mcpServers":{"x":{"env":{"ANTHROPIC_API_KEY":"sk-ant-real-1234"}}}}`},
		{"bearer", `[mcp_servers.x]
command = "foo"
authorization = "Bearer leaked-bearer-token"`},
		{"openssh-key", `{"mcpServers":{"x":{"command":"-----BEGIN OPENSSH PRIVATE KEY-----"}}}`}, // no-secret-lint
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := shadow.RedactConfigSummary([]byte(tc.in))
			// Brutal: assert NO secret-shape substring leaks through.
			for _, leak := range []string{"sk-leaked", "sk-ant-real", "leaked-bearer", "OPENSSH PRIVATE KEY"} {
				if strings.Contains(out, leak) {
					t.Fatalf("redacted output contains leak %q: %q", leak, out)
				}
			}
			// And the summary itself is non-empty + bounded.
			if out == "" {
				t.Fatalf("redacted summary unexpectedly empty for %q", tc.in)
			}
			if len(out) > 2048 {
				t.Fatalf("redacted summary unexpectedly large (%d bytes); should be bounded", len(out))
			}
		})
	}
}

// TestRedactPathStripsHome — relative-only paths in findings; never the
// absolute developer home prefix.
func TestRedactPathStripsHome(t *testing.T) {
	cases := []struct {
		name, in, wantPrefix string
	}{
		{"unix-home", "/home/yaron/.claude/settings.json", "~/"},
		{"mac-home", "/Users/yaron/.claude/settings.json", "~/"},
		{"windows-home", `C:\Users\yaron\.claude\settings.json`, "~/"},
		{"relative", "tools/test-keys/x.asc", "tools/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shadow.RedactPath(tc.in)
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Fatalf("RedactPath(%q) = %q; want prefix %q", tc.in, got, tc.wantPrefix)
			}
			// Never emit a Windows drive letter component.
			if len(got) >= 2 && got[1] == ':' {
				t.Fatalf("RedactPath leaked drive letter: %q", got)
			}
		})
	}
}
