package workflow

import (
	"os"
	"strings"
	"testing"
)

// TestRunWiresLegacyResourceCompatibilityOutsideProduction guards against a
// regression where the live workflow-engine binary never called
// Engine.WithLegacyResourceCompatibility at all. core/infra/resourceio.Reader
// fails closed on a bare JobResult.ResultPtr unless compatibility is
// explicitly enabled (see TestResolveStepOutputLegacyRequiresExplicitCompatibility
// in engine_resource_test.go), so every successful step whose worker reports
// only a legacy Redis pointer (the default/compat-profile norm — CAP-PRODUCTION
// is what requires structured ResultRefs) would be rejected by
// resolveStepOutput() and recorded as a failed step despite the job having
// succeeded. cmd/cordum-scheduler/main.go wires the equivalent opt-in
// conditionally on !capProfile.IsProduction(); RunWithEntitlements must do
// the same for the workflow engine's own resource reader.
func TestRunWiresLegacyResourceCompatibilityOutsideProduction(t *testing.T) {
	src, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatalf("read runner.go: %v", err)
	}
	source := string(src)

	wired := strings.Index(source, "WithLegacyResourceCompatibility(")
	if wired < 0 {
		t.Fatal("runner.go never calls Engine.WithLegacyResourceCompatibility; legacy result_ptr workflows fail closed in the default/compat profile")
	}

	gate := strings.Index(source, "!capProfile.IsProduction()")
	if gate < 0 {
		t.Fatal("runner.go has no !capProfile.IsProduction() gate to guard the compatibility opt-in")
	}

	// The gate and the call must be part of the same conditional block: the
	// opt-in call should appear shortly after the gate check, before any
	// other capProfile-gated block closes it off.
	const window = 400
	if wired < gate || wired-gate > window {
		t.Fatalf("WithLegacyResourceCompatibility (byte %d) is not wired inside the !capProfile.IsProduction() gate (byte %d, window %d)", wired, gate, window)
	}

	engineConstruction := strings.Index(source, "NewEngine(workflowStore, natsBus)")
	if engineConstruction < 0 {
		t.Fatal("runner.go no longer constructs the workflow Engine as expected")
	}
	if wired < engineConstruction {
		t.Fatal("WithLegacyResourceCompatibility wiring runs before the engine is constructed")
	}
}
