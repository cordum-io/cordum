package claude

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/cordum/cordum/core/edge"
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

// EDGE-071 — redactHookBoundaryString MUST fail closed when the
// underlying edge.RedactValue returns an error. The dangerous pattern
// the fix prevents is: redact fails -> fallback returns raw value ->
// the very secret the redactor was supposed to mask leaks into events,
// audit, or logs. The pre-fix code at mapper.go L591 left `candidate`
// as the raw value when `err != nil`, which under
// `result.Redacted/Truncated == false` AND no diagnostic transformation
// returned the raw input verbatim. The fix replaces the err-tolerant
// branch with an early-return placeholder.
func TestRedactHookBoundaryStringFailsClosedOnRedactError(t *testing.T) {
	saved := claudeRedactValue
	defer func() { claudeRedactValue = saved }()
	claudeRedactValue = func(any, edge.RedactionOptions) (edge.RedactionResult, error) {
		return edge.RedactionResult{}, errors.New("forced redactor failure")
	}

	// A value that would NOT match any redaction pattern in the
	// post-redactor diagnostic pass — so pre-fix the leak path was
	// reachable: redactor errored, candidate stayed raw, no diagnostic
	// transformation, no [REDACTED] marker -> return raw.
	rawIDLikeInput := "tenant-acme-prod-001"

	got := redactHookBoundaryString(rawIDLikeInput)
	if got == rawIDLikeInput {
		t.Fatalf("redactHookBoundaryString leaked raw value on redactor error: got %q", got)
	}
	if got != "<redacted>" {
		t.Errorf("redactHookBoundaryString = %q on redactor error, want %q", got, "<redacted>")
	}
}

// EDGE-071 — redactHookBoundaryString MUST fail closed on inputs that
// exceed edge.MaxRedactionInputBytes. The unscanned tail of an oversized
// input might contain secrets, so the call site cannot safely return the
// partially-scanned head. The 1 MiB ceiling is also a memory-safety net
// against attacker-supplied huge payloads.
func TestRedactHookBoundaryStringFailsClosedOnOversizedInput(t *testing.T) {
	// Build a value just past the cap. The body is benign ASCII so no
	// redaction pattern would have fired on a smaller version of it —
	// asserting that the size-bound short-circuit fires regardless of
	// content.
	oversized := strings.Repeat("a", edge.MaxRedactionInputBytes+1)

	got := redactHookBoundaryString(oversized)
	if got != "<redacted>" {
		t.Errorf("redactHookBoundaryString(oversized) = %q (len=%d), want %q",
			got, len(got), "<redacted>")
	}
}

// EDGE-071 — sanity check that the success path still produces the
// pre-fix shape for a benign value: small, no secret, returns the
// trimmed input unchanged. Pins the EDGE-046 over-redaction guard
// (no broad substring check on "secret") against accidental regression
// from the EDGE-071 fail-closed changes.
func TestRedactHookBoundaryStringSuccessPathPreservesBenignValue(t *testing.T) {
	got := redactHookBoundaryString("  tenant-id-with-secret-substring  ")
	if got != "tenant-id-with-secret-substring" {
		t.Errorf("redactHookBoundaryString(benign) = %q, want %q",
			got, "tenant-id-with-secret-substring")
	}
}

// EDGE-071 — when a redaction call site falls back to the safe
// placeholder, the package-level recorder MUST receive a
// RecordRedactionFailed call with the matching site + reason labels.
// This is the operational signal operators rely on to spot the
// fail-closed event and investigate.
type redactionFailedRecorder struct {
	edge.NoopRecorder
	mu    sync.Mutex
	calls []redactionFailedCall
}

type redactionFailedCall struct{ site, reason string }

func (r *redactionFailedRecorder) RecordRedactionFailed(site, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, redactionFailedCall{site: site, reason: reason})
}

func (r *redactionFailedRecorder) snapshot() []redactionFailedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]redactionFailedCall(nil), r.calls...)
}

func TestRedactHookBoundaryStringEmitsMetricOnRedactorError(t *testing.T) {
	rec := &redactionFailedRecorder{}
	SetRedactionRecorder(rec)
	defer SetRedactionRecorder(nil)

	saved := claudeRedactValue
	defer func() { claudeRedactValue = saved }()
	claudeRedactValue = func(any, edge.RedactionOptions) (edge.RedactionResult, error) {
		return edge.RedactionResult{}, errors.New("forced redactor failure")
	}

	if got := redactHookBoundaryString("tenant-acme-prod-001"); got != "<redacted>" {
		t.Fatalf("fail-closed return = %q, want %q", got, "<redacted>")
	}

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("got %d RecordRedactionFailed calls, want 1: %+v", len(calls), calls)
	}
	if calls[0].site != "claude.redact_hook_boundary_string" {
		t.Errorf("site = %q, want %q", calls[0].site, "claude.redact_hook_boundary_string")
	}
	if calls[0].reason != "redactor_error" {
		t.Errorf("reason = %q, want %q", calls[0].reason, "redactor_error")
	}
}

func TestRedactHookBoundaryStringEmitsMetricOnOversizedInput(t *testing.T) {
	rec := &redactionFailedRecorder{}
	SetRedactionRecorder(rec)
	defer SetRedactionRecorder(nil)

	oversized := strings.Repeat("a", edge.MaxRedactionInputBytes+1)
	if got := redactHookBoundaryString(oversized); got != "<redacted>" {
		t.Fatalf("fail-closed return = %q, want %q", got, "<redacted>")
	}

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("got %d RecordRedactionFailed calls, want 1: %+v", len(calls), calls)
	}
	if calls[0].site != "claude.redact_hook_boundary_string" {
		t.Errorf("site = %q, want %q", calls[0].site, "claude.redact_hook_boundary_string")
	}
	if calls[0].reason != "input_too_large" {
		t.Errorf("reason = %q, want %q", calls[0].reason, "input_too_large")
	}
}
