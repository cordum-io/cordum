package safetykernel

import (
	"strings"
	"unicode"
)

// InjectionFinding is the exported, package-agnostic shape of a prompt-injection
// hit. It mirrors only the fields a downstream consumer needs to cite a finding
// (notably the MCP content session-taint path), decoupling callers from the
// unexported internal outputFinding type. Produced by ScanForPromptInjection.
type InjectionFinding struct {
	// Pattern is the human-readable label of the matched rule (e.g.
	// "ignore previous instructions"), not the raw regex source.
	Pattern string
	// Snippet is a bounded, control-char-stripped excerpt of the matched
	// (attacker-controlled) content. Safe to surface in a DENY reason, audit
	// Extra, or log line without risking log injection or unbounded growth.
	Snippet string
	// Severity is the scanner severity for the matched rule (e.g. "high").
	Severity string
	// Confidence is the scanner confidence in [0,1].
	Confidence float64
}

const (
	// maxPromptInjectionScanBytes caps how much content the shared scanner
	// inspects per call so an attacker-supplied huge tool-call result cannot
	// burn CPU. RE2 is linear in input, so 1 MiB is microseconds-cheap while
	// still covering any realistic board read; content beyond it is ignored
	// (acceptable: the demo injection lives in an early item).
	maxPromptInjectionScanBytes = 1 << 20 // 1 MiB
	// maxInjectionSnippetBytes bounds the cited snippet length.
	maxInjectionSnippetBytes = 256
)

// ScanForPromptInjection is the single exported entry point onto the SHIPPED
// prompt-injection scanner (newPromptInjectionScanner — the same 7-pattern
// detector the Safety Kernel input rule uses). It lets callers outside this
// package (notably the MCP gateway action-gate path, which never invokes the
// kernel) reuse the exact shipped detection without importing the unexported
// scanner or duplicating its patterns. Callers wire it in as a dependency at
// gateway wiring time, so core/mcp need not import safetykernel (avoiding an
// import cycle).
//
// It is read-only and side-effect-free: callers decide what to do with a hit
// (e.g. persist a session taint). Returns nil when content is empty or clean.
func ScanForPromptInjection(content []byte) []InjectionFinding {
	if len(content) == 0 {
		return nil
	}
	if len(content) > maxPromptInjectionScanBytes {
		content = content[:maxPromptInjectionScanBytes]
	}
	raw := newPromptInjectionScanner().Scan(content)
	if len(raw) == 0 {
		return nil
	}
	out := make([]InjectionFinding, 0, len(raw))
	for _, f := range raw {
		out = append(out, InjectionFinding{
			Pattern:    f.Detail,
			Snippet:    sanitizeSnippet(f.MatchedPattern, maxInjectionSnippetBytes),
			Severity:   f.Severity,
			Confidence: float64(f.Confidence),
		})
	}
	return out
}

// sanitizeSnippet strips control characters and collapses/bounds whitespace in
// attacker-controlled matched content before it is surfaced in a decision
// reason, audit Extra, or log line — preventing log injection and unbounded
// growth. Multi-byte runes are preserved; a trailing partial rune left by the
// byte cap is dropped.
func sanitizeSnippet(raw string, limit int) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r):
			return -1 // drop other control runes
		default:
			return r
		}
	}, raw)
	out := strings.Join(strings.Fields(mapped), " ")
	if len(out) > limit {
		out = strings.ToValidUTF8(out[:limit], "")
	}
	return out
}
