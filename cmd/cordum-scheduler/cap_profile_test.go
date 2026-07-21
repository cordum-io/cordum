package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/capprofile"
	"github.com/cordum/cordum/core/infra/resource"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type productionTrustResolverStub struct{ key *ecdsa.PublicKey }

func (s productionTrustResolverStub) Resolve(
	context.Context, string, string,
) (*scheduler.HandshakeTrustIdentity, error) {
	return &scheduler.HandshakeTrustIdentity{TenantID: "tenant-a", PublicKey: s.key}, nil
}

func TestInstallSchedulerProductionRuntimeFreezesLandedBoundaries(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	bundle := &handshakeSecurityBundle{
		middleware: scheduler.NewSessionTokenMiddleware(
			&scheduler.SessionTokenIssuer{}, scheduler.HandshakeModeEnforce, scheduler.NewHandshakeMissingTracker(),
		),
		rawTrustResolver: productionTrustResolverStub{key: &key.PublicKey},
	}
	target := &bus.NatsBus{}
	resourceRegistry, err := newSchedulerProductionResourceRegistry(client)
	if err != nil {
		t.Fatalf("newSchedulerProductionResourceRegistry: %v", err)
	}
	deps, err := installSchedulerProductionRuntime(target, bundle, handshakeSecurityConfig{
		schedulerPrivateKey: key, schedulerKeyID: "scheduler-key-1",
	}, client, resourceRegistry)
	if err != nil {
		t.Fatalf("installSchedulerProductionRuntime: %v", err)
	}
	if !deps.rawAdmissionInstalled || !deps.trustStoreConfigured ||
		!deps.sessionResolverReady || !deps.outboundSignerReady || !deps.resourceAllowlistted {
		t.Fatalf("runtime deps = %+v, want all installed boundaries ready", deps)
	}
	if err := target.SetRawPacketAdmission(nil); !errors.Is(err, bus.ErrRawAdmissionFrozen) {
		t.Fatalf("raw admission after install = %v, want frozen", err)
	}
	if err := target.SetPacketEncoder(nil); !errors.Is(err, bus.ErrPacketEncoderFrozen) {
		t.Fatalf("packet encoder after install = %v, want frozen", err)
	}
	assertProductionResourceRegistryResolves(t, deps.resourceRegistry, srv)
}

func assertProductionResourceRegistryResolves(
	t *testing.T,
	registry *resource.Registry,
	srv *miniredis.Miniredis,
) {
	t.Helper()
	body := []byte(`{"safe":true}`)
	digest := sha256.Sum256(body)
	key := schedulerResourceKeyPrefix + "tenant-a:job-a:input"
	srv.Set(key, string(body))
	ref := &agentv1.ResourceRef{
		ResolverId: schedulerResourceResolverID,
		Uri:        "redis://" + schedulerResourceAuthority + "/" + key,
		Sha256:     digest[:], MediaType: "application/json", SizeBytes: uint64(len(body)),
		ExpiresAt: timestamppb.New(time.Now().Add(time.Minute)), Purpose: "job.input",
	}
	resolved, err := registry.Resolve(context.Background(), ref, resource.TrustedContext{
		TenantID: "tenant-a", JobID: "job-a",
	})
	if err != nil || string(resolved.Content) != string(body) {
		t.Fatalf("production resource resolve = %q, %v", resolved.Content, err)
	}
}

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
		transportAuthenticated: true,
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

func TestUnauthenticatedTransportBlocksProduction(t *testing.T) {
	deps := fullySatisfiedDeps()
	deps.transportAuthenticated = false
	if capprofile.Production.AdvertiseProduction(deps.readiness()) {
		t.Fatal("production advertised without authenticated transport")
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
		"authenticated_transport", "handshake_enforced", "raw_admission_installed", "replay_store", "trust_store",
		"session_resolver", "outbound_signer", "resource_allowlist", "safety_kernel",
		"output_safety", "fail_closed_modes",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("zero-deps validation %q missing dependency %q", err.Error(), want)
		}
	}
}
