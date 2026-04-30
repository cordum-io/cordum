package claude

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentdHTTPErrorBodyIsNotLoggedOrReturned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "raw agentd response with sk-test-secret ghp_testtoken password=hunter2", http.StatusInternalServerError)
	}))
	defer server.Close()

	code, stdout, stderr := runHook(t, RunOptions{
		Args:  []string{"claude", "pre-tool-use"},
		Stdin: hookInput(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"npm test"},"transcript_path":"/tmp/transcript-with-sk-test-secret.json"}`),
		Env: map[string]string{
			"CORDUM_AGENTD_URL":         server.URL,
			"CORDUM_AGENTD_FAIL_CLOSED": "true",
		},
	})
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, stderr)
	}
	assertNoSyntheticSecrets(t, stdout)
	assertNoSyntheticSecrets(t, stderr)
	for _, forbidden := range []string{"raw agentd response", "hunter2", "transcript-with"} {
		if strings.Contains(stdout, forbidden) || strings.Contains(stderr, forbidden) {
			t.Fatalf("forbidden diagnostic content %q leaked in stdout=%q stderr=%q", forbidden, stdout, stderr)
		}
	}
	if !strings.Contains(stderr, "agentd_unavailable") || !strings.Contains(stderr, "agentd status 500") {
		t.Fatalf("stderr should expose stable code/status only, got %q", stderr)
	}
}

func TestLongDiagnosticsAreTruncated(t *testing.T) {
	got := redactDiagnostic("prefix " + strings.Repeat("x", 2048) + " sk-test-secret")
	if len(got) > maxDiagnosticLen+3 {
		t.Fatalf("diagnostic len=%d exceeds bound; %q", len(got), got)
	}
	assertNoSyntheticSecrets(t, got)
}
