package main

import (
	"os"
	"strings"
	"testing"
)

// readMainSource loads main.go for the ordering assertions below.
//
// These are source-order tests rather than behavioural ones because the
// property under test lives in main(), which dials Redis/NATS/gRPC and calls
// os.Exit. The defect they guard against is real and was shipped once: the
// capability advertisement used to run immediately after the NATS connection,
// long before the handshake responder and safety dependencies existed, so the
// scheduler announced capabilities it could not yet honour.
func readMainSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	return string(src)
}

func TestHandshakeIsAdvertisedAfterEngineStart(t *testing.T) {
	src := readMainSource(t)

	advertise := strings.Index(src, "bus.PublishHandshake(")
	if advertise < 0 {
		t.Fatal("main.go no longer publishes a handshake advertisement")
	}
	if strings.Count(src, "bus.PublishHandshake(") != 1 {
		t.Fatal("main.go publishes more than one handshake; only the post-readiness advertisement may remain")
	}

	engineStart := strings.Index(src, "engine.Start()")
	if engineStart < 0 {
		t.Fatal("main.go no longer starts the engine")
	}
	if advertise < engineStart {
		t.Fatal("handshake is advertised before engine.Start(); CAP-PRODUCTION requires advertising only after all dependencies initialize")
	}
}

func TestProductionReadinessGateRunsBeforeAdvertisement(t *testing.T) {
	src := readMainSource(t)

	gate := strings.Index(src, "enforceProductionReadiness(")
	if gate < 0 {
		t.Fatal("main.go does not enforce CAP-PRODUCTION readiness")
	}
	advertise := strings.Index(src, "bus.PublishHandshake(")
	if advertise < 0 {
		t.Fatal("main.go no longer publishes a handshake advertisement")
	}
	if gate > advertise {
		t.Fatal("readiness gate runs after the advertisement; production could be announced before it is enforceable")
	}
}

func TestProductionRuntimeInstallsBeforeSchedulerSubscriptions(t *testing.T) {
	src := readMainSource(t)
	install := strings.Index(src, "installSchedulerProductionRuntime(")
	if install < 0 {
		t.Fatal("main.go has no production runtime installer call")
	}
	for _, marker := range []string{"handshakeSubscriber.Start()", "engine.Start()"} {
		subscribe := strings.Index(src, marker)
		if subscribe < 0 || install > subscribe {
			t.Fatalf("production runtime installer must precede %s", marker)
		}
	}
}

func TestProductionResourceRegistryWiredBeforeReadiness(t *testing.T) {
	src := readMainSource(t)
	wired := strings.Index(src, "WithResourceRegistry(runtimeResourceRegistry)")
	gate := strings.Index(src, "enforceProductionReadiness(capProfile, capReadiness)")
	if wired < 0 || gate < 0 || wired > gate || strings.Count(src, "WithResourceRegistry(runtimeResourceRegistry)") != 3 {
		t.Fatal("structured resource registry must reach all readers before readiness in every profile")
	}
}

func TestCompatibilityResourceReadersRequireExplicitOptIn(t *testing.T) {
	src := readMainSource(t)
	if got := strings.Count(src, "WithLegacyResourceCompatibility(nil)"); got != 3 {
		t.Fatalf("explicit compatibility resource opt-ins = %d, want scheduler input/output/schema readers", got)
	}
}

func TestProductionReadinessGateRunsBeforeRuntimeActivation(t *testing.T) {
	src := readMainSource(t)
	mainStart := strings.Index(src, "func main()")
	if mainStart < 0 {
		t.Fatal("main.go no longer defines main")
	}
	mainBody := src[mainStart:]
	gate := strings.Index(mainBody, "enforceProductionReadiness(")
	if gate < 0 {
		t.Fatal("main.go does not enforce CAP-PRODUCTION readiness")
	}
	for _, marker := range []string{
		"go func()", "instReg.Start(", "handshakeSubscriber.Start(",
		"engine.Start(", "go reconciler.Start(", "go pendingReplayer.Start(",
	} {
		activation := strings.Index(mainBody, marker)
		if activation >= 0 && gate > activation {
			t.Errorf("readiness gate runs after runtime activation %q", marker)
		}
	}
}

// The profile must be parsed before dependency construction so an invalid
// value fails fast instead of after side effects have been performed.
func TestProfileIsResolvedBeforeBusConnection(t *testing.T) {
	src := readMainSource(t)

	resolve := strings.Index(src, "resolveSchedulerProfile()")
	if resolve < 0 {
		t.Fatal("main.go does not resolve the CAP profile")
	}
	connect := strings.Index(src, "bus.NewNatsBus(")
	if connect < 0 {
		t.Fatal("main.go no longer connects to NATS")
	}
	if resolve > connect {
		t.Fatal("CAP profile is resolved after the NATS connection; an invalid profile must fail startup first")
	}
}

// The advertisement must go through the profile-aware capability builder.
// A raw map literal here would bypass the CAP-PRODUCTION gate entirely.
func TestAdvertisementUsesGatedCapabilityBuilder(t *testing.T) {
	src := readMainSource(t)

	advertise := strings.Index(src, "bus.PublishHandshake(")
	if advertise < 0 {
		t.Fatal("main.go no longer publishes a handshake advertisement")
	}
	call := src[advertise:]
	if end := strings.Index(call, "\n\t}"); end > 0 {
		call = call[:end]
	}
	if !strings.Contains(call, "schedulerCapabilities(") {
		t.Fatalf("handshake advertisement does not use schedulerCapabilities(); got:\n%s", call)
	}
}
