package main

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"log/slog"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/capprofile"
	"github.com/cordum/cordum/core/infra/replay"
	"github.com/redis/go-redis/v9"
)

// schedulerProductionDeps carries the concrete dependency handles the
// CAP-PRODUCTION readiness check inspects. Each field maps to one entry in
// capprofile.Readiness; nothing here is inferred from a packet or from
// operator intent alone.
type schedulerProductionDeps struct {
	// handshakeEnforcing is true only in enforce mode. warn/off cannot
	// authenticate a worker, so they cannot support CAP-PRODUCTION.
	handshakeEnforcing bool
	// safetyConfigured is the Safety Kernel client handle.
	safetyConfigured bool
	// outputSafetyConfigured is the output-safety checker handle.
	outputSafetyConfigured bool
	// failClosed is the engine's own fail-open audit
	// (Engine.ValidateProductionStartup returning nil).
	failClosed bool
	// replayStoreReachable is a live probe of the shared replay store, not
	// merely a constructed handle.
	replayStoreReachable bool

	// The following boundaries are not yet wired into this binary. They are
	// reported as not-ready so that selecting CAP-PRODUCTION refuses to start
	// with a precise list, rather than running with partial enforcement.
	rawAdmissionInstalled bool
	trustStoreConfigured  bool
	sessionResolverReady  bool
	outboundSignerReady   bool
	resourceAllowlistted  bool
}

// readiness projects the concrete handles onto the shared readiness contract.
func (d schedulerProductionDeps) readiness() capprofile.Readiness {
	return capprofile.Readiness{
		HandshakeEnforced:      d.handshakeEnforcing,
		RawAdmissionInstalled:  d.rawAdmissionInstalled,
		ReplayStoreReady:       d.replayStoreReachable,
		TrustStoreReady:        d.trustStoreConfigured,
		SessionResolverReady:   d.sessionResolverReady,
		OutboundSignerReady:    d.outboundSignerReady,
		ResourceAllowlistReady: d.resourceAllowlistted,
		SafetyKernelReady:      d.safetyConfigured,
		OutputSafetyEnabled:    d.outputSafetyConfigured,
		FailClosedModes:        d.failClosed,
	}
}

// probeReplayStore verifies the shared replay store actually answers before
// readiness claims it. A constructed-but-unreachable store would fail closed at
// admission time, taking the control plane down after it had already advertised
// CAP-PRODUCTION.
func probeReplayStore(ctx context.Context, client redis.UniversalClient) bool {
	if client == nil {
		return false
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return client.Ping(pingCtx).Err() == nil
}

// newSchedulerReplayStore builds the shared replay store used by the
// CAP-PRODUCTION raw admission boundary.
func newSchedulerReplayStore(client redis.UniversalClient) *replay.RedisReplayStore {
	return replay.NewRedisReplayStore(client)
}

func installSchedulerProductionRuntime(
	target *bus.NatsBus,
	bundle *handshakeSecurityBundle,
	config handshakeSecurityConfig,
	client redis.UniversalClient,
) (schedulerProductionDeps, error) {
	if target == nil || bundle == nil || bundle.rawTrustResolver == nil || client == nil {
		return schedulerProductionDeps{}, scheduler.ErrProductionAdmissionUnavailable
	}
	resolveSession, err := scheduler.NewProductionSessionResolver(bundle.middleware)
	if err != nil {
		return schedulerProductionDeps{}, err
	}
	boundary := &scheduler.ProductionRawBoundary{
		ResolveKey: schedulerProductionKeyResolver(bundle.rawTrustResolver),
		Replay:     newSchedulerReplayStore(client),
	}
	if err := scheduler.InstallProductionRawAdmission(target, boundary, resolveSession); err != nil {
		return schedulerProductionDeps{}, err
	}
	encoder, err := bus.NewProductionPacketEncoder(bus.ProductionPacketEncoderOptions{
		Key: config.schedulerPrivateKey, KeyID: config.schedulerKeyID,
	})
	if err != nil {
		return schedulerProductionDeps{}, err
	}
	if err := target.SetPacketEncoder(encoder); err != nil {
		return schedulerProductionDeps{}, err
	}
	target.FreezePacketSecurity()
	return schedulerProductionDeps{
		rawAdmissionInstalled: true, trustStoreConfigured: true,
		sessionResolverReady: true, outboundSignerReady: true,
	}, nil
}

func schedulerProductionKeyResolver(
	resolver scheduler.HandshakeTrustResolver,
) func(string, string, string) (*ecdsa.PublicKey, error) {
	return func(tenant, sender, keyID string) (*ecdsa.PublicKey, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		identity, err := resolver.Resolve(ctx, sender, keyID)
		if err != nil || identity == nil || identity.TenantID != tenant || identity.PublicKey == nil {
			return nil, errors.New("production signing key unavailable")
		}
		return identity.PublicKey, nil
	}
}

// resolveSchedulerProfile parses the explicit profile switch. An unrecognised
// value terminates startup rather than downgrading to compat.
func resolveSchedulerProfile() (capprofile.Profile, error) { return capprofile.FromEnv() }

// schedulerCapabilities returns the handshake capability set to advertise.
// The CAP-PRODUCTION key is added only when the operator selected production
// AND every mandatory dependency is initialized.
func schedulerCapabilities(profile capprofile.Profile, r capprofile.Readiness) map[string]bool {
	return profile.Capabilities(map[string]bool{
		"safety_check": true, "routing": true, "compensation": true,
	}, r)
}

// enforceProductionReadiness terminates startup when CAP-PRODUCTION was
// selected but cannot be enforced. Returning an error (rather than degrading)
// is the fail-closed requirement: a control plane that advertises production
// while missing replay defense or signature verification is worse than one
// that refuses to boot.
func enforceProductionReadiness(profile capprofile.Profile, r capprofile.Readiness) error {
	if !profile.IsProduction() {
		return nil
	}
	return r.Validate()
}

// logProfileActivation records the resolved profile and, in compat mode, the
// compatibility surfaces that remain reachable. This is the compatibility
// telemetry an operator needs to see that they are NOT running CAP-PRODUCTION.
func logProfileActivation(logger *slog.Logger, profile capprofile.Profile, r capprofile.Readiness) {
	if logger == nil {
		logger = slog.Default()
	}
	if profile.AdvertiseProduction(r) {
		logger.Info("CAP profile active",
			"profile", profile.String(),
			"cap_production_advertised", true)
		return
	}
	logger.Warn("CAP-PRODUCTION not active; compatibility surfaces remain reachable",
		"profile", profile.String(),
		"cap_production_advertised", false,
		"env", capprofile.EnvVar)
}

// schedulerFailClosed reports whether the engine has no active fail-open mode.
func schedulerFailClosed(engine *scheduler.Engine) bool {
	return engine != nil && engine.ValidateProductionStartup() == nil
}
