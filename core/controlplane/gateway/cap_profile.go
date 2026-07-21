package gateway

import (
	"fmt"
	"log/slog"

	"github.com/cordum/cordum/core/infra/capprofile"
)

func enforceGatewayProductionReadiness(profile capprofile.Profile, r capprofile.Readiness) error {
	if !profile.IsProduction() {
		return nil
	}
	if err := r.Validate(); err != nil {
		return fmt.Errorf("CAP-PRODUCTION selected but gateway dependencies are not initialized: %w", err)
	}
	return nil
}

// gatewayProductionReadiness reports which CAP-PRODUCTION dependencies this
// binary has actually initialized.
//
// The gateway is an ingress, not a raw-packet admission boundary: it does not
// yet install exact-wire admission, a local signing-key trust store, a
// transport session resolver, an outbound production signer, or a resource
// resolver allowlist. Those are reported not-ready so that selecting
// CORDUM_CAP_PROFILE=production refuses to start with a precise list, instead
// of advertising a profile the gateway cannot enforce.
// The gateway also does not own the Safety Kernel or output-safety clients --
// the scheduler evaluates policy -- so those are reported not-ready here too.
// Only the authenticated handshake is something this binary genuinely proves.
func gatewayProductionReadiness(handshake gatewayHandshakeSecurity) capprofile.Readiness {
	return capprofile.Readiness{
		HandshakeEnforced: handshake.mode.EnforcesHandshake(),

		// Not wired in the gateway; see the doc comment above.
		RawAdmissionInstalled:  false,
		ReplayStoreReady:       false,
		TrustStoreReady:        false,
		SessionResolverReady:   false,
		OutboundSignerReady:    false,
		ResourceAllowlistReady: false,
		SafetyKernelReady:      false,
		OutputSafetyEnabled:    false,
		FailClosedModes:        false,
	}
}

// logGatewayProfileActivation emits the compatibility telemetry an operator
// needs to see that CAP-PRODUCTION is NOT active, without logging any packet
// bytes, tokens, or signatures.
func logGatewayProfileActivation(profile capprofile.Profile, r capprofile.Readiness) {
	if profile.AdvertiseProduction(r) {
		slog.Info("CAP profile active",
			"component", "api-gateway",
			"profile", profile.String(),
			"cap_production_advertised", true)
		return
	}
	slog.Warn("CAP-PRODUCTION not active; compatibility surfaces remain reachable",
		"component", "api-gateway",
		"profile", profile.String(),
		"cap_production_advertised", false,
		"env", capprofile.EnvVar)
}
