package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/cordum/cordum/core/edge/keychain"
)

// bootstrapSecretBindings declares which agentd-consumed environment
// variables are sourced from the OS keychain and the key under which each
// value is stored. The list is intentionally narrow: only credentials the
// agentd boot path needs synchronously appear here. Per-session secrets
// (CORDUM_AGENTD_HOOK_NONCE on the Claude-hook side) are issued by the
// launcher per execution and never persisted to the keychain by agentd.
var bootstrapSecretBindings = []struct {
	envName     string
	keychainKey string
}{
	{envName: "CORDUM_AGENTD_NONCE", keychainKey: "cordum_agentd_nonce"},
	{envName: "CORDUM_API_KEY", keychainKey: "cordum_api_key"},
}

// loadBootstrapSecrets resolves the agentd boot-time secrets by calling
// keychain.LoadSecret for each binding in bootstrapSecretBindings. It
// returns a copy of env with keychain-sourced values overlaid; the input
// env is not mutated.
//
// The returned error wraps the strict-mode failure with the BOOTSTRAP-FAIL
// prefix expected by service-manager log analyzers (launchd/journald/
// EventLog). Error.Error() and any output to stderr contain only the
// secret name + mode + failure category; secret values are never echoed,
// even when the env fallback is populated.
func loadBootstrapSecrets(
	ctx context.Context,
	kr keychain.Keyring,
	mode keychain.BootstrapMode,
	env map[string]string,
	stderr io.Writer,
) (map[string]string, error) {
	logger := bootstrapLogger(stderr)
	resolved := make(map[string]string, len(env))
	for k, v := range env {
		resolved[k] = v
	}

	for _, b := range bootstrapSecretBindings {
		envFallback := envValue(env, b.envName)
		value, err := keychain.LoadSecret(ctx, kr, mode, envFallback, b.keychainKey, logger)
		if err != nil {
			// Dev mode with both keychain and env unset: preserve the
			// pre-existing pass-through where downstream validators
			// (LoadConfig, ValidateExternalNonce) handle the empty
			// value — empty nonce auto-generates; empty api_key fails
			// LoadConfig with a clear missing-required error. Strict
			// mode keeps the BOOTSTRAP-FAIL contract.
			// Linux CI runners don't run dbus / org.freedesktop.secrets,
			// so LoadSecret returns ErrKeyringUnavailable rather than
			// ErrKeyringNotFound. Treat both as "no secret present" in
			// dev mode when the env fallback is also empty — the test
			// TestDefaultRunLeavesNonceEmptyWhenCordumAgentdNonceUnset
			// pins this contract.
			if mode == keychain.ModeDev && envFallback == "" &&
				(errors.Is(err, keychain.ErrKeyringNotFound) ||
					errors.Is(err, keychain.ErrKeyringUnavailable)) {
				continue
			}
			return nil, formatBootstrapError(b.keychainKey, b.envName, mode, err)
		}
		if value == "" {
			continue
		}
		resolved[b.envName] = value
	}
	return resolved, nil
}

// resolveBootstrapMode picks ModeStrict whenever the operator has opted in
// via CORDUM_AGENTD_STRICT=true or the edge policy mode is enterprise-
// strict. Default is ModeDev so an unprovisioned local checkout still
// boots with a banner-warn, matching the pre-existing dev-mode behavior
// agentd already supported under the EDGE-031 plaintext-env path.
func resolveBootstrapMode(env map[string]string) keychain.BootstrapMode {
	if parseBoolEnv(envValue(env, "CORDUM_AGENTD_STRICT")) {
		return keychain.ModeStrict
	}
	if envValue(env, "CORDUM_EDGE_POLICY_MODE") == "enterprise-strict" {
		return keychain.ModeStrict
	}
	return keychain.ModeDev
}

func bootstrapLogger(stderr io.Writer) *slog.Logger {
	if stderr == nil {
		return slog.Default()
	}
	return slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// formatBootstrapError constructs the BOOTSTRAP-FAIL diagnostic without
// echoing any secret material. The error message references the keychain
// key (operator-facing identifier) and the env name (operator-facing
// alternative) plus the failure category, so the operator can choose the
// right remediation (provision the keychain entry, unlock the keychain,
// or run with CORDUM_AGENTD_STRICT=false in development).
func formatBootstrapError(keychainKey, envName string, mode keychain.BootstrapMode, err error) error {
	switch {
	case errors.Is(err, keychain.ErrKeyringUnavailable):
		return fmt.Errorf(
			"BOOTSTRAP-FAIL: keychain unavailable in %s mode for secret %q "+
				"(env %s ignored; set CORDUM_AGENTD_STRICT=false for dev mode "+
				"or provision the keychain entry): %w",
			mode.String(), keychainKey, envName, err,
		)
	case errors.Is(err, keychain.ErrKeyringPermissionDenied):
		return fmt.Errorf(
			"BOOTSTRAP-FAIL: keychain permission denied in %s mode for secret %q "+
				"(grant the agentd process keychain ACL): %w",
			mode.String(), keychainKey, err,
		)
	case errors.Is(err, keychain.ErrKeyringNotFound):
		return fmt.Errorf(
			"BOOTSTRAP-FAIL: secret %q not in keychain (%s mode requires keychain provisioning; "+
				"env %s ignored under strict mode): %w",
			keychainKey, mode.String(), envName, err,
		)
	default:
		return fmt.Errorf(
			"BOOTSTRAP-FAIL: secret %q load failed in %s mode: %w",
			keychainKey, mode.String(), err,
		)
	}
}
