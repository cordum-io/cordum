package safetykernel

import (
	"regexp"
	"strings"
	"testing"
)

const ckAWSKey = "AKIAIOSFODNN7EXAMPLE"

func ckTypes(fs []ContentFinding) map[string]int {
	out := map[string]int{}
	for _, f := range fs {
		out[f.Type]++
	}
	return out
}

func TestScanContent_PIIAndSecret(t *testing.T) {
	content := []byte("Reach alice@example.com and deploy " + ckAWSKey + " now.")
	fs := ScanContent(content, ContentScanOptions{IncludePII: true, IncludeSecrets: true})
	got := ckTypes(fs)
	if got["pii"] == 0 {
		t.Fatalf("expected a pii finding (email): %+v", fs)
	}
	if got["secret_leak"] == 0 {
		t.Fatalf("expected a secret_leak finding (aws key): %+v", fs)
	}
	// Findings carry offsets so callers can redact exactly the span.
	for _, f := range fs {
		if f.Length <= 0 || f.Offset < 0 || f.Offset+f.Length > len(content) {
			t.Fatalf("finding has bad span: %+v", f)
		}
	}
}

func TestRedactContent_InPlacePreservesProse(t *testing.T) {
	content := []byte("Email alice@example.com about the plan; key " + ckAWSKey + " ok.")
	red, fs := RedactContent(content, ContentScanOptions{IncludePII: true, IncludeSecrets: true})
	if strings.Contains(red, "alice@example.com") || strings.Contains(red, ckAWSKey) {
		t.Fatalf("redacted content leaked a value: %q", red)
	}
	if !strings.Contains(red, "<redacted:pii>") || !strings.Contains(red, "<redacted:secret_leak>") {
		t.Fatalf("expected in-place markers: %q", red)
	}
	for _, keep := range []string{"Email ", " about the plan; key ", " ok."} {
		if !strings.Contains(red, keep) {
			t.Fatalf("prose not preserved (%q): %q", keep, red)
		}
	}
	if len(fs) == 0 {
		t.Fatal("expected findings returned")
	}
}

func TestScanContent_LLMProviderKey(t *testing.T) {
	// Unquoted OpenAI/Anthropic-style key must be caught (LLM-proxy critical).
	for _, c := range []string{
		"OPENAI_API_KEY=sk-demo1234567890ABCDEFitsnotreal",
		"use sk-ant-api03-abcdef0123456789ABCDEF please",
		"stripe sk_live_abcdef0123456789ABCDEF",
	} {
		fs := ScanContent([]byte(c), ContentScanOptions{IncludeSecrets: true})
		if ckTypes(fs)["secret_leak"] == 0 {
			t.Fatalf("expected secret_leak for %q: %+v", c, fs)
		}
	}
}

func TestScanContent_Keyword(t *testing.T) {
	fs := ScanContent([]byte("We discussed Project Titan today."), ContentScanOptions{Keywords: []string{"Project Titan"}})
	if ckTypes(fs)["keyword_match"] == 0 {
		t.Fatalf("expected keyword_match: %+v", fs)
	}
}

func TestScanContent_CustomPattern(t *testing.T) {
	salary := regexp.MustCompile(`\$\d{1,3}(?:,\d{3})+`)
	red, fs := RedactContent([]byte("Comp is $185,000 this year."),
		ContentScanOptions{ExtraPatterns: []NamedContentPattern{{Name: "salary", Pattern: salary}}})
	if ckTypes(fs)["custom"] == 0 {
		t.Fatalf("expected custom finding: %+v", fs)
	}
	if strings.Contains(red, "$185,000") {
		t.Fatalf("salary not redacted: %q", red)
	}
}

func TestScanContent_CardLuhn(t *testing.T) {
	valid := ScanContent([]byte("card 4242 4242 4242 4242 ok"), ContentScanOptions{IncludePII: true})
	if ckTypes(valid)["pii"] == 0 {
		t.Fatalf("valid Luhn card should be detected: %+v", valid)
	}
	invalid := ScanContent([]byte("ref 1234 5678 9012 3456 ok"), ContentScanOptions{IncludePII: true})
	if ckTypes(invalid)["pii"] != 0 {
		t.Fatalf("Luhn-invalid digits should NOT be flagged as a card: %+v", invalid)
	}
}

func TestContentScanOptionsFromEnv(t *testing.T) {
	// Defaults: PII + secrets ON.
	def := ContentScanOptionsFromEnv(func(string) string { return "" })
	if !def.IncludePII || !def.IncludeSecrets {
		t.Fatalf("defaults should enable PII+secrets: %+v", def)
	}
	env := map[string]string{
		"CORDUM_EDGE_LLM_DETECT_PII":      "false",
		"CORDUM_EDGE_LLM_REDACT_KEYWORDS": "Project Titan, Acme ,",
		"CORDUM_EDGE_LLM_REDACT_PATTERNS": `[{"name":"salary","pattern":"\\$\\d+"},{"name":"bad","pattern":"("}]`,
	}
	opts := ContentScanOptionsFromEnv(func(k string) string { return env[k] })
	if opts.IncludePII {
		t.Fatalf("DETECT_PII=false should disable PII: %+v", opts)
	}
	if !opts.IncludeSecrets {
		t.Fatalf("secrets should remain on by default: %+v", opts)
	}
	if len(opts.Keywords) != 2 {
		t.Fatalf("keywords parse wrong: %v", opts.Keywords)
	}
	if len(opts.ExtraPatterns) != 1 || opts.ExtraPatterns[0].Name != "salary" {
		t.Fatalf("only the valid named pattern should survive: %+v", opts.ExtraPatterns)
	}
}

// TestRedactContent_OverlapMergesSpans reproduces the leak from a partial
// overlap: finding A covers offset 0-10, finding B covers offset 5-15 (its
// tail, chars 10-15, extends past A's end). Before the fix, the merge loop
// left cursor at A's end (10) when it skipped B, so B's tail ("BBBBB") was
// copied into the output verbatim on the next write. The fix must advance
// cursor to max(cursor, B.Offset+B.Length) so the whole overlapping region
// is folded into one redacted span.
func TestRedactContent_OverlapMergesSpans(t *testing.T) {
	content := []byte("AAAAAAAAAABBBBB_TAIL_SECRET_XYZ")
	outer := regexp.MustCompile(`A{10}`)      // matches offset 0-10
	inner := regexp.MustCompile(`A{5}B{5}`)   // matches offset 5-15 (tail "BBBBB" beyond outer's end)
	red, fs := RedactContent(content, ContentScanOptions{
		ExtraPatterns: []NamedContentPattern{
			{Name: "outer", Pattern: outer},
			{Name: "inner", Pattern: inner},
		},
	})
	if len(fs) != 2 {
		t.Fatalf("expected both overlapping findings reported, got %+v", fs)
	}
	if strings.Contains(red, "BBBBB") {
		t.Fatalf("overlapping finding's tail leaked unredacted: %q", red)
	}
	if !strings.Contains(red, "<redacted:custom>") {
		t.Fatalf("expected a redaction marker: %q", red)
	}
	if !strings.Contains(red, "_TAIL_SECRET_XYZ") {
		t.Fatalf("trailing non-matched text should be preserved: %q", red)
	}
	if got, want := red, "<redacted:custom>_TAIL_SECRET_XYZ"; got != want {
		t.Fatalf("unexpected redacted output: got %q want %q", got, want)
	}
}

// TestRedactContent_ContainedFindingDoesNotLeak covers a finding that is
// fully contained within an earlier, larger finding (not just a partial
// tail overlap). The contained finding must not reopen any output.
func TestRedactContent_ContainedFindingDoesNotLeak(t *testing.T) {
	content := []byte("XXXXXYYYYYZZZZZWWWWW")
	outer := regexp.MustCompile(`XXXXXYYYYYZZZZZWWWWW`) // offset 0-20
	inner := regexp.MustCompile(`YYYYYZZZZZ`)            // offset 5-15, fully inside outer
	red, fs := RedactContent(content, ContentScanOptions{
		ExtraPatterns: []NamedContentPattern{
			{Name: "outer", Pattern: outer},
			{Name: "inner", Pattern: inner},
		},
	})
	if len(fs) != 2 {
		t.Fatalf("expected both findings reported, got %+v", fs)
	}
	if red != "<redacted:custom>" {
		t.Fatalf("contained finding should not leak or reopen output: got %q", red)
	}
}

// TestRedactContent_AdjacentFindingsNotMerged is a regression guard: two
// findings that merely touch (one ends exactly where the next begins) are
// NOT overlapping and must both produce their own redaction marker rather
// than being folded into a single span.
func TestRedactContent_AdjacentFindingsNotMerged(t *testing.T) {
	content := []byte("PPPPPQQQQQ")
	first := regexp.MustCompile(`P{5}`)  // offset 0-5
	second := regexp.MustCompile(`Q{5}`) // offset 5-10, adjacent (not overlapping)
	red, fs := RedactContent(content, ContentScanOptions{
		ExtraPatterns: []NamedContentPattern{
			{Name: "first", Pattern: first},
			{Name: "second", Pattern: second},
		},
	})
	if len(fs) != 2 {
		t.Fatalf("expected both adjacent findings reported, got %+v", fs)
	}
	if want := "<redacted:custom><redacted:custom>"; red != want {
		t.Fatalf("adjacent findings should each get their own marker: got %q want %q", red, want)
	}
	if got := strings.Count(red, "<redacted:custom>"); got != 2 {
		t.Fatalf("expected 2 separate redaction markers, got %d in %q", got, red)
	}
}

func TestScanContent_EmptyAndZeroOptions(t *testing.T) {
	if fs := ScanContent(nil, ContentScanOptions{IncludePII: true}); fs != nil {
		t.Fatalf("empty content should yield no findings: %+v", fs)
	}
	// Zero options scan nothing even on sensitive content.
	if fs := ScanContent([]byte(ckAWSKey), ContentScanOptions{}); len(fs) != 0 {
		t.Fatalf("zero options must scan nothing: %+v", fs)
	}
}
