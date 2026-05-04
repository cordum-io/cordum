package claude

import (
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxDiagnosticLen = 240

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)Authorization\s*:\s*Bearer\s+[^\s,;}]+`),
	regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|credential)(\s*[=:]\s*|"\s*:\s*")[^\s",;}]+`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]+`),
	regexp.MustCompile(`ghp_[A-Za-z0-9_]+`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(?i)\b[0-9a-f]{32}\b`),
	regexp.MustCompile(`\b[A-Za-z0-9_-]{43}\b`),
}

var base64DiagnosticPattern = regexp.MustCompile(`\b[A-Za-z0-9/+=]{40,}\b`)

func redactDiagnostic(input string) string {
	out := strings.ReplaceAll(input, "\r", " ")
	out = strings.ReplaceAll(out, "\n", " ")
	out = strings.ReplaceAll(out, "\t", " ")
	for _, re := range redactionPatterns {
		out = re.ReplaceAllString(out, "[REDACTED]")
	}
	out = redactHighEntropyBase64Diagnostics(out)
	out = strings.Join(strings.Fields(out), " ")
	if len(out) > maxDiagnosticLen {
		// Round down to a UTF-8 rune boundary so multi-byte runes are never split.
		cutoff := maxDiagnosticLen
		for cutoff > 0 && !utf8.RuneStart(out[cutoff]) {
			cutoff--
		}
		out = out[:cutoff] + "..."
	}
	return out
}

func redactHighEntropyBase64Diagnostics(input string) string {
	return base64DiagnosticPattern.ReplaceAllStringFunc(input, func(token string) string {
		trimmed := strings.TrimRight(token, "=")
		if trimmed == "" || !strings.ContainsAny(token, "/+=") {
			return token
		}
		if isHexDiagnosticToken(trimmed) || diagnosticEntropy(trimmed) < 4.5 {
			return token
		}
		return "[REDACTED]"
	})
}

func isHexDiagnosticToken(token string) bool {
	for _, ch := range token {
		if !strings.ContainsRune("0123456789abcdefABCDEF", ch) {
			return false
		}
	}
	return true
}

func diagnosticEntropy(token string) float64 {
	if token == "" {
		return 0
	}
	counts := map[rune]int{}
	for _, ch := range token {
		counts[ch]++
	}
	var entropy float64
	total := float64(len([]rune(token)))
	for _, count := range counts {
		p := float64(count) / total
		entropy -= p * math.Log2(p)
	}
	return entropy
}

func safeID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	redacted := redactDiagnostic(trimmed)
	// EDGE-049: drop the broad substring "secret" check; redactDiagnostic
	// already produces the [REDACTED] marker for actual leak vectors via
	// the EDGE-004 regex patterns (bearer/sk-/AKIA/secret=value/etc.).
	// The bare strings.Contains(..., "secret") confused CONTEXT with
	// CONTENT and wholesale-replaced legitimate IDs that happen to contain
	// the substring (e.g., session IDs like "secret-rotation-bot-001").
	// Sibling fix: EDGE-046 (mapper.go:594 redactHookBoundaryString).
	if strings.Contains(redacted, "[REDACTED]") {
		return "[REDACTED]"
	}
	if len(redacted) <= 8 {
		return redacted
	}
	return redacted[:8] + "..."
}
