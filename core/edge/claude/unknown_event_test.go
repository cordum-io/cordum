package claude

import (
	"strings"
	"testing"
)

func TestRunUnknownEventFallbackByMode(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		wantCode   int
		wantStdout string
	}{
		{name: "default", env: nil, wantCode: 0, wantStdout: ""},
		{name: "observe", env: map[string]string{"CORDUM_EDGE_MODE": "observe"}, wantCode: 0, wantStdout: ""},
		{name: "local_dev_enforce", env: map[string]string{"CORDUM_EDGE_MODE": "local-dev-enforce"}, wantCode: 0, wantStdout: ""},
		{name: "enterprise_strict", env: map[string]string{"CORDUM_EDGE_MODE": "enterprise-strict"}, wantCode: 2, wantStdout: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runHook(t, RunOptions{
				Args:  []string{"claude", "pre-tool-use"},
				Stdin: hookInput(`{"hook_event_name":"SessionStart","session_id":"sess-sk-test-secret","prompt":"ghp_testtoken"}`),
				Env:   tt.env,
			})
			if code != tt.wantCode {
				t.Fatalf("exit code=%d want %d stderr=%q", code, tt.wantCode, stderr)
			}
			if stdout != tt.wantStdout {
				t.Fatalf("stdout=%q want %q", stdout, tt.wantStdout)
			}
			if !strings.Contains(stderr, "unsupported_hook_event") {
				t.Fatalf("stderr missing unsupported warning: %q", stderr)
			}
			assertNoSyntheticSecrets(t, stderr)
		})
	}
}
