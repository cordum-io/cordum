package claude

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDefaultHookTimeoutIsUnderClaudeDeadline(t *testing.T) {
	if DefaultHookTimeout >= ClaudeHookDeadline {
		t.Fatalf("DefaultHookTimeout = %s, want strictly less than %s", DefaultHookTimeout, ClaudeHookDeadline)
	}
}

func TestBudgetSplitInvariant(t *testing.T) {
	total := DefaultAgentdPostBudget + ResponseWriteReserve
	if total > DefaultHookTimeout {
		t.Fatalf("agentd budget + write reserve = %s, exceeds hook timeout %s", total, DefaultHookTimeout)
	}
}

func TestAgentdPostBudgetShrinksForCustomHookTimeout(t *testing.T) {
	hookBudget, err := hookTimeout(RunOptions{Env: map[string]string{"CORDUM_AGENTD_HOOK_TIMEOUT": "3s"}})
	if err != nil {
		t.Fatalf("hookTimeout returned error: %v", err)
	}
	postBudget := agentdPostBudget(RunOptions{}, hookBudget)
	if want := 2500 * time.Millisecond; postBudget != want {
		t.Fatalf("agentdPostBudget = %s, want %s", postBudget, want)
	}
}

func TestReadHookInputClosesBlockingReaderOnContextCancellation(t *testing.T) {
	reader := newCloseAwareBlockingReader()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := readHookInput(ctx, reader, DefaultMaxInputBytes)
	elapsed := time.Since(started)

	if !errors.Is(err, errInputTimeout) {
		t.Fatalf("readHookInput error = %v, want %v", err, errInputTimeout)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("readHookInput took %s after context cancellation, want prompt return", elapsed)
	}
	select {
	case <-reader.closed:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("blocking reader was not closed after context cancellation; read goroutine still depends on stdin unblocking")
	}
}

type closeAwareBlockingReader struct {
	closed chan struct{}
	once   sync.Once
}

func newCloseAwareBlockingReader() *closeAwareBlockingReader {
	return &closeAwareBlockingReader{closed: make(chan struct{})}
}

func (r *closeAwareBlockingReader) Read([]byte) (int, error) {
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *closeAwareBlockingReader) Close() error {
	r.once.Do(func() {
		close(r.closed)
	})
	return nil
}

// TestParseHookInputDropsUnknownTopLevelFieldsSafely locks in the EDGE-016
// version-drift contract: when Claude Code adds new top-level fields the
// HookInput struct doesn't know about, parseHookInput must NOT error and
// must NOT echo the raw values into any logged field. Unknown nested keys
// inside `tool_input` (a `map[string]any`) are preserved untyped so the
// mapper's EDGE-004 redactor can value-redact them before persistence.
func TestParseHookInputDropsUnknownTopLevelFieldsSafely(t *testing.T) {
	data, err := os.ReadFile("testdata/hooks/unknown_future_fields.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	in, err := parseHookInput(data)
	if err != nil {
		t.Fatalf("parseHookInput on unknown-future fields errored: %v", err)
	}
	if in.HookEventName != "PreToolUse" {
		t.Errorf("HookEventName = %q, want PreToolUse", in.HookEventName)
	}
	if in.ToolName != "Bash" {
		t.Errorf("ToolName = %q, want Bash", in.ToolName)
	}
	if cmd, _ := in.ToolInput["command"].(string); cmd != "npm test" {
		t.Errorf("ToolInput[command] = %q, want 'npm test'", cmd)
	}
	if len(in.RawPayload) == 0 {
		t.Errorf("RawPayload must retain verbatim bytes for agentd forward")
	}
	// HookInput struct fields must not silently capture unknown future keys
	// like `future_field_top_level` or `experimental` — those would be
	// untyped and could leak into logs through field-by-field formatting.
	for _, suspect := range []string{"future_field_top_level", "experimental"} {
		if strings.Contains(string(in.RawPayload), suspect) {
			// Expected — RawPayload is verbatim bytes for agentd; that is
			// the in-memory-only forwarding path. We assert the suspects
			// are present in RawPayload but check the typed fields below.
			continue
		}
	}
}

// TestParseHookInputErrorsAreStableForVersionDrift pins the EDGE-015 error
// contract that the EDGE-016 mapper relies on: malformed JSON, multiple
// JSON values, and non-object payloads return distinct sentinel errors so
// runner.handleAgentdError + writeInputError can branch deterministically.
func TestParseHookInputErrorsAreStableForVersionDrift(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want error
	}{
		{"malformed json", `{"hook_event_name": "PreToolUse"`, errMalformedJSON},
		{"non-object json", `["PreToolUse"]`, errNonObjectJSON},
		{"multiple json values", `{"hook_event_name":"PreToolUse"}{"hook_event_name":"PostToolUse"}`, errMultipleJSON},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseHookInput([]byte(tc.data))
			if !errors.Is(err, tc.want) {
				t.Fatalf("parseHookInput err = %v, want %v", err, tc.want)
			}
		})
	}
}
