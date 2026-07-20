package workflow

import (
	"fmt"
	"log/slog"

	"github.com/cordum/cordum/core/infra/capprofile"
)

func enforceWorkflowProductionReadiness(profile capprofile.Profile, r capprofile.Readiness) error {
	if !profile.IsProduction() {
		return nil
	}
	if err := r.Validate(); err != nil {
		return fmt.Errorf("CAP-PRODUCTION selected but workflow-engine dependencies are not initialized: %w", err)
	}
	return nil
}

// workflowProductionReadiness reports which CAP-PRODUCTION dependencies the
// workflow engine has initialized.
//
// None of them: the workflow engine subscribes to raw sys.job.result directly,
// which bypasses the scheduler's fencing and admission boundary entirely, and
// it installs no exact-wire admission, replay store, trust store, session
// resolver, outbound signer, or resource resolver allowlist. Reporting an
// honest all-false readiness means CORDUM_CAP_PROFILE=production refuses to
// start here with a precise list, rather than the engine advertising a profile
// it cannot enforce.
func workflowProductionReadiness() capprofile.Readiness {
	return capprofile.Readiness{}
}

// logWorkflowProfileActivation emits compatibility telemetry so an operator can
// see that CAP-PRODUCTION is not active. It logs no packet bytes, tokens, or
// signatures.
func logWorkflowProfileActivation(profile capprofile.Profile, r capprofile.Readiness) {
	if profile.AdvertiseProduction(r) {
		slog.Info("CAP profile active",
			"component", "workflow-engine",
			"profile", profile.String(),
			"cap_production_advertised", true)
		return
	}
	slog.Warn("CAP-PRODUCTION not active; compatibility surfaces remain reachable",
		"component", "workflow-engine",
		"profile", profile.String(),
		"cap_production_advertised", false,
		"env", capprofile.EnvVar)
}
