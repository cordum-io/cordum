package gateway

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/mcp"
)

func TestMCPApprovalOriginalArguments(t *testing.T) {
	const original = `{"record":"fixture","password":"synthetic-original"}`
	const changed = `{"record":"fixture","password":"synthetic-changed"}`
	redactor := mcp.DefaultRedactor()
	if !bytes.Equal(redactor.Redact(json.RawMessage(original)), redactor.Redact(json.RawMessage(changed))) {
		t.Fatal("fixture calls must differ only inside the redacted span")
	}
	h := agrNewServer(t)
	ref := h.heldAndApproved(t, original)
	if n := h.calls.Load(); n != 0 {
		t.Fatalf("upstream calls before approval resume = %d, want 0", n)
	}
	t.Run("changed_arguments_are_refused", func(t *testing.T) {
		r := h.resume(t, changed, ref)
		if n := h.calls.Load(); n != 0 {
			t.Errorf("upstream calls for changed arguments = %d, want 0", n)
		}
		if r.Error == nil || r.Error.Code != -32096 || !strings.Contains(string(r.Error.Data), `"args_mismatch"`) {
			t.Errorf("changed arguments must return JSON-RPC args_mismatch, got %s", r)
		}
	})
	t.Run("original_arguments_resume", func(t *testing.T) {
		if r := h.resume(t, original, ref); !r.succeeded("deleted fixture") {
			t.Errorf("original arguments must resume, got %s", r)
		}
		if n := h.calls.Load(); n != 1 {
			t.Errorf("upstream calls after original resume = %d, want 1", n)
		}
	})
}
