package model

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxAgentNameLen bounds the human-facing agent display label surfaced in
// registries, worker status, and audit events. Labels longer than this are
// truncated by SanitizeAgentName. It is a rendering bound, NOT a security
// limit: a display label is never an authentication authority.
const MaxAgentNameLen = 128

// SanitizeAgentName normalizes a self-reported agent display label for safe,
// bounded transport and rendering. It trims surrounding whitespace, collapses
// internal runs of whitespace (including newlines and tabs) to a single space,
// drops non-space control characters and invalid runes that could break log
// lines or audit summaries, and truncates to MaxAgentNameLen runes.
//
// The result is a DISPLAY label only. Callers MUST prefer authenticated
// identity records over this value and MUST NOT treat it as proof of identity.
// It mirrors capsdk.SanitizeAgentName on the CAP SDK side so a label is bounded
// identically whether it arrives over the wire or via an explicit cordum label.
func SanitizeAgentName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	lastSpace := false
	for _, r := range name {
		switch {
		case r == utf8.RuneError:
			continue // drop invalid bytes / replacement characters
		case unicode.IsSpace(r):
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		case unicode.IsControl(r):
			continue // drop non-space control characters (NUL, ESC, ...)
		default:
			b.WriteRune(r)
			lastSpace = false
		}
	}
	out := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(out) > MaxAgentNameLen {
		out = strings.TrimSpace(string([]rune(out)[:MaxAgentNameLen]))
	}
	return out
}
