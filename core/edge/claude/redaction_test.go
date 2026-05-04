package claude

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRedactDiagnosticMasksSyntheticSecretsByValue(t *testing.T) {
	input := strings.Join([]string{
		"neutral=sk-test-secret",
		"github=ghp_testtoken",
		"aws_access=AKIAIOSFODNN7EXAMPLE",
		"aws_secret=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"legacy_nonce=f00ddeadbeefcafe0123456789abcdef",
		"agentd_nonce=abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ",
		"header=Authorization: Bearer sk-test-secret",
		`json={"password":"hunter2","note":"token ghp_testtoken inside neutral field"}`,
	}, " ")

	got := redactDiagnostic(input)
	assertNoSyntheticSecrets(t, got)
	for _, mustRedact := range []string{"hunter2", "Authorization: Bearer", "f00ddeadbeefcafe0123456789abcdef", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"} {
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

func TestRedactDiagnosticAvoidsBenignHashFalsePositives(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	sha256 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	got := redactDiagnostic("commit=" + commit + " sha256=" + sha256)
	if !strings.Contains(got, commit) || !strings.Contains(got, sha256) {
		t.Fatalf("benign hash diagnostic was over-redacted: %q", got)
	}
	if strings.Contains(got, "[REDACTED]") {
		t.Fatalf("benign hash diagnostic contained redaction marker: %q", got)
	}
}

func TestRedactDiagnosticMasksHighEntropyStandardBase64(t *testing.T) {
	secret := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	got := redactDiagnostic("blob=" + secret)
	if strings.Contains(got, secret) {
		t.Fatalf("high-entropy base64 diagnostic leaked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected base64 diagnostic redaction marker, got %q", got)
	}
}

func TestRedactDiagnosticTruncatesAtUTF8RuneBoundary(t *testing.T) {
	// "日" is 3 bytes in UTF-8 (0xE6 0x97 0xA5). If the diagnostic exceeds
	// maxDiagnosticLen, naive byte slicing can leave a partial multi-byte
	// rune at the cut point and emit invalid UTF-8 into structured logs.
	const target = "日"
	prefix := strings.Repeat("a", maxDiagnosticLen-2) // forces the cut to land mid-rune
	got := redactDiagnostic(prefix + target + strings.Repeat("b", 32))
	// The trailing rune may or may not survive truncation, but the result
	// must always be valid UTF-8 — never a partial rune.
	if !utf8.ValidString(got) {
		t.Fatalf("redactDiagnostic returned invalid UTF-8: %q (% x)", got, []byte(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncation marker, got %q", got)
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

// EDGE-049 — safeID() must preserve legitimate IDs that happen to contain
// the substring "secret" (e.g., session labels like "secret-rotation-bot").
// Pre-fix, safeID wholesale-replaced any such ID with [REDACTED] via a broad
// strings.Contains(..., "secret") check that confused CONTEXT with CONTENT.
// Sibling fix: EDGE-046 (mapper.go:594 redactHookBoundaryString).
func TestSafeIDPreservesIDsWithSecretSubstring(t *testing.T) {
	got := safeID("secret-rotation-bot-001")
	if got != "secret-r..." {
		t.Errorf("safeID(legitimate ID with 'secret' substring) = %q, want %q", got, "secret-r...")
	}
}

// EDGE-049 — safeID() must STILL redact actual secret values via the
// redactDiagnostic-produced [REDACTED] marker. The sk- token pattern at
// redaction.go:15 catches OpenAI-style API keys; safeID's first-clause
// check on the [REDACTED] substring preserves this protection. (The
// bearer pattern at L13 requires the full "Authorization: Bearer ..."
// prefix; sk- is the simpler trigger for a unit-test-shape value.)
func TestSafeIDStillRedactsActualSecretValue(t *testing.T) {
	got := safeID("sk-test123abc456def789")
	if got != "[REDACTED]" {
		t.Errorf("safeID(actual secret with sk- pattern) = %q, want %q", got, "[REDACTED]")
	}
}

// EDGE-049 — short IDs that don't trigger redaction must pass through
// unchanged (no truncation, no [REDACTED]).
func TestSafeIDPreservesShortIDsUnchanged(t *testing.T) {
	got := safeID("abc-001")
	if got != "abc-001" {
		t.Errorf("safeID(short benign ID) = %q, want %q", got, "abc-001")
	}
}

