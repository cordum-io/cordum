// Package keychain provides a thin abstraction over the host operating
// system's native credential store. cordum-agentd uses it to source the
// boot-time secrets (CORDUM_AGENTD_NONCE, CORDUM_API_KEY) from the OS
// keychain instead of the developer-shell environment, closing the
// EDGE-031 tradeoff where same-user `ps -E` / `/proc/<pid>/environ` /
// shell history could expose runtime credentials.
//
// Backends:
//   - macOS: Security framework via `security` CLI (zalando/go-keyring darwin)
//   - Linux: Secret Service / libsecret via godbus
//   - Windows: Credential Manager via wincred (crypt32.dll)
//
// Tests use NewMockKeyring(); production code calls NewOSKeyring() at
// process startup and passes the resulting Keyring into LoadSecret.
package keychain

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	zkeyring "github.com/zalando/go-keyring"
)

// keyringServiceName is the per-host namespace under which cordum-agentd
// stores its secrets. Operators provision values with platform-native CLIs:
//
//	macOS  : security add-generic-password -a cordum_agentd_nonce -s cordum-agentd -w "<base64-nonce>"
//	Linux  : secret-tool store --label="cordum-agentd nonce" service cordum-agentd username cordum_agentd_nonce
//	Windows: cmdkey /generic:cordum-agentd:cordum_agentd_nonce /user:cordum_agentd_nonce /pass:"<base64-nonce>"
//
// The constant is intentionally not exported — callers receive a Keyring
// already bound to this namespace and have no reason to override it.
const keyringServiceName = "cordum-agentd"

// Keyring is the boundary between cordum-agentd's bootstrap flow and the
// underlying OS credential store. Implementations must be goroutine-safe:
// the agentd process may issue concurrent secret lookups during startup.
type Keyring interface {
	// Get returns the secret previously stored under key, or
	// ErrKeyringNotFound when no entry exists. Implementations MUST NOT
	// echo the returned value into any logger, error message, or
	// telemetry — the value flows through return-only.
	Get(ctx context.Context, key string) (string, error)

	// Set writes value under key. Existing values are overwritten. An
	// empty key returns an error without touching the backend.
	Set(ctx context.Context, key string, value string) error

	// Delete removes the entry for key. Deleting a missing key is not an
	// error; it allows idempotent test teardown and key rotation flows.
	Delete(ctx context.Context, key string) error
}

// NewOSKeyring returns a Keyring backed by the host's native credential
// store. Errors from the underlying backend are normalized into the
// package-level sentinels (ErrKeyringNotFound, ErrKeyringUnavailable,
// ErrKeyringPermissionDenied) so callers can dispatch on them without
// depending on platform-specific error strings.
func NewOSKeyring() Keyring {
	return &osKeyring{service: keyringServiceName}
}

type osKeyring struct {
	service string
}

func (o *osKeyring) Get(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if key == "" {
		return "", ErrKeyringNotFound
	}
	value, err := zkeyring.Get(o.service, key)
	if err != nil {
		return "", normalizeBackendError(err)
	}
	return value, nil
}

func (o *osKeyring) Set(ctx context.Context, key, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("keychain: empty key")
	}
	if err := zkeyring.Set(o.service, key, value); err != nil {
		return normalizeBackendError(err)
	}
	return nil
}

func (o *osKeyring) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return nil
	}
	err := zkeyring.Delete(o.service, key)
	if err == nil {
		return nil
	}
	if errors.Is(err, zkeyring.ErrNotFound) {
		return nil
	}
	return normalizeBackendError(err)
}

// normalizeBackendError maps zalando/go-keyring (and underlying OS) errors
// into the keychain package sentinels. The mapping is conservative: any
// error not explicitly recognized as "not found" or "permission denied" is
// classified as ErrKeyringUnavailable, so strict-mode callers fail closed
// rather than continuing with an ambiguous backend state.
//
// The returned error is a *keyringBackendError that (a) carries a redacted
// summary in Error() — third-party backend impls may embed secret-shaped
// substrings in their raw error text and Error() must never echo them —
// and (b) preserves errors.Is for the package sentinels via Unwrap.
func normalizeBackendError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, zkeyring.ErrNotFound) {
		return ErrKeyringNotFound
	}
	// Redact on the ORIGINAL-case bytes — backendErrorRedactPatterns has
	// rules that are intentionally case-sensitive (AWS `\bAKIA[0-9A-Z]{16}\b`,
	// PEM `-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`) and pre-lowercasing them
	// destroys the match, letting backend error text leak AKIA tokens and
	// PEM framing verbatim through Error() → fmt.Errorf("%w") → stderr/
	// journald. Lowercasing is only applied to the classification copy used
	// by the substring switch below, whose targets are all-lowercase tokens.
	msg := err.Error()
	summary := redactBackendError(msg)
	msgLower := strings.ToLower(msg)
	switch {
	case strings.Contains(msgLower, "permission denied"),
		strings.Contains(msgLower, "access denied"),
		strings.Contains(msgLower, "user interaction not allowed"),
		strings.Contains(msgLower, "keychain item not granted"):
		return &keyringBackendError{sentinel: ErrKeyringPermissionDenied, summary: summary}
	case strings.Contains(msgLower, "no such interface"),
		strings.Contains(msgLower, "service_unknown"),
		strings.Contains(msgLower, "could not get session"),
		strings.Contains(msgLower, "no dbus session"),
		strings.Contains(msgLower, "dbus-launch"),
		strings.Contains(msgLower, "secret service"),
		strings.Contains(msgLower, "no credential manager"),
		strings.Contains(msgLower, "element not found"),
		strings.Contains(msgLower, "the system cannot find"):
		return &keyringBackendError{sentinel: ErrKeyringUnavailable, summary: summary}
	default:
		return &keyringBackendError{sentinel: ErrKeyringUnavailable, summary: summary}
	}
}

// keyringBackendError is the safe wrapper returned by the keychain package
// (and used to re-wrap errors from non-osKeyring impls in LoadSecret). Its
// Error() exposes ONLY the sentinel's category and a pre-redacted summary;
// raw backend bytes — which a custom Keyring backend MAY include secret-
// shaped substrings in — never reach stderr/journald via fmt.Errorf("%w").
// Unwrap() preserves the sentinel so callers keep using errors.Is.
type keyringBackendError struct {
	sentinel error  // ErrKeyringUnavailable / ErrKeyringPermissionDenied / ErrKeyringNotFound
	summary  string // already passed through redactBackendError
}

func (e *keyringBackendError) Error() string {
	if e == nil || e.sentinel == nil {
		return "keychain: unknown backend error"
	}
	if e.summary == "" {
		return e.sentinel.Error()
	}
	return e.sentinel.Error() + ": " + e.summary
}

func (e *keyringBackendError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.sentinel
}

// safeBackendError wraps an arbitrary error coming back from a Keyring impl
// (production OS keyring or test mock) into a keyringBackendError whose
// Error() text is guaranteed redacted. Sentinels pass through unchanged.
// Context errors propagate verbatim so callers can errors.Is them.
func safeBackendError(err error) error {
	if err == nil {
		return nil
	}
	// Already-wrapped: nothing to do.
	var be *keyringBackendError
	if errors.As(err, &be) {
		return err
	}
	// Context errors must propagate verbatim so cancellation/timeout
	// stays catchable via errors.Is(err, context.Canceled/DeadlineExceeded).
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch {
	case errors.Is(err, ErrKeyringNotFound):
		return ErrKeyringNotFound
	case errors.Is(err, ErrKeyringPermissionDenied):
		return &keyringBackendError{sentinel: ErrKeyringPermissionDenied, summary: redactBackendError(err.Error())}
	case errors.Is(err, ErrKeyringUnavailable):
		return &keyringBackendError{sentinel: ErrKeyringUnavailable, summary: redactBackendError(err.Error())}
	default:
		return &keyringBackendError{sentinel: ErrKeyringUnavailable, summary: redactBackendError(err.Error())}
	}
}

// redactBackendError keeps the error category visible (D-Bus session, ACL,
// missing-entry) while stripping anything that might look like a secret.
// A custom Keyring implementation that included a raw value, JWT, base64
// or UUID-shaped token in its error string would otherwise propagate that
// material into agentd's stderr / audit log via Errorf("%s: %w", ...).
// The redact passes catch the common high-entropy substring shapes before
// the truncation pass so the surviving message is operator-readable but
// never carries a credential.
func redactBackendError(msg string) string {
	const maxLen = 200
	cleaned := RedactSecretLike(msg)
	if len(cleaned) > maxLen {
		cleaned = cleaned[:maxLen] + "...[truncated]"
	}
	return cleaned
}

// RedactSecretLike removes credential-shaped substrings from raw diagnostic
// text. It is the canonical raw-string redaction primitive for Edge local
// boundaries; callers that need JSON/value-preserving redaction should use
// core/edge.RedactValue instead.
func RedactSecretLike(msg string) string {
	cleaned := strings.ReplaceAll(msg, "\n", " ")
	cleaned = strings.ReplaceAll(cleaned, "\r", " ")
	for _, p := range backendErrorRedactPatterns {
		cleaned = p.MustCompile.ReplaceAllString(cleaned, p.Replacement)
	}
	return cleaned
}

type backendRedactRule struct {
	MustCompile *regexp.Regexp
	Replacement string
}

// backendErrorRedactPatterns scrubs long high-entropy substrings before
// the truncation pass. Ordered most-specific → most-general so a JWT is
// caught before its base64 segments individually trip the generic rule
// and explicit credential markers (Bearer/PEM/AKIA/GitHub) are caught
// before the catch-all hex/base64 rules so the replacement classifier
// reads "[REDACTED:jwt]" instead of a generic "[REDACTED:base64]".
//
// Mirrors core/edge/redaction.go patterns where the surface overlap is
// safe — kept local because that file's helpers operate on JSON/values,
// not raw error-string bytes, and importing core/edge here would create
// a cycle through core/edge → core/edge/keychain → core/edge.
// TODO(post-PR-#276): unify into a single redactBackendString helper
// once the Sub-E argument_redactor/policy_evaluate slice lands.
var backendErrorRedactPatterns = []backendRedactRule{
	// PEM block — match BEGIN/END framing across newlines (which the
	// caller pre-normalizes to spaces). Body is base64+whitespace.
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), "[REDACTED:pem]"},
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*CERTIFICATE-----.*?-----END [A-Z0-9 ]*CERTIFICATE-----`), "[REDACTED:cert]"},
	// Authorization: Bearer <token> header.
	{regexp.MustCompile(`(?i)authorization\s*:\s*bearer\s+[^\s,;}]+`), "[REDACTED:bearer]"},
	// Standalone `Bearer <token>` without the Authorization prefix.
	{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=\-]{8,}\b`), "[REDACTED:bearer]"},
	// AWS access key id — fixed prefix + 16 char body.
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "[REDACTED:aws]"},
	// GitHub token families (classic, OAuth, user, server, refresh,
	// fine-grained PAT). Case-insensitive prefix; tail is alnum+_=-.
	{regexp.MustCompile(`(?i)\b(?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_=-]{8,}\b`), "[REDACTED:github]"},
	// OpenAI / Anthropic style sk- tokens.
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{12,}\b`), "[REDACTED:sk]"},
	// Generic `key=value` / `key:"value"` secret-bearing assignments.
	{regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|credential)(\s*[=:]\s*|"\s*:\s*")[^\s",;}]+`), "[REDACTED:assignment]"},
	// JWT (three base64url segments separated by dots).
	{regexp.MustCompile(`\b[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b`), "[REDACTED:jwt]"},
	// UUID v1..v5.
	{regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "[REDACTED:uuid]"},
	// Long hex run (≥32 chars) — covers SHA256/512 digests + raw key material.
	{regexp.MustCompile(`(?i)\b[0-9a-f]{32,}\b`), "[REDACTED:hex]"},
	// Long base64/base64url run (≥24 chars) — covers raw tokens, nonces,
	// HMAC outputs. Constrained to alnum + url-safe + padding so we don't
	// chew through ordinary English.
	{regexp.MustCompile(`\b[A-Za-z0-9+/_\-]{24,}={0,2}\b`), "[REDACTED:base64]"},
}
