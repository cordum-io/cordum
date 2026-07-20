package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/capprofile"
	"github.com/redis/go-redis/v9"
)

func TestResolveSchedulerProfileRejectsUnknownValue(t *testing.T) {
	t.Setenv(capprofile.EnvVar, "prod")
	if _, err := resolveSchedulerProfile(); !errors.Is(err, capprofile.ErrUnknownProfile) {
		t.Fatalf("resolveSchedulerProfile() error = %v, want ErrUnknownProfile", err)
	}
}

func TestResolveSchedulerProfileDefaultsToCompat(t *testing.T) {
	t.Setenv(capprofile.EnvVar, "")
	profile, err := resolveSchedulerProfile()
	if err != nil {
		t.Fatalf("resolveSchedulerProfile() error = %v", err)
	}
	if profile.IsProduction() {
		t.Fatal("unset profile resolved to production")
	}
}

func fullySatisfiedDeps() schedulerProductionDeps {
	return schedulerProductionDeps{
		handshakeEnforcing:     true,
		safetyConfigured:       true,
		outputSafetyConfigured: true,
		failClosed:             true,
		replayStoreReachable:   true,
		rawAdmissionInstalled:  true,
		trustStoreConfigured:   true,
		sessionResolverReady:   true,
		outboundSignerReady:    true,
		resourceAllowlistted:   true,
	}
}

// Compat is the default and MUST NOT advertise CAP-PRODUCTION even when every
// dependency happens to be initialized.
func TestSchedulerCapabilitiesOmitProductionInCompatMode(t *testing.T) {
	caps := schedulerCapabilities(capprofile.Compat, fullySatisfiedDeps().readiness())
	if caps[capprofile.CapabilityProduction] {
		t.Fatalf("compat scheduler advertised %s", capprofile.CapabilityProduction)
	}
	for _, base := range []string{"safety_check", "routing", "compensation"} {
		if !caps[base] {
			t.Fatalf("compat scheduler dropped base capability %q", base)
		}
	}
}

func TestSchedulerCapabilitiesAdvertiseProductionOnlyWhenFullyReady(t *testing.T) {
	ready := schedulerCapabilities(capprofile.Production, fullySatisfiedDeps().readiness())
	if !ready[capprofile.CapabilityProduction] {
		t.Fatalf("fully-ready production scheduler missing %s", capprofile.CapabilityProduction)
	}

	partial := fullySatisfiedDeps()
	partial.replayStoreReachable = false
	notReady := schedulerCapabilities(capprofile.Production, partial.readiness())
	if notReady[capprofile.CapabilityProduction] {
		t.Fatalf("scheduler advertised %s without a reachable replay store", capprofile.CapabilityProduction)
	}
}

// Selecting production while a mandatory dependency is missing must terminate
// startup. Degrading to partial enforcement while advertising production is
// strictly worse than refusing to boot.
func TestEnforceProductionReadinessFailsStartupOnMissingDependency(t *testing.T) {
	deps := fullySatisfiedDeps()
	deps.handshakeEnforcing = false
	deps.replayStoreReachable = false

	err := enforceProductionReadiness(capprofile.Production, deps.readiness())
	if !errors.Is(err, capprofile.ErrProductionDependencyMissing) {
		t.Fatalf("enforceProductionReadiness() = %v, want ErrProductionDependencyMissing", err)
	}
	for _, want := range []string{"handshake_enforced", "replay_store"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("startup error %q must name missing dependency %q", err.Error(), want)
		}
	}
}

// Compat mode must never be blocked by production-only dependencies.
func TestEnforceProductionReadinessIgnoresCompatMode(t *testing.T) {
	var none schedulerProductionDeps
	if err := enforceProductionReadiness(capprofile.Compat, none.readiness()); err != nil {
		t.Fatalf("compat startup blocked by production readiness: %v", err)
	}
}

// A warn/off handshake cannot authenticate a worker, so it can never satisfy
// CAP-PRODUCTION.
func TestNonEnforcingHandshakeBlocksProduction(t *testing.T) {
	deps := fullySatisfiedDeps()
	deps.handshakeEnforcing = false
	if capprofile.Production.AdvertiseProduction(deps.readiness()) {
		t.Fatal("production advertised with a non-enforcing handshake")
	}
}

func TestProbeReplayStoreDetectsUnreachableRedis(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client.Close() }()

	if !probeReplayStore(context.Background(), client) {
		t.Fatal("probeReplayStore() = false for a live store")
	}

	srv.Close()
	if probeReplayStore(context.Background(), client) {
		t.Fatal("probeReplayStore() = true for an unreachable store")
	}
	if probeReplayStore(context.Background(), nil) {
		t.Fatal("probeReplayStore(nil) = true")
	}
}

func TestNewSchedulerReplayStoreSatisfiesAdmissionContract(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	defer srv.Close()
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer func() { _ = client.Close() }()

	if store := newSchedulerReplayStore(client); store == nil {
		t.Fatal("newSchedulerReplayStore() = nil")
	}
}

// The readiness projection must be total: every schedulerProductionDeps field
// has to reach capprofile.Readiness, or a missing dependency would be invisible
// to the gate.
func TestReadinessProjectionIsTotal(t *testing.T) {
	if !fullySatisfiedDeps().readiness().Ready() {
		t.Fatal("fully satisfied deps did not project to a ready Readiness")
	}
	var none schedulerProductionDeps
	r := none.readiness()
	if r.Ready() {
		t.Fatal("zero deps projected to a ready Readiness")
	}
	err := r.Validate()
	for _, want := range []string{
		"handshake_enforced", "raw_admission_installed", "replay_store", "trust_store",
		"session_resolver", "outbound_signer", "resource_allowlist", "safety_kernel",
		"output_safety", "fail_closed_modes",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("zero-deps validation %q missing dependency %q", err.Error(), want)
		}
	}
}
