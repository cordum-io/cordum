package claude

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupportsHookOnFailure(t *testing.T) {
	for version, want := range map[string]bool{
		"2.1.295": true, "2.1.296": true, "2.2.0": true, "3.0.0": true, "10.0.0": true,
		"2.1.294": false, "2.0.999": false, "1.99.999": false, "": false,
	} {
		if got := SupportsHookOnFailure(version); got != want {
			t.Errorf("SupportsHookOnFailure(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestClaudeCodeVersionReadsFirstSemver(t *testing.T) {
	env := envWith("CORDUM_TEST_HELPER_PROCESS", "1", "CORDUM_TEST_CLAUDE_VERSION", "v2.1.300 (Claude Code)")
	got, err := ClaudeCodeVersion(context.Background(), os.Args[0], env)
	if err != nil || got != "2.1.300" {
		t.Fatalf("ClaudeCodeVersion = %q, %v; want 2.1.300", got, err)
	}
	env = envWith("CORDUM_TEST_HELPER_PROCESS", "1", "CORDUM_TEST_CLAUDE_VERSION", "Claude Code nightly")
	if got, err := ClaudeCodeVersion(context.Background(), os.Args[0], env); err == nil {
		t.Fatalf("ClaudeCodeVersion = %q, want error for output without X.Y.Z", got)
	}
}

func TestPreflightClaudeHookOnFailure(t *testing.T) {
	cases := []struct {
		mode, version     string
		wantErr, wantWarn string
	}{
		{mode: "observe", version: "2.1.200 (Claude Code)"},
		{mode: "enforce", version: "2.1.295 (Claude Code)"},
		{mode: "enterprise-strict", version: "2.1.300 (Claude Code)"},
		{mode: "enforce", version: "2.1.200 (Claude Code)", wantWarn: "Claude Code 2.1.200 is older than 2.1.295"},
		{mode: "local-dev-enforce", version: "2.1.200 (Claude Code)", wantWarn: "fails open on this version"},
		{mode: "enterprise-strict", version: "2.1.200 (Claude Code)", wantErr: "enterprise-strict refuses to launch it"},
		{mode: "enforce", version: "unknown", wantWarn: "could not determine the Claude Code version"},
		{mode: "enterprise-strict", version: "unknown", wantWarn: "could not determine the Claude Code version"},
	}
	for _, tc := range cases {
		t.Run(tc.mode+"/"+tc.version, func(t *testing.T) {
			var stderr bytes.Buffer
			env := envWith("CORDUM_TEST_HELPER_PROCESS", "1", "CORDUM_TEST_CLAUDE_VERSION", tc.version)
			err := preflightClaudeHookOnFailure(context.Background(), os.Args[0], tc.mode, env, &stderr)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			if tc.wantWarn == "" && stderr.Len() > 0 {
				t.Fatalf("unexpected stderr: %s", stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.wantWarn) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.wantWarn)
			}
		})
	}
}

func TestLaunchEdgeClaudeEnterpriseStrictRefusesOldClaudeCode(t *testing.T) {
	helperPath := os.Args[0]
	capture := filepath.Join(t.TempDir(), "agentd-env.json")
	_, err := LaunchEdgeClaude(context.Background(), LaunchOptions{
		Env: envWith("CORDUM_TEST_HELPER_PROCESS", "1", "CORDUM_TEST_AGENTD_ENV_PATH", capture,
			"CORDUM_TEST_CLAUDE_VERSION", "2.1.200 (Claude Code)"),
		Gateway: "http://gateway.local", APIKey: "secret-token", TenantID: "tenant-a",
		PrincipalID: "user-a", CWD: t.TempDir(), AgentdPath: helperPath, HookCommand: helperPath,
		ClaudePath: helperPath, PolicyMode: "enterprise-strict",
	})
	if err == nil || !strings.Contains(err.Error(), "older than "+MinHookOnFailureVersion) {
		t.Fatalf("error = %v, want refusal naming the minimum version", err)
	}
	if _, statErr := os.Stat(capture); !os.IsNotExist(statErr) {
		t.Fatalf("agentd should not start when Claude Code is too old, stat err=%v", statErr)
	}
}
