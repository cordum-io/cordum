package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

const syntheticPEMBodyForRedactionTest = "SAMPLE_BASE64_SYNTHETIC"

func syntheticPrivateKeyPEMForRedactionTest(labelPrefix string) string {
	return "-----BEGIN " + labelPrefix + "PRIVATE KEY-----\n" +
		syntheticPEMBodyForRedactionTest +
		"\n-----END " + labelPrefix + "PRIVATE KEY-----"
}

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
		name      string
		input     string
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

func TestDefaultRedactor_PEMPrivateKeyFamilies(t *testing.T) {
	t.Parallel()
	r := DefaultRedactor()

	cases := []struct {
		name        string
		labelPrefix string
	}{
		{name: "rsa", labelPrefix: "RSA "},
		{name: "pkcs8_bare", labelPrefix: ""},
		{name: "ec", labelPrefix: "EC "},
		{name: "openssh", labelPrefix: "OPENSSH "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pemBlock := syntheticPrivateKeyPEMForRedactionTest(tc.labelPrefix)
			input, err := json.Marshal(map[string]string{"note": "inspect\n" + pemBlock})
			if err != nil {
				t.Fatalf("marshal input: %v", err)
			}

			out := string(r.Redact(input))
			if strings.Contains(out, syntheticPEMBodyForRedactionTest) || strings.Contains(out, "PRIVATE KEY") {
				t.Fatalf("PEM private key material leaked for %s: %s", tc.name, out)
			}
			if !strings.Contains(out, "[REDACTED:pem_private_key]") {
				t.Fatalf("PEM redaction marker missing for %s: %s", tc.name, out)
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

// TestDefaultRedactor_ExtendedTokenFamilies covers credential shapes that
// landed in step-10: Anthropic-style sk- keys and GitHub Personal Access
// Tokens. Step-7 shipped only the AWS AKIA / Stripe sk_live_ / JWT / PEM
// heuristics; the broader sk- and ghp_ families slipped past field-name
// matching when callers smuggle the value into a free-form string field.
func TestDefaultRedactor_ExtendedTokenFamilies(t *testing.T) {
	t.Parallel()
	r := DefaultRedactor()
	cases := []struct {
		name      string
		input     string
		sensitive string
	}{
		// Fake fixtures assembled from fragments to keep GitHub secret-scanning
		// push protection from flagging the source as a leaked credential.
		// Runtime semantics unchanged.
		{"anthropic_sk", `{"note":"use ` + "sk-" + "ant" + "0123456789abcdef0123456789abcdef" + `"}`, "sk-" + "ant" + "0123456789abcdef"},
		{"github_classic_pat", `{"note":"set TOKEN=` + "ghp_" + "0123456789abcdef0123456789abcdef0123" + `"}`, "ghp_" + "0123456789abcdef"},
		{"github_oauth_token", `{"note":"oauth ` + "gho_" + "0123456789abcdef0123456789abcdef0123" + `"}`, "gho_" + "0123456789abcdef"},
		{"github_user_server_token", `{"note":"server ` + "ghs_" + "0123456789abcdef0123456789abcdef0123" + `"}`, "ghs_" + "0123456789abcdef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := r.Redact(json.RawMessage(tc.input))
			if strings.Contains(string(out), tc.sensitive) {
				t.Errorf("secret family %s leaked: %s", tc.name, out)
			}
			if !strings.Contains(string(out), "[REDACTED:") {
				t.Errorf("no redaction marker for %s: %s", tc.name, out)
			}
		})
	}
}

// TestDefaultRedactor_GitHubTokenFamilies is the PR #276 Sub-E finding
// #25 regression: the default redactor MUST scrub every GitHub token
// family — classic PAT, OAuth, user-server, server-server, refresh,
// fine-grained PAT, AND Enterprise — when a token shape lands in a free-
// form string field. Pre-Sub-E coverage only handled the gh[opusr]_
// shape (ghp/gho/ghu/ghs/ghr); github_pat_ and ghe_ slipped past every
// regex and would survive into the redacted args of a failed event.
func TestDefaultRedactor_GitHubTokenFamilies(t *testing.T) {
	t.Parallel()
	r := DefaultRedactor()
	cases := []struct {
		name      string
		input     string
		sensitive string
	}{
		// Fixtures assembled from fragments so GitHub secret-scanning push
		// protection does not flag the source as a leaked credential.
		{"classic_pat", `{"note":"` + "ghp_" + "0123456789abcdef0123456789abcdef0123" + `"}`, "ghp_" + "0123456789abcdef"},
		{"oauth", `{"note":"` + "gho_" + "0123456789abcdef0123456789abcdef0123" + `"}`, "gho_" + "0123456789abcdef"},
		{"user_server", `{"note":"` + "ghu_" + "0123456789abcdef0123456789abcdef0123" + `"}`, "ghu_" + "0123456789abcdef"},
		{"server_server", `{"note":"` + "ghs_" + "0123456789abcdef0123456789abcdef0123" + `"}`, "ghs_" + "0123456789abcdef"},
		{"refresh", `{"note":"` + "ghr_" + "0123456789abcdef0123456789abcdef0123" + `"}`, "ghr_" + "0123456789abcdef"},
		{"fine_grained_pat", `{"note":"` + "github_pat_" + "11A0123456789_0123456789abcdef0123456789abcdef0123456789abcdef0123" + `"}`, "github_pat_" + "11A"},
		{"enterprise", `{"note":"` + "ghe_" + "0123456789abcdef0123456789abcdef0123" + `"}`, "ghe_" + "0123456789abcdef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := r.Redact(json.RawMessage(tc.input))
			if strings.Contains(string(out), tc.sensitive) {
				t.Errorf("redactor leaked %s token family: %s", tc.name, out)
			}
			if !strings.Contains(string(out), "[REDACTED:github_token]") {
				t.Errorf("expected [REDACTED:github_token] marker for %s, got: %s", tc.name, out)
			}
		})
	}
}

func TestMergeRedactionRules_EmptyFieldNameAndRegexDedupe(t *testing.T) {
	base := []RedactionRule{
		{
			FieldName:   " \t ",
			Regex:       `token-[A-Z]+`,
			Replacement: "[REDACTED:base-token]",
			Description: "base-token",
		},
		{
			FieldName:   "api_key",
			Replacement: "[REDACTED:base-api]",
			Description: "base-api",
		},
	}
	overrides := []RedactionRule{
		{FieldName: " ", Replacement: "[REDACTED:empty-one]", Description: "empty-one"},
		{FieldName: "\t", Replacement: "[REDACTED:empty-two]", Description: "empty-two"},
		{Regex: `token-[A-Z]+`, Replacement: "[REDACTED:dup-token]", Description: "dup-token"},
		{Regex: ` token-[A-Z]+ `, Replacement: "[REDACTED:dup-token-spaced]", Description: "dup-token-spaced"},
	}

	merged := MergeRedactionRules(base, overrides)

	if merged[0].Regex != `token-[A-Z]+` || merged[0].Replacement != "[REDACTED:base-token]" {
		t.Fatalf("empty FieldName override overwrote first base rule: %#v", merged)
	}
	if merged[1].FieldName != "api_key" || merged[1].Replacement != "[REDACTED:base-api]" {
		t.Fatalf("named base rule changed unexpectedly: %#v", merged)
	}
	if countRegexRule(merged, `token-[A-Z]+`) != 1 {
		t.Fatalf("duplicate regex rules were not deduped: %#v", merged)
	}
}

func countRegexRule(rules []RedactionRule, pattern string) int {
	count := 0
	for _, rule := range rules {
		if strings.TrimSpace(rule.Regex) == pattern {
			count++
		}
	}
	return count
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
