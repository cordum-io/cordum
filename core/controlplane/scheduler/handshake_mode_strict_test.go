package scheduler

import "testing"

// RED coverage for task-948d913b: the strict boot-time parser. The lenient
// ParseHandshakeMode maps any unrecognized value to warn (kept for runtime
// callers); the boot path must instead REFUSE to start on a typo rather than
// silently degrade to admit.
func TestParseHandshakeModeStrict(t *testing.T) {
	t.Parallel()
	valid := map[string]HandshakeMode{
		"":          HandshakeModeOff, // unset stays back-compat (gate disabled)
		"off":       HandshakeModeOff,
		"OFF":       HandshakeModeOff,
		" warn ":    HandshakeModeWarn,
		"warn":      HandshakeModeWarn,
		"enforce":   HandshakeModeEnforce,
		" ENFORCE ": HandshakeModeEnforce,
	}
	for in, want := range valid {
		got, err := ParseHandshakeModeStrict(in)
		if err != nil {
			t.Errorf("ParseHandshakeModeStrict(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseHandshakeModeStrict(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"enforse", "enforce-mode", "true", "bogus", "1", "yes"} {
		if _, err := ParseHandshakeModeStrict(in); err == nil {
			t.Errorf("ParseHandshakeModeStrict(%q) must return an error (no silent degrade to admit)", in)
		}
	}
}
