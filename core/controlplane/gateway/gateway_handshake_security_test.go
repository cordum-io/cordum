package gateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/policysign"
)

func clearGatewayHandshakeSigningEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		policysign.EnvSigningKey,
		policysign.EnvSigningKeyPath,
		policysign.EnvSigningKeyID,
		policysign.EnvDevSigningSeed,
	} {
		t.Setenv(name, "")
	}
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(name, policysign.EnvPublicKeyPrefix) {
			t.Setenv(name, "")
		}
	}
}

func configureGatewayHandshakeSigningEnv(t *testing.T) {
	t.Helper()
	clearGatewayHandshakeSigningEnv(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	t.Setenv(policysign.EnvSigningKeyID, "GATEWAY")
	t.Setenv(policysign.EnvSigningKey, base64.StdEncoding.EncodeToString(privateKey))
	t.Setenv(policysign.EnvPublicKeyPrefix+"GATEWAY", base64.StdEncoding.EncodeToString(publicKey))
}

func TestLoadGatewayHandshakeSecurityRejectsInvalidMode(t *testing.T) {
	t.Setenv(scheduler.EnvHandshakeMode, "enforse")
	t.Setenv(scheduler.EnvHeartbeatMode, "authority")

	_, err := loadGatewayHandshakeSecurity(nil)
	if err == nil || !strings.Contains(err.Error(), "unrecognized") {
		t.Fatalf("loadGatewayHandshakeSecurity() error = %v, want invalid mode", err)
	}
}

func TestLoadGatewayHandshakeSecurityOffNeedsNoSigningAuthority(t *testing.T) {
	t.Setenv(scheduler.EnvHandshakeMode, "off")
	t.Setenv(scheduler.EnvHeartbeatMode, "authority")
	clearGatewayHandshakeSigningEnv(t)

	security, err := loadGatewayHandshakeSecurity(nil)
	if err != nil {
		t.Fatalf("loadGatewayHandshakeSecurity(off): %v", err)
	}
	if security.mode != scheduler.HandshakeModeOff ||
		security.heartbeatMode != scheduler.HeartbeatModeAuthority || security.issuer != nil {
		t.Fatalf("off security = %+v, want off with nil issuer", security)
	}
}

func TestLoadGatewayHandshakeSecurityRejectsInvalidHeartbeatMode(t *testing.T) {
	t.Setenv(scheduler.EnvHandshakeMode, "off")
	t.Setenv(scheduler.EnvHeartbeatMode, "autority")

	_, err := loadGatewayHandshakeSecurity(nil)
	if err == nil || !strings.Contains(err.Error(), scheduler.EnvHeartbeatMode) {
		t.Fatalf("loadGatewayHandshakeSecurity() error = %v, want heartbeat mode error", err)
	}
}

func TestLoadGatewayHandshakeSecurityRejectsAuthorityMismatch(t *testing.T) {
	for _, test := range []struct {
		name      string
		handshake string
		heartbeat string
	}{
		{name: "off with session authority", handshake: "off", heartbeat: "warn"},
		{name: "warn with heartbeat authority", handshake: "warn", heartbeat: "authority"},
		{name: "enforce with heartbeat authority", handshake: "enforce", heartbeat: "authority"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(scheduler.EnvHandshakeMode, test.handshake)
			t.Setenv(scheduler.EnvHeartbeatMode, test.heartbeat)
			_, err := loadGatewayHandshakeSecurity(nil)
			if err == nil || !strings.Contains(err.Error(), "transition together") {
				t.Fatalf("loadGatewayHandshakeSecurity() error = %v, want authority mismatch", err)
			}
		})
	}
}

func TestLoadGatewayHandshakeSecurityAcceptsAuthorityPairMatrix(t *testing.T) {
	s, _, _ := newTestGateway(t)
	configureGatewayHandshakeSigningEnv(t)
	for _, test := range []struct {
		name         string
		handshake    string
		heartbeat    string
		wantIssuer   bool
		wantHBMode   scheduler.HeartbeatMode
		wantHandMode scheduler.HandshakeMode
	}{
		{"legacy", "off", "authority", false, scheduler.HeartbeatModeAuthority, scheduler.HandshakeModeOff},
		{"warn compare", "warn", "warn", true, scheduler.HeartbeatModeWarn, scheduler.HandshakeModeWarn},
		{"warn telemetry", "warn", "telemetry", true, scheduler.HeartbeatModeTelemetry, scheduler.HandshakeModeWarn},
		{"enforce compare", "enforce", "warn", true, scheduler.HeartbeatModeWarn, scheduler.HandshakeModeEnforce},
		{"enforce telemetry", "enforce", "telemetry", true, scheduler.HeartbeatModeTelemetry, scheduler.HandshakeModeEnforce},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(scheduler.EnvHandshakeMode, test.handshake)
			t.Setenv(scheduler.EnvHeartbeatMode, test.heartbeat)
			security, err := loadGatewayHandshakeSecurity(s.jobStore.Client())
			if err != nil {
				t.Fatalf("loadGatewayHandshakeSecurity(): %v", err)
			}
			if security.mode != test.wantHandMode || security.heartbeatMode != test.wantHBMode {
				t.Fatalf("modes = %q/%q, want %q/%q", security.mode, security.heartbeatMode, test.wantHandMode, test.wantHBMode)
			}
			if (security.issuer != nil) != test.wantIssuer {
				t.Fatalf("issuer present = %t, want %t", security.issuer != nil, test.wantIssuer)
			}
		})
	}
}

func TestLoadGatewayHandshakeSecurityActiveModeRequiresRedis(t *testing.T) {
	t.Setenv(scheduler.EnvHandshakeMode, "enforce")
	t.Setenv(scheduler.EnvHeartbeatMode, "telemetry")
	configureGatewayHandshakeSigningEnv(t)

	_, err := loadGatewayHandshakeSecurity(nil)
	if err == nil || !strings.Contains(err.Error(), "Redis") {
		t.Fatalf("loadGatewayHandshakeSecurity() error = %v, want Redis authority error", err)
	}
}

func TestLoadGatewayHandshakeSecurityActiveModeRequiresSigningKey(t *testing.T) {
	s, _, _ := newTestGateway(t)
	t.Setenv(scheduler.EnvHandshakeMode, "warn")
	t.Setenv(scheduler.EnvHeartbeatMode, "warn")
	clearGatewayHandshakeSigningEnv(t)

	_, err := loadGatewayHandshakeSecurity(s.jobStore.Client())
	if err == nil || !strings.Contains(err.Error(), "signing key") {
		t.Fatalf("loadGatewayHandshakeSecurity() error = %v, want signing-key error", err)
	}
}

func TestLoadGatewayHandshakeSecurityBuildsVerifiableIssuer(t *testing.T) {
	s, _, _ := newTestGateway(t)
	t.Setenv(scheduler.EnvHandshakeMode, "enforce")
	t.Setenv(scheduler.EnvHeartbeatMode, "telemetry")
	configureGatewayHandshakeSigningEnv(t)

	security, err := loadGatewayHandshakeSecurity(s.jobStore.Client())
	if err != nil {
		t.Fatalf("loadGatewayHandshakeSecurity(): %v", err)
	}
	if security.mode != scheduler.HandshakeModeEnforce ||
		security.heartbeatMode != scheduler.HeartbeatModeTelemetry || security.issuer == nil {
		t.Fatalf("active security = %+v, want enforce with issuer", security)
	}
	token, err := security.issuer.MintServiceToken(servicetoken.IdentityGateway)
	if err != nil {
		t.Fatalf("mint gateway service token: %v", err)
	}
	claims, err := security.issuer.VerifyService(token)
	if err != nil {
		t.Fatalf("verify gateway service token: %v", err)
	}
	if claims.Subject != servicetoken.IdentityGateway {
		t.Fatalf("token subject = %q, want %q", claims.Subject, servicetoken.IdentityGateway)
	}
}
