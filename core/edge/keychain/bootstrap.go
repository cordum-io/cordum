package keychain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// BootstrapMode selects the LoadSecret failure policy. ModeStrict is the
// production default; ModeDev relaxes the keychain requirement so a
// developer can run cordum-agentd against a checkout without provisioning
// secrets into the OS keychain first.
type BootstrapMode int

const (
	// ModeStrict requires every secret to come from the keychain. A miss
	// or backend-unavailable error surfaces verbatim — agentd must fail
	// closed when it cannot source its boot-time credentials securely.
	ModeStrict BootstrapMode = iota

	// ModeDev permits an env-variable fallback when the keychain has no
	// entry for the requested secret. A structured warning is emitted on
	// fallback (with the secret name but NEVER the value) so an operator
	// can spot accidental dev-mode runs in production.
	ModeDev
)

// String implements fmt.Stringer for structured-log attribute clarity.
func (m BootstrapMode) String() string {
	switch m {
	case ModeStrict:
		return "strict"
	case ModeDev:
		return "dev"
	default:
		return fmt.Sprintf("unknown(%d)", int(m))
	}
}

// LoadSecret resolves secretName from kr, applying the policy implied by
// mode. The function NEVER logs the returned value; structured logs
// include only the secret name, the mode, and the resolved source
// ("keychain" / "env-fallback").
//
// ModeStrict semantics:
//   - keychain hit                          → (value, nil)
//   - keychain ErrKeyringNotFound           → ("", ErrKeyringNotFound)
//   - keychain ErrKeyringUnavailable/other  → ("", wrapped error)
//   - envFallback is ignored in strict mode and never read.
//
// ModeDev semantics:
//   - keychain hit                          → (value, nil)
//   - keychain ErrKeyringNotFound + envFallback != ""
//     → (envFallback, nil) + warn log
//   - keychain ErrKeyringNotFound + envFallback == ""
//     → ("", ErrKeyringNotFound)
//   - keychain ErrKeyringUnavailable + envFallback != ""
//     → (envFallback, nil) + warn log
//   - keychain ErrKeyringUnavailable + envFallback == ""
//     → ("", wrapped error)
//
// secretName == "" returns ErrKeyringNotFound without touching kr; this
// makes "this secret is not provisioned in this build" a single error
// path callers can switch on.
func LoadSecret(
	ctx context.Context,
	kr Keyring,
	mode BootstrapMode,
	envFallback string,
	secretName string,
	logger *slog.Logger,
) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if secretName == "" {
		return "", ErrKeyringNotFound
	}
	if kr == nil {
		if mode == ModeStrict {
			return "", fmt.Errorf("%w: nil keychain provider", ErrKeyringUnavailable)
		}
		if envFallback != "" {
			warnEnvFallback(logger, secretName, mode, "nil keychain provider")
			return envFallback, nil
		}
		return "", ErrKeyringNotFound
	}

	value, err := kr.Get(ctx, secretName)
	// Re-wrap any error from a 3rd-party Keyring impl through the safe
	// sentinel-wrapper BEFORE inspecting/logging. Mock keyrings, custom
	// production impls, and platform backends all converge on the same
	// keyringBackendError type so .Error() never echoes raw backend
	// bytes via fmt.Errorf("%w", ...) at the caller.
	safeErr := safeBackendError(err)
	category := classifyBootstrapErr(safeErr)
	switch {
	case err == nil:
		logger.Debug(
			"keychain.load",
			slog.String("secret_name", secretName),
			slog.String("source", "keychain"),
			slog.String("mode", mode.String()),
		)
		return value, nil

	case errors.Is(safeErr, ErrKeyringNotFound):
		if mode == ModeStrict {
			logger.Error(
				"keychain.load.miss",
				slog.String("secret_name", secretName),
				slog.String("mode", mode.String()),
				slog.String("keyring_error_class", category),
			)
			return "", ErrKeyringNotFound
		}
		if envFallback != "" {
			warnEnvFallback(logger, secretName, mode, category)
			return envFallback, nil
		}
		return "", ErrKeyringNotFound

	default:
		// Log a coarse error class rather than err.Error(): a custom
		// Keyring impl could embed the secret value or other sensitive
		// material (raw command output, JWT, base64 token) in its error
		// string and we don't want that flowing to stderr / journald.
		// The keyring_error_class label is sufficient for an operator to
		// tell "permission" from "ipc unavailable" from "other" without
		// exposing payload bytes.
		if mode == ModeStrict {
			logger.Error(
				"keychain.load.unavailable",
				slog.String("secret_name", secretName),
				slog.String("mode", mode.String()),
				slog.String("keyring_error_class", category),
			)
			return "", safeErr
		}
		if envFallback != "" {
			warnEnvFallback(logger, secretName, mode, category)
			return envFallback, nil
		}
		return "", safeErr
	}
}

// classifyBootstrapErr maps an underlying keyring error to a fixed
// vocabulary of operator-readable categories so logs never echo raw
// backend message bytes. Each label corresponds 1:1 to the sentinel
// errors exported by this package.
func classifyBootstrapErr(err error) string {
	switch {
	case errors.Is(err, ErrKeyringUnavailable):
		return "backend_unavailable"
	case errors.Is(err, ErrKeyringPermissionDenied):
		return "permission_denied"
	case errors.Is(err, ErrKeyringNotFound):
		return "secret_not_found"
	default:
		return "keyring_error"
	}
}

func warnEnvFallback(logger *slog.Logger, secretName string, mode BootstrapMode, reason string) {
	logger.Warn(
		"keychain.env_fallback",
		slog.String("secret_name", secretName),
		slog.String("source", "env-fallback"),
		slog.String("mode", mode.String()),
		slog.String("keyring_error_class", reason),
	)
}
