package safetykernel

import (
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// normalizationMetadata captures what input normalization did to a candidate.
// Only counts and mode flags are recorded; raw and normalized payload text
// must never be placed on this struct so it is safe to attach to findings.
type normalizationMetadata struct {
	changed                bool
	nfkcChanged            bool
	strippedZeroWidthCount int
	strippedBidiCount      int
}

// normalizationResult is the transient candidate set produced by
// normalizeInputCandidates. raw is always preserved verbatim. normalized is
// nil unless NFKC or zero-width/bidi stripping changed the content; callers
// must scan raw first and only fall back to normalized when hasNormalized
// returns true.
type normalizationResult struct {
	raw        []byte
	normalized []byte
	metadata   normalizationMetadata
}

// hasNormalized reports whether a distinct normalized candidate is available.
func (r normalizationResult) hasNormalized() bool {
	return r.metadata.changed && len(r.normalized) > 0
}

// normalizationModes returns a comma-separated list of modes that fired during
// normalization, suitable for safe finding metadata. Empty when nothing changed.
func (r normalizationResult) modes() string {
	if !r.metadata.changed {
		return ""
	}
	parts := make([]byte, 0, 32)
	if r.metadata.nfkcChanged {
		parts = append(parts, "nfkc"...)
	}
	if r.metadata.strippedZeroWidthCount > 0 {
		if len(parts) > 0 {
			parts = append(parts, '+')
		}
		parts = append(parts, "zero_width"...)
	}
	if r.metadata.strippedBidiCount > 0 {
		if len(parts) > 0 {
			parts = append(parts, '+')
		}
		parts = append(parts, "bidi"...)
	}
	return string(parts)
}

// isZeroWidthControl reports whether r is a zero-width or BOM-style formatting
// control that must be stripped before scanner evaluation.
func isZeroWidthControl(r rune) bool {
	switch r {
	case 0x200B, // ZERO WIDTH SPACE
		0x200C, // ZERO WIDTH NON-JOINER
		0x200D, // ZERO WIDTH JOINER
		0x2060, // WORD JOINER
		0xFEFF: // ZERO WIDTH NO-BREAK SPACE / BOM
		return true
	}
	return false
}

// isBidiControl reports whether r is a Unicode bidirectional formatting
// control that must be stripped before scanner evaluation.
func isBidiControl(r rune) bool {
	switch r {
	case 0x200E, 0x200F: // LRM, RLM
		return true
	}
	if r >= 0x202A && r <= 0x202E { // LRE, RLE, PDF, LRO, RLO
		return true
	}
	if r >= 0x2066 && r <= 0x2069 { // LRI, RLI, FSI, PDI
		return true
	}
	return false
}

// stripZeroWidthAndBidi removes zero-width and bidi formatting controls from
// s and returns the cleaned text alongside per-category counts.
//
// Invalid UTF-8 byte sequences are preserved verbatim so the helper never
// silently corrupts content; the strip pass only removes the explicit control
// runes listed above.
func stripZeroWidthAndBidi(s string) (string, int, int) {
	if !mayContainTargetControl(s) {
		return s, 0, 0
	}
	zwCount := 0
	bidiCount := 0
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			out = append(out, s[i])
			i++
			continue
		}
		switch {
		case isZeroWidthControl(r):
			zwCount++
		case isBidiControl(r):
			bidiCount++
		default:
			out = append(out, s[i:i+size]...)
		}
		i += size
	}
	if zwCount == 0 && bidiCount == 0 {
		return s, 0, 0
	}
	return string(out), zwCount, bidiCount
}

// mayContainTargetControl is a fast pre-check that returns true when s might
// contain one of our target controls. All in-scope controls (U+200B..U+200F,
// U+202A..U+202E, U+2060, U+2066..U+2069, U+FEFF) start with the UTF-8 lead
// bytes 0xE2 or 0xEF, so an ASCII-only buffer can short-circuit the rune
// decode loop entirely.
func mayContainTargetControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0xE2 || s[i] == 0xEF {
			return true
		}
	}
	return false
}

// scanWithNormalization runs scan on the raw candidate first and, when a
// distinct normalized candidate is available, runs it on the normalized
// candidate too. Findings are deduplicated by (Type, Scanner, Detail,
// MatchedPattern). It reports whether any finding came exclusively from the
// normalized candidate so callers can attach a metadata-only normalization
// finding to the rule.
func scanWithNormalization(
	cands normalizationResult,
	scan func(content []byte) []outputFinding,
) ([]outputFinding, bool) {
	rawFindings := scan(cands.raw)
	if !cands.hasNormalized() {
		return rawFindings, false
	}
	normFindings := scan(cands.normalized)
	if len(normFindings) == 0 {
		return rawFindings, false
	}
	type fpKey struct {
		typ, scanner, detail, matchedPattern string
	}
	seen := make(map[fpKey]struct{}, len(rawFindings))
	for _, f := range rawFindings {
		seen[fpKey{f.Type, f.Scanner, f.Detail, f.MatchedPattern}] = struct{}{}
	}
	out := append([]outputFinding(nil), rawFindings...)
	normalizedOnly := false
	for _, f := range normFindings {
		k := fpKey{f.Type, f.Scanner, f.Detail, f.MatchedPattern}
		if _, dup := seen[k]; dup {
			continue
		}
		out = append(out, f)
		normalizedOnly = true
	}
	return out, normalizedOnly
}

// normalizationFinding builds a metadata-only finding that describes which
// normalization modes fired during scanning. It carries no raw or normalized
// payload text — only mode names — so it is safe to surface in audit reasons.
func normalizationFinding(severity string, cands normalizationResult) outputFinding {
	return outputFinding{
		Type:     "input_normalized",
		Severity: severity,
		Detail:   "input normalized for scanner evaluation (" + cands.modes() + ")",
		Scanner:  "input_normalization",
	}
}

// normalizeInputCandidates returns the raw input plus an optional normalized
// candidate suitable for scanner evaluation. The raw slice is preserved
// verbatim for audit. The normalized slice is populated only when NFKC or
// zero-width/bidi stripping changed the content, and is intended only for
// transient in-memory scanner evaluation — callers must not log it as part
// of audit/evidence content.
func normalizeInputCandidates(raw []byte) normalizationResult {
	if len(raw) == 0 {
		return normalizationResult{raw: raw}
	}
	rawStr := string(raw)
	nfkcChanged := !norm.NFKC.IsNormalString(rawStr)
	working := rawStr
	if nfkcChanged {
		working = norm.NFKC.String(rawStr)
	}
	stripped, zwCount, bidiCount := stripZeroWidthAndBidi(working)
	changed := nfkcChanged || zwCount > 0 || bidiCount > 0
	if !changed {
		return normalizationResult{raw: raw}
	}
	return normalizationResult{
		raw:        raw,
		normalized: []byte(stripped),
		metadata: normalizationMetadata{
			changed:                true,
			nfkcChanged:            nfkcChanged,
			strippedZeroWidthCount: zwCount,
			strippedBidiCount:      bidiCount,
		},
	}
}
