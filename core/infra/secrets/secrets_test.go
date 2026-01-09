package secrets

import (
	"encoding/json"
	"testing"
)

func TestContainsAndRedactSecretRefs(t *testing.T) {
	payload := map[string]any{
		"token": "secret://vault/api",
		"nested": map[string]any{
			"value": "secret://vault/nested",
		},
		"list": []any{"ok", "secret://vault/list"},
	}

	if !ContainsSecretRefs(payload) {
		t.Fatalf("expected secret refs to be detected")
	}

	redacted, changed := RedactSecretRefs(payload)
	if !changed {
		t.Fatalf("expected redaction to report changes")
	}

	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted: %v", err)
	}
	if string(data) == "" || !ContainsSecretRefs(payload) {
		t.Fatalf("redacted output malformed")
	}
	if string(data) == string(mustJSON(payload)) {
		t.Fatalf("expected redacted payload to differ")
	}
}

func TestRedactJSON(t *testing.T) {
	input := []byte(`{"token":"secret://vault/token","ok":"value"}`)
	out, changed, err := RedactJSON(input)
	if err != nil {
		t.Fatalf("redact json: %v", err)
	}
	if !changed {
		t.Fatalf("expected redaction to report changes")
	}
	if string(out) == string(input) {
		t.Fatalf("expected redacted payload to differ")
	}

	unchanged, changed, err := RedactJSON([]byte(`{"ok":"value"}`))
	if err != nil {
		t.Fatalf("redact json: %v", err)
	}
	if changed {
		t.Fatalf("expected no changes for non-secret payload")
	}
	if string(unchanged) != `{"ok":"value"}` {
		t.Fatalf("unexpected unchanged payload: %s", unchanged)
	}
}

func TestContainsSecretRefsFalse(t *testing.T) {
	payload := map[string]any{"ok": "value"}
	if ContainsSecretRefs(payload) {
		t.Fatalf("expected no secret refs")
	}
	redacted, changed := RedactSecretRefs(payload)
	if changed {
		t.Fatalf("expected no redaction")
	}
	if string(mustJSON(redacted)) != string(mustJSON(payload)) {
		t.Fatalf("unexpected redaction output")
	}
}

func TestRedactSecretRefsStringCollections(t *testing.T) {
	payload := map[string]string{
		"token": "secret://vault/token",
		"plain": "value",
	}
	redacted, changed := RedactSecretRefs(payload)
	if !changed {
		t.Fatalf("expected redaction for string map")
	}
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) == string(mustJSON(payload)) {
		t.Fatalf("expected redacted output to differ")
	}

	list := []string{"ok", "secret://vault/list"}
	redacted, changed = RedactSecretRefs(list)
	if !changed {
		t.Fatalf("expected redaction for string list")
	}
	data, err = json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	if string(data) == string(mustJSON(list)) {
		t.Fatalf("expected redacted list to differ")
	}
}

func TestRedactJSONInvalid(t *testing.T) {
	_, _, err := RedactJSON([]byte("{bad-json"))
	if err == nil {
		t.Fatalf("expected error for invalid json")
	}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}
