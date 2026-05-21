package shadow

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestStripSecretMarkers_ExistingPatterns(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		forbidden string
	}{
		{
			name:      "anthropic",
			input:     "token=cordum_fake_sk-ant-cordumtest2026realfakekey0123",
			forbidden: "cordum_fake_sk-ant-cordumtest2026realfakekey0123",
		},
		{
			name:      "openai",
			input:     "token=cordum_fake_sk-cordumtest2026realfakekey0123",
			forbidden: "cordum_fake_sk-cordumtest2026realfakekey0123",
		},
		{
			name:      "github_pat",
			input:     "token=cordum_fake_ghp_cordumtest2026realkey0123",
			forbidden: "cordum_fake_ghp_cordumtest2026realkey0123",
		},
		{
			name:      "github_oauth",
			input:     "token=cordum_fake_gho_cordumtest2026realkey0123",
			forbidden: "cordum_fake_gho_cordumtest2026realkey0123",
		},
		{
			name:      "slack_bot",
			input:     "token=cordum_fake_xoxb-cordumtest2026realkey0123",
			forbidden: "cordum_fake_xoxb-cordumtest2026realkey0123",
		},
		{
			name:      "bearer",
			input:     "Authorization: Bearer cordum_fake_token_0123456789",
			forbidden: "Bearer cordum_fake_token_0123456789",
		},
		{
			name:      "private_key",
			input:     "-----BEGIN CORDUM TEST " + "PRIVATE KEY-----",
			forbidden: "PRIVATE" + " KEY",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripSecretMarkers(tc.input)
			assertRedacted(t, got, tc.forbidden)
		})
	}
}

func TestStripSecretMarkers_PEMPrivateKeyFamilies(t *testing.T) {
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
			got := stripSecretMarkers("config=" + syntheticShadowPrivateKeyBlock(tc.labelPrefix))

			assertNotContains(t, got, "BEGIN "+tc.labelPrefix+"PRIVATE"+" KEY")
			if !strings.Contains(got, "<REDACTED>") {
				t.Fatalf("stripSecretMarkers result %q did not include <REDACTED>", compactTestString(got))
			}
		})
	}
}

func TestStripSecretMarkers_HomoglyphHyphens(t *testing.T) {
	hyphens := []struct {
		name string
		rune string
	}{
		{name: "u2010", rune: "\u2010"},
		{name: "u2011", rune: "\u2011"},
		{name: "u2012", rune: "\u2012"},
		{name: "u2013", rune: "\u2013"},
		{name: "u2014", rune: "\u2014"},
		{name: "uff0d", rune: "\uff0d"},
	}

	for _, tc := range hyphens {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.ReplaceAll("cordum_fake_sk-ant-cordumtest2026realfakekey0123", "-", tc.rune)
			got := stripSecretMarkers("signal=" + raw)

			assertRedacted(t, got, raw)
			assertNotContains(t, got, "cordumtest2026realfakekey0123")
		})
	}

	t.Run("mixed", func(t *testing.T) {
		raw := "cordum_fake_sk\u2010ant\u2011cordumtest2026realfakekey0123"
		got := stripSecretMarkers("signal=" + raw)

		assertRedacted(t, got, raw)
		assertNotContains(t, got, "cordumtest2026realfakekey0123")
	})
}

func TestStripSecretMarkers_ROT13(t *testing.T) {
	cases := []struct {
		name   string
		secret string
	}{
		{name: "anthropic", secret: "cordum_fake_sk-ant-cordumtest2026realfakekey0123"},
		{name: "openai", secret: "cordum_fake_sk-cordumtest2026realfakekey0123"},
		{name: "github_pat", secret: "cordum_fake_ghp_cordumtest2026realkey0123"},
		{name: "github_oauth", secret: "cordum_fake_gho_cordumtest2026realkey0123"},
		{name: "slack_bot", secret: "cordum_fake_xoxb-cordumtest2026realkey0123"},
		{name: "bearer", secret: "Authorization: Bearer cordum_fake_token_0123456789"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := encodeROT13ForTest(tc.secret)
			got := stripSecretMarkers("signal=" + encoded)

			assertRedacted(t, got, encoded)
			assertNotContains(t, got, tc.secret)
		})
	}
}

func TestStripSecretMarkers_Base64(t *testing.T) {
	cases := []struct {
		name   string
		secret string
	}{
		{name: "anthropic", secret: "cordum_fake_sk-ant-cordumtest2026realfakekey0123"},
		{name: "openai", secret: "cordum_fake_sk-cordumtest2026realfakekey0123"},
		{name: "github_pat", secret: "cordum_fake_ghp_cordumtest2026realkey0123"},
		{name: "github_oauth", secret: "cordum_fake_gho_cordumtest2026realkey0123"},
		{name: "slack_bot", secret: "cordum_fake_xoxb-cordumtest2026realkey0123"},
		{name: "bearer", secret: "Authorization: Bearer cordum_fake_token_0123456789"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString([]byte(tc.secret))
			got := stripSecretMarkers("payload=" + encoded)

			assertRedacted(t, got, encoded)
			assertNotContains(t, got, tc.secret)
		})
	}
}

func TestStripSecretMarkers_NonSecretInput_Unchanged(t *testing.T) {
	cases := []string{
		"unmanaged_mcp_server",
		"plain text with hyphenated words and underscores",
		"payload=" + base64.StdEncoding.EncodeToString([]byte("not a credential")),
		"malformed QUJD=REVGR0hJSktMTU5P payload",
	}

	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			got := stripSecretMarkers(input)
			if got != input {
				t.Fatalf("stripSecretMarkers(%q) = %q, want unchanged", input, got)
			}
		})
	}
}

func TestStripSecretMarkers_LargeInputBounded(t *testing.T) {
	secret := "cordum_fake_sk-ant-cordumtest2026realfakekey0123"
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	input := strings.Repeat("safe-value ", 10_000) + encoded + strings.Repeat(" tail-safe", 10_000)

	start := time.Now()
	got := stripSecretMarkers(input)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("stripSecretMarkers took %s for bounded large input", elapsed)
	}

	assertRedacted(t, got, encoded)
	assertNotContains(t, got, secret)
}

func assertRedacted(t *testing.T, got string, forbidden string) {
	t.Helper()

	assertNotContains(t, got, forbidden)
	if !strings.Contains(got, "<REDACTED>") {
		t.Fatalf("stripSecretMarkers result %q did not include <REDACTED>", compactTestString(got))
	}
}

func assertNotContains(t *testing.T, got string, forbidden string) {
	t.Helper()

	if strings.Contains(got, forbidden) {
		t.Fatalf("stripSecretMarkers result %q still contains %q", compactTestString(got), forbidden)
	}
}

func compactTestString(s string) string {
	const maxLen = 240
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func encodeROT13ForTest(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune('A' + (r-'A'+13)%26)
		case r >= 'a' && r <= 'z':
			b.WriteRune('a' + (r-'a'+13)%26)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

const syntheticShadowPrivateKeyBody = "SAMPLE_BASE64_SYNTHETIC"

func syntheticShadowPrivateKeyBlock(labelPrefix string) string {
	return "-----BEGIN " + labelPrefix + "PRIVATE KEY-----\n" +
		syntheticShadowPrivateKeyBody +
		"\n-----END " + labelPrefix + "PRIVATE KEY-----"
}
