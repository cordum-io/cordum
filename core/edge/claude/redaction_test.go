package claude

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRedactDiagnosticMasksSyntheticSecretsByValue(t *testing.T) {
	input := strings.Join([]string{
		"neutral=sk-test-secret",
		"github=ghp_testtoken",
		"aws_access=AKIAIOSFODNN7EXAMPLE",
		"aws_secret=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"header=Authorization: Bearer sk-test-secret",
		`json={"password":"hunter2","note":"token ghp_testtoken inside neutral field"}`,
	}, " ")

	got := redactDiagnostic(input)
	assertNoSyntheticSecrets(t, got)
	for _, mustRedact := range []string{"hunter2", "Authorization: Bearer"} {
		if strings.Contains(got, mustRedact) {
			t.Fatalf("redaction missed %q in %q", mustRedact, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redacted diagnostic should contain marker, got %q", got)
	}
	if len(got) > 256 {
		t.Fatalf("diagnostic should be bounded, got len=%d text=%q", len(got), got)
	}
}

func TestRunRedactsSecretsFromPayloadEnvAndAgentdErrors(t *testing.T) {
	agentd := &fakeAgentdClient{fn: func(context.Context, AgentdRequest) (AgentdDecision, error) {
		return AgentdDecision{}, errors.New("upstream saw ghp_testtoken and Authorization: Bearer sk-test-secret")
	}}
	payload := `{
		"hook_event_name":"PreToolUse",
		"tool_name":"Bash",
		"tool_input":{
			"command":"echo sk-test-secret",
			"env":{"AWS_SECRET_ACCESS_KEY":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
			"metadata":{"neutral":"ghp_testtoken"}
		},
		"tool_response":{"stdout":"AKIAIOSFODNN7EXAMPLE","stderr":"password=hunter2"}
	}`

	_, stdout, stderr := runHook(t, RunOptions{
		Args:   []string{"claude", "pre-tool-use"},
		Stdin:  hookInput(payload),
		Agentd: agentd,
		Env: map[string]string{
			"CORDUM_AGENTD_FAIL_CLOSED": "true",
			"CORDUM_AGENTD_URL":         "http://127.0.0.1:7778/?token=sk-test-secret",
		},
	})

	assertNoSyntheticSecrets(t, stdout)
	assertNoSyntheticSecrets(t, stderr)
	for _, leaked := range []string{"hunter2", "Authorization: Bearer", "echo sk-test-secret"} {
		if strings.Contains(stdout, leaked) || strings.Contains(stderr, leaked) {
			t.Fatalf("leaked %q in stdout=%q stderr=%q", leaked, stdout, stderr)
		}
	}
}

func TestUnknownEventDiagnosticsRedactSecretsEvenWithoutSensitiveKeys(t *testing.T) {
	code, stdout, stderr := runHook(t, RunOptions{
		Args:  []string{"claude", "pre-tool-use"},
		Stdin: hookInput(`{"hook_event_name":"ConfigChange","session_id":"sess-ghp_testtoken","prompt":"sk-test-secret","details":{"note":"AKIAIOSFODNN7EXAMPLE"}}`),
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout=%q, want empty", stdout)
	}
	assertNoSyntheticSecrets(t, stderr)
	if strings.Contains(stderr, "sess-ghp_testtoken") || strings.Contains(stderr, "ConfigChange payload") {
		t.Fatalf("stderr leaked raw event context: %q", stderr)
	}
}
