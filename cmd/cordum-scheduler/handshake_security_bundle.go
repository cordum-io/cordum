package main

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/policysign"
)

type handshakeSecurityConfig struct {
	mode                  scheduler.HandshakeMode
	audience              string
	schedulerID           string
	schedulerKeyID        string
	schedulerPrivateKey   *ecdsa.PrivateKey
	schedulerPublicKeyPEM []byte
	publicKeySHA256       string
}

type handshakeSecurityDependencies struct {
	redis  redis.UniversalClient
	config *configsvc.Service
	audit  scheduler.AuditSink
}

type handshakeSecurityBundle struct {
	mode             scheduler.HandshakeMode
	middleware       *scheduler.SessionTokenMiddleware
	service          *scheduler.HandshakeService
	dispatchResolver *scheduler.TrustResolver
	publicKeySHA256  string
}

func loadHandshakeSecurityConfig() (handshakeSecurityConfig, error) {
	mode, err := scheduler.ParseHandshakeModeStrict(os.Getenv(scheduler.EnvHandshakeMode))
	if err != nil {
		return handshakeSecurityConfig{}, err
	}
	cfg := handshakeSecurityConfig{mode: mode, audience: scheduler.WorkerHandshakeAudience}
	settings := handshakeTrustSettingsFromEnv()
	if mode == scheduler.HandshakeModeOff {
		if name := firstConfiguredHandshakeSetting(settings); name != "" {
			return handshakeSecurityConfig{}, fmt.Errorf("%s=off conflicts with configured %s", scheduler.EnvHandshakeMode, name)
		}
		return cfg, nil
	}
	if err := requireHandshakeTrustSettings(settings); err != nil {
		return handshakeSecurityConfig{}, err
	}
	if err := validateHandshakeBootID(scheduler.EnvHandshakeSchedulerID, settings[scheduler.EnvHandshakeSchedulerID]); err != nil {
		return handshakeSecurityConfig{}, err
	}
	if err := validateHandshakeBootID(scheduler.EnvHandshakeSchedulerKeyID, settings[scheduler.EnvHandshakeSchedulerKeyID]); err != nil {
		return handshakeSecurityConfig{}, err
	}
	key, publicPEM, fingerprint, err := loadPinnedHandshakeKeyPair(
		settings[scheduler.EnvHandshakePrivateKeyFile], settings[scheduler.EnvHandshakePublicKeyFile],
	)
	if err != nil {
		return handshakeSecurityConfig{}, err
	}
	cfg.schedulerID = settings[scheduler.EnvHandshakeSchedulerID]
	cfg.schedulerKeyID = settings[scheduler.EnvHandshakeSchedulerKeyID]
	cfg.schedulerPrivateKey = key
	cfg.schedulerPublicKeyPEM = publicPEM
	cfg.publicKeySHA256 = fingerprint
	return cfg, nil
}

func newHandshakeSecurityBundle(cfg handshakeSecurityConfig, deps handshakeSecurityDependencies) (*handshakeSecurityBundle, error) {
	bundle := &handshakeSecurityBundle{mode: cfg.mode, publicKeySHA256: cfg.publicKeySHA256}
	if cfg.mode == scheduler.HandshakeModeOff {
		return bundle, nil
	}
	if cfg.mode != scheduler.HandshakeModeWarn && cfg.mode != scheduler.HandshakeModeEnforce {
		return nil, errors.New("scheduler: invalid handshake security mode")
	}
	if missingSecurityValue(reflect.ValueOf(deps.redis)) || deps.config == nil ||
		missingSecurityValue(reflect.ValueOf(deps.audit)) {
		return nil, errors.New("scheduler: handshake Redis, config, and audit authorities required")
	}
	issuer, err := newSessionTokenIssuer(deps.redis)
	if err != nil {
		return nil, fmt.Errorf("scheduler: session-token authority unavailable: %w", err)
	}
	credentials := workercredentials.NewService(deps.config)
	resolver, err := newHandshakeTrustResolver(deps, credentials)
	if err != nil {
		return nil, err
	}
	dispatchResolver, err := scheduler.NewBoundTrustResolver(deps.redis, credentials)
	if err != nil {
		return nil, fmt.Errorf("scheduler: construct bound dispatch trust resolver: %w", err)
	}
	service, err := scheduler.NewHandshakeService(issuer, resolver,
		scheduler.NewRedisHandshakeChallengeStore(deps.redis), deps.audit,
		scheduler.HandshakeServiceOptions{
			Audience: cfg.audience, SchedulerID: cfg.schedulerID,
			SchedulerKeyID: cfg.schedulerKeyID, SchedulerPrivateKey: cfg.schedulerPrivateKey,
		})
	if err != nil {
		return nil, fmt.Errorf("scheduler: construct handshake service: %w", err)
	}
	bundle.middleware = scheduler.NewSessionTokenMiddleware(issuer, cfg.mode, scheduler.NewHandshakeMissingTracker())
	bundle.service = service
	bundle.dispatchResolver = dispatchResolver
	return bundle, nil
}

func newSessionTokenIssuer(rdb redis.UniversalClient) (*scheduler.SessionTokenIssuer, error) {
	if missingSecurityValue(reflect.ValueOf(rdb)) {
		return nil, errors.New("redis session store required")
	}
	privateKey, keyID, err := policysign.LoadPrivateKeyFromEnv()
	if err != nil {
		return nil, err
	}
	trust, err := policysign.LoadTrustStoreFromEnv()
	if err != nil {
		return nil, err
	}
	return scheduler.NewSessionTokenIssuer(privateKey, keyID, trust, rdb, scheduler.SessionTokenIssuerOptions{})
}

func newHandshakeTrustResolver(deps handshakeSecurityDependencies, credentials *workercredentials.Service) (scheduler.HandshakeTrustResolver, error) {
	agents := store.NewAgentIdentityStoreFromClient(deps.redis)
	resolver, err := scheduler.NewCredentialHandshakeTrustResolver(credentials, agents)
	if err != nil {
		return nil, fmt.Errorf("scheduler: construct handshake trust resolver: %w", err)
	}
	return resolver, nil
}

func handshakeTrustSettingsFromEnv() map[string]string {
	settings := make(map[string]string, len(handshakeTrustSettingNames()))
	for _, name := range handshakeTrustSettingNames() {
		settings[name] = strings.TrimSpace(os.Getenv(name))
	}
	return settings
}

func handshakeTrustSettingNames() []string {
	return []string{
		scheduler.EnvHandshakeSchedulerID, scheduler.EnvHandshakeSchedulerKeyID,
		scheduler.EnvHandshakePrivateKeyFile, scheduler.EnvHandshakePublicKeyFile,
	}
}

func firstConfiguredHandshakeSetting(settings map[string]string) string {
	for _, name := range handshakeTrustSettingNames() {
		if settings[name] != "" {
			return name
		}
	}
	return ""
}

func requireHandshakeTrustSettings(settings map[string]string) error {
	for _, name := range handshakeTrustSettingNames() {
		if settings[name] == "" {
			return fmt.Errorf("%s is required when %s is warn or enforce", name, scheduler.EnvHandshakeMode)
		}
	}
	return nil
}

func missingSecurityValue(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

type auditSenderSink struct{ sender audit.AuditSender }

func (s *auditSenderSink) Emit(_ context.Context, event audit.SIEMEvent) {
	if s != nil && s.sender != nil {
		s.sender.Send(event)
	}
}

func newHandshakeAuditSink(bus audit.AuditBus) (scheduler.AuditSink, audit.AuditSender, error) {
	if missingSecurityValue(reflect.ValueOf(bus)) {
		return nil, nil, errors.New("scheduler: handshake audit bus required")
	}
	fallback, err := audit.NewExporterFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("scheduler: handshake audit exporter: %w", err)
	}
	if fallback == nil {
		fallback = audit.NewBufferedExporter(audit.NewDiscardExporter())
	}
	sender := audit.NewNATSAuditPublisher(bus, fallback)
	return &auditSenderSink{sender: sender}, sender, nil
}

func initializeHandshakeSecurity(
	cfg handshakeSecurityConfig,
	rdb redis.UniversalClient,
	configService *configsvc.Service,
	auditBus audit.AuditBus,
) (*handshakeSecurityBundle, audit.AuditSender, error) {
	deps := handshakeSecurityDependencies{redis: rdb, config: configService}
	if cfg.mode == scheduler.HandshakeModeOff {
		bundle, err := newHandshakeSecurityBundle(cfg, deps)
		return bundle, nil, err
	}
	sink, sender, err := newHandshakeAuditSink(auditBus)
	if err != nil {
		return nil, nil, err
	}
	deps.audit = sink
	bundle, err := newHandshakeSecurityBundle(cfg, deps)
	if err != nil {
		_ = sender.Close()
		return nil, nil, err
	}
	return bundle, sender, nil
}
