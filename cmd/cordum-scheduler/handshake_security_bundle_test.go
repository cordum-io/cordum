package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/policysign"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type handshakeAuditRecorder struct{}

func (*handshakeAuditRecorder) Emit(context.Context, audit.SIEMEvent) {}

type handshakeAuditBus struct{ published int }

func (b *handshakeAuditBus) Publish(string, *pb.BusPacket) error {
	b.published++
	return nil
}

func (*handshakeAuditBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

func TestLoadHandshakeSecurityConfigRequiresExplicitMode(t *testing.T) {
	clearHandshakeSecurityEnv(t)
	if _, err := loadHandshakeSecurityConfig(); err == nil || !strings.Contains(err.Error(), scheduler.EnvHandshakeMode) {
		t.Fatalf("unset mode must refuse boot with env guidance; got %v", err)
	}
}

func TestLoadHandshakeSecurityConfigOffRejectsTrustSettings(t *testing.T) {
	for _, name := range handshakeTrustEnvNames() {
		t.Run(name, func(t *testing.T) {
			clearHandshakeSecurityEnv(t)
			t.Setenv(scheduler.EnvHandshakeMode, "off")
			t.Setenv(name, "configured")
			if _, err := loadHandshakeSecurityConfig(); err == nil {
				t.Fatalf("off with %s set must reject contradictory trust config", name)
			}
		})
	}
}

func TestLoadHandshakeSecurityConfigWarnAndEnforceRequireCompleteBundle(t *testing.T) {
	for _, mode := range []string{"warn", "enforce"} {
		for _, missing := range handshakeTrustEnvNames() {
			t.Run(mode+"/missing_"+missing, func(t *testing.T) {
				setValidHandshakeSecurityEnv(t, mode)
				t.Setenv(missing, "")
				if _, err := loadHandshakeSecurityConfig(); err == nil || !strings.Contains(err.Error(), missing) {
					t.Fatalf("missing %s must refuse %s boot; got %v", missing, mode, err)
				}
			})
		}
	}
}

func TestNewHandshakeSecurityBundleRequiresAllAuthorities(t *testing.T) {
	setValidHandshakeSecurityEnv(t, "warn")
	setSessionSigningEnv(t)
	cfg, err := loadHandshakeSecurityConfig()
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	configService := configsvc.NewFromClient(rdb)
	auditSink := &handshakeAuditRecorder{}

	valid := handshakeSecurityDependencies{redis: rdb, config: configService, audit: auditSink}
	for name, deps := range map[string]handshakeSecurityDependencies{
		"redis":  {config: configService, audit: auditSink},
		"config": {redis: rdb, audit: auditSink},
		"audit":  {redis: rdb, config: configService},
	} {
		t.Run("missing_"+name, func(t *testing.T) {
			if _, err := newHandshakeSecurityBundle(cfg, deps); err == nil {
				t.Fatalf("missing %s authority must refuse boot", name)
			}
		})
	}

	bundle, err := newHandshakeSecurityBundle(cfg, valid)
	if err != nil {
		t.Fatalf("build complete security bundle: %v", err)
	}
	if bundle.middleware == nil || bundle.service == nil || bundle.dispatchResolver == nil ||
		!bundle.dispatchResolver.BoundAuthorityReady() || bundle.publicKeySHA256 == "" {
		t.Fatalf("bundle is incomplete: %#v", bundle)
	}
}

func TestNewHandshakeSecurityBundleOffNeedsNoAuthorities(t *testing.T) {
	clearHandshakeSecurityEnv(t)
	t.Setenv(scheduler.EnvHandshakeMode, "off")
	cfg, err := loadHandshakeSecurityConfig()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := newHandshakeSecurityBundle(cfg, handshakeSecurityDependencies{})
	if err != nil || bundle == nil || bundle.middleware != nil || bundle.service != nil || bundle.dispatchResolver != nil {
		t.Fatalf("explicit off should produce an inactive bundle; bundle=%#v err=%v", bundle, err)
	}
}

func TestNewProductionDispatchGateRequiresBoundAuthorityInSessionModes(t *testing.T) {
	for _, mode := range []scheduler.HeartbeatMode{scheduler.HeartbeatModeWarn, scheduler.HeartbeatModeTelemetry} {
		t.Run(mode.String(), func(t *testing.T) {
			if _, err := newProductionDispatchGate(mode, nil); err == nil {
				t.Fatal("session-authority mode accepted missing bound resolver")
			}
		})
	}
	gate, err := newProductionDispatchGate(scheduler.HeartbeatModeAuthority, nil)
	if err != nil || gate == nil || gate.EnforcesSession() {
		t.Fatalf("legacy authority gate rejected: gate=%#v err=%v", gate, err)
	}
}

func TestNewHandshakeAuditSinkPublishesAndCloses(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "none")
	bus := &handshakeAuditBus{}
	sink, sender, err := newHandshakeAuditSink(bus)
	if err != nil {
		t.Fatalf("build audit sink: %v", err)
	}
	sink.Emit(context.Background(), audit.SIEMEvent{EventType: scheduler.EventWorkerHandshake})
	if bus.published != 1 {
		t.Fatalf("handshake audit publish count = %d, want 1", bus.published)
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("close handshake audit sender: %v", err)
	}
}

func TestInitializeHandshakeSecurityOwnsAuditSender(t *testing.T) {
	setValidHandshakeSecurityEnv(t, "enforce")
	setSessionSigningEnv(t)
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "none")
	cfg, err := loadHandshakeSecurityConfig()
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	bundle, sender, err := initializeHandshakeSecurity(cfg, rdb, configsvc.NewFromClient(rdb), &handshakeAuditBus{})
	if err != nil || bundle == nil || sender == nil {
		t.Fatalf("initialize active handshake security: bundle=%#v sender=%v err=%v", bundle, sender, err)
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("close owned audit sender: %v", err)
	}
}

func clearHandshakeSecurityEnv(t *testing.T) {
	t.Helper()
	for _, name := range append([]string{scheduler.EnvHandshakeMode}, handshakeTrustEnvNames()...) {
		t.Setenv(name, "")
	}
}

func handshakeTrustEnvNames() []string {
	return []string{
		scheduler.EnvHandshakeSchedulerID,
		scheduler.EnvHandshakeSchedulerKeyID,
		scheduler.EnvHandshakePrivateKeyFile,
		scheduler.EnvHandshakePublicKeyFile,
	}
}

func setValidHandshakeSecurityEnv(t *testing.T, mode string) {
	t.Helper()
	clearHandshakeSecurityEnv(t)
	t.Setenv(scheduler.EnvHandshakeMode, mode)
	setHandshakeIdentityEnv(t)
	privatePath, publicPath := writeHandshakeKeyPair(t, elliptic.P256())
	t.Setenv(scheduler.EnvHandshakePrivateKeyFile, privatePath)
	t.Setenv(scheduler.EnvHandshakePublicKeyFile, publicPath)
}

func setHandshakeIdentityEnv(t *testing.T) {
	t.Helper()
	t.Setenv(scheduler.EnvHandshakeSchedulerID, "cordum-scheduler")
	t.Setenv(scheduler.EnvHandshakeSchedulerKeyID, "scheduler-key-v1")
}

func writeHandshakeKeyPair(t *testing.T, curve elliptic.Curve) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "scheduler-private.pem")
	publicPath := filepath.Join(dir, "scheduler-public.pem")
	writePEM(t, privatePath, "PRIVATE KEY", privateDER, 0o600)
	writePEM(t, publicPath, "PUBLIC KEY", publicDER, 0o644)
	return privatePath, publicPath
}

func writePEM(t *testing.T, path, blockType string, der []byte, mode os.FileMode) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func setSessionSigningEnv(t *testing.T) {
	t.Helper()
	clearSigningKeyEnv(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "session_v1"
	t.Setenv(policysign.EnvSigningKey, base64.StdEncoding.EncodeToString(privateKey))
	t.Setenv(policysign.EnvSigningKeyID, keyID)
	t.Setenv(policysign.EnvPublicKeyPrefix+"SESSION_V1", base64.StdEncoding.EncodeToString(publicKey))
}
