package k8s

import (
	"encoding/base64"
	"strings"
	"testing"
)

// secretCases is the canonical list of secret shapes the shared
// shadow.StripSecretMarkers pipeline scrubs. Mirrored from
// core/edge/shadow/redaction_test.go so the k8s redactor proves it
// inherits every parent encoding path, not just the direct regex
// strip. Tokens are intentionally `cordum_fake_*` so repository
// secret scanners stay quiet (task rail #3).
var secretCases = []struct {
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

func TestRedactField_DirectSecrets(t *testing.T) {
	for _, tc := range secretCases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactField("k8s-field=" + tc.secret)
			assertRedacted(t, got, tc.secret)
		})
	}

	t.Run("private_key_header", func(t *testing.T) {
		// Split across two strings so the literal does not trip
		// repository secret scanners or this test's own assertions.
		header := "-----BEGIN CORDUM TEST " + "PRIVATE KEY-----"
		got := redactField("inline=" + header)
		assertRedacted(t, got, header)
	})
}

func TestRedactField_HomoglyphHyphens(t *testing.T) {
	hyphens := []struct {
		name string
		rune string
	}{
		{name: "u2010", rune: "‐"},
		{name: "u2011", rune: "‑"},
		{name: "u2012", rune: "‒"},
		{name: "u2013", rune: "–"},
		{name: "u2014", rune: "—"},
		{name: "uff0d", rune: "－"},
	}

	for _, tc := range hyphens {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.ReplaceAll(
				"cordum_fake_sk-ant-cordumtest2026realfakekey0123",
				"-",
				tc.rune,
			)
			got := redactField("signal=" + raw)

			assertRedacted(t, got, raw)
			assertNotContains(t, got, "cordumtest2026realfakekey0123")
		})
	}

	t.Run("mixed", func(t *testing.T) {
		raw := "cordum_fake_sk‐ant‑cordumtest2026realfakekey0123"
		got := redactField("signal=" + raw)
		assertRedacted(t, got, raw)
		assertNotContains(t, got, "cordumtest2026realfakekey0123")
	})
}

func TestRedactField_ROT13(t *testing.T) {
	for _, tc := range secretCases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := rot13ForTest(tc.secret)
			got := redactField("rot13=" + encoded)

			assertRedacted(t, got, encoded)
			assertNotContains(t, got, tc.secret)
		})
	}
}

func TestRedactField_Base64(t *testing.T) {
	for _, tc := range secretCases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString([]byte(tc.secret))
			got := redactField("b64=" + encoded)

			assertRedacted(t, got, encoded)
			assertNotContains(t, got, tc.secret)
		})
	}
}

func TestRedactField_NonSecret_Unchanged(t *testing.T) {
	cases := []string{
		"unmanaged_mcp_server",
		"plain text with hyphenated-words and underscores",
		"namespace=tenant-a",
		"image registry not in operator allowlist; image=registry/foo:v1.2.3",
		"b64=" + base64.StdEncoding.EncodeToString([]byte("just an ordinary phrase")),
		"malformed QUJD=REVGR0hJSktMTU5P payload",
	}

	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			got := redactField(input)
			if got != input {
				t.Fatalf("redactField(%q) = %q, want unchanged", input, got)
			}
		})
	}
}

func TestRedactField_TruncatesOversize(t *testing.T) {
	// Build an input safely under regex amplification but well over
	// maxFieldBytes so the truncation suffix is exercised. The redactor
	// runs the shared pipeline first; the cap MUST be enforced on the
	// post-redaction string so a secret that survives past 2048 bytes
	// of innocuous prefix can never be emitted via truncation either.
	prefix := strings.Repeat("a", maxFieldBytes+512)
	got := redactField(prefix)

	if len(got) > maxFieldBytes {
		t.Fatalf("redactField did not truncate: len=%d, cap=%d", len(got), maxFieldBytes)
	}
	if !strings.HasSuffix(got, " …truncated") {
		t.Fatalf("redactField missing truncation sentinel: %q", got[len(got)-32:])
	}
}

func TestImageTagSafe_SecretShapeTagsRedacted(t *testing.T) {
	for _, tc := range secretCases {
		t.Run("direct/"+tc.name, func(t *testing.T) {
			image := "registry.local/agent-img:" + tc.secret
			got := imageTagSafe(image)
			assertNotContains(t, got, tc.secret)
			if !strings.HasSuffix(got, ":<redacted>") {
				t.Fatalf("imageTagSafe(%q) = %q, want trailing :<redacted>", image, got)
			}
		})

		t.Run("homoglyph/"+tc.name, func(t *testing.T) {
			homoglyph := strings.ReplaceAll(tc.secret, "-", "–")
			image := "registry.local/agent-img:" + homoglyph
			got := imageTagSafe(image)
			assertNotContains(t, got, homoglyph)
			if !strings.HasSuffix(got, ":<redacted>") {
				t.Fatalf("imageTagSafe homoglyph(%q) = %q, want trailing :<redacted>", image, got)
			}
		})

		t.Run("rot13/"+tc.name, func(t *testing.T) {
			image := "registry.local/agent-img:" + rot13ForTest(tc.secret)
			got := imageTagSafe(image)
			assertNotContains(t, got, tc.secret)
			if !strings.HasSuffix(got, ":<redacted>") {
				t.Fatalf("imageTagSafe rot13(%q) = %q, want trailing :<redacted>", image, got)
			}
		})

		t.Run("base64/"+tc.name, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString([]byte(tc.secret))
			image := "registry.local/agent-img:" + encoded
			got := imageTagSafe(image)
			assertNotContains(t, got, tc.secret)
			assertNotContains(t, got, encoded)
			if !strings.HasSuffix(got, ":<redacted>") {
				t.Fatalf("imageTagSafe base64(%q) = %q, want trailing :<redacted>", image, got)
			}
		})
	}
}

func TestImageTagSafe_PlainTagsPassThrough(t *testing.T) {
	cases := []struct {
		name  string
		image string
		want  string
	}{
		{name: "semver", image: "registry.local/agent-img:v1.2.3", want: "registry.local/agent-img:v1.2.3"},
		{name: "latest", image: "registry.local/agent-img:latest", want: "registry.local/agent-img:latest"},
		{name: "no_tag", image: "registry.local/agent-img", want: "registry.local/agent-img"},
		{name: "empty", image: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := imageTagSafe(tc.image)
			if got != tc.want {
				t.Fatalf("imageTagSafe(%q) = %q, want %q", tc.image, got, tc.want)
			}
		})
	}
}

func TestImageTagSafe_DigestStrippedAndScrubbed(t *testing.T) {
	// Image with both :tag and @sha256:digest. The @-truncation must
	// happen before secret-shape evaluation so the digest itself is
	// never persisted, and the truncation must not panic even when the
	// original last-colon position lay inside the digest suffix.
	image := "registry.local/agent-img:v1.2.3@sha256:abcdef0123456789abcdef0123456789"
	got := imageTagSafe(image)
	assertNotContains(t, got, "sha256:abcdef0123456789")
	if got != "registry.local/agent-img:v1.2.3" {
		t.Fatalf("imageTagSafe(%q) = %q, want %q", image, got, "registry.local/agent-img:v1.2.3")
	}
}

func TestImageTagSafe_SecretInBase(t *testing.T) {
	// Secret-shape lurking in the registry/name portion (not the tag)
	// must still be redacted by the base's redactField call.
	image := "registry.local/" + "cordum_fake_ghp_cordumtest2026realkey0123" + "/agent-img:v1"
	got := imageTagSafe(image)
	assertNotContains(t, got, "cordum_fake_ghp_cordumtest2026realkey0123")
	if !strings.HasSuffix(got, ":v1") {
		t.Fatalf("imageTagSafe(%q) = %q, want trailing :v1", image, got)
	}
}

func TestLeadingToken_StripsAfterFirstWhitespace(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "command_with_args", input: "/usr/bin/agent --flag value", want: "/usr/bin/agent"},
		{name: "tab_separated", input: "binary\t--arg", want: "binary"},
		{name: "leading_whitespace", input: "   binary --arg", want: "binary"},
		{name: "empty", input: "", want: ""},
		{name: "whitespace_only", input: "   \t  ", want: ""},
		{name: "single_token", input: "binary", want: "binary"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := leadingToken(tc.input)
			if got != tc.want {
				t.Fatalf("leadingToken(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func assertRedacted(t *testing.T, got string, forbidden string) {
	t.Helper()
	assertNotContains(t, got, forbidden)
	if !strings.Contains(got, "<REDACTED>") {
		t.Fatalf("k8s redactor result %q did not include <REDACTED>", compactTestString(got))
	}
}

func assertNotContains(t *testing.T, got string, forbidden string) {
	t.Helper()
	if strings.Contains(got, forbidden) {
		t.Fatalf("k8s redactor result %q still contains %q", compactTestString(got), forbidden)
	}
}

func compactTestString(s string) string {
	const maxLen = 240
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func rot13ForTest(s string) string {
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
