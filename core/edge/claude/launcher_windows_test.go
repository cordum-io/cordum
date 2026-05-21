//go:build windows

package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchEdgeClaudeWindowsInheritedListenerUsesHandleAndBlocksHijack(t *testing.T) {
	helperPath := os.Args[0]
	dir := t.TempDir()
	capture := filepath.Join(dir, "agentd-env.json")
	probe := filepath.Join(dir, "hijack-probe.json")

	result, err := LaunchEdgeClaude(context.Background(), LaunchOptions{
		Env: envWith(
			"CORDUM_TEST_HELPER_PROCESS", "1",
			"CORDUM_TEST_AGENTD_ENV_PATH", capture,
			"CORDUM_TEST_AGENTD_HIJACK_PROBE_PATH", probe,
		),
		Gateway: "http://gateway.local", APIKey: "secret-token", TenantID: "tenant-a",
		PrincipalID: "user-a", CWD: t.TempDir(), AgentdPath: helperPath, HookCommand: helperPath,
		ClaudePath: helperPath, PolicyMode: "enforce", DryRun: true,
	})
	if err != nil {
		t.Fatalf("LaunchEdgeClaude returned error: %v", err)
	}

	env := readJSONMap(t, capture)
	if got := strings.TrimSpace(env["CORDUM_AGENTD_LISTENER_HANDLE"]); got == "" {
		t.Fatalf("Windows launcher must pass inherited listener as CORDUM_AGENTD_LISTENER_HANDLE, env=%#v", env)
	}
	if got := strings.TrimSpace(env["CORDUM_AGENTD_LISTENER_FD"]); got != "" {
		t.Fatalf("Windows launcher must not expose Unix fd handoff env, got CORDUM_AGENTD_LISTENER_FD=%q in %#v", got, env)
	}
	if strings.Contains(string(result.SettingsJSON), "CORDUM_AGENTD_LISTENER_") {
		t.Fatalf("settings leaked internal listener handoff env: %s", result.SettingsJSON)
	}

	probeResult := readJSONMap(t, probe)
	if probeResult["bind"] == "succeeded" {
		t.Fatalf("same-user hijack bind succeeded while agentd inherited listener should hold %s: %#v", probeResult["host"], probeResult)
	}
	if strings.TrimSpace(probeResult["bind_error"]) == "" {
		t.Fatalf("hijack probe did not record the expected bind failure: %#v", probeResult)
	}
	for _, forbidden := range []string{env["CORDUM_AGENTD_NONCE"], env["CORDUM_API_KEY"], result.SessionID, result.ExecutionID} {
		if forbidden != "" && strings.Contains(strings.Join(mapValues(probeResult), "\n"), forbidden) {
			t.Fatalf("hijack probe leaked launcher payload or nonce %q in %#v", forbidden, probeResult)
		}
	}
}

func mapValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
