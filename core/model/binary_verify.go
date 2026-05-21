// Package model — EDGE-151-DASHBOARD: BinaryVerifyEvent.
//
// Mirror of the JSON-line shape that tools/scripts/install.sh `emit_audit`
// (line 24-32) writes to stderr per docs/security/binary-signing.md §8.
// Schema is frozen — downstream SIEM mappings pin to the 7 field names + order.
//
// Receive flow: operators capture install-script stderr, batch-upload via
// the gateway POST endpoint /api/v1/edge/binary-integrity/events. The gateway
// validates + persists each event into the existing audit-bus (audit.Chainer.Append)
// as a SIEMEvent with EventType="binary-verify-ok"|"binary-verify-fail".

package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// BinaryVerify field constants. The accepted enum sets are pinned by the
// install scripts (read-only per the EDGE-151-DASHBOARD task rail #1) — if
// install.sh ever adds a new sig_scheme, this enum + a focused validator test
// must be updated in lockstep.
const (
	BinaryVerifyEventOK   = "binary-verify-ok"
	BinaryVerifyEventFail = "binary-verify-fail"

	BinaryVerifySigSchemeGPG          = "gpg"
	BinaryVerifySigSchemeCodesign     = "codesign"
	BinaryVerifySigSchemeAuthenticode = "authenticode"
	BinaryVerifySigSchemeDev          = "dev"

	// MaxBinaryVerifyReasonLen caps the reason field — the install scripts
	// only emit controlled BINARY-VERIFY-FAIL text, but operator relays
	// might splice in gpg stderr (which can contain absolute paths).
	// Truncate defensively so a path leak can't slip through into an
	// indexed audit row.
	MaxBinaryVerifyReasonLen = 512

	// MaxBinaryVerifyPathLen caps the path field. install.sh's
	// reject_path() refuses anything that's not a manifest-relative
	// basename, so 256 chars is plenty.
	MaxBinaryVerifyPathLen = 256
)

// BinaryVerifyEvent is one structured outcome from the pre-activation
// integrity gate in tools/scripts/install.{sh,ps1}. Field names + order
// MUST stay aligned with install.sh emit_audit (line 24-32) — drift breaks
// downstream SIEM mappings per docs/security/binary-signing.md §8.
type BinaryVerifyEvent struct {
	Event       string `json:"event"`
	Hash        string `json:"hash"`
	Path        string `json:"path"`
	SigScheme   string `json:"sig_scheme"`
	Fingerprint string `json:"fingerprint"`
	Reason      string `json:"reason"`
	ExitCode    int    `json:"exit_code"`
}

var (
	// hashSHA256Pattern matches a 64-char lowercase-hex SHA-256 digest.
	hashSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

	// fingerprintGPGPattern matches a 40-char uppercase-hex GPG
	// fingerprint. Empty fingerprint is also valid (codesign / authenticode
	// emit no fingerprint per the install-script contract).
	fingerprintGPGPattern = regexp.MustCompile(`^[A-F0-9]{40}$`)
)

// ErrInvalidBinaryVerify is the sentinel returned by Validate. Callers
// surface this as HTTP 400; per-field reasons attach via fmt.Errorf("%w: ...").
var ErrInvalidBinaryVerify = errors.New("invalid binary-verify event")

// Validate enforces the schema. Returns nil on success; wraps
// ErrInvalidBinaryVerify with a per-field reason otherwise. Validate is
// pure and safe to call on un-trusted decoded input.
func (e BinaryVerifyEvent) Validate() error {
	switch e.Event {
	case BinaryVerifyEventOK, BinaryVerifyEventFail:
	default:
		return fmt.Errorf("%w: event must be %q or %q, got %q",
			ErrInvalidBinaryVerify, BinaryVerifyEventOK, BinaryVerifyEventFail, e.Event)
	}
	if !hashSHA256Pattern.MatchString(e.Hash) {
		return fmt.Errorf("%w: hash must match ^[a-f0-9]{64}$ (sha256 hex)", ErrInvalidBinaryVerify)
	}
	if err := validateBinaryVerifyPath(e.Path); err != nil {
		return fmt.Errorf("%w: path: %v", ErrInvalidBinaryVerify, err)
	}
	switch e.SigScheme {
	case BinaryVerifySigSchemeGPG,
		BinaryVerifySigSchemeCodesign,
		BinaryVerifySigSchemeAuthenticode,
		BinaryVerifySigSchemeDev:
	default:
		return fmt.Errorf("%w: sig_scheme must be one of gpg|codesign|authenticode|dev, got %q",
			ErrInvalidBinaryVerify, e.SigScheme)
	}
	if e.Fingerprint != "" && !fingerprintGPGPattern.MatchString(e.Fingerprint) {
		return fmt.Errorf("%w: fingerprint must be empty or match ^[A-F0-9]{40}$ (uppercase gpg)",
			ErrInvalidBinaryVerify)
	}
	if e.SigScheme == BinaryVerifySigSchemeGPG && e.Fingerprint == "" {
		return fmt.Errorf("%w: fingerprint required when sig_scheme is gpg",
			ErrInvalidBinaryVerify)
	}
	if len(e.Reason) > MaxBinaryVerifyReasonLen {
		return fmt.Errorf("%w: reason exceeds %d chars",
			ErrInvalidBinaryVerify, MaxBinaryVerifyReasonLen)
	}
	if e.Event == BinaryVerifyEventOK && e.ExitCode != 0 {
		return fmt.Errorf("%w: exit_code must be 0 when event is binary-verify-ok", ErrInvalidBinaryVerify)
	}
	if e.Event == BinaryVerifyEventFail && e.ExitCode == 0 {
		return fmt.Errorf("%w: exit_code must be non-zero when event is binary-verify-fail", ErrInvalidBinaryVerify)
	}
	return nil
}

// IsFailure returns true when the event represents a verification failure.
// Convenience for downstream consumers that pick severity based on outcome.
func (e BinaryVerifyEvent) IsFailure() bool {
	return e.Event == BinaryVerifyEventFail
}

// validateBinaryVerifyPath mirrors install.sh reject_path (line 47-57):
// refuse absolute paths, drive-rooted paths, and any segment containing `..`.
// install.sh already enforces this at emit time; revalidating at the
// receive boundary is defense-in-depth in case a relay re-shapes the field.
func validateBinaryVerifyPath(p string) error {
	if p == "" {
		return errors.New("must be non-empty (manifest-relative basename)")
	}
	if len(p) > MaxBinaryVerifyPathLen {
		return fmt.Errorf("must not exceed %d chars", MaxBinaryVerifyPathLen)
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return errors.New("must not be absolute")
	}
	if len(p) >= 2 && isAlphaASCII(p[0]) && p[1] == ':' {
		return errors.New("must not be drive-rooted (Windows)")
	}
	// reject_path checks the path for "/../" segments; mirror the same shape
	// by inspecting normalised slash boundaries.
	normalised := "/" + strings.ReplaceAll(p, `\`, "/") + "/"
	if strings.Contains(normalised, "/../") {
		return errors.New("must not contain parent-traversal segments")
	}
	return nil
}

func isAlphaASCII(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
