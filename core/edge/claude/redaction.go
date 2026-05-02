package claude

import (
	"regexp"
	"strings"
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
	regexp.MustCompile(`[A-Za-z0-9/+=]{40,}`),
}

func redactDiagnostic(input string) string {
	out := strings.ReplaceAll(input, "\r", " ")
	out = strings.ReplaceAll(out, "\n", " ")
	out = strings.ReplaceAll(out, "\t", " ")
	for _, re := range redactionPatterns {
		out = re.ReplaceAllString(out, "[REDACTED]")
	}
	out = strings.Join(strings.Fields(out), " ")
	if len(out) > maxDiagnosticLen {
		out = out[:maxDiagnosticLen] + "..."
	}
	return out
}

func safeID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	redacted := redactDiagnostic(trimmed)
	if strings.Contains(redacted, "[REDACTED]") || strings.Contains(strings.ToLower(trimmed), "secret") {
		return "[REDACTED]"
	}
	if len(redacted) <= 8 {
		return redacted
	}
	return redacted[:8] + "..."
}
