package k8s

import (
	"strings"

	"github.com/cordum/cordum/core/edge/shadow"
)

// maxFieldBytes caps the size of every persisted string field this
// package writes. Longer inputs are truncated with a sentinel suffix
// per design doc §5.3.
const maxFieldBytes = 2048

// redactField is the universal entry point for every string the
// detector persists. It delegates the secret-shape scrub to
// shadow.StripSecretMarkers — the single source of truth for the
// 8-pattern + homoglyph-hyphen + ROT13 + base64 redaction pipeline —
// then applies the k8s-specific maxFieldBytes cap. Detector code MUST
// funnel every caller-visible string through redactField — never
// through fmt.Sprintf directly — so a single contract change in
// shadow propagates here automatically.
func redactField(s string) string {
	s = shadow.StripSecretMarkers(s)
	if len(s) > maxFieldBytes {
		s = s[:maxFieldBytes-len(" …truncated")] + " …truncated"
	}
	return s
}

// imageTagSafe returns the registry+name portion of a container image
// string with the tag scrubbed if it shows secret-shape (direct,
// homoglyph, ROT13, or base64). Plain semver tags pass through
// unchanged; anything that the shared shadow redactor would scrub
// degrades to "<image>:<redacted>".
func imageTagSafe(image string) string {
	if image == "" {
		return ""
	}
	colon := strings.LastIndex(image, ":")
	at := strings.Index(image, "@")
	if at > 0 {
		image = image[:at] // drop @sha256:... digest, not a leak but noisy
	}
	if colon <= 0 || colon >= len(image) {
		return redactField(image)
	}
	base := image[:colon]
	tag := image[colon+1:]
	if shadow.StripSecretMarkers(tag) != tag {
		return redactField(base) + ":<redacted>"
	}
	return redactField(base) + ":" + redactField(tag)
}

// leadingToken returns the first space-and-shell-meta-stripped token
// of an arg or command slice entry. Per design doc §5.2 the detector
// captures ONLY this leading token — never the subsequent values.
func leadingToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		s = s[:i]
	}
	return s
}
