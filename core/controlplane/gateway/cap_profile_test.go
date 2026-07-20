package gateway

import (
	"os"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/capprofile"
)

// The gateway is an ingress, not a raw-packet admission boundary. It must not
// advertise CAP-PRODUCTION regardless of handshake mode.
func TestGatewayNeverAdvertisesProductionWhileUnwired(t *testing.T) {
	for _, mode := range []scheduler.HandshakeMode{
		scheduler.HandshakeModeOff,
		scheduler.HandshakeModeWarn,
		scheduler.HandshakeModeEnforce,
	} {
		r := gatewayProductionReadiness(gatewayHandshakeSecurity{mode: mode})
		if r.Ready() {
			t.Fatalf("mode %s: gateway readiness reports ready while no production boundary is wired", mode)
		}
		if capprofile.Production.AdvertiseProduction(r) {
			t.Fatalf("mode %s: gateway advertised CAP-PRODUCTION", mode)
		}
	}
}

// Enforce mode is genuinely observable and must be reported truthfully, even
// though it alone is not sufficient for production.
func TestGatewayReadinessReportsHandshakeModeTruthfully(t *testing.T) {
	enforcing := gatewayProductionReadiness(gatewayHandshakeSecurity{mode: scheduler.HandshakeModeEnforce})
	if !enforcing.HandshakeEnforced {
		t.Fatal("enforce mode did not report HandshakeEnforced")
	}
	for _, mode := range []scheduler.HandshakeMode{scheduler.HandshakeModeOff, scheduler.HandshakeModeWarn} {
		r := gatewayProductionReadiness(gatewayHandshakeSecurity{mode: mode})
		if r.HandshakeEnforced {
			t.Fatalf("mode %s incorrectly reported HandshakeEnforced", mode)
		}
	}
}

func TestGatewayCompatModeKeepsBaseCapabilities(t *testing.T) {
	caps := capprofile.Compat.Capabilities(map[string]bool{
		"http": true, "grpc": true, "websocket": true, "mcp": true,
	}, gatewayProductionReadiness(gatewayHandshakeSecurity{mode: scheduler.HandshakeModeEnforce}))

	if caps[capprofile.CapabilityProduction] {
		t.Fatalf("compat gateway advertised %s", capprofile.CapabilityProduction)
	}
	for _, want := range []string{"http", "grpc", "websocket", "mcp"} {
		if !caps[want] {
			t.Fatalf("compat gateway dropped capability %q", want)
		}
	}
}

func TestEnforceGatewayProductionReadinessFailsClosed(t *testing.T) {
	readiness := gatewayProductionReadiness(gatewayHandshakeSecurity{mode: scheduler.HandshakeModeEnforce})
	if err := enforceGatewayProductionReadiness(capprofile.Production, readiness); err == nil {
		t.Fatal("production mode accepted an incomplete gateway boundary")
	}
	if err := enforceGatewayProductionReadiness(capprofile.Compat, readiness); err != nil {
		t.Fatalf("compat mode rejected incomplete production-only dependencies: %v", err)
	}
}

// Guards the advertise-before-ready defect: the advertisement used to run
// immediately after the NATS connection, before the workflow store, config
// service, schema registry, servers, and sweepers existed.
func TestGatewayAdvertisesAfterDependenciesAreInitialized(t *testing.T) {
	src, err := os.ReadFile("gateway.go")
	if err != nil {
		t.Fatalf("read gateway.go: %v", err)
	}
	text := string(src)

	advertise := strings.Index(text, "bus.PublishHandshake(")
	if advertise < 0 {
		t.Fatal("gateway.go no longer publishes a handshake advertisement")
	}
	if strings.Count(text, "bus.PublishHandshake(") != 1 {
		t.Fatal("gateway.go publishes more than one handshake advertisement")
	}
	connect := strings.Index(text, "bus.NewNatsBus(")
	grpcSetup := strings.Index(text, "grpc.NewServer(")
	if connect < 0 || grpcSetup < 0 {
		t.Fatal("gateway.go structure changed; update this ordering guard")
	}
	if advertise < grpcSetup {
		t.Fatal("gateway advertises before its gRPC server is constructed")
	}

	profileResolve := strings.Index(text, "capprofile.FromEnv()")
	if profileResolve < 0 {
		t.Fatal("gateway.go does not resolve the CAP profile")
	}
	if profileResolve > connect {
		t.Fatal("CAP profile is resolved after the NATS connection; an invalid profile must fail startup first")
	}
	if !strings.Contains(text[advertise:], "capProfile.Capabilities(") {
		t.Fatal("gateway advertisement bypasses the profile-aware capability builder")
	}
}

func TestGatewayReadinessGatePrecedesListenersAndGoroutines(t *testing.T) {
	src, err := os.ReadFile("gateway.go")
	if err != nil {
		t.Fatalf("read gateway.go: %v", err)
	}
	text := string(src)
	runStart := strings.Index(text, "func RunWithAuth(")
	if runStart < 0 {
		t.Fatal("gateway.go no longer defines RunWithAuth")
	}
	runBody := text[runStart:]
	gate := strings.Index(runBody, "enforceGatewayProductionReadiness(")
	if gate < 0 {
		t.Fatal("gateway does not enforce CAP-PRODUCTION readiness")
	}
	for _, marker := range []string{
		"cordumotel.InitTracer(", "auth.NewBasicAuthProvider(", "store.NewRedisStore(",
		"StartCleanupLoop(", ".telemetry.Start(", ".instanceRegistry.Start(",
		"startBusTaps(", "go wfReconciler.Start(", "net.Listen(", "go func()",
		"go edgeSweeper.Run(", "go edgeApprovalSweeper.Run(", "startHTTPServer(",
	} {
		activation := strings.Index(runBody, marker)
		if activation >= 0 && gate > activation {
			t.Errorf("readiness gate runs after runtime activation %q", marker)
		}
	}
}
