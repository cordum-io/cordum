package workflow

import (
	"os"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/capprofile"
)

// The workflow engine consumes raw sys.job.result directly, bypassing the
// scheduler's admission and fencing boundary. Until that is routed through a
// trusted accepted-result path it cannot enforce CAP-PRODUCTION, so it must
// never advertise it.
func TestWorkflowNeverAdvertisesProductionWhileUnwired(t *testing.T) {
	r := workflowProductionReadiness()
	if r.Ready() {
		t.Fatal("workflow readiness reports ready while no production boundary is wired")
	}
	if capprofile.Production.AdvertiseProduction(r) {
		t.Fatal("workflow engine advertised CAP-PRODUCTION without a production boundary")
	}
	caps := capprofile.Production.Capabilities(map[string]bool{"workflows": true}, r)
	if caps[capprofile.CapabilityProduction] {
		t.Fatalf("workflow capabilities contain %s", capprofile.CapabilityProduction)
	}
	if !caps["workflows"] {
		t.Fatal("workflow capabilities dropped the base capability")
	}
}

// Selecting production must name the reason startup is refused.
func TestWorkflowProductionReadinessNamesMissingDependencies(t *testing.T) {
	err := workflowProductionReadiness().Validate()
	if err == nil {
		t.Fatal("workflow readiness validated clean while unwired")
	}
	for _, want := range []string{"raw_admission_installed", "replay_store", "trust_store", "outbound_signer"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("readiness error %q must name %q", err.Error(), want)
		}
	}
}

// Compat mode is the default and must remain fully functional.
func TestWorkflowCompatModeAdvertisesBaseCapabilities(t *testing.T) {
	caps := capprofile.Compat.Capabilities(map[string]bool{
		"workflows": true, "approvals": true,
	}, workflowProductionReadiness())
	if caps[capprofile.CapabilityProduction] {
		t.Fatalf("compat workflow advertised %s", capprofile.CapabilityProduction)
	}
	for _, want := range []string{"workflows", "approvals"} {
		if !caps[want] {
			t.Fatalf("compat workflow dropped capability %q", want)
		}
	}
}

func TestEnforceWorkflowProductionReadinessFailsClosed(t *testing.T) {
	readiness := workflowProductionReadiness()
	if err := enforceWorkflowProductionReadiness(capprofile.Production, readiness); err == nil {
		t.Fatal("production mode accepted an incomplete workflow boundary")
	}
	if err := enforceWorkflowProductionReadiness(capprofile.Compat, readiness); err != nil {
		t.Fatalf("compat mode rejected incomplete production-only dependencies: %v", err)
	}
}

// Guards the advertise-before-ready defect: the advertisement used to run
// immediately after the NATS connection, before the engine, service-token
// minting, and the result subscription existed.
func TestWorkflowAdvertisesOnlyAfterSubscriptionsAreLive(t *testing.T) {
	src, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatalf("read runner.go: %v", err)
	}
	text := string(src)

	advertise := strings.Index(text, "bus.PublishHandshake(")
	if advertise < 0 {
		t.Fatal("runner.go no longer publishes a handshake advertisement")
	}
	if strings.Count(text, "bus.PublishHandshake(") != 1 {
		t.Fatal("runner.go publishes more than one handshake advertisement")
	}
	subscribe := strings.Index(text, "natsBus.SubscribeWithContext(")
	if subscribe < 0 {
		t.Fatal("runner.go no longer subscribes to results")
	}
	if advertise < subscribe {
		t.Fatal("workflow engine advertises before its result subscription is live")
	}
	if !strings.Contains(text[advertise:], "capProfile.Capabilities(") &&
		!strings.Contains(text[:advertise], "capProfile.Capabilities(") {
		t.Fatal("workflow advertisement bypasses the profile-aware capability builder")
	}
}

func TestWorkflowReadinessGatePrecedesListenersAndGoroutines(t *testing.T) {
	src, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatalf("read runner.go: %v", err)
	}
	text := string(src)
	runStart := strings.Index(text, "func RunWithEntitlements(")
	if runStart < 0 {
		t.Fatal("runner.go no longer defines RunWithEntitlements")
	}
	runBody := text[runStart:]
	gate := strings.Index(runBody, "enforceWorkflowProductionReadiness(")
	if gate < 0 {
		t.Fatal("workflow engine does not enforce CAP-PRODUCTION readiness")
	}
	for _, marker := range []string{
		"instReg.Start(", "go engine.startDelayPoller(", "go rec.Start(",
		"SubscribeWithContext(", "startHealthServer(",
	} {
		activation := strings.Index(runBody, marker)
		if activation >= 0 && gate > activation {
			t.Errorf("readiness gate runs after runtime activation %q", marker)
		}
	}
}
