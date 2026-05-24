//go:build windows

package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaunchEdgeClaudeWindowsUsesLegacyCloseThenBind verifies the Windows agentd
// bind fix. supportsAgentdListenerInheritance() returns false on Windows (see
// listener_inheritance_windows.go), so prepareLaunchConfig reserves the loopback
// port with reserveLoopbackHookURLLegacy (net.Listen -> Close), hands cordum-agentd
// only the URL, and never sets CORDUM_AGENTD_LISTENER_HANDLE/_FD. agentd then binds
// the freed port itself and becomes ready. Pre-fix, this path inherited the held
// socket handle and agentd failed with "Only one usage of each socket address"
// before becoming ready.
//
// Tradeoff note: the close-then-bind path does NOT hold the port across the
// reserve->bind window, so — unlike the held-listener handoff — it cannot block a
// same-user loopback hijack of that port. That is the deliberate, documented
// Option-A tradeoff (identical to the Unix legacy fallback). There is no held port
// to assert a hijack-block against, so the prior held-handle hijack assertion is
// intentionally gone.
func TestLaunchEdgeClaudeWindowsUsesLegacyCloseThenBind(t *testing.T) {
	helperPath := os.Args[0]
	capture := filepath.Join(t.TempDir(), "agentd-env.json")

	result, err := LaunchEdgeClaude(context.Background(), LaunchOptions{
		Env: envWith(
			"CORDUM_TEST_HELPER_PROCESS", "1",
			"CORDUM_TEST_AGENTD_ENV_PATH", capture,
		),
		Gateway: "http://gateway.local", APIKey: "secret-token", TenantID: "tenant-a",
		PrincipalID: "user-a", CWD: t.TempDir(), AgentdPath: helperPath, HookCommand: helperPath,
		ClaudePath: helperPath, PolicyMode: "enforce", DryRun: true,
	})
	if err != nil {
		t.Fatalf("LaunchEdgeClaude returned error: %v", err)
	}

	// agentd bound the freed loopback port fresh, became ready, and the edge
	// session was created (the helper writes session state only after it binds).
	if result.SessionID != "sess-launch" || result.ExecutionID != "exec-launch" {
		t.Fatalf("expected edge session created via fresh agentd bind, got %#v", result)
	}

	env := readJSONMap(t, capture)
	if got := strings.TrimSpace(env["CORDUM_AGENTD_LISTENER_HANDLE"]); got != "" {
		t.Fatalf("Windows legacy path must not hand off a listener handle, got CORDUM_AGENTD_LISTENER_HANDLE=%q in %#v", got, env)
	}
	if got := strings.TrimSpace(env["CORDUM_AGENTD_LISTENER_FD"]); got != "" {
		t.Fatalf("Windows legacy path must not expose Unix fd handoff, got CORDUM_AGENTD_LISTENER_FD=%q in %#v", got, env)
	}
	if strings.Contains(string(result.SettingsJSON), "CORDUM_AGENTD_LISTENER_") {
		t.Fatalf("settings leaked internal listener handoff env: %s", result.SettingsJSON)
	}
}
