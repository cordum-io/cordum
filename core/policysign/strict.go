package policysign

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// EnvStrictMode controls how aggressively verifiers treat signature
// problems. See Mode for the allowed values.
const EnvStrictMode = "CORDUM_POLICY_STRICT"

// Mode represents the strictness of signature enforcement. The zero
// value is intentionally ModeWarn so callers that forget to parse env
// get the safe middle option.
type Mode int

const (
	// ModeWarn logs verification failures but returns ok. This is the
	// default and the only mode appropriate during a staged rollout.
	ModeWarn Mode = iota

	// ModeOff skips verification entirely. Intended for local development
	// and tests where operators explicitly opt out of signing.
	ModeOff

	// ModeEnforce rejects bundles that are unsigned, malformed, signed by
	// an untrusted key, or whose signature does not match the payload.
	ModeEnforce
)

// ErrInvalidMode is returned by ParseMode for values outside the
// recognised set.
var ErrInvalidMode = errors.New("policysign: invalid strict mode")

// ParseMode normalises raw and returns the matching Mode. Accepted
// values (case-insensitive, whitespace-trimmed):
//
//	""         -> ModeWarn (default)
//	"off"      -> ModeOff
//	"disabled" -> ModeOff (alias)
//	"warn"     -> ModeWarn
//	"warning"  -> ModeWarn (alias)
//	"enforce"  -> ModeEnforce
//	"strict"   -> ModeEnforce (alias)
func ParseMode(raw string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ModeWarn, nil
	case "off", "disabled", "none", "false", "0":
		return ModeOff, nil
	case "warn", "warning":
		return ModeWarn, nil
	case "enforce", "strict", "required", "true", "1":
		return ModeEnforce, nil
	default:
		return ModeWarn, fmt.Errorf("%w: %q", ErrInvalidMode, raw)
	}
}

// ModeFromEnv reads EnvStrictMode and returns the parsed Mode. Unset is
// equivalent to "warn". Malformed values fall back to warn but return
// an error so callers can decide whether to log, abort, or ignore.
func ModeFromEnv() (Mode, error) {
	return ParseMode(os.Getenv(EnvStrictMode))
}

// String implements fmt.Stringer for logs.
func (m Mode) String() string {
	switch m {
	case ModeOff:
		return "off"
	case ModeWarn:
		return "warn"
	case ModeEnforce:
		return "enforce"
	default:
		return fmt.Sprintf("unknown(%d)", int(m))
	}
}

// SkipsVerification reports whether this mode should bypass signature
// checks altogether. Only ModeOff skips.
func (m Mode) SkipsVerification() bool { return m == ModeOff }

// RejectsOnFailure reports whether verification failures should be
// treated as fatal. Only ModeEnforce rejects.
func (m Mode) RejectsOnFailure() bool { return m == ModeEnforce }
