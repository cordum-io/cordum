package policysign

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// ErrEnforceMissingSigningKey is returned by CheckGatewayBoot when
// enforce mode is active but no signing key is configured. The gateway
// must refuse to start because it cannot sign bundles.
var ErrEnforceMissingSigningKey = errors.New("policysign: CORDUM_POLICY_STRICT=enforce requires CORDUM_POLICY_SIGNING_KEY or CORDUM_POLICY_SIGNING_KEY_PATH")

// ErrEnforceMissingTrustStore is returned by CheckKernelBoot when
// enforce mode is active but no trusted public keys are registered.
// The kernel must refuse to start because it cannot verify bundles.
var ErrEnforceMissingTrustStore = errors.New("policysign: CORDUM_POLICY_STRICT=enforce requires at least one CORDUM_POLICY_PUBLIC_KEY_<ID>")

// CheckGatewayBoot validates the gateway's policy-signing configuration
// at boot. It returns an error in enforce mode when no signing key is
// configured (the gateway cannot sign bundles without one).
//
// In all modes it emits exactly one structured INFO line describing the
// active mode and key_id (when applicable) so operators can confirm the
// rollout state in boot logs.
func CheckGatewayBoot() error {
	mode, modeErr := ModeFromEnv()
	if modeErr != nil {
		slog.Warn("policy signing: invalid CORDUM_POLICY_STRICT; defaulting to warn", "err", modeErr)
	}

	priv, keyID, err := LoadPrivateKeyFromEnv()
	// Malformed keys are fatal regardless of mode — the operator set
	// the env var explicitly, so silently running unsigned would hide
	// the misconfiguration.
	if err != nil && !errors.Is(err, ErrSigningKeyNotConfigured) {
		slog.Error("policy signing: signing key failed to parse",
			"component", "gateway",
			"mode", mode.String(),
			"err", err,
		)
		return fmt.Errorf("policysign gateway boot: %w", err)
	}
	keyConfigured := err == nil && len(priv) > 0

	switch {
	case mode == ModeOff:
		slog.Info("policy signing: disabled",
			"component", "gateway",
			"mode", mode.String(),
			"signing_key_configured", keyConfigured,
		)
		return nil
	case mode == ModeEnforce && !keyConfigured:
		slog.Error("policy signing: enforce mode requires signing key",
			"component", "gateway",
			"mode", mode.String(),
		)
		return ErrEnforceMissingSigningKey
	case !keyConfigured:
		slog.Warn("policy signing: no signing key configured; new bundles will be unsigned",
			"component", "gateway",
			"mode", mode.String(),
		)
		return nil
	default:
		slog.Info("policy signing: enabled",
			"component", "gateway",
			"mode", mode.String(),
			"key_id", keyID,
		)
		return nil
	}
}

// CheckKernelBoot validates the safety kernel's trust store at boot.
// It returns an error in enforce mode when the trust store is empty.
//
// In all modes it emits a single structured INFO line summarising the
// active mode and the list of trusted key ids.
func CheckKernelBoot() error {
	mode, modeErr := ModeFromEnv()
	if modeErr != nil {
		slog.Warn("policy signing: invalid CORDUM_POLICY_STRICT; defaulting to warn", "err", modeErr)
	}

	store, err := LoadTrustStoreFromEnv()
	if err != nil {
		slog.Error("policy signing: trust store load failed",
			"component", "kernel",
			"mode", mode.String(),
			"err", err,
		)
		if mode == ModeEnforce {
			return fmt.Errorf("policysign kernel boot: %w", err)
		}
	}
	ids := []string{}
	if store != nil {
		ids = store.IDs()
	}

	switch {
	case mode == ModeOff:
		slog.Info("policy signing: disabled",
			"component", "kernel",
			"mode", mode.String(),
			"trusted_keys", len(ids),
		)
		return nil
	case mode == ModeEnforce && len(ids) == 0:
		slog.Error("policy signing: enforce mode requires at least one trusted public key",
			"component", "kernel",
			"mode", mode.String(),
		)
		return ErrEnforceMissingTrustStore
	case len(ids) == 0:
		slog.Warn("policy signing: no trusted keys; verification will be skipped",
			"component", "kernel",
			"mode", mode.String(),
		)
		return nil
	default:
		slog.Info("policy signing: enabled",
			"component", "kernel",
			"mode", mode.String(),
			"trusted_keys", strings.Join(ids, ","),
		)
		return nil
	}
}
