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
// The workflow engine consumes only scheduler-accepted results, but it still
// installs none of the shared exact-wire, replay, trust, session, signing,
// resource, or safety dependencies. Reporting an honest all-false readiness
// means CORDUM_CAP_PROFILE=production refuses to start rather than advertising
// a profile this binary cannot enforce independently.
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
