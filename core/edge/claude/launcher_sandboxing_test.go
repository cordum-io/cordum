package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchEdgeClaudeRejectsTraversalStateDirBeforeCreate(t *testing.T) {
	helperPath := os.Args[0]
	_, err := LaunchEdgeClaude(context.Background(), LaunchOptions{
		Env:     envWith("CORDUM_TEST_HELPER_PROCESS", "1"),
		Gateway: "http://gateway.local", APIKey: "secret-token", TenantID: "tenant-a",
		PrincipalID: "user-a", CWD: t.TempDir(), AgentdPath: helperPath,
		HookCommand: helperPath, ClaudePath: helperPath, DryRun: true,
		StateDir: ".." + string(filepath.Separator) + "escape",
	})
	if err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("error=%v, want traversal rejection", err)
	}
}

func TestLaunchEdgeClaudeRejectsClaudePathArgOutsideAllowedPrefixes(t *testing.T) {
	helperPath := os.Args[0]
	cwd := t.TempDir()
	capture := filepath.Join(t.TempDir(), "claude.json")
	outside := filepath.Join(filepath.Dir(cwd), "outside.txt")
	_, err := LaunchEdgeClaude(context.Background(), LaunchOptions{
		Env:     envWith("CORDUM_TEST_HELPER_PROCESS", "1", "CORDUM_TEST_CLAUDE_CAPTURE", capture),
		Gateway: "http://gateway.local", APIKey: "secret-token", TenantID: "tenant-a",
		PrincipalID: "user-a", CWD: cwd, AgentdPath: helperPath, HookCommand: helperPath,
		ClaudePath: helperPath, PolicyMode: "enforce", ClaudeArgs: []string{outside},
	})
	if err == nil || !strings.Contains(err.Error(), "argv path outside allowed prefixes") {
		t.Fatalf("error=%v, want argv path prefix rejection", err)
	}
	if _, statErr := os.Stat(capture); !os.IsNotExist(statErr) {
		t.Fatalf("claude helper should not run after argv path rejection, stat err=%v", statErr)
	}
}
