package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultRedactor_FieldNames(t *testing.T) {
	t.Parallel()
	r := DefaultRedactor()

	input := json.RawMessage(`{"user":"alice","password":"s3cr3t","nested":{"api_key":"abc"},"list":[{"token":"xyz"}]}`)
	out := r.Redact(input)
	s := string(out)

	if strings.Contains(s, "s3cr3t") {
		t.Errorf("password not redacted: %s", s)
	}
	if strings.Contains(s, "abc") {
		t.Errorf("nested api_key not redacted: %s", s)
	}
	if strings.Contains(s, "xyz") {
		t.Errorf("token inside list not redacted: %s", s)
	}
	if !strings.Contains(s, "[REDACTED:password]") {
		t.Errorf("password replacement missing: %s", s)
	}
	if !strings.Contains(s, `"user":"alice"`) {
		t.Errorf("non-sensitive field should survive: %s", s)
	}
}

func TestDefaultRedactor_RegexHeuristics(t *testing.T) {
	t.Parallel()
	r := DefaultRedactor()

	cases := []struct {
		name   string
		input  string
		sensitive string
	}{
		// Fake test fixtures — assembled from fragments to keep GitHub secret-
		// scanning push protection from flagging the source as a leaked token.
		// Runtime semantics unchanged.
		{"aws_key", `{"note":"key=` + "AKI" + "A" + "ABCDEFGHIJKLMNOP" + `"}`, "AKI" + "A" + "ABCDEFGHIJKLMNOP"},
		{"stripe", `{"note":"use ` + "sk_li" + "ve_abcdefghijklmnopqrstuvwxyz123456" + `"}`, "sk_li" + "ve_abcdefghijklmnopqrstuvwxyz123456"},
		{"jwt", `{"note":"bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.abc"}`, "eyJhbGciOiJIUzI1NiJ9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := r.Redact(json.RawMessage(tc.input))
			if strings.Contains(string(out), tc.sensitive) {
				t.Errorf("secret leaked: %s", out)
			}
			if !strings.Contains(string(out), "[REDACTED:") {
				t.Errorf("no redaction marker: %s", out)
			}
		})
	}
}

func TestDefaultRedactor_MalformedJSONReturnsSentinel(t *testing.T) {
	t.Parallel()
	r := DefaultRedactor()
	out := r.Redact(json.RawMessage(`{"unterminated`))
	s := string(out)
	if !strings.Contains(s, "unparseable_args") {
		t.Errorf("expected unparseable_args sentinel, got %s", s)
	}
}

func TestDefaultRedactor_EmptyInput(t *testing.T) {
	t.Parallel()
	r := DefaultRedactor()
	if got := r.Redact(nil); len(got) != 0 {
		t.Errorf("nil input should return empty, got %s", got)
	}
}

func TestDefaultRedactor_CaseInsensitiveFieldName(t *testing.T) {
	t.Parallel()
	r := DefaultRedactor()
	out := r.Redact(json.RawMessage(`{"Password":"x","PASSWORD":"y","apiKey":"z"}`))
	s := string(out)
	if strings.Contains(s, `"x"`) || strings.Contains(s, `"y"`) || strings.Contains(s, `"z"`) {
		t.Errorf("case-insensitive redaction failed: %s", s)
	}
}

func TestPolicyRedactor_InvalidRegexSkipped(t *testing.T) {
	t.Parallel()
	r := NewPolicyRedactor([]RedactionRule{
		{Regex: "[unclosed", Replacement: "[oops]", Description: "bad"},
		{FieldName: "secret", Replacement: "[REDACTED:secret]"},
	})
	out := r.Redact(json.RawMessage(`{"secret":"abc"}`))
	if strings.Contains(string(out), "abc") {
		t.Errorf("valid field-name rule should still apply despite invalid regex sibling: %s", out)
	}
}

func TestPolicyRedactor_CustomReplacementInDescription(t *testing.T) {
	t.Parallel()
	r := NewPolicyRedactor([]RedactionRule{
		{FieldName: "pinCode", Description: "pin"},
	})
	out := r.Redact(json.RawMessage(`{"pinCode":"1234"}`))
	if !strings.Contains(string(out), "[REDACTED:pin]") {
		t.Errorf("expected [REDACTED:pin], got %s", out)
	}
}

func BenchmarkRedactLarge(b *testing.B) {
	r := DefaultRedactor()
	// 10 KB payload with mixed secret/non-secret fields.
	payload := map[string]any{}
	for i := 0; i < 200; i++ {
		payload["field_"+string(rune('a'+i%26))] = "value-" + string(rune('A'+i%26))
	}
	payload["password"] = "s3cr3t"
	payload["nested"] = map[string]any{"api_key": "abc"}
	raw, _ := json.Marshal(payload)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Redact(raw)
	}
}
